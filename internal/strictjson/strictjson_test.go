package strictjson

import (
	"errors"
	"testing"
)

type doc struct {
	X int `json:"x"`
}

func TestDecode(t *testing.T) {
	for _, body := range []string{`{"x":1}`, " {\"x\":1}\n", "{\"x\":1}\r\n\t "} {
		var d doc
		if err := Decode([]byte(body), &d); err != nil || d.X != 1 {
			t.Errorf("Decode(%q) = %+v, %v; want x 1", body, d, err)
		}
	}
}

func TestDecodeRefusesTrailingData(t *testing.T) {
	for _, body := range []string{`{"x":1} ]`, `{"x":1}}`, `{"x":1} {"x":2}`, `{"x":1} null`, `{"x":1} 1`, `{"x":1} x`, `{"x":1},`} {
		var d doc
		if err := Decode([]byte(body), &d); !errors.Is(err, ErrTrailing) {
			t.Errorf("Decode(%q) = %v, want ErrTrailing", body, err)
		}
	}
}

func TestDecodeRefusesAMalformedDocument(t *testing.T) {
	for _, body := range []string{``, ` `, `{"x":1,"y":2}`, `{"x":"1"}`, `{"x":1`, `]`} {
		var d doc
		if err := Decode([]byte(body), &d); err == nil || errors.Is(err, ErrTrailing) {
			t.Errorf("Decode(%q) = %v, want the decoder's error", body, err)
		}
	}
}
