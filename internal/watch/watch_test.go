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
	"github.com/cplieger/animap/mapread"
)

func tv(ts ...mapread.Target) *[]mapread.Target {
	if ts == nil {
		ts = []mapread.Target{}
	}
	return &ts
}

func at(season, episode int) mapread.Target { return mapread.Target{Season: season, Episode: episode} }

func whole(aid int) []mapread.Part { return []mapread.Part{{AniDBID: aid, Kind: mapread.Regular}} }

func combined(aid int) []mapread.Part {
	return []mapread.Part{{AniDBID: aid, Kind: mapread.Regular, Combined: true}}
}

// TestGaps pins each route and scope arm against seadex-scout's reading of
// an entry: mapping.RoutedIDs and match.FindByID route a MOVIE by TMDB or
// IMDb id, then TVDB, and any other type by TVDB id, else TMDB movie ids
// (never IMDb); align.scope lets Radarr decide a movie's scope, compares a
// Sonarr match on its positive seasons, or on the whole series when an
// episode has nothing stated, and needs every episode of an entry wholly on
// season 0 stated there.
func TestGaps(t *testing.T) {
	two := mapread.Last{Regular: 2}
	for _, tc := range []struct {
		anime mapread.Anime
		entry *mapread.Entry
		name  string
		want  []Gap
	}{
		{name: "no entry", want: []Gap{noRecord}},
		{name: "no route and no AniDB id", entry: &mapread.Entry{AniListID: 1, Type: "SPECIAL"}, want: []Gap{noAniDB}},
		{name: "ambiguous, so no parts", entry: &mapread.Entry{AniListID: 1, Type: "TV", IDs: &mapread.IDs{AniDB: []int{1, 2}}}, want: []Gap{noAniDB}},
		{name: "no route", anime: mapread.Anime{Type: "OVA", Last: two}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}, want: []Gap{noTVDB}},
		{name: "no route and no type", entry: &mapread.Entry{Type: "UNKNOWN", Parts: whole(1)}, want: []Gap{unknownType}},
		{name: "a movie with no route", entry: &mapread.Entry{Type: "MOVIE", Parts: whole(1)}, want: []Gap{movieNoRoute}},
		{name: "a TMDB show routes nowhere", anime: mapread.Anime{TMDBTVID: 9}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}, want: []Gap{noTVDB}},
		{name: "IMDb never routes a non-MOVIE", anime: mapread.Anime{IMDbIDs: []string{"tt0000001"}}, entry: &mapread.Entry{Type: "SPECIAL", Parts: whole(1)}, want: []Gap{noTVDB}},
		{name: "a non-MOVIE with no TVDB id goes to Radarr", anime: mapread.Anime{TMDBMovieIDs: []int{9}}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}},
		{name: "an untyped entry goes to Radarr", anime: mapread.Anime{TMDBMovieIDs: []int{9}}, entry: &mapread.Entry{Type: "UNKNOWN", Parts: whole(1)}},
		{name: "a non-MOVIE with a TVDB id goes to Sonarr only", anime: mapread.Anime{TVDBID: 5, TMDBMovieIDs: []int{9}}, entry: &mapread.Entry{Type: "ONA", Parts: whole(1)}, want: []Gap{episodeCountUnknown}},
		{name: "a movie's TMDB id goes to Radarr", anime: mapread.Anime{TMDBMovieIDs: []int{9}}, entry: &mapread.Entry{Type: "MOVIE", Parts: whole(1)}},
		{name: "a movie's IMDb id goes to Radarr", anime: mapread.Anime{IMDbIDs: []string{"tt0000001"}}, entry: &mapread.Entry{Type: "MOVIE", Parts: whole(1)}},
		{
			name:  "Radarr decides a movie's scope whatever its season 0",
			anime: mapread.Anime{TVDBID: 5, TMDBMovieIDs: []int{9}, Last: mapread.Last{Regular: 1}, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1}}},
			entry: &mapread.Entry{Type: "MOVIE", Parts: whole(1)},
		},
		{
			name:  "a link to a movie anime's specials does not inherit its films",
			anime: mapread.Anime{TVDBID: 5, TMDBMovieIDs: []int{9}, Last: mapread.Last{Special: 1}, Episodes: []mapread.Run{{Kind: mapread.Special, Start: 1, End: 1, TVDB: tv(mapread.Target{Episode: 1, Default: true})}}},
			entry: &mapread.Entry{Type: "MOVIE", Parts: []mapread.Part{{AniDBID: 1, Kind: mapread.Special, Start: 1, End: 1}}}, want: []Gap{movieSpecialUnresolved},
		},
		{name: "a movie with no Radarr id on a season goes to Sonarr", anime: mapread.Anime{TVDBID: 5, Last: mapread.Last{Regular: 1}, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(1, 1))}}}, entry: &mapread.Entry{Type: "MOVIE", Parts: whole(1)}},
		{
			name:  "a movie special Sonarr cannot scope",
			anime: mapread.Anime{TVDBID: 5, Last: mapread.Last{Regular: 1}, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(mapread.Target{Episode: 1, Default: true})}}},
			entry: &mapread.Entry{Type: "MOVIE", Parts: whole(1)}, want: []Gap{movieSpecialUnresolved},
		},
		{name: "a movie special stated", anime: mapread.Anime{TVDBID: 5, Last: mapread.Last{Regular: 1}, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(0, 3))}}}, entry: &mapread.Entry{Type: "MOVIE", Parts: whole(1)}},
		{name: "a season by default", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 2, TVDB: tv(mapread.Target{Season: 1, Episode: 1, Default: true})}}}, entry: &mapread.Entry{Type: "TV", Parts: whole(1)}},
		{name: "absolute numbering nothing resolves", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 2}}}, entry: &mapread.Entry{Type: "TV", Parts: whole(1)}},
		{name: "partly nowhere and partly nothing stated", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1}}}, entry: &mapread.Entry{Type: "TV", Parts: whole(1)}},
		{name: "a season placed partly nowhere", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(1, 1))}}}, entry: &mapread.Entry{Type: "TV", Parts: whole(1)}},
		{name: "wholly nowhere", anime: mapread.Anime{TVDBID: 5, Last: two}, entry: &mapread.Entry{Type: "TV", Parts: whole(1)}, want: []Gap{landsNowhere}},
		{name: "season 0 stated", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 2, TVDB: tv(at(0, 4))}}}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}},
		{name: "season 0 stated and partly nowhere", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 2, End: 2, TVDB: tv(at(0, 4))}}}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}},
		{name: "season 0 by default", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 2, TVDB: tv(mapread.Target{Episode: 1, Default: true})}}}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}, want: []Gap{season0Unresolved}},
		{name: "season 0 with one episode unstated", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(0, 4))}, {Kind: mapread.Regular, Start: 2, End: 2}}}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}, want: []Gap{season0Unresolved}},
		{name: "season 0 and a regular season", anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(0, 4))}, {Kind: mapread.Regular, Start: 2, End: 2, TVDB: tv(mapread.Target{Season: 2, Episode: 1, Default: true})}}}, entry: &mapread.Entry{Type: "OVA", Parts: whole(1)}},
		{name: "a link to stated specials", anime: mapread.Anime{TVDBID: 5, Last: mapread.Last{Special: 3}, Episodes: []mapread.Run{{Kind: mapread.Special, Start: 2, End: 3, TVDB: tv(at(0, 6))}}}, entry: &mapread.Entry{Type: "ONA", Parts: []mapread.Part{{AniDBID: 1, Kind: mapread.Special, Start: 2, End: 3}}}},
		{
			name:  "a combined episode stated on season 0 by one of its AniDB episodes",
			anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(0, 4))}, {Kind: mapread.Regular, Start: 2, End: 2}}},
			entry: &mapread.Entry{Type: "OVA", Parts: combined(1)},
		},
		{
			name:  "a combined episode on season 0 only by default",
			anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(mapread.Target{Episode: 4, Default: true})}, {Kind: mapread.Regular, Start: 2, End: 2}}},
			entry: &mapread.Entry{Type: "OVA", Parts: combined(1)}, want: []Gap{season0Unresolved},
		},
		{name: "a combined episode nowhere", anime: mapread.Anime{TVDBID: 5, Last: two}, entry: &mapread.Entry{Type: "OVA", Parts: combined(1)}, want: []Gap{landsNowhere}},
		{
			name:  "a combined episode partly nowhere and partly nothing stated",
			anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 2, End: 2}}},
			entry: &mapread.Entry{Type: "OVA", Parts: combined(1)},
		},
		{
			name:  "a combined episode on season 0, by default then stated",
			anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(mapread.Target{Episode: 4, Default: true})}, {Kind: mapread.Regular, Start: 2, End: 2, TVDB: tv(at(0, 5))}}},
			entry: &mapread.Entry{Type: "OVA", Parts: combined(1)}, want: []Gap{season0Unresolved},
		},
		{
			name:  "a combined episode on season 0, stated then by default",
			anime: mapread.Anime{TVDBID: 5, Last: two, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(0, 4))}, {Kind: mapread.Regular, Start: 2, End: 2, TVDB: tv(mapread.Target{Episode: 5, Default: true})}}},
			entry: &mapread.Entry{Type: "OVA", Parts: combined(1)}, want: []Gap{season0Unresolved},
		},
		{name: "no episodes counted", anime: mapread.Anime{TVDBID: 5}, entry: &mapread.Entry{Type: "SPECIAL", Parts: whole(1)}, want: []Gap{episodeCountUnknown}},
		{name: "a combined part with no episodes counted", anime: mapread.Anime{TVDBID: 5, Type: "OVA"}, entry: &mapread.Entry{Type: "OVA", Parts: combined(1)}, want: []Gap{episodeCountUnknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.anime.AniDBID = 1
			x := mapread.NewIndex(&mapread.Document{Anime: []mapread.Anime{tc.anime}})
			if got := gaps(x, tc.entry); !slices.Equal(got, tc.want) {
				t.Errorf("gaps(%+v) = %v, want %v", tc.entry, got, tc.want)
			}
		})
	}
}

// TestGapsAcrossTVDBSeries pins the route of an entry whose parts name
// anime on two TVDB series: seadex-scout's record names a TVDB series only
// when the entry reaches exactly one, so two leave it no Sonarr route.
func TestGapsAcrossTVDBSeries(t *testing.T) {
	one := mapread.Last{Regular: 1}
	onSeason := func(aid, series int, films ...int) mapread.Anime {
		return mapread.Anime{AniDBID: aid, TVDBID: series, TMDBMovieIDs: films, Last: one, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(1, aid))}}}
	}
	both := []mapread.Part{{AniDBID: 1, Kind: mapread.Regular}, {AniDBID: 2, Kind: mapread.Regular}}
	for _, tc := range []struct {
		name  string
		entry mapread.Entry
		anime []mapread.Anime
		want  []Gap
	}{
		{name: "two series, each on a season", anime: []mapread.Anime{onSeason(1, 5), onSeason(2, 6)}, entry: mapread.Entry{Type: "TV", Parts: both}, want: []Gap{severalTVDBSeries}},
		{name: "a movie on two series with no Radarr id", anime: []mapread.Anime{onSeason(1, 5), onSeason(2, 6)}, entry: mapread.Entry{Type: "MOVIE", Parts: both}, want: []Gap{severalTVDBSeries}},
		{name: "two series and a TMDB movie go to Radarr", anime: []mapread.Anime{onSeason(1, 5, 9), onSeason(2, 6)}, entry: mapread.Entry{Type: "OVA", Parts: both}},
		{name: "two anime on one series go to Sonarr", anime: []mapread.Anime{onSeason(1, 5), onSeason(2, 5)}, entry: mapread.Entry{Type: "TV", Parts: both}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := mapread.NewIndex(&mapread.Document{Anime: tc.anime})
			if got := gaps(x, &tc.entry); !slices.Equal(got, tc.want) {
				t.Errorf("gaps(%+v) = %v, want %v", tc.entry, got, tc.want)
			}
		})
	}
}

// The same AniDB episodes as one combined entry episode and as two read
// differently, so a ruling on one never covers the other.
func TestCombinedAndSeparateEpisodesDigestApart(t *testing.T) {
	anime := mapread.Anime{AniDBID: 1, TVDBID: 5, Last: mapread.Last{Regular: 2}, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(0, 4))}, {Kind: mapread.Regular, Start: 2, End: 2}}}
	x := mapread.NewIndex(&mapread.Document{Anime: []mapread.Anime{anime}})
	one := &mapread.Entry{AniListID: 1, Type: "OVA", Parts: combined(1)}
	two := &mapread.Entry{AniListID: 1, Type: "OVA", Parts: []mapread.Part{{AniDBID: 1, Kind: mapread.Regular, Start: 1, End: 2}}}
	if g1, g2 := gaps(x, one), gaps(x, two); slices.Equal(g1, g2) {
		t.Fatalf("gaps = %v and %v, want the two shapes to read differently", g1, g2)
	}
	if d := review.RecordDigest(x, one); d == review.RecordDigest(x, two) {
		t.Errorf("both shapes digest to %s", d)
	}
}

func TestEvaluate(t *testing.T) {
	doc := &mapread.Document{
		Anime: []mapread.Anime{{AniDBID: 10, TVDBID: 5, Last: mapread.Last{Regular: 1}, Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(at(0, 1))}}}},
		Entries: []mapread.Entry{
			{AniListID: 1, Type: "TV", Parts: whole(10)},
			{AniListID: 2, Type: "SPECIAL"},
			{Type: "OVA", IDs: &mapread.IDs{MAL: []int{3}}},
			{AniListID: 4, Type: "MOVIE", Parts: []mapread.Part{{AniDBID: 10, Kind: mapread.Regular, Start: 1, End: 1}}},
		},
	}
	x := mapread.NewIndex(doc)
	got := Evaluate([]int{3, 2, 1, 4}, x)
	if len(got) != 2 || got[0].AniListID != 2 || got[1].AniListID != 3 || !slices.Equal(got[1].Gaps, []Gap{noRecord}) {
		t.Fatalf("Evaluate = %+v, want 2 (no_anidb) then 3 (no_record; entries with no AniList id are not AniList ids)", got)
	}
	if got[0].RecordSHA256 != review.RecordDigest(x, &doc.Entries[1]) || got[1].RecordSHA256 != review.RecordDigest(x, nil) {
		t.Errorf("Evaluate digests = %q, %q; want each entry's digest, absent for none", got[0].RecordSHA256, got[1].RecordSHA256)
	}
	doc.Anime[0].Episodes = nil
	x = mapread.NewIndex(doc)
	got = Evaluate([]int{1, 4}, x)
	if len(got) != 2 || got[0].AniDBID != 10 || got[0].TVDBID != 5 || got[1].AniDBID != 0 || got[1].TVDBID != 5 {
		t.Errorf("Evaluate = %+v, want AniList 1 the anime it is (AniDB 10) and the series it reaches, AniList 4 a link to it with the series only", got)
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
