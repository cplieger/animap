package offlinedb

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const meta = `{"license":{"name":"Open Data Commons Open Database License (ODbL) v1.0","url":"u"},"lastUpdate":"2026-10-04"}`

func TestLoadMini(t *testing.T) {
	entries, err := Load("../../testdata/aod-mini.jsonl", DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 11 {
		t.Fatalf("Load = %d entries, want 11", len(entries))
	}
	want := Entry{Type: "TV", AniList: []int{100}, AniDB: []int{10}, MAL: []int{1000}, Episodes: 12}
	if !reflect.DeepEqual(entries[0], want) {
		t.Errorf("entries[0] = %+v, want %+v", entries[0], want)
	}
	if got := entries[3].AniList; !reflect.DeepEqual(got, []int{102, 103}) {
		t.Errorf("two AniList sources read as %v", got)
	}
}

func TestReadRefuses(t *testing.T) {
	small := Limits{MaxFileBytes: 1 << 20, MaxLineBytes: 256, MaxEntries: 2, MaxSources: 2}
	line := `{"sources":["https://anilist.co/anime/1"],"type":"TV"}`
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{"no licence", `{"license":{"name":"MIT"}}` + "\n" + line, errLicense},
		{"malformed metadata", "nope\n" + line, errFormat},
		{"malformed entry", meta + "\n" + line + "\n{bad", errFormat},
		{"empty", "", errFormat},
		{"no entries", meta + "\n", errFormat},
		{"line over the cap", meta + "\n" + `{"sources":["` + strings.Repeat("x", 300) + `"]}`, errTooLarge},
		{"too many entries", meta + "\n" + strings.Repeat(line+"\n", 3), errTooLarge},
		{"too many sources", meta + "\n" + `{"sources":["a","b","c"]}`, errTooLarge},
		{"unpublished type", meta + "\n" + `{"sources":["https://anilist.co/anime/1"],"type":"TV\n@x"}`, errFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := read(strings.NewReader(tc.body), small); !errors.Is(err, tc.want) {
				t.Errorf("read(%s) = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

func TestLoadRefusesOversizeFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db.jsonl")
	if err := os.WriteFile(p, []byte(meta+"\n"+`{"sources":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p, Limits{MaxFileBytes: 10, MaxLineBytes: 1 << 10, MaxEntries: 10, MaxSources: 10}); !errors.Is(err, errTooLarge) {
		t.Errorf("Load(oversize) = %v, want errTooLarge", err)
	}
}

func TestSourceIDs(t *testing.T) {
	body := meta + "\n" + `{"sources":["https://anilist.co/anime/7","https://anilist.co/anime/7","https://anidb.net/anime/0","https://anidb.net/anime/x","https://kitsu.app/anime/3"],"episodes":-1}`
	entries, err := read(strings.NewReader(body), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if e := entries[0]; !reflect.DeepEqual(e.AniList, []int{7}) || e.AniDB != nil || e.Episodes != 0 {
		t.Errorf("entry = %+v, want AniList [7], no AniDB, episodes 0", e)
	}
}
