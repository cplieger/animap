package release

import (
	"bytes"
	"math"
	"os"
	"strconv"
	"testing"
)

// FuzzDecode_conformsToTheSchema holds Decode to the JSON Schema on any
// input, as TestDecodeConformsToTheSchema does on the goldens' mutations.
func FuzzDecode_conformsToTheSchema(f *testing.F) {
	for _, path := range goldens {
		body, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(body)
		f.Add(bytes.Replace(body, []byte(`"notice":"a`), []byte("\"notice\":\"\xffa"), 1))
		f.Add(bytes.Replace(body, []byte(`"notice":"a`), []byte("\"notice\":\"é翼🎌\uFFFDa"), 1))
		f.Add(bytes.Replace(body, []byte(`"generated_at":"2026-10-05T00:00:00Z"`), []byte(`"generated_at":"2026-10-05T00:00:00+00:00"`), 1))
		f.Add(bytes.Replace(body, []byte(`"version":2`), []byte(`"version":`+strconv.Itoa(math.MinInt)), 1))
	}
	f.Add([]byte(`{"version":1,"records":[]}`))
	f.Add([]byte(`{"version":2,"generated_at":"","sources":{},"attribution":{},"anime":[],"entries":[]}`))
	f.Add([]byte(`{"version":2}`))
	f.Add([]byte(`null`))
	sch := compileSchema(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, problem := conformance(sch, data, false); problem != nil {
			t.Fatal(problem)
		}
	})
}
