// Copyright (c) 2026, The Decred developers
// See LICENSE for details.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/go-chi/chi/v5"

	m "github.com/decred/dcrdata/cmd/dcrdata/internal/middleware"
	apitypes "github.com/decred/dcrdata/v8/api/types"
	"github.com/decred/dcrdata/v8/db/dbtypes"
)

// stubDataSource implements only the DataSource methods exercised by these
// tests; calling any other method panics on the nil embedded interface.
type stubDataSource struct {
	DataSource
	poolInfoRangeCalls int
	poolValSizeCalls   int
	addrRows           []*dbtypes.AddressRowCompact
}

func (s *stubDataSource) GetPoolInfoRange(_ context.Context, idx0, idx1 int) []apitypes.TicketPoolInfo {
	s.poolInfoRangeCalls++
	tpis := make([]apitypes.TicketPoolInfo, 0, idx1-idx0+1)
	for h := idx0; h <= idx1; h++ {
		tpis = append(tpis, apitypes.TicketPoolInfo{Height: uint32(h)})
	}
	return tpis
}

func (s *stubDataSource) GetPoolValAndSizeRange(_ context.Context, idx0, idx1 int) ([]float64, []uint32) {
	s.poolValSizeCalls++
	n := idx1 - idx0 + 1
	return make([]float64, n), make([]uint32, n)
}

func (s *stubDataSource) AddressRowsCompact(context.Context, string) ([]*dbtypes.AddressRowCompact, error) {
	return s.addrRows, nil
}

func TestExistsMaskToBools(t *testing.T) {
	tests := []struct {
		name    string
		mask    string
		n       int
		want    []bool
		wantErr bool
	}{
		{"empty", "", 2, []bool{false, false}, false},
		{"one byte", "05", 3, []bool{true, false, true}, false},
		{"eight bytes", "0100000000000080", 64, func() []bool {
			b := make([]bool, 64)
			b[0], b[63] = true, true
			return b
		}(), false},
		{"nine bytes", "010000000000000000", 1, nil, true},
		{"bad hex", "zz", 1, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := existsMaskToBools(tt.mask, tt.n)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTicketPoolInfoRange(t *testing.T) {
	ds := &stubDataSource{}
	app := &appContext{DataSource: ds}
	mux := chi.NewRouter()
	mux.With(m.BlockIndex0PathCtx, m.BlockIndexPathCtx).Get("/r/{idx0}/{idx}", app.getTicketPoolInfoRange)

	tests := []struct {
		name       string
		path       string
		wantCode   int
		wantArrays bool // TicketPoolValsAndSizes rather than []TicketPoolInfo
	}{
		{"omitted", "/r/10/20", http.StatusOK, false},
		{"arrays=true", "/r/10/20?arrays=true", http.StatusOK, true},
		{"arrays=false", "/r/10/20?arrays=false", http.StatusOK, false},
		{"arrays invalid", "/r/10/20?arrays=maybe", http.StatusBadRequest, false},
		{"objects at limit", fmt.Sprintf("/r/0/%d", maxBlockRangeCount-1), http.StatusOK, false},
		{"objects over limit", fmt.Sprintf("/r/0/%d", maxBlockRangeCount), http.StatusBadRequest, false},
		{"arrays over limit", fmt.Sprintf("/r/0/%d?arrays=true", maxBlockRangeCount), http.StatusOK, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds.poolInfoRangeCalls, ds.poolValSizeCalls = 0, 0
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCode != http.StatusOK {
				if ds.poolInfoRangeCalls+ds.poolValSizeCalls != 0 {
					t.Errorf("rejected request reached the data source")
				}
				return
			}
			if tt.wantArrays {
				var resp apitypes.TicketPoolValsAndSizes
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || ds.poolValSizeCalls != 1 {
					t.Errorf("expected arrays form: err=%v calls=%d", err, ds.poolValSizeCalls)
				}
				return
			}
			var resp []apitypes.TicketPoolInfo
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || ds.poolInfoRangeCalls != 1 {
				t.Errorf("expected object form: err=%v calls=%d", err, ds.poolInfoRangeCalls)
			}
		})
	}
}

// flushCounter counts calls to Flush on a ResponseRecorder.
type flushCounter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushCounter) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

func TestAddressIoCsv(t *testing.T) {
	const numRows = 2500
	rows := make([]*dbtypes.AddressRowCompact, numRows)
	for i := range rows {
		rows[i] = &dbtypes.AddressRowCompact{
			TxBlockTime:    int64(i),
			TxVinVoutIndex: uint32(i),
			ValidMainChain: true,
			IsFunding:      i%2 == 0,
			Value:          uint64(i) * 1e8,
		}
	}
	app := &appContext{
		DataSource: &stubDataSource{addrRows: rows},
		Params:     chaincfg.MainNetParams(),
		Status:     apitypes.NewStatus(1, 0, 0, "", "mainnet"),
	}
	mux := chi.NewRouter()
	mux.With(m.AddressPathCtxN(1)).Get("/io/{address}", app.addressIoCsvNoCR)

	w := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/io/Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("body does not end with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != numRows+1 {
		t.Fatalf("got %d lines, want %d", len(lines), numRows+1)
	}
	zeroHash := dbtypes.ChainHash{}.String()
	if want := zeroHash + ",-1,2499,1,2499,2499,Regular,"; lines[numRows] != want {
		t.Errorf("last row = %q, want %q", lines[numRows], want)
	}
	// One flush after the header, then one per 1000 rows; the remainder is
	// written when the handler returns.
	if w.flushes != 3 {
		t.Errorf("flushes = %d, want 3", w.flushes)
	}
}
