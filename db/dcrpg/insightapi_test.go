// Copyright (c) 2019-2021, The Decred developers
// See LICENSE for details.

package dcrpg

import (
	"reflect"
	"sort"
	"testing"

	"github.com/decred/dcrd/chaincfg/chainhash"

	"github.com/decred/dcrdata/v8/db/dbtypes"
)

type txSortable struct {
	Hash chainhash.Hash
	Time int64
}

func sortTxsByTimeAndHash(txns []txSortable) {
	sort.Slice(txns, func(i, j int) bool {
		if txns[i].Time == txns[j].Time {
			return txns[i].Hash.String() < txns[j].Hash.String()
		}
		return txns[i].Time > txns[j].Time
	})
}

func Test_sortTxsByTimeAndHash(t *testing.T) {
	h0, _ := chainhash.NewHashFromStr("79936a1fb658ba249443f0caf4a6a44ce73afe16d543d4f7b8dcf847dfb21a9d")
	h1, _ := chainhash.NewHashFromStr("484e1a03f7c3795b468d7d46071e73e207aec8295917dec569a76cca981f1b95")
	tests := []struct {
		name string
		txns []txSortable
		want []txSortable
	}{
		{
			name: "ok",
			txns: []txSortable{
				{
					Hash: *h0,
					Time: 16341234,
				},
				{
					Hash: *h1,
					Time: 12341234,
				},
				{
					Hash: *h0,
					Time: 12341234,
				},
				{
					Hash: *h1,
					Time: 14341234,
				},
			},
			want: []txSortable{
				{
					Hash: *h0,
					Time: 16341234,
				},
				{
					Hash: *h1,
					Time: 14341234,
				},
				{
					Hash: *h1,
					Time: 12341234,
				},
				{
					Hash: *h0,
					Time: 12341234,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortTxsByTimeAndHash(tt.txns)
			if !reflect.DeepEqual(tt.txns, tt.want) {
				t.Errorf("txsByTimeAndHash failed. Wanted %v, got %v.",
					tt.want, tt.txns)
			}
		})
	}
}

func Test_mergeAddressTxns(t *testing.T) {
	row := func(b byte, blockTime int64) *dbtypes.AddressRowMerged {
		return &dbtypes.AddressRowMerged{TxHash: dbtypes.ChainHash{b}, TxBlockTime: blockTime}
	}
	hashes := func(bs ...byte) []chainhash.Hash {
		var hs []chainhash.Hash
		for _, b := range bs {
			hs = append(hs, chainhash.Hash{b})
		}
		return hs
	}

	tests := []struct {
		name       string
		rows       [][]*dbtypes.AddressRowMerged
		recent     int64
		wantTxs    []chainhash.Hash
		wantRecent []chainhash.Hash
	}{
		{
			name:       "single address keeps its order",
			rows:       [][]*dbtypes.AddressRowMerged{{row(3, 30), row(2, 20), row(1, 10)}},
			recent:     15,
			wantTxs:    hashes(3, 2, 1),
			wantRecent: hashes(3, 2),
		},
		{
			// Transaction 2 involves both addresses.
			name: "shared transaction listed once, newest first",
			rows: [][]*dbtypes.AddressRowMerged{
				{row(4, 40), row(2, 20)},
				{row(3, 30), row(2, 20), row(1, 10)},
			},
			recent:     25,
			wantTxs:    hashes(4, 3, 2, 1),
			wantRecent: hashes(4, 3),
		},
		{
			name: "equal block times keep address order",
			rows: [][]*dbtypes.AddressRowMerged{
				{row(5, 10)},
				{row(6, 10)},
			},
			recent:  10,
			wantTxs: hashes(5, 6),
		},
		{
			name:    "no rows",
			rows:    [][]*dbtypes.AddressRowMerged{{}, {}},
			wantTxs: []chainhash.Hash{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txs, recent := mergeAddressTxns(tt.rows, tt.recent)
			if !reflect.DeepEqual(txs, tt.wantTxs) {
				t.Errorf("txs = %v, want %v", txs, tt.wantTxs)
			}
			if !reflect.DeepEqual(recent, tt.wantRecent) {
				t.Errorf("recentTxs = %v, want %v", recent, tt.wantRecent)
			}
		})
	}
}
