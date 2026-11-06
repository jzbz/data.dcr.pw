package stakedb

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/slog"
	"github.com/dgraph-io/badger"

	apitypes "github.com/decred/dcrdata/v8/api/types"
)

func randomHash() chainhash.Hash {
	var hash chainhash.Hash
	if _, err := rand.Read(hash[:]); err != nil {
		panic("boom")
	}
	return hash
}

func randomHashSlice(N int) []chainhash.Hash {
	s := make([]chainhash.Hash, 0, N)
	for i := 0; i < N; i++ {
		s = append(s, randomHash())
	}
	return s
}

func TestMain(m *testing.M) {
	UseLogger(slog.NewBackend(os.Stdout).Logger("EXE"))
	log.SetLevel(slog.LevelTrace)
	returnVal := m.Run()

	if err := os.RemoveAll(dbFolder); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Failed to delete db file: %v\n", err)
	}

	os.Exit(returnVal)
}

var (
	dbFolder             = "pooldiffs.bdgr"
	dbFolderRefV0        = "pooldiffs0.bdgr"
	dbFolderRefV1        = "pooldiffs1.bdgr"
	dbFolderFullV0       = "test_ticket_pool.bdgr"
	dbFolderFullV1       = "test_ticket_pool_v1.bdgr"
	fullHeight     int64 = 239191
	fullPoolSize         = 40763
	poolSpots            = []int64{32215, 43268, 85508, 66322, 178346, 11013, 98265, 1081,
		40170, 105957, 187824, 148370, 14478, 153239, 109536, 169157, 92064,
		200277, 27558, 116203, 2338, 28323, 51459, 145753, 137377, 108501,
		138521, 33203, 39979, 56765, 175322, 87124, 192306, 157350, 13823,
		49777, 104992, 167135, 4507, 102258, 138354, 99312, 100434, 135096,
		46955, 127918, 138468, 190953, 135998, 64990, 87542, 149742, 30626,
		196567, 55479, 63701, 117733, 46739, 141352, 146690, 75763, 76768,
		112648, 124313, 857, 159517, 48344, 200476, 140524, 73190, 12496, 40740,
		124512, 55456, 100552, 166467, 197815, 195949, 33245, 27286, 4914,
		200908, 194977, 174689, 93864, 121588, 157975, 140562, 111278, 106384,
		160849, 69184, 31170, 125034, 109197, 78142, 138721, 186486, 69486, 6549}
	poolSizes = []int{42958, 41214, 42175, 43086, 41214, 42193, 42511, 11335,
		42083, 43278, 40946, 43035, 41655, 41109, 42460, 40875, 42580, 41582,
		42941, 41145, 22774, 43317, 41798, 43964, 43015, 44058, 43059, 42244,
		41857, 41198, 41104, 42370, 40283, 40769, 41569, 41044, 43205, 40659,
		33501, 42661, 43393, 42751, 42601, 43079, 40992, 42884, 42824, 42420,
		43353, 42766, 42086, 41893, 42310, 41423, 41552, 41727, 40955, 41509,
		42896, 44797, 42578, 42323, 41769, 43066, 6855, 41000, 41498, 41595,
		44108, 42370, 41775, 41834, 42125, 41587, 42134, 40977, 40619, 41825,
		42132, 42625, 37710, 41431, 40037, 41130, 41997, 41667, 41016, 43916,
		41379, 43994, 40935, 43343, 43027, 42718, 42673, 41802, 44422, 40786,
		42078, 41450}
)

// TestTicketPoolTraverseFull tests AdvanceToTip and Pool for random access.
// After each Pool call, it checks the pool size against the expected size.
// testTicketPoolTraverseFull tests AdvanceToTip and Pool for random access.
// After each Pool call, it checks the pool size against the expected size.
func TestTicketPoolTraverseFullV0(t *testing.T) {
	testTicketPoolTraverseFull(t, dbFolderFullV0) // modifies dbFolderFullV0
}

func TestTicketPoolTraverseFullV1(t *testing.T) {
	testTicketPoolTraverseFull(t, dbFolderFullV1)
}

func testTicketPoolTraverseFull(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("%s not found, skipping TestTicketPoolTraverseFull", dir)
	}

	t.Logf("Loading entire ticket pool diffs from %s...", dir)
	p, err := NewTicketPool(".", dir)
	if err != nil {
		t.Fatalf("NewTicketPool failed: %v", err)
	}
	defer p.Close()

	tip := p.Tip()
	if tip != fullHeight {
		t.Fatalf("tip incorrect. expected %d, got %d", fullHeight, tip)
	}

	initPoolSize := p.CurrentPoolSize()
	if initPoolSize != 0 {
		t.Fatalf("initial pool size incorrect. expected 0, got %d", initPoolSize)
	}

	cursor := p.Cursor()
	if cursor != 0 {
		t.Errorf("cursor incorrect. expected 0, got %d", cursor)
	}

	t.Log("Advancing cursor to tip (applying all diffs and building pool map).")
	if _, err = p.AdvanceToTip(); err != nil {
		t.Errorf("failed to advance pool to tip: %v", err)
	}

	cursor = p.Cursor()
	if cursor != tip {
		t.Errorf("cursor not at tip. expected %d, got %d", tip, cursor)
	}

	poolSize := p.CurrentPoolSize()
	if poolSize != fullPoolSize {
		t.Errorf("pool size incorrect. expected %d, got %d", fullPoolSize, poolSize)
	}

	ph, cursor2 := p.CurrentPool()
	if cursor != cursor2 {
		t.Errorf("incorrect cursor returned from CurrentPool. expected %d, got %d", cursor, cursor2)
	}

	if len(ph) != poolSize {
		t.Errorf("incorrect pool size from CurrentPool. expected %d, got %d", len(ph), poolSize)
	}

	pss := make([]int, len(poolSpots))
	for i, spot := range poolSpots {
		ps, err := p.Pool(spot)
		if err != nil {
			t.Errorf("unable to scan pool to block %d: %v", spot, err)
		}
		pss[i] = len(ps)
		//t.Logf("Pool size at height %d: %d", spot, len(ps))
		if poolSizes[i] != pss[i] {
			t.Errorf("incorrect pool size at height %d. expected %d, got %d",
				spot, poolSizes[i], pss[i])
		}
	}
}

// TestTicketPoolAppendInvalid ensures that AppendAndAdvancePool (advance) will
// error if non-existing tickets are listed in Out slice of a PoolDiff. It also
// tests correct use of AppendAndAdvancePool, verifying that tickets are removed
// from the pool as expected.
func TestTicketPoolAppendInvalid(t *testing.T) {
	if err := os.RemoveAll(dbFolder); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Failed to delete db file: %v", err)
	}
	p, err := NewTicketPool(".", dbFolder)
	if err != nil {
		t.Fatalf("NewTicketPool failed: %v", err)
	}
	defer p.Close()

	tip := p.Tip()
	if tip != 0 {
		t.Fatalf("tip incorrect. expected 0, got %d", tip)
	}

	initPoolSize := p.CurrentPoolSize()
	if initPoolSize != 0 {
		t.Fatalf("initial pool size incorrect. expected 0, got %d", initPoolSize)
	}

	inN, outN := 4, 3
	diff := &PoolDiff{
		In:  randomHashSlice(inN),
		Out: randomHashSlice(outN),
	}
	if err := p.AppendAndAdvancePool(diff, tip+1); err == nil {
		t.Errorf("AppendAndAdvancePool did not error while advancing and removing non-existing hashes")
	}

	newTip := p.Tip()
	if newTip != tip+1 {
		t.Errorf("new tip incorrect. expected %d, got %d", tip+1, newTip)
	}
	tip++

	poolSize := p.CurrentPoolSize()
	delta := inN // out were not in the pool to begin with
	if poolSize != initPoolSize+delta {
		t.Errorf("pool size incorrect. expected %d, got %d", initPoolSize+delta, poolSize)
	}
	t.Logf("Pool size: %d", poolSize)

	// advance, removing 2 and adding 2
	initPoolSize = poolSize
	inN = 2
	diff2 := &PoolDiff{
		In:  randomHashSlice(inN),
		Out: diff.In[:2], // remove first two added last time
	}

	if err := p.AppendAndAdvancePool(diff2, tip+1); err != nil {
		t.Errorf("AppendAndAdvancePool failed to advance: %v", err)
	}

	newTip = p.Tip()
	if newTip != tip+1 {
		t.Errorf("new tip incorrect. expected %d, got %d", tip+1, newTip)
	}
	//tip++

	poolSize = p.CurrentPoolSize()
	if poolSize != initPoolSize {
		t.Errorf("pool size incorrect. expected %d, got %d", initPoolSize, poolSize)
	}
}

// TestTicketPoolRetreat tests retreat.
func TestTicketPoolRetreat(t *testing.T) {
	if err := os.RemoveAll(dbFolder); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Failed to delete db file: %v", err)
	}
	p, err := NewTicketPool(".", dbFolder)
	if err != nil {
		t.Fatalf("NewTicketPool failed: %v", err)
	}
	defer p.Close()

	tip := p.Tip()
	if tip != 0 {
		t.Fatalf("tip incorrect. expected 0, got %d", tip)
	}
	cursor := p.Cursor()
	if cursor != 0 {
		t.Errorf("cursor incorrect. expected 0, got %d", cursor)
	}

	initPoolSize := p.CurrentPoolSize()
	if initPoolSize != 0 {
		t.Fatalf("initial pool size incorrect. expected 0, got %d", initPoolSize)
	}

	inN := 4
	diff := &PoolDiff{
		In: randomHashSlice(inN),
	}
	if err := p.AppendAndAdvancePool(diff, tip+1); err != nil {
		t.Errorf("AppendAndAdvancePool failed: %v", err)
	}

	newTip := p.Tip()
	if newTip != tip+1 {
		t.Errorf("new tip incorrect. expected %d, got %d", tip+1, newTip)
	}
	tip++

	newCursor := p.Cursor()
	if newCursor != cursor+1 {
		t.Errorf("cursor incorrect. expected %d, got %d", cursor+1, newCursor)
	}
	cursor++

	poolSize := p.CurrentPoolSize()
	delta := inN // out were not in the pool to begin with
	if poolSize != initPoolSize+delta {
		t.Errorf("pool size incorrect. expected %d, got %d", initPoolSize+delta, poolSize)
	}

	if err := p.retreat(); err != nil {
		t.Errorf("retreat: %v", err)
	}

	poolSize2 := p.CurrentPoolSize()
	if poolSize-delta != poolSize2 {
		t.Errorf("pool size incorrect. expected %d, got %d", poolSize-delta, poolSize2)
	}

	newTip = p.Tip()
	if newTip != tip {
		t.Errorf("tip changed. expected %d, got %d", tip+1, newTip)
	}
	//tip++

	newCursor = p.Cursor()
	if newCursor != cursor-1 {
		t.Errorf("cursor incorrect. expected %d, got %d", cursor-1, newCursor)
	}
	//cursor++
}

// TestTicketPoolHeight tests Pool(height).
func TestTicketPoolHeight(t *testing.T) {
	if err := os.RemoveAll(dbFolder); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Failed to delete db file: %v", err)
	}
	p, err := NewTicketPool(".", dbFolder)
	if err != nil {
		t.Fatalf("NewTicketPool failed: %v", err)
	}
	defer p.Close()

	tip := p.Tip()
	if tip != 0 {
		t.Fatalf("tip incorrect. expected 0, got %d", tip)
	}
	cursor := p.Cursor()
	if cursor != 0 {
		t.Errorf("cursor incorrect. expected 0, got %d", cursor)
	}

	initPoolSize := p.CurrentPoolSize()
	if initPoolSize != 0 {
		t.Fatalf("initial pool size incorrect. expected 0, got %d", initPoolSize)
	}

	inN := 4
	diff := &PoolDiff{
		In: randomHashSlice(inN),
	}
	if err := p.AppendAndAdvancePool(diff, tip+1); err != nil {
		t.Errorf("AppendAndAdvancePool failed: %v", err)
	}

	newTip := p.Tip()
	if newTip != tip+1 {
		t.Errorf("new tip incorrect. expected %d, got %d", tip+1, newTip)
	}
	tip++

	newCursor := p.Cursor()
	if newCursor != cursor+1 {
		t.Errorf("cursor incorrect. expected %d, got %d", cursor+1, newCursor)
	}
	cursor++

	if cursor != tip {
		t.Errorf("cursor (%d) != tip (%d)", cursor, tip)
	}

	// Generate ref test files
	// diff2 := &PoolDiff{
	// 	In:  randomHashSlice(inN),
	// 	Out: diff.In[:2], // remove first two added last time
	// }
	// if err := p.AppendAndAdvancePool(diff2, tip+1); err != nil {
	// 	t.Errorf("AppendAndAdvancePool failed: %v", err)
	// }

	poolSize := p.CurrentPoolSize()
	delta := inN // out were not in the pool to begin with
	if poolSize != initPoolSize+delta {
		t.Errorf("pool size incorrect. expected %d, got %d", initPoolSize+delta, poolSize)
	}

	pX, _ := p.CurrentPool()
	if len(pX) != poolSize {
		t.Errorf("CurrrentPool size incorrect. expected %d, got %d", poolSize, len(pX))
	}

	pY, err := p.Pool(tip)
	if err != nil {
		t.Errorf("Pool(tip) failed: %v", err)
	}
	if len(pY) != poolSize {
		t.Errorf("incorrect pool size. expected %d, got %d", poolSize, len(pY))
	}

	p0, err := p.Pool(0)
	if err != nil {
		t.Errorf("Pool(tip) failed: %v", err)
	}
	if len(p0) != initPoolSize {
		t.Errorf("incorrect pool size. expected %d, got %d", poolSize, len(pY))
	}
	cursor = p.Cursor()
	if cursor != 0 {
		t.Errorf("Cursor returned %d, expected 0", cursor)
	}
}

// TestTicketPoolPersistent tests the persistent DB by loading a reference db,
// and verifies its state is as expected, and Pool works on it.
func TestTicketPoolPersistent(t *testing.T) {
	tmpDir := t.TempDir()
	err := copyDir(dbFolderRefV0, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	testDBDir := func(dir string) {
		p, err := NewTicketPool("", dir)
		if err != nil {
			t.Fatalf("NewTicketPool failed: %v", err)
		}
		defer p.Close()

		tip := p.Tip()
		if tip != 2 {
			t.Fatalf("tip incorrect. expected 2, got %d", tip)
		}
		cursor := p.Cursor()
		if cursor != 0 {
			t.Errorf("cursor incorrect. expected 0, got %d", cursor)
		}

		initPoolSize := p.CurrentPoolSize()
		if initPoolSize != 0 {
			t.Fatalf("initial pool size incorrect. expected 0, got %d", initPoolSize)
		}

		// Debug ticket pool contents
		// for i := int64(0); i <= tip; i++ {
		// 	t.Log(p.Pool(i))
		// }

		tipPool, err := p.Pool(tip)
		if err != nil {
			t.Fatalf("Pool(tip) failed: %v", err)
		}
		t.Logf("tip pool size: %d", len(tipPool))

		tipPoolSize := p.CurrentPoolSize()
		if tipPoolSize != 6 {
			t.Fatalf("initial pool size incorrect. expected 4, got %d", tipPoolSize)
		}
	}

	// Test the upgrade of the v0 folder in the temp copy.
	t.Logf("Testing upgrade of v0 DB at %v", tmpDir)
	testDBDir(tmpDir)

	// Now test the upgraded DB folder.
	t.Logf("Testing newly upgraded v1 DB at %v", tmpDir)
	testDBDir(tmpDir)

	// Now the reference v1 folder (should not modify it).
	t.Logf("Testing reference v1 DB at %v", dbFolderRefV1)
	testDBDir(dbFolderRefV1)
}

// TestTicketPoolCorruptDiffLengths ensures that pool diffs whose length
// prefixes do not match the stored value are rejected with an error rather
// than causing a huge allocation or a panic.
func TestTicketPoolCorruptDiffLengths(t *testing.T) {
	hashes := func(n int) []byte { return make([]byte, n*chainhash.HashSize) }
	u64 := func(v uint64) []byte {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], v)
		return b[:]
	}
	cat := func(parts ...[]byte) []byte {
		var b []byte
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}

	tests := []struct {
		name  string
		value []byte
		valid bool
	}{
		{"valid", encodeDiff(&PoolDiff{In: randomHashSlice(2), Out: randomHashSlice(1)}), true},
		{"empty", nil, false},
		{"short", u64(0), false},
		{"partial hash", cat(u64(0), hashes(1)[:5], u64(0)), false},
		// inLen*HashSize wraps to 0, so the Out length would be read from the
		// start of the In hash.
		{"in length overflow", cat(u64(1<<59), hashes(1), u64(0)), false},
		{"in length too large", cat(u64(2), hashes(1), u64(0)), false},
		{"out length too large", cat(u64(1), hashes(1), u64(1<<40)), false},
		{"out length too small", cat(u64(0), u64(0), hashes(1)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p, err := NewTicketPool(dir, "tp")
			if err != nil {
				t.Fatalf("NewTicketPool failed: %v", err)
			}
			err = p.diffDB.Update(func(txn *badger.Txn) error {
				return txn.Set(uint64Bytes(1), tt.value)
			})
			p.Close()
			if err != nil {
				t.Fatal(err)
			}

			p, err = NewTicketPool(dir, "tp")
			if tt.valid {
				if err != nil {
					t.Fatalf("NewTicketPool failed: %v", err)
				}
				defer p.Close()
				if p.Tip() != 1 {
					t.Errorf("expected tip 1, got %d", p.Tip())
				}
				return
			}
			if err == nil {
				p.Close()
				t.Fatal("NewTicketPool accepted a corrupt pool diff")
			}
			t.Log(err)
		})
	}
}

// TestTicketPoolAppendStoreFailure ensures that a failed DB write leaves the
// in-memory diffs and tip unchanged.
func TestTicketPoolAppendStoreFailure(t *testing.T) {
	dir := t.TempDir()
	p, err := NewTicketPool(dir, "tp")
	if err != nil {
		t.Fatalf("NewTicketPool failed: %v", err)
	}
	p.Close()

	opts := badger.DefaultOptions(filepath.Join(dir, "tp"))
	opts.Logger = &badgerLogger{log}
	opts.ReadOnly = true
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("badger.Open failed: %v", err)
	}
	defer db.Close()
	p = &TicketPool{
		pool:   make(map[chainhash.Hash]struct{}),
		diffDB: db,
	}

	diff := &PoolDiff{In: randomHashSlice(2)}
	if err = p.Append(diff, 1); err == nil {
		t.Fatal("Append to a read-only DB did not fail")
	}
	if err = p.AppendAndAdvancePool(diff, 1); err == nil {
		t.Fatal("AppendAndAdvancePool to a read-only DB did not fail")
	}
	if p.Tip() != 0 || len(p.diffs) != 0 || p.CurrentPoolSize() != 0 {
		t.Errorf("in-memory state changed: tip %d, %d diffs, pool size %d",
			p.Tip(), len(p.diffs), p.CurrentPoolSize())
	}
	if err = p.Append(diff, 2); err == nil {
		t.Error("Append accepted a height that does not build on the tip")
	}
}

func TestPoolInfoCacheCapacity(t *testing.T) {
	c, err := NewPoolInfoCache(2)
	if err != nil {
		t.Fatal(err)
	}
	h1, h2, h3, h4 := randomHash(), randomHash(), randomHash(), randomHash()
	c.Set(h1, &apitypes.TicketPoolInfo{Height: 1})
	c.Set(h2, &apitypes.TicketPoolInfo{Height: 2})
	if _, ok := c.Get(h1); !ok {
		t.Fatal("h1 evicted before the cache reached capacity")
	}
	if _, ok := c.Get(h2); !ok {
		t.Fatal("h2 not stored")
	}

	// Updating an existing entry must not evict anything.
	c.Set(h2, &apitypes.TicketPoolInfo{Height: 20})
	if _, ok := c.Get(h1); !ok {
		t.Fatal("h1 evicted when updating h2")
	}
	if tpi, _ := c.Get(h2); tpi.Height != 20 {
		t.Errorf("h2 not updated, height %d", tpi.Height)
	}

	c.Set(h3, &apitypes.TicketPoolInfo{Height: 3})
	if _, ok := c.Get(h1); ok {
		t.Error("h1 not evicted")
	}
	if len(c.poolInfo) != 2 || len(c.expireQueue) != 2 {
		t.Errorf("expected 2 entries, got %d in map, %d queued", len(c.poolInfo), len(c.expireQueue))
	}

	if err = c.SetCapacity(3); err != nil {
		t.Fatal(err)
	}
	c.Set(h4, &apitypes.TicketPoolInfo{Height: 4})
	if len(c.poolInfo) != 3 {
		t.Errorf("expected 3 entries, got %d", len(c.poolInfo))
	}
	if err = c.SetCapacity(2); err != nil {
		t.Fatal(err)
	}
	if len(c.poolInfo) != 2 || len(c.expireQueue) != 2 {
		t.Errorf("expected 2 entries, got %d in map, %d queued", len(c.poolInfo), len(c.expireQueue))
	}
	if _, ok := c.Get(h2); ok {
		t.Error("h2 not evicted by SetCapacity")
	}
}
