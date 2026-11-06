package dcrpg

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/decred/dcrd/chaincfg/v3"
	chainjson "github.com/decred/dcrd/rpc/jsonrpc/types/v4"
	"github.com/decred/dcrdata/v8/db/dbtypes"
)

func TestUpdateChainState(t *testing.T) {
	// rawData is a sample payload format as returned by getBlockChainInfo rpc endpoint.
	var rawData = []byte(`{
		"chain": "mainnet",
		"blocks": 316016,
		"headers": 316016,
		"syncheight": 316016,
		"bestblockhash": "00000000000000001d8cfa54dc13cfb0563421fd017801401cb2bdebe3579355",
		"difficulty": 406452686,
		"verificationprogress": 1,
		"chainwork": "0000000000000000000000000000000000000000000209c779c196914f038522",
		"initialblockdownload": false,
		"maxblocksize": 393216,
		"deployments": {
			"fixlnseqlocks": {
				"status": "defined",
				"starttime": 1548633600,
				"expiretime": 1580169600
			},
			"lnfeatures": {
				"status": "active",
				"since": 189568,
				"starttime": 1505260800,
				"expiretime": 1536796800
			},
			"sampleagenda1": {
				"status": "lockedin",
				"since": 119248,
				"starttime": 1493164800,
				"expiretime": 1508976000
			},
			"sampleagenda2": {
				"status": "failed",
				"since": 149248,
				"starttime": 1493164800,
				"expiretime": 1524700800
			},
			"sampleagenda3": {
				"status": "started",
				"since": 149248,
				"starttime": 1493164800,
				"expiretime": 1524700800
			}
		}
	}`)

	var chainInfoData = new(chainjson.GetBlockChainInfoResult)
	err := json.Unmarshal(rawData, chainInfoData)
	if err != nil {
		t.Fatalf("expected no error to be returned but found: %v", err)
	}

	bbh, _ := chainHashFromStr("00000000000000001d8cfa54dc13cfb0563421fd017801401cb2bdebe3579355")

	// Expected payload
	var expectedPayload = dbtypes.BlockChainData{
		Chain:                  "mainnet",
		SyncHeight:             316016,
		BestHeight:             316016,
		BestBlockHash:          bbh,
		Difficulty:             406452686,
		VerificationProgress:   1.0,
		ChainWork:              "0000000000000000000000000000000000000000000209c779c196914f038522",
		IsInitialBlockDownload: false,
		MaxBlockSize:           393216,
		AgendaMileStones: map[string]dbtypes.MileStone{
			"fixlnseqlocks": {
				Status:     dbtypes.InitialAgendaStatus,
				StartTime:  time.Unix(1548633600, 0).UTC(),
				ExpireTime: time.Unix(1580169600, 0).UTC(),
			},
			"lnfeatures": {
				Status:        dbtypes.ActivatedAgendaStatus,
				VotingStarted: 173440, // since - 2*RCI
				VotingDone:    181503,
				Activated:     189568,
				StartTime:     time.Unix(1505260800, 0).UTC(),
				ExpireTime:    time.Unix(1536796800, 0).UTC(),
			},
			"sampleagenda1": {
				Status:        dbtypes.LockedInAgendaStatus,
				VotingStarted: 111184, // since - RCI
				VotingDone:    119247,
				Activated:     127312,
				StartTime:     time.Unix(1493164800, 0).UTC(),
				ExpireTime:    time.Unix(1508976000, 0).UTC(),
			},
			"sampleagenda2": {
				Status:        dbtypes.FailedAgendaStatus,
				VotingStarted: 141184, // since - RCI
				VotingDone:    149247,
				StartTime:     time.Unix(1493164800, 0).UTC(),
				ExpireTime:    time.Unix(1524700800, 0).UTC(),
			},
			"sampleagenda3": {
				// Voting started at the start of the current RCI:
				// 316016 - (316016-149248)%8064.
				Status:        dbtypes.StartedAgendaStatus,
				VotingStarted: 310528,
				VotingDone:    318591,
				Activated:     326656,
				StartTime:     time.Unix(1493164800, 0).UTC(),
				ExpireTime:    time.Unix(1524700800, 0).UTC(),
			},
		},
	}

	dbRPC := &ChainDB{
		chainParams: &chaincfg.Params{
			RuleChangeActivationInterval: 8064,
		},
		deployments: new(ChainDeployments),
	}

	// This sets the dbRPC.chainInfo value.
	dbRPC.UpdateChainState(chainInfoData)

	// Checks if the set dbRPC.chainInfo has a similar format and content as the
	// expected payload including the internal UnmarshalJSON implementation.
	if dbRPC.deployments.chainInfo == nil {
		t.Fatal("chainInfo was not set")
	}
	if !reflect.DeepEqual(*dbRPC.deployments.chainInfo, expectedPayload) {
		t.Fatalf("expected both payloads to match but they did not.\ngot:  %+v\nwant: %+v",
			*dbRPC.deployments.chainInfo, expectedPayload)
	}
}
