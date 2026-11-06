package types

import (
	"bytes"
	"testing"
)

func TestHexBytes_UnmarshalJSON(t *testing.T) {
	in := []byte(`"deadbeef"`)
	var hb HexBytes
	err := hb.UnmarshalJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xde, 0xad, 0xbe, 0xef}; !bytes.Equal(hb, want) {
		t.Errorf("decoded %x, want %x", []byte(hb), want)
	}
	out, err := hb.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("encoded %s, want %s", out, in)
	}

	// Empty input decodes to an empty slice.
	var empty HexBytes
	if err = empty.UnmarshalJSON([]byte(`""`)); err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("decoded %x, want empty", []byte(empty))
	}

	// Invalid hex and non-string JSON are errors.
	for _, bad := range []string{`"xyz"`, `"abc"`, `1234`} {
		var hb HexBytes
		if err = hb.UnmarshalJSON([]byte(bad)); err == nil {
			t.Errorf("no error decoding %s", bad)
		}
	}
}
