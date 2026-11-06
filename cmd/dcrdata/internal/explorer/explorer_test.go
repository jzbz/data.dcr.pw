package explorer

import (
	// Imports for TestThreeSigFigs
	// "fmt"
	// "math"
	// "math/rand"
	// "time"

	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/decred/dcrd/chaincfg/v3"
	chainjson "github.com/decred/dcrd/rpc/jsonrpc/types/v4"
	"github.com/decred/dcrd/wire"
	"github.com/decred/dcrdata/gov/v6/agendas"
	"github.com/decred/dcrdata/v8/db/dbtypes"
	"github.com/decred/dcrdata/v8/explorer/types"
)

func TestTestNet3Name(t *testing.T) {
	netName := netName(chaincfg.TestNet3Params())
	if netName != testnetNetName {
		t.Errorf(`Net name not "%s": %s`, testnetNetName, netName)
	}
}

func TestMainNetName(t *testing.T) {
	netName := netName(chaincfg.MainNetParams())
	if netName != "Mainnet" {
		t.Errorf(`Net name not "Mainnet": %s`, netName)
	}
}

func TestSimNetName(t *testing.T) {
	netName := netName(chaincfg.SimNetParams())
	if netName != "Simnet" {
		t.Errorf(`Net name not "Simnet": %s`, netName)
	}
}

// func TestThreeSigFigs(t *testing.T) {
// 	source := rand.NewSource(time.Now().Unix())
// 	generator := rand.New(source)
// 	for i := 0; i < 8; i++ {
// 		flt := generator.Float64()
// 		flt = flt * math.Pow10(-i)
// 		fmt.Printf("%.8f -> %s\n", flt, threeSigFigs(flt))
// 	}
// 	for i := 0; i < 13; i++ {
// 		flt := generator.Float64()
// 		flt = flt * math.Pow10(i)
// 		fmt.Printf("%.8f -> %s\n", flt, threeSigFigs(flt))
// 	}
// }

// blockSourceStub serves block summaries and counts the GetExplorerBlocks
// calls, which each cost dcrd two RPCs per block.
type blockSourceStub struct {
	ChainDBStub
	blockCalls atomic.Int32
	tipHash    string
	gapAt      int // index of a block GetExplorerBlocks fails to fetch, or -1
}

func (s *blockSourceStub) GetExplorerBlock(context.Context, string) *types.BlockInfo {
	return nil // as the database returns when the block cannot be fetched
}

func (s *blockSourceStub) GetExplorerBlocks(_ context.Context, start, end int) []*types.BlockBasic {
	s.blockCalls.Add(1)
	var blocks []*types.BlockBasic
	for i := start; i > end; i-- {
		b := &types.BlockBasic{Height: int64(i), Hash: "h" + string(rune('a'+start-i))}
		if i == start {
			b.Hash = s.tipHash
		}
		if len(blocks) == s.gapAt {
			b = new(types.BlockBasic)
		}
		blocks = append(blocks, b)
	}
	return blocks
}

// TestStoreMissingBlock covers a block that cannot be fetched for the explorer.
// Store must report it as a saver error, which the notifier handles, rather
// than dereference the nil block in the notifier goroutine and crash.
func TestStoreMissingBlock(t *testing.T) {
	exp := &explorerUI{dataSource: &blockSourceStub{}}
	if err := exp.Store(nil, &wire.MsgBlock{}); err == nil {
		t.Fatal("Store succeeded without block data")
	}
}

// TestDashboardBlocksCache covers the dashboard's recent-block cache: one
// fetch per chain tip, and nothing cached that is incomplete.
func TestDashboardBlocksCache(t *testing.T) {
	ctx := context.Background()
	src := &blockSourceStub{tipHash: "tip1", gapAt: -1}
	exp := &explorerUI{dataSource: src}

	first := exp.dashboardBlocks(ctx, 100, "tip1")
	if len(first) != dashboardBlockCount || first[0].Height != 100 {
		t.Fatalf("got %d blocks from height %d, want %d from 100",
			len(first), first[0].Height, dashboardBlockCount)
	}
	again := exp.dashboardBlocks(ctx, 100, "tip1")
	if src.blockCalls.Load() != 1 {
		t.Errorf("same tip fetched %d times, want 1", src.blockCalls.Load())
	}
	if &again[0] != &first[0] {
		t.Error("cached blocks not returned for the same tip")
	}

	// A new tip, including a reorg to the same height, refetches.
	src.tipHash = "tip2"
	if got := exp.dashboardBlocks(ctx, 100, "tip2"); got[0].Hash != "tip2" {
		t.Errorf("got tip %q after the tip changed, want tip2", got[0].Hash)
	}
	if src.blockCalls.Load() != 2 {
		t.Errorf("fetched %d times after the tip changed, want 2", src.blockCalls.Load())
	}

	// A set with a block that could not be fetched is not cached.
	src.tipHash, src.gapAt = "tip3", 3
	exp.dashboardBlocks(ctx, 101, "tip3")
	exp.dashboardBlocks(ctx, 101, "tip3")
	if src.blockCalls.Load() != 4 {
		t.Errorf("incomplete set was cached: %d fetches, want 4", src.blockCalls.Load())
	}

	// Nor is anything before the explorer has a tip.
	exp.dashboardBlocks(ctx, -1, "")
	exp.dashboardBlocks(ctx, -1, "")
	if src.blockCalls.Load() != 6 {
		t.Errorf("blocks cached without a tip: %d fetches, want 6", src.blockCalls.Load())
	}
}

// TestSyncStatusSnapshot covers the sync status readers, which encode the
// returned progress bars after the lock is released while the sync routine
// updates them in place.
func TestSyncStatusSnapshot(t *testing.T) {
	blockchainSyncStatus.Lock()
	saved := blockchainSyncStatus.ProgressBars
	blockchainSyncStatus.ProgressBars = []SyncStatusInfo{{ProgressBarID: "a", BarSubtitle: "old"}}
	blockchainSyncStatus.Unlock()
	defer func() {
		blockchainSyncStatus.Lock()
		blockchainSyncStatus.ProgressBars = saved
		blockchainSyncStatus.Unlock()
	}()

	snap := SyncStatus()

	blockchainSyncStatus.Lock()
	blockchainSyncStatus.ProgressBars[0].BarSubtitle = "new"
	blockchainSyncStatus.Unlock()

	if snap[0].BarSubtitle != "old" {
		t.Errorf("SyncStatus result changed after it returned: subtitle %q", snap[0].BarSubtitle)
	}
}

// agendaSourceStub serves one agenda for the agenda page.
type agendaSourceStub struct{}

func (agendaSourceStub) AgendaInfo(id string) (*agendas.AgendaTagged, error) {
	return &agendas.AgendaTagged{
		ID: id,
		Choices: []chainjson.Choice{
			{ID: "abstain", IsAbstain: true}, {ID: "no", IsNo: true}, {ID: "yes"},
		},
	}, nil
}
func (agendaSourceStub) AllAgendas() ([]*agendas.AgendaTagged, error) { return nil, nil }
func (agendaSourceStub) UpdateAgendas() error                         { return nil }

// futureAgendaSource returns no vote summary, as the database does for an
// agenda whose start time is in the future.
type futureAgendaSource struct {
	ChainDBStub
}

func (futureAgendaSource) AgendasVotesSummary(context.Context, string) (*dbtypes.AgendaSummary, error) {
	return nil, nil
}

// TestAgendaPageFutureAgenda covers the agenda page for an agenda that has not
// started, which has no vote summary.
func TestAgendaPageFutureAgenda(t *testing.T) {
	params := chaincfg.MainNetParams()
	tm := newTemplates("../../views_v2", false, []string{"chrome"},
		makeTemplateFuncMap(params, NewAssetManager("../../public")))
	if err := tm.addTemplate("agenda"); err != nil {
		t.Fatalf("addTemplate(agenda): %v", err)
	}
	exp := &explorerUI{
		dataSource:    &futureAgendaSource{},
		agendasSource: agendaSourceStub{},
		templates:     tm,
		ChainParams:   params,
		pageData: &pageData{
			BlockInfo: &types.BlockInfo{BlockBasic: &types.BlockBasic{Height: 900000}},
			HomeInfo:  &types.HomeInfo{},
		},
	}

	r := httptest.NewRequest(http.MethodGet, "/agenda/futureagenda", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxAgendaId, "futureagenda"))
	rr := httptest.NewRecorder()
	exp.AgendaPage(rr, r) // panicked on the nil summary

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "futureagenda") {
		t.Error("agenda page does not name the agenda")
	}
}
