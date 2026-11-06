// Copyright (c) 2026, The Decred developers
// See LICENSE for details.

package mempool

import (
	"sync"
	"testing"

	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/wire"

	"github.com/decred/dcrdata/v8/txhelpers"
)

func testHash(b byte) chainhash.Hash {
	var h chainhash.Hash
	h[0] = b
	return h
}

func TestUnconfirmedTxnsForAddressSnapshot(t *testing.T) {
	const addr = "DsTestAddress"
	fundHash, spendHash, outHash := testHash(1), testHash(2), testHash(3)

	stored := &txhelpers.AddressOutpoints{
		Address:   addr,
		Outpoints: []*wire.OutPoint{wire.NewOutPoint(&outHash, 0, wire.TxTreeRegular)},
		PrevOuts: []txhelpers.PrevOut{{
			TxSpending:       spendHash,
			PreviousOutpoint: wire.NewOutPoint(&fundHash, 1, wire.TxTreeRegular),
		}},
	}

	p := &MempoolMonitor{
		// fundHash is deliberately absent.
		txnsStore: txhelpers.TxnsStore{
			spendHash: {Tx: wire.NewMsgTx()},
			outHash:   {Tx: wire.NewMsgTx()},
		},
	}
	p.addrMap.store = txhelpers.MempoolAddressStore{addr: stored}

	outs, n, err := p.UnconfirmedTxnsForAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(outs.TxnsStore) != 2 {
		t.Fatalf("expected 2 transactions, got n=%d, len=%d", n, len(outs.TxnsStore))
	}
	for h, tx := range outs.TxnsStore {
		if tx == nil {
			t.Errorf("nil transaction stored for %v", h)
		}
	}
	if len(outs.Outpoints) != 1 || len(outs.PrevOuts) != 1 {
		t.Fatalf("wrong outpoint counts: %d, %d", len(outs.Outpoints), len(outs.PrevOuts))
	}

	// Modifying the result must not affect the address store.
	outs.Outpoints[0] = nil
	outs.PrevOuts[0].InputIndex = 5
	outs.TxnsStore[fundHash] = nil
	if stored.Outpoints[0] == nil || stored.PrevOuts[0].InputIndex != 0 {
		t.Errorf("result shares slices with the address store")
	}
	if stored.TxnsStore != nil {
		t.Errorf("address store entry was modified")
	}

	// Unknown address.
	outs, n, err = p.UnconfirmedTxnsForAddress("DsOther")
	if err != nil || n != 0 || outs == nil || outs.Address != "DsOther" {
		t.Errorf("unexpected result for unknown address: %v, %d, %v", outs, n, err)
	}
}

// TestUnconfirmedTxnsForAddressConcurrent mirrors the writes TxHandler makes
// under addrMap.mtx while callers read lookup results without any lock. Run
// with -race.
func TestUnconfirmedTxnsForAddressConcurrent(t *testing.T) {
	const addr = "DsTestAddress"
	p := &MempoolMonitor{txnsStore: make(txhelpers.TxnsStore)}
	p.addrMap.store = txhelpers.MempoolAddressStore{
		addr: {Address: addr},
	}

	const n = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			h := testHash(byte(i))
			h[1] = byte(i >> 8)
			p.addrMap.mtx.Lock()
			outs := p.addrMap.store[addr]
			outs.Outpoints = append(outs.Outpoints, wire.NewOutPoint(&h, 0, wire.TxTreeRegular))
			p.txnsStore[h] = &txhelpers.TxWithBlockData{Tx: wire.NewMsgTx()}
			p.addrMap.mtx.Unlock()
		}
	}()

	for i := 0; i < n; i++ {
		outs, _, err := p.UnconfirmedTxnsForAddress(addr)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range outs.Outpoints {
			if _, ok := outs.TxnsStore[op.Hash]; !ok {
				t.Errorf("missing transaction %v", op.Hash)
			}
		}
	}
	wg.Wait()
}
