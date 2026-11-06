package dcrpg

import (
	"context"
	"errors"
	"testing"

	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/dcrutil/v4"
	"github.com/decred/dcrd/wire"
)

func TestIsRetryError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"true", retryError{}, true},
		{"false", errors.New("adsf"), false},
		{"nil false", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryError(tt.err); got != tt.want {
				t.Errorf("IsRetryError() = %v, want %v", got, tt.want)
			}
		})
	}
}

type countingTxGetter struct {
	calls map[chainhash.Hash]int
	fail  bool
}

func (g *countingTxGetter) GetRawTransaction(_ context.Context, txHash *chainhash.Hash) (*dcrutil.Tx, error) {
	g.calls[*txHash]++
	if g.fail {
		return nil, errors.New("no such tx")
	}
	msgTx := wire.NewMsgTx()
	msgTx.LockTime = uint32(txHash[0])
	return dcrutil.NewTx(msgTx), nil
}

func TestRawTxMemo(t *testing.T) {
	ctx := context.Background()
	h1, h2 := chainhash.Hash{1}, chainhash.Hash{2}

	g := &countingTxGetter{calls: make(map[chainhash.Hash]int)}
	m := &rawTxMemo{c: g}
	for _, h := range []chainhash.Hash{h1, h2, h1, h1, h2} {
		tx, err := m.GetRawTransaction(ctx, &h)
		if err != nil {
			t.Fatalf("GetRawTransaction(%v): %v", h, err)
		}
		if tx.MsgTx().LockTime != uint32(h[0]) {
			t.Fatalf("GetRawTransaction(%v) returned the wrong transaction", h)
		}
	}
	if g.calls[h1] != 1 || g.calls[h2] != 1 {
		t.Errorf("expected one lookup per hash, got %d and %d", g.calls[h1], g.calls[h2])
	}

	// Failures are not remembered.
	g = &countingTxGetter{calls: make(map[chainhash.Hash]int), fail: true}
	m = &rawTxMemo{c: g}
	for range 2 {
		if _, err := m.GetRawTransaction(ctx, &h1); err == nil {
			t.Fatal("expected an error")
		}
	}
	if g.calls[h1] != 2 {
		t.Errorf("expected failed lookups to be retried, got %d calls", g.calls[h1])
	}
}
