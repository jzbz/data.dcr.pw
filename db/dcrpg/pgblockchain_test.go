//go:build pgonline

package dcrpg

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/dcrutil/v4"

	"github.com/decred/dcrdata/v8/db/cache"
	"github.com/decred/dcrdata/v8/db/dbtypes"
	"github.com/decred/dcrdata/v8/txhelpers"
)

// func TestTreasuryTxns(t *testing.T) {
// 	txns, err := db.TreasuryTxns(12, 32)
// 	t.Log(err)
// 	spew.Dump(txns)

// 	bal, spent, txCount, err := db.TreasuryBalance(675538)
// 	t.Log(bal, spent, txCount, err)
// }

func TestInsertSwap(t *testing.T) {
	contractHash := chainhash.Hash{1, 2}
	spendHash := chainhash.Hash{3, 4}

	_, err := db.db.Exec(`TRUNCATE swaps;`)
	if err != nil {
		t.Fatal(err)
	}

	asd := &txhelpers.AtomicSwapData{
		ContractTx:       &contractHash,
		ContractVout:     2,
		SpendTx:          &spendHash,
		SpendVin:         1,
		Value:            22_0000_0000,
		ContractAddress:  "Dcasdfasdfasdf",
		RecipientAddress: "Ds1324", // not stored
		RefundAddress:    "Ds9876", // not stored
		Locktime:         1621787740,
		SecretHash:       [32]byte{3, 2, 1},
		Secret:           []byte{'a', 'b', 'c'},
		Contract:         []byte{1, 2, 3, 4, 5, 6, 7, 8}, // not stored
		IsRefund:         true,
	}
	err = insertSwap(db.db, 1234, asd)
	if err != nil {
		t.Fatal(err)
	}

	asd.SpendTx = &chainhash.Hash{5, 6}
	asd.SpendVin = 2
	asd.Secret = nil
	err = insertSwap(db.db, 1234, asd)
	if err != nil {
		t.Fatal(err)
	}
}

func TestChainDB_AddressTransactionsAll(t *testing.T) {
	// address with no transactions.
	address := "DsUBCQWJsW8raht1i4gXTv7xPu3ySpUxxxx"
	rows, err := db.AddressTransactionsAll(context.Background(), address)
	if err != nil {
		t.Errorf("err should have been nil, was: %v", err)
	}
	if rows != nil {
		t.Fatalf("should have been no rows, got %v", rows)
	}

	height, hash, _ := db.HeightHashDBLegacy(context.Background())
	h, _ := chainhash.NewHashFromStr(hash)
	blockID := cache.NewBlockID(h, int64(height))
	wasStored := db.AddressCache.StoreRows(address, rows, blockID)
	if !wasStored {
		t.Fatalf("Address not stored in cache!")
	}

	r, bid := db.AddressCache.Rows(address)
	if bid == nil {
		t.Errorf("BlockID should not have been nil since this is a cache hit.")
	}
	if r == nil || len(r) > 0 {
		t.Errorf("rows should have been non-nil empty slice, got: %v", r)
	}
}

func TestMergeRows(t *testing.T) {
	address := "Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx"

	rows, err := db.AddressTransactionsAll(context.Background(), address)
	if err != nil {
		t.Errorf("err should have been nil, was: %v", err)
	}
	if rows == nil {
		t.Fatalf("should have rows, got none")
	}

	tStart := time.Now()
	// mergedRows, mrMap, err := dbtypes.MergeRows(rows)
	mergedRows, err := dbtypes.MergeRows(rows)
	if err != nil {
		t.Fatalf("MergeRows failed: %v", err)
	}
	t.Logf("%d rows combined to %d merged rows in %v", len(rows),
		len(mergedRows), time.Since(tStart))

	mergedRows0, err := db.AddressTransactionsAllMerged(context.Background(), address)
	if err != nil {
		t.Errorf("err should have been nil, was: %v", err)
	}
	if mergedRows0 == nil {
		t.Fatalf("should have rows, got none")
	}

	if len(mergedRows) != len(mergedRows0) {
		t.Errorf("len(mergedRows) = %d != len(mergedRows0) = %d",
			len(mergedRows), len(mergedRows0))
	}

	// for _, mr0 := range mergedRows0 {
	// 	mr, ok := mrMap[mr0.TxHash]
	// 	if !ok {
	// 		t.Errorf("TxHash %s not found in mergedRows.", mr0.TxHash)
	// 		continue
	// 	}
	// 	if !reflect.DeepEqual(mr, mr0) {
	// 		t.Errorf("wanted %v, got %v", mr0, mr)
	// 	}
	// }
}

func TestRetrieveUTXOs(t *testing.T) {
	utxos, err := retrieveUTXOs(context.Background(), db.db)
	if err != nil {
		t.Fatal(err)
	}

	// m := memStats()
	// t.Log(m)

	var totalValue int64
	for i := range utxos {
		totalValue += utxos[i].Value
	}

	t.Logf("Found %d utxos with a total value of %v", len(utxos),
		dcrutil.Amount(totalValue))
}

func TestUtxoStore_Reinit(t *testing.T) {
	utxos, err := retrieveUTXOs(context.Background(), db.db)
	if err != nil {
		t.Fatal(err)
	}

	uc := newUtxoStore(100)
	uc.Reinit(utxos)
}

func TestCheckColumnDataType(t *testing.T) {
	dataType, err := CheckColumnDataType(db.db, "blocks", "time")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(dataType)
}

func TestCheckCurrentTimeZone(t *testing.T) {
	currentTZ, err := CheckCurrentTimeZone(db.db)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Set time zone: %v", currentTZ)
}

func TestCheckDefaultTimeZone(t *testing.T) {
	defaultTZ, currentTZ, err := CheckDefaultTimeZone(db.db)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Set time zone: %v", currentTZ)
	t.Logf("Default time zone: %v", defaultTZ)
}

func TestDeleteBestBlock(t *testing.T) {
	ctx := context.Background()
	res, height, hash, err := deleteBestBlock(ctx, db.db)
	t.Logf("Deletion summary for block %d (%s): %v", height, hash, res)
	if err != nil {
		t.Errorf("Failed to delete best block data: %v", err)
	}

	timings := fmt.Sprintf("Blocks: %v\n", time.Duration(res.Timings.Blocks))
	timings = timings + fmt.Sprintf("\tVins: %v\n", time.Duration(res.Timings.Vins))
	timings = timings + fmt.Sprintf("\tVouts: %v\n", time.Duration(res.Timings.Vouts))
	timings = timings + fmt.Sprintf("\tAddresses: %v\n", time.Duration(res.Timings.Addresses))
	timings = timings + fmt.Sprintf("\tTransactions: %v\n", time.Duration(res.Timings.Transactions))
	timings = timings + fmt.Sprintf("\tTickets: %v\n", time.Duration(res.Timings.Tickets))
	timings = timings + fmt.Sprintf("\tVotes: %v\n", time.Duration(res.Timings.Votes))
	timings = timings + fmt.Sprintf("\tMisses: %v\n", time.Duration(res.Timings.Misses))
	t.Logf("Timings:\n%s", timings)

	t.Log("**************************** WARNING ****************************")
	t.Log("*** Blocks deleted from DB! Resync or download new test data! ***")
	t.Log("*****************************************************************")
}

func TestDeleteBlocks(t *testing.T) {
	height0, hash0, err := retrieveBestBlockHeight(context.Background(), db.db)
	if err != nil {
		t.Error(err)
	}

	N := int64(64)
	if N > int64(height0) {
		t.Fatalf("Cannot remove %d blocks from block chain of height %d.",
			N, height0)
	}

	t.Logf("Initial best block %d (%s).", height0, hash0)

	start := time.Now()
	ctx := context.Background()

	res, _, _, err := deleteBlocks(ctx, N, db.db)
	if err != nil {
		t.Error(err)
	}
	if len(res) != int(N) {
		t.Errorf("Expected to delete %d blocks; actually deleted %d.", N, len(res))
	}

	t.Log("**************************** WARNING ****************************")
	t.Log("*** Blocks deleted from DB! Resync or download new test data! ***")
	t.Log("*****************************************************************")

	height, hash, err := retrieveBestBlockHeight(ctx, db.db)
	if err != nil {
		t.Error(err)
	}
	t.Logf("Final best block %d (%s).", height, hash)

	summary := dbtypes.DeletionSummarySlice(res).Reduce()
	if summary.Blocks != N {
		t.Errorf("Expected summary of %d deleted blocks, got %d.", N, summary.Blocks)
	}
	t.Logf("Removed %d blocks in %v:\n%v.", N, time.Since(start), summary)
}

func TestRetrieveTxsByBlockHash(t *testing.T) {
	//block80740 := "00000000000003ae4fa13a6dcd53bf2fddacfac12e86e5b5f98a08a71d3e6caa"
	block0, _ := chainHashFromStr("298e5cc3d985bfe7f81dc135f360abe089edd4396b86d2de66b0cef42b21d980") // genesis
	_, _, _, blockTimes, _ := retrieveTxsByBlockHash(context.Background(), db.db, block0)
	// Check TimeDef.String
	blockTimeStr := blockTimes[0].String()
	t.Log(blockTimeStr)
	for i := range blockTimes {
		// Test TimeDef.Value (sql.Valuer)
		v, err := blockTimes[i].Value()
		if err != nil {
			t.Error(err)
		}
		tT, ok := v.(time.Time)
		if !ok {
			t.Errorf("v (%T) not a time.Time", v)
		}
		t.Log(tT.Format(time.RFC3339), tT.Unix())
		if tT.Unix() != trefUNIX {
			t.Errorf("Incorrect block time: got %d, expected %d",
				tT.Unix(), trefUNIX)
		}
	}
}
