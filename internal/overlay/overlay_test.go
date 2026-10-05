package overlay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
)

func valid() Entry {
	eps := []skyhook.Episode{{Season: 2, Number: 1, AirDate: "2000-01-27"}}
	return Entry{
		AniDBID: 544, Title: "Oh! Super Milk-chan", Justification: "AniDB 1-12 = TVDB 2x01-2x12, same dates.",
		Set:      Set{DefaultTVDBSeason: new("2"), TMDBSeason: new("2")},
		Evidence: map[string]string{"anidb": "https://anidb.net/anime/544", "tvdb": "https://thetvdb.com/series/x/seasons/official/2"},
		Upstream: UpstreamPending, Episodes: Episodes{Regular: 12, Specials: []int{}},
		Captured: Captured{
			At: "2026-10-05", AnimeListsCommit: strings.Repeat("a", 40), NodeSHA256: strings.Repeat("b", 64),
			TVDB: &CapturedTVDB{Series: 91391, Seasons: []int{2}, Episodes: eps, SHA256: skyhook.Hash(eps)},
		},
	}
}

func TestValidate(t *testing.T) {
	e := valid()
	if err := e.Validate(); err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	for _, tc := range []struct {
		name string
		mut  func(*Entry)
	}{
		{"no anidb id", func(e *Entry) { e.AniDBID = 0 }},
		{"no justification", func(e *Entry) { e.Justification = " " }},
		{"bad upstream", func(e *Entry) { e.Upstream = "https://example.com/pull/1" }},
		{"no evidence", func(e *Entry) { e.Evidence = nil }},
		{"evidence on another host", func(e *Entry) { e.Evidence["x"] = "https://example.com/a" }},
		{"evidence over http", func(e *Entry) { e.Evidence["anidb"] = "http://anidb.net/anime/544" }},
		{"empty set", func(e *Entry) { e.Set = Set{} }},
		{"bad season", func(e *Entry) { e.Set.DefaultTVDBSeason = new("b") }},
		{"empty tvdbid", func(e *Entry) { e.Set.TVDBID = new("") }},
		{"bad tvdbid", func(e *Entry) { e.Set.TVDBID = new("-3") }},
		{"bad imdb list", func(e *Entry) { e.Set.IMDbID = new("tt0000001,nope") }},
		{"bad tmdb list", func(e *Entry) { e.Set.TMDBID = new("1,,2") }},
		{"row without a side", func(e *Entry) { e.Set.MappingList = &[]schema.Row{{AniDBSeason: 1}} }},
		{"row with a bad pair", func(e *Entry) {
			e.Set.MappingList = &[]schema.Row{{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 0}}}}
		}},
		{"short commit", func(e *Entry) { e.Captured.AnimeListsCommit = "abc" }},
		{"bad node hash", func(e *Entry) { e.Captured.NodeSHA256 = "zz" }},
		{"absent hash without create", func(e *Entry) { e.Captured.NodeSHA256 = animelists.HashAbsent }},
		{"create without absent hash", func(e *Entry) { e.Create, e.Name = true, "N" }},
		{"layout hash mismatch", func(e *Entry) { e.Captured.TVDB.SHA256 = strings.Repeat("c", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := valid()
			tc.mut(&e)
			if err := e.Validate(); !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate(%s) = %v, want ErrInvalid", tc.name, err)
			}
		})
	}
}

func write(t *testing.T, dir, name string, e Entry) {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "544.json", valid())
	got, err := LoadDir(dir)
	if err != nil || len(got) != 1 || got[0].AniDBID != 544 {
		t.Fatalf("LoadDir = %v, %v", got, err)
	}

	misnamed := t.TempDir()
	write(t, misnamed, "545.json", valid())
	if _, err := LoadDir(misnamed); !errors.Is(err, ErrInvalid) {
		t.Errorf("misnamed file: %v, want ErrInvalid", err)
	}

	unknown := t.TempDir()
	var m map[string]any
	b, _ := json.Marshal(valid())
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["extra"] = 1
	b, _ = json.Marshal(m)
	if err := os.WriteFile(filepath.Join(unknown, "544.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(unknown); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown key: %v, want ErrInvalid", err)
	}

	big := t.TempDir()
	if err := os.WriteFile(filepath.Join(big, "544.json"), make([]byte, MaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(big); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversize file: %v, want ErrInvalid", err)
	}
}

func node() *animelists.Node {
	return &animelists.Node{
		AniDBID: 544, Name: "Oh! Super Milk-chan",
		Attrs: map[string]string{"tvdbid": "91391", "defaulttvdbseason": "1", "tmdbtv": "34087", "tmdbseason": "1"},
		Rows:  []animelists.Row{{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-1;"}},
	}
}

func TestApply(t *testing.T) {
	up := node()
	e := valid()
	nodes := map[int]*animelists.Node{544: up}
	out := Apply(nodes, []Entry{e})
	got := out[544]
	if got.Attr("defaulttvdbseason") != "2" || got.Attr("tmdbseason") != "2" || got.Attr("tvdbid") != "91391" || len(got.Rows) != 1 {
		t.Errorf("patched node = %+v", got)
	}
	if up.Attr("defaulttvdbseason") != "1" {
		t.Error("Apply modified the upstream node")
	}

	clear := valid()
	clear.Set = Set{MappingList: &[]schema.Row{}}
	if rows := Patch(up, &clear).Rows; len(rows) != 0 {
		t.Errorf("mapping_list [] left %d rows", len(rows))
	}

	create := valid()
	create.AniDBID, create.Create, create.Name = 999, true, "New"
	create.Set = Set{TVDBID: new("1"), DefaultTVDBSeason: new("1")}
	c := Apply(map[int]*animelists.Node{}, []Entry{create})[999]
	if c == nil || c.Name != "New" || c.Attr("tvdbid") != "1" {
		t.Errorf("create = %+v", c)
	}
}

func TestLanded(t *testing.T) {
	e := valid()
	if Landed(node(), &e) {
		t.Error("an unchanged upstream node reads as landed")
	}
	up := node()
	up.Attrs["defaulttvdbseason"], up.Attrs["tmdbseason"] = "2", "2"
	if !Landed(up, &e) {
		t.Error("an upstream node carrying every set value does not read as landed")
	}
	if Landed(nil, &e) {
		t.Error("an absent node reads as landed")
	}
	rows := valid()
	rows.Set = Set{MappingList: &[]schema.Row{}}
	if Landed(node(), &rows) {
		t.Error("a node that still has rows reads as landed for mapping_list []")
	}
	bare := node()
	bare.Rows = nil
	if !Landed(bare, &rows) {
		t.Error("a node with no rows does not read as landed for mapping_list []")
	}
}

func TestTouchedSeasons(t *testing.T) {
	e := valid()
	p := Patch(node(), &e)
	if got := TouchedSeasons(p, &e); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("TouchedSeasons = %v, want [0 2] (default season and the row's)", got)
	}
	abs := valid()
	abs.Set = Set{DefaultTVDBSeason: new("a")}
	if got := TouchedSeasons(Patch(node(), &abs), &abs); got != nil {
		t.Errorf("absolute node TouchedSeasons = %v, want nil", got)
	}
	sp := valid()
	sp.Episodes.Specials = []int{1}
	bare := node()
	bare.Rows = nil
	if got := TouchedSeasons(Patch(bare, &sp), &sp); len(got) != 2 || got[0] != 0 {
		t.Errorf("node with AniDB specials TouchedSeasons = %v, want season 0 included", got)
	}
}
