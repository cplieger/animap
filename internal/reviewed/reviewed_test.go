package reviewed

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/datecheck"
)

func fingerprints() datecheck.Fingerprints {
	return datecheck.Fingerprints{Node: strings.Repeat("a", 64), TVDB: strings.Repeat("b", 64), AniDB: strings.Repeat("c", 64)}
}

func valid() Record {
	return Record{
		AniDBID: 3195, Title: "Kamichu!", Verdict: Verdict,
		Findings: []string{"date-mismatch:8", "missing-target:S1"},
		Reason:   "AniDB 8 \"Summer Break\" is TVDB 1x08 \"Summer Break\"; TVDB dates it from the DVD.",
		Evidence: []string{"https://anidb.net/anime/3195", "https://thetvdb.com/series/kamichu/seasons/official/1"},
		Captured: Captured{Date: "2026-10-13", Fingerprints: fingerprints()},
	}
}

func writeRecord(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, ok := v.(json.RawMessage)
	if !ok {
		b = mustJSON(t, v)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func withField(t *testing.T, r Record, name string, v any) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(mustJSON(t, r), &m); err != nil {
		t.Fatal(err)
	}
	m[name] = v
	return m
}

func TestLoadDirReadsValidRecords(t *testing.T) {
	dir := t.TempDir()
	writeRecord(t, dir, "3195.json", valid())
	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir = %v", err)
	}
	if r, ok := got[3195]; !ok || r.Title != "Kamichu!" || r.Captured.TVDB != strings.Repeat("b", 64) {
		t.Errorf("LoadDir = %+v, want the record for AniDB 3195 with its fingerprints", got)
	}
	none, err := LoadDir(filepath.Join(dir, "missing"))
	if err != nil || len(none) != 0 {
		t.Errorf("LoadDir(a missing directory) = %v, %v; want no records and no error", none, err)
	}
}

func TestLoadDirRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		body any
	}{
		{"malformed JSON", "3195.json", json.RawMessage(`{"anidb_id": 3195,`)},
		{"an unknown field", "3195.json", withField(t, valid(), "upstream", "TODO-PR")},
		{"trailing data", "3195.json", json.RawMessage(string(mustJSON(t, valid())) + "{}")},
		{"an id the file name does not match", "3196.json", valid()},
		{"a file name that is no id", "kamichu.json", valid()},
		{"no id", "0.json", func() Record { r := valid(); r.AniDBID = 0; return r }()},
		{"no title", "3195.json", func() Record { r := valid(); r.Title = " "; return r }()},
		{"a verdict other than fine", "3195.json", func() Record { r := valid(); r.Verdict = "wrong"; return r }()},
		{"no finding", "3195.json", func() Record { r := valid(); r.Findings = nil; return r }()},
		{"a malformed finding", "3195.json", func() Record { r := valid(); r.Findings = []string{"date-mismatch:8", "dates differ"}; return r }()},
		{"unsorted findings", "3195.json", func() Record { r := valid(); r.Findings = []string{"missing-target:S1", "date-mismatch:8"}; return r }()},
		{"a finding twice", "3195.json", func() Record { r := valid(); r.Findings = []string{"date-mismatch:8", "date-mismatch:8"}; return r }()},
		{"an empty reason", "3195.json", func() Record { r := valid(); r.Reason = "\n "; return r }()},
		{"no evidence", "3195.json", func() Record { r := valid(); r.Evidence = nil; return r }()},
		{"an http evidence link", "3195.json", func() Record { r := valid(); r.Evidence = []string{"http://anidb.net/anime/3195"}; return r }()},
		{"evidence on another host", "3195.json", func() Record { r := valid(); r.Evidence = []string{"https://example.com/3195"}; return r }()},
		{"evidence with a user name", "3195.json", func() Record {
			r := valid()
			r.Evidence = []string{"https://reviewer:pw@anidb.net/anime/3195"}
			return r
		}()},
		{"a malformed date", "3195.json", func() Record { r := valid(); r.Captured.Date = "13/10/2026"; return r }()},
		{"a short node fingerprint", "3195.json", func() Record { r := valid(); r.Captured.Node = strings.Repeat("a", 63); return r }()},
		{"an upper-case TVDB fingerprint", "3195.json", func() Record { r := valid(); r.Captured.TVDB = strings.Repeat("B", 64); return r }()},
		{"no AniDB fingerprint", "3195.json", func() Record { r := valid(); r.Captured.AniDB = ""; return r }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeRecord(t, dir, tc.file, tc.body)
			if got, err := LoadDir(dir); !errors.Is(err, errInvalid) {
				t.Errorf("LoadDir = %v, %v; want errInvalid", got, err)
			}
		})
	}
}

func TestLoadDirRejectsAnOversizedRecord(t *testing.T) {
	dir := t.TempDir()
	r := valid()
	r.Reason = strings.Repeat("x", MaxFileBytes)
	writeRecord(t, dir, "3195.json", r)
	if _, err := LoadDir(dir); !errors.Is(err, errInvalid) {
		t.Errorf("LoadDir(an oversized record) = %v, want errInvalid", err)
	}
}

// Encode writes a record at the size bound that LoadDir still reads, and
// refuses one byte more.
func TestEncodeKeepsToTheBoundLoadDirReads(t *testing.T) {
	r := valid()
	b, err := r.Encode()
	if err != nil {
		t.Fatalf("Setup: Encode(valid) = %v", err)
	}
	r.Reason += strings.Repeat("x", MaxFileBytes-len(b))
	b, err = r.Encode()
	if err != nil || len(b) != MaxFileBytes {
		t.Fatalf("Encode(a record of %d bytes) = %d bytes, %v; want it encoded", MaxFileBytes, len(b), err)
	}
	dir := t.TempDir()
	writeRecord(t, dir, "3195.json", json.RawMessage(b))
	if got, err := LoadDir(dir); err != nil || got[3195].Reason != r.Reason {
		t.Errorf("LoadDir(Encode's %d bytes) = %v; want the record read back", len(b), err)
	}
	r.Reason += "x"
	if b, err := r.Encode(); !errors.Is(err, errInvalid) || !strings.Contains(err.Error(), "bytes, at most") {
		t.Errorf("Encode(one byte over the bound) = %d bytes, %v; want errInvalid naming the bound", len(b), err)
	}
}

func TestEncodeRefusesAnInvalidRecord(t *testing.T) {
	r := valid()
	r.Reason = " "
	if b, err := r.Encode(); !errors.Is(err, errInvalid) || b != nil {
		t.Errorf("Encode(a blank reason) = %q, %v; want no bytes and errInvalid", b, err)
	}
}

func TestLoadDirRejectsTooManyRecords(t *testing.T) {
	dir := t.TempDir()
	for i := range maxRecords + 1 {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i+1)+".json"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadDir(dir); !errors.Is(err, errInvalid) || !strings.Contains(err.Error(), "at most") {
		t.Errorf("LoadDir(%d files) = %v, want a refusal naming the bound", maxRecords+1, err)
	}
}

func TestChanges(t *testing.T) {
	r := valid()
	other := strings.Repeat("d", 64)
	for _, tc := range []struct {
		name     string
		fp       func(*datecheck.Fingerprints)
		findings []string
		want     []Cause
	}{
		{"nothing moved", func(*datecheck.Fingerprints) {}, []string{"date-mismatch:8", "missing-target:S1"}, nil},
		{"a finding cleared", func(*datecheck.Fingerprints) {}, []string{"missing-target:S1"}, nil},
		{"the node", func(f *datecheck.Fingerprints) { f.Node = other }, r.Findings, []Cause{Node}},
		{"TVDB's order", func(f *datecheck.Fingerprints) { f.TVDB = other }, r.Findings, []Cause{TVDB}},
		{"AniDB's list", func(f *datecheck.Fingerprints) { f.AniDB = other }, r.Findings, []Cause{AniDB}},
		{"a new finding", func(*datecheck.Fingerprints) {}, []string{"date-mismatch:8", "date-mismatch:9"}, []Cause{NewFinding}},
		{
			"everything", func(f *datecheck.Fingerprints) { f.Node, f.TVDB, f.AniDB = other, other, other },
			[]string{"out-of-order:2"},
			[]Cause{Node, TVDB, AniDB, NewFinding},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := fingerprints()
			tc.fp(&fp)
			if got := r.Changes(fp, tc.findings); !slices.Equal(got, tc.want) {
				t.Errorf("Changes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPath(t *testing.T) {
	if got := Path(3195); got != "reviewed/3195.json" {
		t.Errorf("Path(3195) = %q, want reviewed/3195.json", got)
	}
}

func TestTheRepositoryRecordsLoad(t *testing.T) {
	if _, err := LoadDir("../../" + Dir); err != nil {
		t.Errorf("the records in %s/ do not load: %v", Dir, err)
	}
}
