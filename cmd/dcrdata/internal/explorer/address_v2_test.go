// Copyright (c) 2024, The Decred developers
// See LICENSE for details.

package explorer

import (
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/decred/dcrd/dcrutil/v4"
	"github.com/decred/dcrdata/v8/db/dbtypes"
)

// TestV2AddressTemplateRenders renders the redesigned address template through
// the real template machinery + funcMap with mock AddressInfo data, verifying
// the balance tiles, chart placeholder and signed-amount history without a DB.
func TestV2AddressTemplateRenders(t *testing.T) {
	assets := NewAssetManager("../../public")
	fm := makeTemplateFuncMap(chaincfg.MainNetParams(), assets)
	tm := newTemplates("../../views_v2", false, []string{"chrome"}, fm)
	if err := tm.addTemplate("address"); err != nil {
		t.Fatalf("addTemplate(address): %v", err)
	}

	h, _ := chainhash.NewHashFromStr("a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90")
	page := &addressPage{
		CommonPageData: tdCommon(),
		Data: &dbtypes.AddressInfo{
			Address:  "DsXyz1aBcDeFgHiJkLmNoPqRsTuVwXyZ12",
			TxnCount: 42,
			// Page-scoped sums over the displayed rows only. The stat tiles
			// must NOT use these; they must come from Balance below. The
			// values are distinctive so a regression is unambiguous.
			AmountReceived: dcrutil.Amount(11111111111),
			AmountSent:     dcrutil.Amount(22222222222),
			AmountUnspent:  dcrutil.Amount(33333333333),
			Balance: &dbtypes.AddressBalance{
				NumUnspent: 7,
				// Fractional atoms: the tiles must render these exactly,
				// never compressed to k/M magnitudes.
				TotalUnspent: 60000012345678, // 600000.12345678 DCR
				TotalSpent:   90000000000000, // 900000 DCR
			},
			Transactions: []*dbtypes.AddressTx{
				{TxID: dbtypes.ChainHash(*h), TxType: "Regular", IsFunding: true, ReceivedTotal: 500.12345678,
					Confirmations: 37, Time: dbtypes.NewTimeDef(time.Now())},
				{TxID: dbtypes.ChainHash(*h), TxType: "Regular", IsFunding: false, SentTotal: 120.5,
					Confirmations: 12, Time: dbtypes.NewTimeDef(time.Now().Add(-time.Hour))},
			},
		},
	}

	out, err := tm.exec("address", page)
	if err != nil {
		t.Fatalf("exec(address): %v", err)
	}

	for _, want := range []string{
		"/css/v2/main.css?v=",                // pipeline
		"class=\"sidebar\"",                  // shell
		"DsXyz1aBcDeFgHiJkLmNoPqRsTuVwXyZ12", // address
		"Balance",                            // balance tile
		// Balance: comma-grouped, two decimals visible, remaining precision
		// in the hover-revealed span — nothing rounded away.
		">600,000<span class=\"frac\">.12<span class=\"frac-x\">345678</span></span><",
		// Received = TotalSpent + TotalUnspent, same treatment.
		">1,500,000<span class=\"frac\">.12<span class=\"frac-x\">345678</span></span><",
		// Sent: whole amount — two zero decimals, no hover residue.
		">900,000<span class=\"frac\">.00</span><",
		// Tx row credit and debit: same exact formatting.
		"+500<span class=\"frac\">.12<span class=\"frac-x\">345678</span></span><",
		"120<span class=\"frac\">.50</span><",
		"Transaction history",       // history section
		"data-controller=\"chart\"", // balance-over-time chart
		"/amountflow/day",           // chart data source
		"amt-in",                    // signed incoming amount
		"amt-out",                   // signed outgoing amount
		"/tx/",                      // tx links to v2
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered v2 address page missing %q", want)
		}
	}

	// The stat tiles must reflect the full-history balance, not the sums of
	// only the transactions on the current page (which can even go negative
	// on a spend-heavy page), and must never be magnitude-compressed.
	for _, stale := range []string{">111.11111111<", ">222.22222222<", ">333.33333333<", "600.00k", "1.50M", "900.00k"} {
		if strings.Contains(out, stale) {
			t.Errorf("rendered v2 address page shows page-scoped or rounded amount %q", stale)
		}
	}
}
