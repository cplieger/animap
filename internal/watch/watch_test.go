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

	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/schema"
)

// TestGaps pins each route and scope arm against seadex-scout at 3c723d0:
// mapping.RoutedIDs and match.FindByID route a MOVIE by TMDB or IMDb id,
// then TVDB, and any other type by TVDB id, else TMDB movie ids (never
// IMDb); align.scope lets Radarr decide a movie's scope and scopes a
// Sonarr match by RecordSeason, falling back to the whole season bucket
// when the placement lands nowhere.
func TestGaps(t *testing.T) {
	s0, s1 := new(0), new(1)
	placed := []schema.Segment{{Start: 1, End: 1, Season: new(0), Episode: new(3)}, {Start: 2, End: 2}}
	onSeason1 := []schema.Segment{{Start: 1, End: 1, Season: new(1), Episode: new(1)}}
	nowhere := []schema.Segment{{Start: 1, End: 2}}
	for _, tc := range []struct {
		rec  *schema.Record
		name string
		want []Gap
	}{
		{nil, "no record", []Gap{noRecord}},
		{&schema.Record{AniListID: 1, Type: "SPECIAL"}, "no route and no AniDB id", []Gap{noAniDB}},
		{&schema.Record{AniDBID: 1, Type: "OVA"}, "no route", []Gap{noTVDB}},
		{&schema.Record{AniDBID: 1, Type: "UNKNOWN"}, "no route and no type", []Gap{unknownType}},
		{&schema.Record{AniDBID: 1, Type: "MOVIE"}, "a movie with no route", []Gap{movieNoRoute}},
		{&schema.Record{AniDBID: 1, Type: "OVA", TMDBTVID: 9}, "a TMDB show routes nowhere", []Gap{noTVDB}},
		{&schema.Record{AniDBID: 1, Type: "SPECIAL", IMDbIDs: []string{"tt0000001"}}, "IMDb never routes a non-MOVIE", []Gap{noTVDB}},
		{&schema.Record{AniDBID: 1, Type: "OVA", TMDBMovieIDs: []int{9}}, "a non-MOVIE with no TVDB id goes to Radarr", nil},
		{&schema.Record{AniDBID: 1, Type: "UNKNOWN", TMDBMovieIDs: []int{9}}, "an untyped record goes to Radarr", nil},
		{&schema.Record{AniDBID: 1, Type: "ONA", TVDBID: 5, TMDBMovieIDs: []int{9}}, "a non-MOVIE with a TVDB id goes to Sonarr only", []Gap{noTVDBSeason}},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", TMDBMovieIDs: []int{9}}, "a movie's TMDB id goes to Radarr", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", IMDbIDs: []string{"tt0000001"}}, "a movie's IMDb id goes to Radarr", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", Episodes: 1, TVDBID: 5, TVDBSeason: s0, TMDBMovieIDs: []int{9}}, "Radarr decides a movie's scope whatever its season 0", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", TVDBID: 5, TVDBSeason: s1}, "a movie with no Radarr id goes to Sonarr", nil},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", Episodes: 1, TVDBID: 5, TVDBSeason: s0}, "a movie special Sonarr cannot scope", []Gap{movieSpecialUnresolved}},
		{&schema.Record{AniDBID: 1, Type: "MOVIE", Episodes: 1, TVDBID: 5, TVDBSeason: s0, TVDBPlacement: placed}, "a movie special placed", nil},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5, TVDBSeason: s1}, "a season", nil},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5, TVDBAbsolute: true}, "absolute numbering", nil},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5}, "no season and not absolute", []Gap{noTVDBSeason}},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5, TVDBSeason: s1, TVDBPlacement: placed}, "a season placed partly nowhere", nil},
		{&schema.Record{AniDBID: 1, Type: "TV", TVDBID: 5, TVDBSeason: new(5), TVDBPlacement: nowhere}, "a season placed wholly nowhere", []Gap{landsNowhere}},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 2, TVDBID: 5, TVDBSeason: s0, TVDBPlacement: placed}, "season 0 placed on specials", nil},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 2, TVDBID: 5, TVDBSeason: s0, TVDBPlacement: nowhere}, "season 0 placed wholly nowhere", []Gap{landsNowhere}},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 1, TVDBID: 5, TVDBSeason: s0, TVDBPlacement: onSeason1}, "season 0 placed on a regular season", []Gap{season0Unresolved}},
		{&schema.Record{AniDBID: 1, Type: "OVA", Episodes: 3, TVDBID: 5, TVDBSeason: s0}, "season 0 not placed", []Gap{season0Unresolved}},
		{&schema.Record{AniDBID: 1, Type: "SPECIAL", TVDBID: 5, TVDBSeason: s0}, "season 0 with no count", []Gap{episodeCountUnknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gaps(tc.rec); !slices.Equal(got, tc.want) {
				t.Errorf("gaps(%+v) = %v, want %v", tc.rec, got, tc.want)
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
	if len(got) != 2 || got[0].AniListID != 2 || got[1].AniListID != 3 || !slices.Equal(got[1].Gaps, []Gap{noRecord}) {
		t.Fatalf("Evaluate = %+v, want 2 (no_anidb) then 3 (no_record; AniDB-keyed records are not AniList ids)", got)
	}
	if got[0].RecordSHA256 != review.RecordDigest(&doc.Records[1]) || got[1].RecordSHA256 != review.RecordDigest(nil) {
		t.Errorf("Evaluate digests = %q, %q; want each record's digest, absent for no record", got[0].RecordSHA256, got[1].RecordSHA256)
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
	cfg := &Config{ListURL: "https://seadex.moe/api/collections/entries/records", IDField: "alID", PerPage: 2, MaxPages: 3}
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
			if _, err := FetchIDs(t.Context(), tc.p, cfg); !errors.Is(err, errIncomplete) {
				t.Errorf("FetchIDs(%s) = %v, want errIncomplete", tc.name, err)
			}
		})
	}
}

func TestLoadSeadexConfig(t *testing.T) {
	c, err := LoadConfig("../../watch/seadex.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := EntryLink(c.EntryURL, 154587); got != "https://seadex.moe/154587" {
		t.Errorf("EntryLink = %s", got)
	}
}

func TestGapsLinkedRecord(t *testing.T) {
	row := func(season int, pairs ...[]int) []schema.Row {
		return []schema.Row{{AniDBSeason: 1, TVDBSeason: new(season), Episodes: pairs}}
	}
	for _, tc := range []struct {
		rec  *schema.Record
		name string
		want []Gap
	}{
		{&schema.Record{
			AniListID: 9, Type: "SPECIAL", Episodes: 1, TVDBID: 5, TVDBSeason: new(0),
			AniDBParent: &schema.ParentSpecials{AniDBID: 100, Specials: []int{1}}, MappingList: row(0, []int{1, 8}),
			TVDBPlacement: []schema.Segment{{Start: 1, End: 1, Season: new(0), Episode: new(8)}},
		}, "a linked special", nil},
		{&schema.Record{
			AniListID: 1563, Type: "TV", Episodes: 29, TVDBID: 71634, TVDBSeason: new(2), MappingList: row(2, []int{1, 1}),
			TVDBPlacement: []schema.Segment{{Start: 1, End: 29, Season: new(2), Episode: new(1)}},
		}, "a linked regular run", nil},
		{&schema.Record{
			AniListID: 2593, Type: "MOVIE", Episodes: 1, TVDBID: 82099, TVDBSeason: new(1), MappingList: row(1, []int{1, 1}),
			TVDBPlacement: []schema.Segment{{Start: 1, End: 1, Season: new(1), Episode: new(1)}},
		}, "a linked film", nil},
		{&schema.Record{
			AniListID: 2269, Type: "SPECIAL", Episodes: 3, TVDBID: 254931, TVDBSeason: new(0), MappingList: row(0, []int{1, 2}, []int{2, 3}, []int{3, 4}),
			TVDBPlacement: []schema.Segment{{Start: 1, End: 3, Season: new(0), Episode: new(2)}},
		}, "a linked compilation", nil},
		{&schema.Record{AniListID: 9, Type: "OVA", Episodes: 1, TMDBMovieIDs: []int{9}}, "no AniDB id and no link, a Radarr route", nil},
		{&schema.Record{AniListID: 9, Type: "OVA", Episodes: 1}, "no AniDB id and no link", []Gap{noAniDB}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if g := gaps(tc.rec); !slices.Equal(g, tc.want) {
				t.Errorf("gaps = %v, want %v", g, tc.want)
			}
		})
	}
}

func TestLoadConfigRefusesTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seadex.json")
	doc := `{"name":"seadex","list_url":"https://seadex.moe/api/collections/entries/records","id_field":"alID","per_page":500,"max_pages":40,"entry_url":"https://seadex.moe/{id}"}`
	for name, trailer := range map[string]string{"a stray ]": "]", "a stray }": "}", "a second document": doc} {
		if err := os.WriteFile(path, []byte(doc+"\n"+trailer), 0o600); err != nil {
			t.Fatal(err)
		}
		if c, err := LoadConfig(path); err == nil {
			t.Errorf("LoadConfig(a config followed by %s) = %+v, want an error", name, c)
		}
	}
}
