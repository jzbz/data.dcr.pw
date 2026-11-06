package agendas

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/decred/dcrd/dcrjson/v4"
	chainjson "github.com/decred/dcrd/rpc/jsonrpc/types/v4"
)

type dataSourceStub struct{}

func (source dataSourceStub) GetStakeVersionInfo(_ context.Context, version int32) (*chainjson.GetStakeVersionInfoResult, error) {
	if version > 6 {
		return nil, fmt.Errorf(" ")
	}
	h := int64(version * 50000)
	return &chainjson.GetStakeVersionInfoResult{
		CurrentHeight: h,
		Hash:          strconv.Itoa(int(version)),
		Intervals: []chainjson.VersionInterval{
			{
				StartHeight: h - 500,
				EndHeight:   h + 500,
				PoSVersions: []chainjson.VersionCount{
					{
						Version: uint32(version),
						Count:   5,
					},
					{
						Version: uint32(version),
						Count:   100000,
					},
				},
				VoteVersions: []chainjson.VersionCount{
					{
						Version: uint32(version),
						Count:   5,
					},
					{
						Version: uint32(version),
						Count:   100000,
					},
				},
			},
			{
				StartHeight: h - 1500,
				EndHeight:   h - 501,
				PoSVersions: []chainjson.VersionCount{
					{
						Version: uint32(version),
						Count:   5,
					},
					{
						Version: uint32(version),
						Count:   100000,
					},
				},
				VoteVersions: []chainjson.VersionCount{
					{
						Version: uint32(version),
						Count:   5,
					},
					{
						Version: uint32(version),
						Count:   100000,
					},
				},
			},
		},
	}, nil
}

func (source dataSourceStub) GetVoteInfo(_ context.Context, version uint32) (*chainjson.GetVoteInfoResult, error) {
	if version > 6 {
		msg := fmt.Sprintf("stake version %d does not exist", version)
		return nil, dcrjson.NewRPCError(dcrjson.ErrRPCInvalidParameter, msg)
	}
	h := int64(version * 50000)
	return &chainjson.GetVoteInfoResult{
		CurrentHeight: h,
		StartHeight:   h - 1500,
		EndHeight:     h + 500,
		Hash:          strconv.Itoa(int(version)),
		VoteVersion:   version,
		Quorum:        4032,
		TotalVotes:    10000,
		Agendas: []chainjson.Agenda{
			{
				ID:             "test agenda",
				Description:    "agenda for testing",
				Mask:           6,
				StartTime:      5,
				ExpireTime:     10,
				Status:         "failed",
				QuorumProgress: 0,
				Choices: []chainjson.Choice{
					{
						ID:          "abstain",
						Description: "abstain voting for change",
						Bits:        0,
						IsAbstain:   true,
						IsNo:        false,
						Count:       0,
						Progress:    0,
					},
					{
						ID:          "no",
						Description: "keep the existing consensus rules",
						Bits:        2,
						IsAbstain:   false,
						IsNo:        true,
						Count:       0,
						Progress:    0,
					},
					{
						ID:          "yes",
						Description: "change to the new consensus rules",
						Bits:        4,
						IsAbstain:   false,
						IsNo:        false,
						Count:       0,
						Progress:    0,
					},
				},
			},
		},
	}, nil
}

func (source dataSourceStub) GetStakeVersions(_ context.Context, hash string, count int32) (*chainjson.GetStakeVersionsResult, error) {
	h, _ := strconv.Atoi(hash)
	result := &chainjson.GetStakeVersionsResult{
		StakeVersions: make([]chainjson.StakeVersions, int(count)),
	}
	c := int(count)
	for i := 0; i < c; i++ {
		result.StakeVersions[i] = chainjson.StakeVersions{
			Hash:         strconv.Itoa(h),
			Height:       int64(h),
			BlockVersion: 6,
			StakeVersion: 6,
			Votes:        []chainjson.VersionBits{}, // VoteTracker does not use this
		}
		h--
	}
	return result, nil
}

func counter(_ context.Context, hash string) (uint32, uint32, uint32, error) {
	return 1, 2, 3, nil
}

func TestVoteTracker(t *testing.T) {
	tracker, err := NewVoteTracker(chaincfg.MainNetParams(), dataSourceStub{}, counter)
	if err != nil {
		t.Fatalf("NewVoteTracker error: %v", err)
	}

	summary := tracker.Summary()
	if summary == nil {
		t.Errorf("nil VoteSummary error")
	}
}

// upgradingSourceStub is a dataSourceStub whose latest stake version can be
// raised, and which can be made to fail with a non-RPC error.
type upgradingSourceStub struct {
	dataSourceStub
	maxVersion uint32
	fail       error
}

func (source *upgradingSourceStub) GetVoteInfo(ctx context.Context, version uint32) (*chainjson.GetVoteInfoResult, error) {
	if source.fail != nil {
		return nil, source.fail
	}
	if version > source.maxVersion {
		msg := fmt.Sprintf("stake version %d does not exist", version)
		return nil, dcrjson.NewRPCError(dcrjson.ErrRPCInvalidParameter, msg)
	}
	return source.dataSourceStub.GetVoteInfo(ctx, version)
}

func TestRefreshRCIVersion(t *testing.T) {
	source := &upgradingSourceStub{maxVersion: 4}
	tracker, err := NewVoteTracker(chaincfg.MainNetParams(), source, counter)
	if err != nil {
		t.Fatalf("NewVoteTracker error: %v", err)
	}
	if v := tracker.Version(); v != 4 {
		t.Fatalf("wrong initial version: wanted 4, got %d", v)
	}

	// Two new stake versions. The tracker must move to the latest one, not one
	// past it.
	source.maxVersion = 6
	voteInfo, err := tracker.refreshRCI()
	if err != nil {
		t.Fatalf("refreshRCI error: %v", err)
	}
	if voteInfo.VoteVersion != 6 {
		t.Errorf("wrong vote info version: wanted 6, got %d", voteInfo.VoteVersion)
	}
	if v := tracker.Version(); v != 6 {
		t.Fatalf("wrong version after upgrade: wanted 6, got %d", v)
	}

	// Later refreshes still find the latest version.
	if _, err = tracker.refreshRCI(); err != nil {
		t.Fatalf("refreshRCI error after upgrade: %v", err)
	}
	if v := tracker.Version(); v != 6 {
		t.Fatalf("wrong version after second refresh: wanted 6, got %d", v)
	}

	// Other errors are reported, and leave the version alone.
	source.fail = errors.New("connection refused")
	if _, err = tracker.refreshRCI(); err == nil {
		t.Fatalf("expected an error from refreshRCI")
	}
	if v := tracker.Version(); v != 6 {
		t.Fatalf("version changed after a failed refresh: %d", v)
	}
}
