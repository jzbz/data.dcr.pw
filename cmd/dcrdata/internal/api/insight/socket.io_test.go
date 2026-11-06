package insight

import (
	"encoding/json"
	"testing"

	"github.com/decred/dcrd/chaincfg/v3"
	chainjson "github.com/decred/dcrd/rpc/jsonrpc/types/v4"
	"github.com/decred/dcrd/wire"
)

func TestMarshalInsightTx(t *testing.T) {
	isv := InsightSocketVout{
		Address: "DsZQaCQES5vh3JmcyyFokJYz3aSw8Sm1dsQ",
		Value:   13741789,
	}

	b, err := json.Marshal(isv)
	if err != nil {
		t.Errorf("Failed to marshal InsightSocketVout: %v", err)
	}

	t.Logf("%s", string(b))
	expectedJSON := `{"Address":"DsZQaCQES5vh3JmcyyFokJYz3aSw8Sm1dsQ","Value":13741789}`
	if string(b) != expectedJSON {
		t.Errorf("json.Marshal of InsightSocketVout incorrect. "+
			"Expected %s, got %s", expectedJSON, string(b))
	}
}

func TestMarshalInsightTxSlice(t *testing.T) {
	isv := []InsightSocketVout{
		{
			Address: "DsZQaCQES5vh3JmcyyFokJYz3aSw8Sm1dsQ",
			Value:   13741789,
		},
		{
			Address: "DsidNx5RnC7jMHdvUSEV54WANwMyKbyGtjh",
			Value:   1000000,
		},
	}

	b, err := json.Marshal(isv)
	if err != nil {
		t.Errorf("Failed to marshal InsightSocketVout: %v", err)
	}

	t.Logf("%s", string(b))
	expectedJSON := `[{"DsZQaCQES5vh3JmcyyFokJYz3aSw8Sm1dsQ":13741789},` +
		`{"DsidNx5RnC7jMHdvUSEV54WANwMyKbyGtjh":1000000}]`
	if string(b) != expectedJSON {
		t.Errorf("json.Marshal of InsightSocketVout incorrect. "+
			"Expected %s, got %s", expectedJSON, string(b))
	}
}

func TestSendNewTxVoutsMismatch(t *testing.T) {
	soc := &SocketServer{
		params:           chaincfg.MainNetParams(),
		watchedAddresses: &roomSubscriptionCounter{c: make(map[string]int)},
	}
	tx := wire.NewMsgTx()
	tx.AddTxOut(wire.NewTxOut(1, nil))
	tx.AddTxOut(wire.NewTxOut(2, nil))
	// One vout for a transaction with two outputs must be rejected rather than
	// indexed out of range.
	if err := soc.sendNewTx(tx, make([]chainjson.Vout, 1)); err == nil {
		t.Fatal("expected an error for mismatched vouts")
	}
}
