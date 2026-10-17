package strictjson

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/jsoncap/v2"
)

type doc struct {
	Inner struct {
		Y string `json:"y"`
	} `json:"inner"`
	X int `json:"x"`
}

// reads are the two decodes, which share every rule but undeclared members.
var reads = []struct {
	read func([]byte, int, any) error
	name string
}{{Decode, "Decode"}, {Unmarshal, "Unmarshal"}}

func TestReadsAcceptAWellFormedBody(t *testing.T) {
	for _, r := range reads {
		for _, body := range []string{`{"x":1}`, " {\"x\":1}\n", "{\"x\":1}\r\n\t ", `{"x":1,"inner":{"y":"翼\uFFFD"}}`} {
			var d doc
			if err := r.read([]byte(body), len(body), &d); err != nil || d.X != 1 {
				t.Errorf("%s(%q) = %+v, %v; want x 1", r.name, body, d, err)
			}
		}
	}
}

func TestReadsRefuse(t *testing.T) {
	for _, c := range []struct {
		is   error
		name string
		body string
		want string
	}{
		{name: "an invalid byte in a member name", body: "{\"x\xff\":1}", is: ErrNotUTF8},
		{name: "an invalid byte in a string", body: "{\"x\":1,\"inner\":{\"y\":\"a\xffb\"}}", is: ErrNotUTF8},
		{name: "an encoded surrogate", body: "{\"x\":1,\"inner\":{\"y\":\"\xed\xa0\x80\"}}", is: ErrNotUTF8},
		{name: "a truncated rune", body: "{\"x\":1,\"inner\":{\"y\":\"\xe7\xbf\"}}", is: ErrNotUTF8},
		{name: "a member twice", body: `{"x":1,"x":2}`, is: jsoncap.ErrDuplicateKey},
		{name: "a member twice in another case", body: `{"x":1,"X":2}`, is: jsoncap.ErrDuplicateKey},
		{name: "a member twice in a nested object", body: `{"x":1,"inner":{"y":"a","y":"b"}}`, is: jsoncap.ErrDuplicateKey},
		{name: "a member twice in an object in an array", body: `[{"a":1,"A":2}]`, is: jsoncap.ErrDuplicateKey},
		{name: "a second value", body: `{"x":1} {"x":2}`, want: "trailing data"},
		{name: "a stray closer", body: `{"x":1}}`, want: "trailing data"},
		{name: "a stray bracket", body: `{"x":1} ]`, want: "trailing data"},
		{name: "a trailing comma", body: `{"x":1},`, want: "trailing data"},
		{name: "a trailing scalar", body: `{"x":1} null`, want: "trailing data"},
		{name: "an empty body", body: ``, want: "EOF"},
		{name: "an unclosed object", body: `{"x":1`, want: "unexpected end"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, r := range reads {
				var d doc
				err := r.read([]byte(c.body), 1<<10, &d)
				switch {
				case err == nil:
					t.Errorf("%s(%q) accepted %+v", r.name, c.body, d)
				case c.is != nil && !errors.Is(err, c.is):
					t.Errorf("%s(%q) = %v, want %v", r.name, c.body, err, c.is)
				case c.want != "" && !strings.Contains(err.Error(), c.want):
					t.Errorf("%s(%q) = %v, want %q in it", r.name, c.body, err, c.want)
				}
			}
		})
	}
}

func TestReadsHoldTheByteCap(t *testing.T) {
	body := []byte(`{"x":1}`)
	for _, r := range reads {
		var d doc
		if err := r.read(body, len(body), &d); err != nil {
			t.Errorf("%s at the cap = %v, want accepted", r.name, err)
		}
		if err := r.read(body, len(body)-1, &d); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s one byte over the cap = %v, want ErrTooLarge", r.name, err)
		}
	}
}

func TestDecodeRefusesAnUndeclaredMemberAndUnmarshalSkipsIt(t *testing.T) {
	body := []byte(`{"x":1,"y":2}`)
	var d doc
	if err := Decode(body, len(body), &d); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("Decode = %v, want the undeclared member refused", err)
	}
	d = doc{}
	if err := Unmarshal(body, len(body), &d); err != nil || d.X != 1 {
		t.Errorf("Unmarshal = %+v, %v; want x 1, y skipped", d, err)
	}
}

func TestDecodeRefusesAMistypedValue(t *testing.T) {
	for _, r := range reads {
		var d doc
		if err := r.read([]byte(`{"x":"1"}`), 1<<10, &d); err == nil {
			t.Errorf("%s accepted a string for an int", r.name)
		}
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := ReadFile(path, 7); err != nil || string(body) != `{"x":1}` {
		t.Errorf("ReadFile at the cap = %q, %v", body, err)
	}
	if _, err := ReadFile(path, 6); !errors.Is(err, ErrTooLarge) {
		t.Errorf("ReadFile over the cap = %v, want ErrTooLarge", err)
	}
	if _, err := ReadFile(filepath.Join(dir, "missing.json"), 7); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile of a missing file = %v, want os.ErrNotExist", err)
	}
}
