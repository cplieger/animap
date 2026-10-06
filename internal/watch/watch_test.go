package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/schema"
)

func TestGaps(t *testing.T) {
	s0, s1 := new(0), new(1)
	row := func(pairs ...[]int) []schema.Row {
		return []schema.Row{{AniDBSeason: 1, TVDBSeason: new(0), Episodes: pairs}}
	}
	for _, tc := range []struct {
		rec  *schema.Record
		name string
		want []Gap
	}{
		{nil, "no record", []Gap{NoRecord}},
		{&schema.Record{AniListID: 1, Type: "SPECIAL"}, "type-only", []Gap{NoAniDB}},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5, TVDBSeason: s1}, "series season", nil},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5, TVDBAbsolute: true}, "absolute run", nil},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5}, "series without a season", []Gap{NoTVDBSeason}},
		{&schema.Record{AniDBID: 1, Type: "OVA"}, "no tvdb", []Gap{NoTVDB}},
		{&schema.Record{AniDBID: 1, Type: "UNKNOWN"}, "unknown type", []Gap{UnknownType}},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 2, TVDBID: 5, TVDBSeason: s0, MappingList: row([]int{1, 3}, []int{2})}, "season 0 every episode rowed, -0 counts", nil},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 2, TVDBID: 5, TVDBSeason: s0, MappingList: row([]int{1, 3})}, "season 0 one episode unrowed", []Gap{Season0Unresolved}},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 3, TVDBID: 5, TVDBSeason: s0, TVDBEpisodeOffset: new(4)}, "season 0 explicit offset", nil},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 3, TVDBID: 5, TVDBSeason: s0}, "season 0 no offset no rows", []Gap{Season0Unresolved}},
		{&schema.Record{AniDBID: 1, Type: "SPECIAL", TVDBID: 5, TVDBSeason: s0}, "season 0 count unknown", []Gap{EpisodeCountUnknown}},
		{&schema.Record{
			AniDBID: 1, Type: "OVA", Episodes: 4, TVDBID: 5, TVDBSeason: s0,
			MappingList: []schema.Row{{AniDBSeason: 1, TVDBSeason: new(0), Start: 1, End: 4, Offset: 2}},
		}, "season 0 ranged row", nil},
		{&schema.Record{
			AniDBID: 1, Type: "OVA", Episodes: 1, TVDBID: 5, TVDBSeason: s0,
			MappingList: []schema.Row{{AniDBSeason: 1, TVDBSeason: new(1), Episodes: [][]int{{1, 1}}}},
		}, "a row on another season does not resolve", []Gap{Season0Unresolved}},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", TMDBMovieIDs: []int{9}}, "movie Radarr route", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", IMDbIDs: []string{"tt0000001"}}, "movie IMDb route", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", TVDBID: 5, TVDBSeason: s1}, "movie Sonarr season route", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE"}, "movie no route", []Gap{MovieNoRoute}},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", TVDBID: 5, TVDBSeason: s0, MappingList: row([]int{1, 7})}, "movie special resolved", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", TVDBID: 5, TVDBSeason: s0, TMDBMovieIDs: []int{9}}, "movie special unresolved despite Radarr", []Gap{MovieSpecialUnresolved}},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", TVDBID: 5, TVDBSeason: s0}, "movie special unresolved, no route", []Gap{MovieSpecialUnresolved, MovieNoRoute}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Gaps(tc.rec); !slices.Equal(got, tc.want) {
				t.Errorf("Gaps(%+v) = %v, want %v", tc.rec, got, tc.want)
			}
		})
	}
}

func TestEvaluate(t *testing.T) {
	doc := &schema.Document{Records: []schema.Record{
		{AniListID: 1, AniDBID: 10, Type: "TV", TVDBID: 5, TVDBSeason: new(1)},
		{AniListID: 2, Type: "SPECIAL"},
		{AniDBID: 3, Type: "TV"},
	}}
	got := Evaluate([]int{3, 2, 1}, doc)
	if len(got) != 2 || got[0].AniListID != 2 || got[1].AniListID != 3 || !slices.Equal(got[1].Gaps, []Gap{NoRecord}) {
		t.Errorf("Evaluate = %+v, want 2 (no_anidb) then 3 (no_record; AniDB-keyed records are not AniList ids)", got)
	}
}

func TestClassify(t *testing.T) {
	backlog := &Backlog{Entries: []Entry{
		{AniListID: 1, Gaps: []Gap{Season0Unresolved}},
		{AniListID: 2, Gaps: []Gap{MovieSpecialUnresolved, MovieNoRoute}},
		{AniListID: 3, Gaps: []Gap{NoTVDB}},
	}}
	c := Classify([]Entry{
		{AniListID: 1, Gaps: []Gap{Season0Unresolved}},
		{AniListID: 2, Gaps: []Gap{MovieNoRoute}},
		{AniListID: 4, Gaps: []Gap{NoTVDB}},
		{AniListID: 1 + 4, Gaps: nil},
	}, backlog, nil)
	ids := func(es []Entry) []int {
		var out []int
		for _, e := range es {
			out = append(out, e.AniListID)
		}
		return out
	}
	if !slices.Equal(ids(c.Known), []int{1, 2}) || !slices.Equal(ids(c.New), []int{4, 5}) || !slices.Equal(ids(c.Resolved), []int{3}) {
		t.Errorf("Classify = known %v new %v resolved %v", ids(c.Known), ids(c.New), ids(c.Resolved))
	}
	grown := Classify([]Entry{{AniListID: 1, Gaps: []Gap{Season0Unresolved, NoTVDBSeason}}}, backlog, nil)
	if !slices.Equal(ids(grown.New), []int{1}) {
		t.Errorf("a backlog entry with a new gap kind is not new: %+v", grown)
	}
}

type pages map[string]string

func (p pages) Get(_ context.Context, url string, _ int64) ([]byte, error) {
	for k, v := range p {
		if strings.Contains(url, "page="+k+"&") {
			return []byte(v), nil
		}
	}
	return nil, fmt.Errorf("unexpected %s", url)
}

func TestFetchIDs(t *testing.T) {
	cfg := &Config{ListURL: "https://releases.moe/api/collections/entries/records", IDField: "alID", PerPage: 2, MaxPages: 3}
	page := func(total, pages int, ids ...int) string {
		items := make([]string, 0, len(ids))
		for _, id := range ids {
			items = append(items, fmt.Sprintf(`{"alID":%d}`, id))
		}
		return fmt.Sprintf(`{"page":1,"totalItems":%d,"totalPages":%d,"items":[%s]}`, total, pages, strings.Join(items, ","))
	}
	got, err := FetchIDs(t.Context(), pages{"1": page(3, 2, 10, 11), "2": page(3, 2, 12)}, cfg)
	if err != nil || !slices.Equal(got, []int{10, 11, 12}) {
		t.Fatalf("FetchIDs = %v, %v", got, err)
	}
	for _, tc := range []struct {
		name string
		p    pages
	}{
		{"total mismatch", pages{"1": page(4, 2, 10, 11), "2": page(4, 2, 12)}},
		{"repeated id", pages{"1": page(3, 2, 10, 11), "2": page(3, 2, 11)}},
		{"non-positive id", pages{"1": page(2, 1, 10, 0)}},
		{"total moves", pages{"1": page(3, 2, 10, 11), "2": page(4, 2, 12, 13)}},
		{"too many pages", pages{"1": page(8, 4, 1, 2), "2": page(8, 4, 3, 4), "3": page(8, 4, 5, 6), "4": page(8, 4, 7, 8)}},
		{"malformed page", pages{"1": "not json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := FetchIDs(t.Context(), tc.p, cfg); !errors.Is(err, ErrIncomplete) {
				t.Errorf("FetchIDs(%s) = %v, want ErrIncomplete", tc.name, err)
			}
		})
	}
}

func TestLoadSeadexConfig(t *testing.T) {
	c, err := LoadConfig("../../watch/seadex.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := EntryLink(c.EntryURL, 154587); got != "https://releases.moe/154587" {
		t.Errorf("EntryLink = %s", got)
	}
	b, err := LoadBacklog("../../watch/backlog.json")
	if err != nil || b.WatchSet != c.Name {
		t.Errorf("LoadBacklog = %+v, %v", b, err)
	}
}

// A gap is tracked by one list: an id in both lists is reported resolved
// and cleared at once, so closing it asks for two edits.
func TestTrackedListsTrackEachGapOnce(t *testing.T) {
	seen := map[int]string{}
	for _, path := range []string{"../../watch/backlog.json", "../../checks/unmappable.json"} {
		b, err := LoadBacklog(path)
		if err != nil {
			t.Fatalf("LoadBacklog(%s): %v", path, err)
		}
		for i, e := range b.Entries {
			if i > 0 && e.AniListID <= b.Entries[i-1].AniListID {
				t.Errorf("%s: AniList %d follows %d, want ascending ids", path, e.AniListID, b.Entries[i-1].AniListID)
			}
			if prev, dup := seen[e.AniListID]; dup {
				t.Errorf("AniList %d is in %s and %s, want one list", e.AniListID, prev, path)
			}
			seen[e.AniListID] = path
			if len(e.Gaps) == 0 || e.Detail == "" {
				t.Errorf("%s: AniList %d has gaps %v and detail %q, want both", path, e.AniListID, e.Gaps, e.Detail)
			}
			if wantReason := strings.HasSuffix(path, "unmappable.json"); wantReason != (e.Reason == "unmappable" || e.Reason == "upstream") {
				t.Errorf("%s: AniList %d has reason %q", path, e.AniListID, e.Reason)
			}
		}
	}
}

func TestClassifyUnmappable(t *testing.T) {
	unmappable := &Backlog{Entries: []Entry{
		{AniListID: 1, Gaps: []Gap{NoTVDB}, Reason: "unmappable"},
		{AniListID: 2, Gaps: []Gap{NoAniDB}, Reason: "upstream"},
		{AniListID: 3, Gaps: []Gap{Season0Unresolved}, Reason: "unmappable"},
	}}
	c := Classify([]Entry{
		{AniListID: 1, Gaps: []Gap{NoTVDB}},
		{AniListID: 3, Gaps: []Gap{Season0Unresolved, EpisodeCountUnknown}},
		{AniListID: 4, Gaps: []Gap{NoTVDB}},
	}, &Backlog{}, unmappable)
	ids := func(es []Entry) (out []int) {
		for _, e := range es {
			out = append(out, e.AniListID)
		}
		return out
	}
	if !slices.Equal(ids(c.Unmappable), []int{1}) || !slices.Equal(ids(c.New), []int{3, 4}) || !slices.Equal(ids(c.Cleared), []int{2}) {
		t.Errorf("Classify = unmappable %v new %v cleared %v; want 1 listed, 3 (a new gap kind) and 4 new, 2 cleared", ids(c.Unmappable), ids(c.New), ids(c.Cleared))
	}
	if c.Unmappable[0].Reason != "unmappable" {
		t.Errorf("a listed entry lost its reason: %+v", c.Unmappable[0])
	}
}

func TestGapsBridgedRecord(t *testing.T) {
	bridged := &schema.Record{
		AniListID: 9, Type: "SPECIAL", Episodes: 1, TVDBID: 5, TVDBSeason: new(0),
		AniDBParent: &schema.ParentSpecials{AniDBID: 100, Specials: []int{1}},
		MappingList: []schema.Row{{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 8}}}},
	}
	if g := Gaps(bridged); g != nil {
		t.Errorf("Gaps(a bridged special) = %v, want fully mapped", g)
	}
}

func TestLoadBacklogRefusesTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backlog.json")
	doc := `{"version":1,"watch_set":"seadex","captured_at":"2026-10-06","entries":[]}`
	for name, trailer := range map[string]string{"a stray ]": "]", "a stray }": "}", "a second document": doc} {
		if err := os.WriteFile(path, []byte(doc+"\n"+trailer), 0o600); err != nil {
			t.Fatal(err)
		}
		if b, err := LoadBacklog(path); err == nil {
			t.Errorf("LoadBacklog(a backlog followed by %s) = %+v, want an error", name, b)
		}
	}
}

func TestLoadConfigRefusesTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seadex.json")
	doc := `{"name":"seadex","list_url":"https://releases.moe/api/collections/entries/records","id_field":"alID","per_page":500,"max_pages":40,"entry_url":"https://releases.moe/{id}"}`
	for name, trailer := range map[string]string{"a stray ]": "]", "a stray }": "}", "a second document": doc} {
		if err := os.WriteFile(path, []byte(doc+"\n"+trailer), 0o600); err != nil {
			t.Fatal(err)
		}
		if c, err := LoadConfig(path); err == nil {
			t.Errorf("LoadConfig(a config followed by %s) = %+v, want an error", name, c)
		}
	}
}
