package anidb

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func row() Row {
	return Row{
		AniDBID: 17281, RegularEpisodes: 2, Evidence: "https://anidb.net/anime/17281",
		Date: "2026-10-04", Justification: "AniDB lists regular episodes 1 and 2",
	}
}

func encode(t *testing.T, rows any) []byte {
	t.Helper()
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeRows(t *testing.T) {
	second := row()
	second.AniDBID, second.Evidence = 18239, "https://anidb.net/anime/18239"
	got, err := decodeRows(encode(t, []Row{row(), second}))
	if err != nil || len(got) != 2 || got[0] != row() || got[1] != second {
		t.Fatalf("decodeRows(two valid rows) = %+v, %v", got, err)
	}
	if got, err := decodeRows([]byte("[]")); err != nil || len(got) != 0 {
		t.Errorf("decodeRows([]) = %+v, %v, want no rows", got, err)
	}
}

func TestDecodeRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Row)
	}{
		{"no anidb id", func(r *Row) { r.AniDBID, r.Evidence = 0, "https://anidb.net/anime/0" }},
		{"zero episodes", func(r *Row) { r.RegularEpisodes = 0 }},
		{"negative episodes", func(r *Row) { r.RegularEpisodes = -1 }},
		{"evidence for another anime", func(r *Row) { r.Evidence = "https://anidb.net/anime/1728" }},
		{"evidence over http", func(r *Row) { r.Evidence = "http://anidb.net/anime/17281" }},
		{"evidence on another site", func(r *Row) { r.Evidence = "https://anilist.co/anime/17281" }},
		{"no date", func(r *Row) { r.Date = "" }},
		{"not a date", func(r *Row) { r.Date = "2026-13-01" }},
		{"no justification", func(r *Row) { r.Justification = " " }},
		{"justification over two lines", func(r *Row) { r.Justification = "one\ntwo" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := row()
			tc.mut(&r)
			if _, err := decodeRows(encode(t, []Row{r})); !errors.Is(err, errInvalid) {
				t.Errorf("decodeRows(%+v) = %v, want errInvalid", r, err)
			}
		})
	}
	earlier := row()
	earlier.AniDBID, earlier.Evidence = 100, "https://anidb.net/anime/100"
	many := make([]Row, maxRows+1)
	for i := range many {
		many[i] = row()
		many[i].AniDBID, many[i].Evidence = i+1, "https://anidb.net/anime/"+strconv.Itoa(i+1)
	}
	for name, body := range map[string][]byte{
		"unknown field":   []byte(`[{"anidb_id":17281,"regular_episodes":2,"evidence":"https://anidb.net/anime/17281","date":"2026-10-04","justification":"x","specials":1}]`),
		"an object":       []byte(`{"anidb_id":17281}`),
		"null":            []byte(`null`),
		"a second list":   append(encode(t, []Row{row()}), []byte("[]")...),
		"a trailing ]":    append(encode(t, []Row{row()}), []byte("]")...),
		"a trailing }":    append(encode(t, []Row{row()}), []byte("}")...),
		"an id twice":     encode(t, []Row{row(), row()}),
		"out of order":    encode(t, []Row{row(), earlier}),
		"over the bound":  encode(t, many),
		"a string count":  []byte(`[{"anidb_id":17281,"regular_episodes":"2","evidence":"https://anidb.net/anime/17281","date":"2026-10-04","justification":"x"}]`),
		"a decimal count": []byte(`[{"anidb_id":17281,"regular_episodes":2.5,"evidence":"https://anidb.net/anime/17281","date":"2026-10-04","justification":"x"}]`),
	} {
		if _, err := decodeRows(body); !errors.Is(err, errInvalid) {
			t.Errorf("decodeRows(%s) = %v, want errInvalid", name, err)
		}
	}
	if _, err := decodeRows(encode(t, many[:maxRows])); err != nil {
		t.Errorf("decodeRows(%d rows, the bound) = %v, want accepted", maxRows, err)
	}
}

func TestLoadRows(t *testing.T) {
	dir := t.TempDir()
	if rows, err := LoadRows(filepath.Join(dir, "absent.json")); err != nil || rows != nil {
		t.Errorf("LoadRows(absent) = %+v, %v, want no rows and no error", rows, err)
	}
	p := filepath.Join(dir, "counts.json")
	if err := os.WriteFile(p, encode(t, []Row{row()}), 0o600); err != nil {
		t.Fatal(err)
	}
	if rows, err := LoadRows(p); err != nil || len(rows) != 1 || rows[0] != row() {
		t.Errorf("LoadRows(one row) = %+v, %v", rows, err)
	}
	if err := os.WriteFile(p, []byte("["+strings.Repeat(" ", maxCountsFileSize)+"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRows(p); !errors.Is(err, errInvalid) {
		t.Errorf("LoadRows(over %d bytes) = %v, want errInvalid", maxCountsFileSize, err)
	}
}
