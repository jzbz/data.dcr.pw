// Copyright (c) 2019-2021, The Decred developers
// See LICENSE for details.

package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/go-chi/chi/v5"
)

func TestGetAddressCtx(t *testing.T) {
	activeNetParams := chaincfg.MainNetParams()
	type args struct {
		maxAddrs int
		addrs    []string
	}
	tests := []struct {
		testName string
		args     args
		want     []string
		wantErr  bool
		errMsg   string
		wantCode int
		wantBody string // response body when the middleware rejects
	}{
		{
			testName: "ok2",
			args:     args{2, []string{"Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx"}},
			want:     []string{"Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx"},
			wantErr:  false,
		},
		{
			testName: "ok1",
			args:     args{1, []string{"Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx"}},
			want:     []string{"Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx"},
			wantErr:  false,
		},
		{
			testName: "bad0",
			args:     args{0, []string{"Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx"}},
			want:     nil, // not []string{}
			wantErr:  true,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: "too many address",
		},
		{
			testName: "bad3",
			args: args{2, []string{"Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx",
				"DseXBL6g6GxvfYAnKqdao2f7WkXDmYTYW87",
				"Dsi8hhDzr3SvcGcv4NEGvRqFkwZ2ncRhukk"}},
			want:     nil,
			wantErr:  true,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: "address parse error",
		},
		{
			// This tests that the middleware counts before removing dups.
			testName: "bad_dup3",
			args: args{2, []string{"Dsi8hhDzr3SvcGcv4NEGvRqFkwZ2ncRhukk",
				"DseXBL6g6GxvfYAnKqdao2f7WkXDmYTYW87",
				"Dsi8hhDzr3SvcGcv4NEGvRqFkwZ2ncRhukk"}},
			want:     nil,
			wantErr:  true,
			wantCode: http.StatusUnprocessableEntity,
			wantBody: "address parse error",
		},
		{
			// This tests that the middleware counts removes dups.
			testName: "ok_dup3",
			args: args{3, []string{"Dsi8hhDzr3SvcGcv4NEGvRqFkwZ2ncRhukk",
				"DseXBL6g6GxvfYAnKqdao2f7WkXDmYTYW87",
				"Dsi8hhDzr3SvcGcv4NEGvRqFkwZ2ncRhukk"}},
			want: []string{"Dsi8hhDzr3SvcGcv4NEGvRqFkwZ2ncRhukk",
				"DseXBL6g6GxvfYAnKqdao2f7WkXDmYTYW87"},
			wantErr: false,
		},
		{
			testName: "invalid",
			args:     args{2, []string{"DcxxxxcGjmENx4DhNqDctW5wJCVyT3Qeqkx"}},
			want:     nil,
			wantErr:  true,
			errMsg:   `invalid address "DcxxxxcGjmENx4DhNqDctW5wJCVyT3Qeqkx" for this network: failed to decode address "DcxxxxcGjmENx4DhNqDctW5wJCVyT3Qeqkx": checksum error`},
		{
			testName: "wrong_net",
			args:     args{2, []string{"TsWmwignm9Q6iBQMSHw9WhBeR5wgUPpD14Q"}},
			want:     nil,
			wantErr:  true,
			errMsg:   `invalid address "TsWmwignm9Q6iBQMSHw9WhBeR5wgUPpD14Q" for this network: address "TsWmwignm9Q6iBQMSHw9WhBeR5wgUPpD14Q" is not a supported type`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			router := chi.NewRouter()
			tAddrCtx := AddressPathCtxN(tt.args.maxAddrs)
			var run bool
			router.With(tAddrCtx).Get("/{address}", func(w http.ResponseWriter, r *http.Request) {
				run = true
				got, err := GetAddressCtx(r, activeNetParams) //, tt.args.maxAddrs)
				if (err != nil) != tt.wantErr {
					t.Errorf("GetAddressCtx() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("GetAddressCtx() = %v, want %v", got, tt.want)
				}
				if err != nil && tt.errMsg != err.Error() {
					t.Fatalf(`GetAddressCtx() error = "%v", expected "%s"`, err, tt.errMsg)
				}
			})
			writer := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/"+strings.Join(tt.args.addrs, ","), nil)
			router.ServeHTTP(writer, req)
			switch tt.wantCode {
			case 0, 200:
				if !run {
					t.Errorf("handler not reached")
				}
				if writer.Code != 200 {
					t.Errorf("expected 200, got %d", writer.Code)
				}
			default:
				if run {
					t.Errorf("handler reached, but the middleware should have rejected the request")
				}
				if tt.wantCode != writer.Code {
					t.Errorf("expected response code %d, got %d", tt.wantCode, writer.Code)
				}
				if body := strings.TrimSpace(writer.Body.String()); body != tt.wantBody {
					t.Errorf("expected response body %q, got %q", tt.wantBody, body)
				}
			}
		})
	}
}

func TestTransactionsCtx(t *testing.T) {
	const addr = "Dcur2mcGjmENx4DhNqDctW5wJCVyT3Qeqkx"
	const hash = "0000000000000000000000000000000000000000000000000000000000000001"
	tests := []struct {
		name     string
		query    string
		wantAddr bool
		wantHash bool
	}{
		{"neither", "", false, false},
		{"address", "?address=" + addr, true, false},
		{"block", "?block=" + hash, false, true},
		{"both", "?address=" + addr + "&block=" + hash, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			h := TransactionsCtx(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, ok := r.Context().Value(CtxAddress).([]string)
				if ok != tt.wantAddr {
					t.Errorf("address in context = %v, want %v", ok, tt.wantAddr)
				}
				_, err := GetBlockHashCtx(r)
				if (err == nil) != tt.wantHash {
					t.Errorf("block hash in context = %v, want %v", err == nil, tt.wantHash)
				}
			}))
			req := httptest.NewRequest("GET", "/txs"+tt.query, nil)
			h.ServeHTTP(httptest.NewRecorder(), req)
			if calls != 1 {
				t.Errorf("next handler called %d times, want 1", calls)
			}
		})
	}
}

func TestPostTxnsCtxLimit(t *testing.T) {
	const txid = "0000000000000000000000000000000000000000000000000000000000000001"
	body := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = `"` + txid + `"`
		}
		return `{"transactions":[` + strings.Join(ids, ",") + `]}`
	}
	for _, tc := range []struct {
		n        int
		wantCode int
	}{
		{1, http.StatusOK},
		{maxTxnsPerPost, http.StatusOK},
		{maxTxnsPerPost + 1, http.StatusUnprocessableEntity},
	} {
		var run bool
		h := PostTxnsCtx(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			run = true
			hashes, err := GetTxnsCtx(r)
			if err != nil || len(hashes) != tc.n {
				t.Errorf("GetTxnsCtx: got %d hashes, err = %v; want %d", len(hashes), err, tc.n)
			}
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/txs", strings.NewReader(body(tc.n))))
		if rec.Code != tc.wantCode {
			t.Errorf("%d txns: got code %d, want %d", tc.n, rec.Code, tc.wantCode)
		}
		if run != (tc.wantCode == http.StatusOK) {
			t.Errorf("%d txns: handler run = %v", tc.n, run)
		}
	}
}

func TestOriginalRequestURI(t *testing.T) {
	tests := []struct {
		header   string
		wantPath string
	}{
		{"", "/insight/api"},
		{"/api/status", "/api/status"},
		{"/a%2Fb?x=1", "/a/b"},
		{"http://example.invalid//attacker.example", "/insight/api"},
		{"//attacker.example/x", "/insight/api"},
		{"/\\attacker.example", "/insight/api"},
		{"https://attacker.example/x", "/insight/api"},
		{"relative/path", "/insight/api"},
		{"mailto:a@b", "/insight/api"},
	}
	for _, tt := range tests {
		var got string
		h := OriginalRequestURI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Path
		}))
		req := httptest.NewRequest("GET", "/insight/api", nil)
		if tt.header != "" {
			req.Header.Set("X-Original-Request-URI", tt.header)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got != tt.wantPath {
			t.Errorf("X-Original-Request-URI %q: path = %q, want %q", tt.header, got, tt.wantPath)
		}
	}
}

func TestRateLimiterIgnoresForwardedHeaders(t *testing.T) {
	h := RateLimiter(1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	var limited int
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		req.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited != 4 {
		t.Errorf("%d of 5 requests from one address were limited, want 4", limited)
	}

	// A non-positive limit disables the limiter.
	h = RateLimiter(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("disabled limiter returned code %d", rec.Code)
		}
	}
}
