package join

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/release"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/mapread"
)

func fixtures(t *testing.T) ([]offlinedb.Entry, map[int]*animelists.Node) {
	t.Helper()
	entries, err := offlinedb.Load("../../testdata/aod-mini.jsonl", offlinedb.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../../testdata/anime-list-mini.xml")
	if err != nil {
		t.Fatal(err)
	}
	l, err := animelists.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return entries, l.Nodes
}

// databaseFacts counts each AniDB id as the database does, with a mirror
// that lists the anime in listed.
func databaseFacts(entries []offlinedb.Entry, listed map[int]anidb.Anime) *anidb.Facts {
	database := map[int]int{}
	for i := range entries {
		for _, ad := range entries[i].AniDB {
			if _, ok := database[ad]; !ok {
				database[ad] = entries[i].Episodes
			}
		}
	}
	return anidb.NewFacts(&anidb.Snapshot{Commit: strings.Repeat("a", 40), Anime: listed}, nil, database)
}

func buildMini(t *testing.T) ([]mapread.Anime, []mapread.Entry, Stats) {
	t.Helper()
	entries, nodes := fixtures(t)
	anime, rows, st := Build(&Inputs{Entries: entries, Nodes: nodes, Facts: databaseFacts(entries, nil)})
	return anime, rows, st
}

func byAniList(entries []mapread.Entry) map[int]mapread.Entry {
	out := map[int]mapread.Entry{}
	for _, e := range entries {
		if e.AniListID > 0 {
			out[e.AniListID] = e
		}
	}
	return out
}

func animeOf(rows []mapread.Anime, aid int) *mapread.Anime {
	for i := range rows {
		if rows[i].AniDBID == aid {
			return &rows[i]
		}
	}
	return nil
}

// regularIDs is regular episodes 1 to len(ids), each with its AniDB id.
func regularIDs(ids ...int) []anidb.Episode {
	out := make([]anidb.Episode, len(ids))
	for i, id := range ids {
		out[i] = anidb.Episode{Number: i + 1, ID: id}
	}
	return out
}

func whole(aid int) []mapread.Part { return []mapread.Part{{AniDBID: aid, Kind: mapread.Regular}} }

func TestBuildEntries(t *testing.T) {
	_, entries, st := buildMini(t)
	al := byAniList(entries)
	want100 := mapread.Entry{AniListID: 100, Type: "TV", Episodes: 12, IDs: &mapread.IDs{AniDB: []int{10}, MAL: []int{1000}}, Parts: whole(10)}
	if !reflect.DeepEqual(al[100], want100) {
		t.Errorf("entry 100 = %+v, want %+v", al[100], want100)
	}
	if e := al[101]; e.Type != "SPECIAL" || e.Parts != nil || !reflect.DeepEqual(e.IDs, &mapread.IDs{MAL: []int{1001}}) {
		t.Errorf("AniList-only entry 101 = %+v, want its MAL id and no parts", e)
	}
	if !reflect.DeepEqual(al[102].Parts, whole(13)) || !reflect.DeepEqual(al[103].Parts, whole(13)) {
		t.Errorf("both AniList ids of one database entry must be AniDB 13: %+v %+v", al[102], al[103])
	}
	for _, tc := range []struct {
		al    int
		anidb []int
	}{{104, []int{14, 15}}, {105, []int{16, 17}}} {
		if e := al[tc.al]; e.Parts != nil || !slices.Equal(e.IDs.AniDB, tc.anidb) {
			t.Errorf("ambiguous AniList %d = %+v, want no parts and candidates %v", tc.al, e, tc.anidb)
		}
	}
	others := entries[len(al):]
	wantOthers := []mapread.Entry{
		{Type: "OVA", Episodes: 2, IDs: &mapread.IDs{AniDB: []int{12}, Kitsu: []int{5}}, Parts: whole(12)},
		{Type: "TV", Episodes: 3, IDs: &mapread.IDs{MAL: []int{1007}}},
	}
	if !reflect.DeepEqual(others, wantOthers) {
		t.Errorf("entries with no AniList id = %+v, want %+v", others, wantOthers)
	}
	if st.AmbiguousAniDB != 2 || st.AniListEntries != 9 || st.OtherEntries != 2 || st.UnparsedRows != 1 {
		t.Errorf("Stats = %+v, want 2 ambiguous, 9 AniList entries, 2 others and 1 unparsed row", st)
	}
}

func TestBuildAnimeRowsFromTheList(t *testing.T) {
	anime, _, _ := buildMini(t)
	ids := make([]int, 0, len(anime))
	for _, a := range anime {
		ids = append(ids, a.AniDBID)
	}
	if want := []int{10, 12, 13, 14, 15, 16, 17, 18, 19, 20, 30}; !slices.Equal(ids, want) {
		t.Errorf("anime rows = %v, want %v: a node that says nothing has no row", ids, want)
	}
	if a := animeOf(anime, 13); !slices.Equal(a.TMDBMovieIDs, []int{77, 78}) || !slices.Equal(a.IMDbIDs, []string{"tt0000002"}) {
		t.Errorf("comma lists = %v %v, want de-duplicated valid ids", a.TMDBMovieIDs, a.IMDbIDs)
	}
	if a := animeOf(anime, 18); a.TVDBID != 0 || a.Episodes != nil || !slices.Equal(a.TMDBMovieIDs, []int{555}) || a.Type != "MOVIE" {
		t.Errorf("tvdbid=movie row = %+v, want no TVDB id or runs, its TMDB movie kept", a)
	}
	if a := animeOf(anime, 30); a.Type != "" || a.TVDBID != 5006 || a.Episodes != nil {
		t.Errorf("node-only row = %+v, want its TVDB id and no runs, since nothing counts its episodes", a)
	}
	tv := func(ts ...mapread.Target) *[]mapread.Target { return targets(ts) }
	want10 := []mapread.Run{
		{Kind: mapread.Regular, Start: 1, End: 12, TVDB: tv(mapread.Target{Season: 1, Episode: 1, Default: true}), TMDB: tv(mapread.Target{Season: 1, Episode: 1, Default: true})},
		{Kind: mapread.Special, Start: 1, End: 1, TVDB: tv(mapread.Target{Season: 0, Episode: 2}), TMDB: tv(mapread.Target{Season: 0, Episode: 1, Default: true})},
		{Kind: mapread.Special, Start: 3, End: 3, TVDB: tv(), TMDB: tv(mapread.Target{Season: 0, Episode: 3, Default: true})},
	}
	if a := animeOf(anime, 10); !reflect.DeepEqual(a.Episodes, want10) || a.Last != (mapread.Last{Regular: 12, Special: 3}) || a.EpisodesListed {
		t.Errorf("AniDB 10 = last %+v, listed %t, runs %s; want last regular 12 and special 3 (row-named), no AniDB list, and no run for special 2, which no row names, runs %s",
			a.Last, a.EpisodesListed, runsText(a.Episodes), runsText(want10))
	}
	if a := animeOf(anime, 12); !reflect.DeepEqual(a.Episodes, []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 2, TVDB: tv(mapread.Target{Season: 0, Episode: 1})}}) {
		t.Errorf("AniDB 12 runs = %s, want a written offset of 0 stated, its unparseable row claiming nothing", runsText(a.Episodes))
	}
	if a := animeOf(anime, 19); !reflect.DeepEqual(a.Episodes, []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 100}}) {
		t.Errorf("absolute AniDB 19 runs = %s, want one run stating nothing", runsText(a.Episodes))
	}
	want20 := []mapread.Run{
		{Kind: mapread.Regular, Start: 1, End: 1, TVDB: tv(mapread.Target{Season: 0, Episode: 4}, mapread.Target{Season: 0, Episode: 5})},
		{Kind: mapread.Regular, Start: 2, End: 2, TVDB: tv(mapread.Target{Season: 0, Episode: 6})},
		{Kind: mapread.Regular, Start: 3, End: 3, TVDB: tv(mapread.Target{Season: 0, Episode: 3, Default: true})},
	}
	if a := animeOf(anime, 20); !reflect.DeepEqual(a.Episodes, want20) {
		t.Errorf("AniDB 20 runs = %s, want %s", runsText(a.Episodes), runsText(want20))
	}
}

func targets(ts []mapread.Target) *[]mapread.Target {
	if ts == nil {
		ts = []mapread.Target{}
	}
	return &ts
}

func runsText(runs []mapread.Run) string {
	b, _ := mapread.Encode(&mapread.Document{Anime: []mapread.Anime{{Episodes: runs}}})
	return string(b)
}

// placeFixture is one node on TVDB 500 and TMDB 600 with every statement
// Place can lead to, an overlay entry capturing its absolute layout, a
// mirror listing specials, every extra kind, extras past their block and
// a special numbered past 100, anime-offline-database entries naming every
// service it publishes, and a link to specials and one to the whole anime.
func placeFixture(t *testing.T) *Inputs {
	t.Helper()
	n := &animelists.Node{AniDBID: 1, Attrs: map[string]string{"tvdbid": "500", "defaulttvdbseason": "a", "tmdbtv": "600", "tmdbseason": "1", "tmdboffset": "-2", "tmdbid": "700", "imdbid": "tt0000009"}, Rows: []animelists.Row{
		{Attrs: map[string]string{"anidbseason": "1", "tvdbseason": "2"}, Text: ";1-3+4;2-0;6-1;7-1;"},
		{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-0;101-9;102-0;401-7;150-3;201-11;301-12;"},
		{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "3", "start": "2", "end": "3", "offset": "-5"}},
		{Attrs: map[string]string{"anidbseason": "1", "tmdbseason": "4"}, Text: ";4-0;"},
		{Attrs: map[string]string{"anidbseason": "1", "tmdbseason": "4", "start": "2", "end": "2", "offset": "-5"}},
	}}
	mirror := anidb.Anime{
		Regular: regularIDs(1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008, 1009), Finished: true,
		Specials: []anidb.Episode{{Number: 1, ID: 2001}, {Number: 2, ID: 2002}, {Number: 4, ID: 2004}, {Number: 5, ID: 2005}, {Number: 101, ID: 2101}},
		Extras: []anidb.Extra{
			{Kind: anidb.Credit, Number: 1, ID: 3001},
			{Kind: anidb.Credit, Number: 2, ID: 3002, SeasonZero: 102},
			{Kind: anidb.Credit, Number: 120, ID: 3120},
			{Kind: anidb.Promotion, Number: 1, ID: 4001, SeasonZero: 201},
			{Kind: anidb.Parody, Number: 1, ID: 5001, SeasonZero: 301},
			{Kind: anidb.Other, Number: 1, ID: 6001, SeasonZero: 401},
			{Kind: anidb.Other, Number: 2, ID: 6002, SeasonZero: 402},
		},
	}
	captured := overlay.Entry{AniDBID: 1, Captured: overlay.Captured{TVDB: &overlay.CapturedTVDB{Series: 500, Episodes: []skyhook.Episode{
		{Season: 1, Number: 1, Absolute: 3}, {Season: 1, Number: 2, Absolute: 4}, {Season: 1, Number: 3, Absolute: 5},
	}}}}
	entries, err := offlinedb.Load("../../testdata/aod-place.jsonl", offlinedb.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	specials := overlay.Link{AniListID: 77, Parts: []overlay.Part{{AniDBID: 1, Specials: []int{4, 150, 201, 301, 402}}}}
	whole := overlay.Link{AniListID: 76, Parts: []overlay.Part{{AniDBID: 1, Whole: true}}}
	return &Inputs{
		Nodes:   map[int]*animelists.Node{1: n},
		Facts:   anidb.NewFacts(&anidb.Snapshot{Commit: strings.Repeat("a", 40), Anime: map[int]anidb.Anime{1: mirror}}, nil, nil),
		Entries: entries,
		Overlay: []overlay.Entry{captured},
		Links: applyAll(
			Linked{Link: &specials, Compiled: overlay.Compiled{AniList: 77, Series: 500, Targets: []int{4, 3, 11, 12, 402}, Claims: []overlay.Claim{
				{Parent: 1, AniDBSeason: 0, Number: 4, Target: [2]int{0, 4}},
				{Parent: 1, AniDBSeason: 0, Number: 150, Target: [2]int{0, 3}},
				{Parent: 1, AniDBSeason: 0, Number: 201, Target: [2]int{0, 11}},
				{Parent: 1, AniDBSeason: 0, Number: 301, Target: [2]int{0, 12}},
				{Parent: 1, AniDBSeason: 0, Number: 402, Target: [2]int{0, 402}},
			}}},
			Linked{Link: &whole, Compiled: overlay.Compiled{AniList: 76, Series: 500, Targets: []int{1}}},
		),
	}
}

// applyAll is links as a build with no database entries applies them:
// every one.
func applyAll(links ...Linked) Applied {
	a, _ := Applicable(nil, nil, links)
	return a
}

// Every statement comes from Node.Place, a link's claim or a capture: a
// row's targets, its -0 and a target below 1; a written offset and one
// below 1; an absolute number with and without a capture; and the season 0
// numbers of each kind.
func TestBuildRunsFromPlace(t *testing.T) {
	anime, _, _ := Build(placeFixture(t))
	a := animeOf(anime, 1)
	tv := func(ts ...mapread.Target) *[]mapread.Target { return targets(ts) }
	at := func(s, e int) mapread.Target { return mapread.Target{Season: s, Episode: e} }
	dflt := func(s, e int) mapread.Target { return mapread.Target{Season: s, Episode: e, Default: true} }
	got := map[[2]any]*mapread.Run{}
	for i := range a.Episodes {
		r := &a.Episodes[i]
		for k := r.Start; k <= r.End; k++ {
			got[[2]any{r.Kind, k}] = r
		}
	}
	stmt := func(kind mapread.Kind, k int, site func(*mapread.Run) *[]mapread.Target) *[]mapread.Target {
		r := got[[2]any{kind, k}]
		if r == nil {
			return nil
		}
		ts := site(r)
		if ts == nil {
			return nil
		}
		out := []mapread.Target{}
		for _, t := range *ts {
			t.Episode += k - r.Start
			out = append(out, t)
		}
		return &out
	}
	tvdb := func(r *mapread.Run) *[]mapread.Target { return r.TVDB }
	tmdb := func(r *mapread.Run) *[]mapread.Target { return r.TMDB }
	id := func(kind mapread.Kind, k int) int {
		r := got[[2]any{kind, k}]
		switch {
		case r == nil || r.AniDBEpisodeIDs == nil:
			return 0
		case k-r.Start >= len(r.AniDBEpisodeIDs):
			return -1
		}
		return r.AniDBEpisodeIDs[k-r.Start]
	}
	norun := tv(mapread.Target{Season: -1})
	for _, tc := range []struct {
		name       string
		kind       mapread.Kind
		k          int
		tvdb, tmdb *[]mapread.Target
		id         int
	}{
		{"a row to two episodes, TMDB below 1 by its written offset", mapread.Regular, 1, tv(at(2, 3), at(2, 4)), tv(), 1001},
		{"a -0 row, TMDB's row target below 1", mapread.Regular, 2, tv(), nil, 1002},
		{"an absolute number its capture resolves, TMDB by its written offset", mapread.Regular, 3, tv(at(1, 1)), tv(at(1, 1)), 1003},
		{"a TMDB -0 row, TVDB through the capture", mapread.Regular, 4, tv(at(1, 2)), tv(), 1004},
		{"an absolute number past the capture", mapread.Regular, 6, tv(at(2, 1)), tv(at(1, 4)), 1006},
		{"an absolute number nothing captures", mapread.Regular, 8, nil, tv(at(1, 6)), 1008},
		{"a special's -0 row", mapread.Special, 1, tv(), tv(dflt(0, 1)), 2001},
		{"a special's row target below 1", mapread.Special, 2, nil, tv(dflt(0, 2)), 2002},
		{"an unlisted special a range row names", mapread.Special, 3, nil, tv(dflt(0, 3)), 0},
		{"a linked special", mapread.Special, 4, tv(at(0, 4)), tv(dflt(0, 4)), 2004},
		{"a special with no row", mapread.Special, 5, tv(dflt(0, 5)), tv(dflt(0, 5)), 2005},
		{"a special AniDB skips and no row names", mapread.Special, 50, norun, norun, 0},
		{"a special numbered past 100", mapread.Special, 101, tv(at(0, 9)), tv(dflt(0, 101)), 2101},
		{"a credit on a special's number", mapread.Credit, 1, tv(), tv(), 3001},
		{"a credit's -0 row", mapread.Credit, 2, tv(), tv(), 3002},
		{"a credit past its block", mapread.Credit, 120, tv(), tv(), 3120},
		{"an unlisted credit a row names", mapread.Credit, 50, tv(at(0, 3)), tv(), 0},
		{"a promotion's row", mapread.Promotion, 1, tv(at(0, 11)), tv(), 4001},
		{"a parody's row", mapread.Parody, 1, tv(at(0, 12)), tv(), 5001},
		{"an other episode's row", mapread.Other, 1, tv(at(0, 7)), tv(), 6001},
		{"a linked other episode with no row", mapread.Other, 2, tv(at(0, 402)), tv(), 6002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tvdb == norun {
				if r := got[[2]any{tc.kind, tc.k}]; r != nil {
					t.Errorf("%s %d is in run %+v, want it to land nowhere with no run", tc.kind, tc.k, *r)
				}
				return
			}
			if g := stmt(tc.kind, tc.k, tvdb); !reflect.DeepEqual(g, tc.tvdb) {
				t.Errorf("TVDB = %v, want %v", deref(g), deref(tc.tvdb))
			}
			if g := stmt(tc.kind, tc.k, tmdb); !reflect.DeepEqual(g, tc.tmdb) {
				t.Errorf("TMDB = %v, want %v", deref(g), deref(tc.tmdb))
			}
			if g := id(tc.kind, tc.k); g != tc.id {
				t.Errorf("AniDB episode id = %d, want %d", g, tc.id)
			}
		})
	}
	if !a.EpisodesListed {
		t.Error("episodes_listed is unset on an anime the mirror lists")
	}
	wantLast := mapread.Last{Regular: 9, Special: 101, Credit: 120, Promotion: 1, Parody: 1, Other: 2}
	if a.Last != wantLast {
		t.Errorf("last = %+v, want %+v: the mirror's highest numbers, credits past 100 included", a.Last, wantLast)
	}
}

func deref(ts *[]mapread.Target) any {
	if ts == nil {
		return "nothing stated"
	}
	return *ts
}

// A special number AniDB skips past 100 lands nowhere when no row names
// it. A row's season 0 number reads as the one episode SeasonZeroOf
// decodes: 102 is credit 2 while no listed special holds 102.
func TestBuildSkippedSpecialPast100(t *testing.T) {
	n := &animelists.Node{AniDBID: 3, Attrs: map[string]string{"tvdbid": "5", "defaulttvdbseason": "1", "tmdbtv": "6", "tmdbseason": "1"}}
	mirror := anidb.Anime{Regular: regularIDs(31, 32), Finished: true, Specials: []anidb.Episode{{Number: 101, ID: 33}, {Number: 103, ID: 34}}}
	build := func() *mapread.Index {
		anime, _, _ := Build(&Inputs{
			Nodes: map[int]*animelists.Node{3: n},
			Facts: anidb.NewFacts(&anidb.Snapshot{Commit: strings.Repeat("a", 40), Anime: map[int]anidb.Anime{3: mirror}}, nil, nil),
		})
		return mapread.NewIndex(&mapread.Document{Anime: anime})
	}
	ep, ok := build().Episode(mapread.Ref{AniDB: 3, Kind: mapread.Special, Number: 102})
	if !ok || ep.Listing != mapread.NotListed || ep.TVDB.State != mapread.Nowhere || ep.TMDB.State != mapread.Nowhere {
		t.Errorf("Episode(special 102) = %+v, %t; want not listed and nowhere on both sites", ep, ok)
	}
	n.Rows = []animelists.Row{{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";102-200;"}}
	got := build().AtTVDB(mapread.TVDBEpisode{Series: 5, Season: 0, Episode: 200})
	wantAt := []mapread.Landing{{AniDB: 3, Kind: mapread.Credit, Number: 2}}
	if !reflect.DeepEqual(got, wantAt) {
		t.Errorf("AtTVDB(S00E200) = %+v, want %+v", got, wantAt)
	}
}

// AniDB's list reaches the row of every anime it lists: regular episodes
// past Facts.Count with their ids and nothing stated, an anime no node
// names, one listing no episode, one whose regular list has a gap, and none
// on an anime the mirror does not list.
func TestBuildListsAniDBsEpisodes(t *testing.T) {
	n := &animelists.Node{AniDBID: 4, Attrs: map[string]string{"tvdbid": "5", "defaulttvdbseason": "1"}}
	airing := anidb.Anime{Regular: regularIDs(41, 42, 43), Specials: []anidb.Episode{}}
	unnamed := anidb.Anime{Regular: regularIDs(51), Finished: true, Specials: []anidb.Episode{{Number: 2, ID: 52}}}
	gapped := anidb.Anime{Regular: []anidb.Episode{{Number: 1, ID: 91}, {Number: 2, ID: 92}, {Number: 5, ID: 95}}, Finished: true, Specials: []anidb.Episode{}}
	anime, _, _ := Build(&Inputs{
		Nodes: map[int]*animelists.Node{
			4: n, 6: {AniDBID: 6, Attrs: map[string]string{"tvdbid": "7", "defaulttvdbseason": "1"}},
			9: {AniDBID: 9, Attrs: map[string]string{"tvdbid": "11", "defaulttvdbseason": "1"}},
		},
		Facts: anidb.NewFacts(&anidb.Snapshot{Commit: strings.Repeat("a", 40), Anime: map[int]anidb.Anime{
			4: airing, 5: unnamed, 8: {Specials: []anidb.Episode{}, Extras: []anidb.Extra{}}, 9: gapped,
		}}, nil, map[int]int{4: 1, 6: 2, 9: 4}),
	})
	if a := animeOf(anime, 8); a == nil || !reflect.DeepEqual(*a, mapread.Anime{AniDBID: 8, EpisodesListed: true}) {
		t.Errorf("AniDB 8, listed with no episode = %+v, want a row saying AniDB lists none", a)
	}
	want4 := []mapread.Run{
		{Kind: mapread.Regular, Start: 1, End: 1, AniDBEpisodeIDs: []int{41}, TVDB: targets([]mapread.Target{{Season: 1, Episode: 1, Default: true}})},
		{Kind: mapread.Regular, Start: 2, End: 3, AniDBEpisodeIDs: []int{42, 43}},
	}
	if a := animeOf(anime, 4); a == nil || !a.EpisodesListed || a.Last.Regular != 1 || !reflect.DeepEqual(a.Episodes, want4) {
		t.Errorf("airing AniDB 4 = %+v, want last regular 1 (its count) and runs %s", a, runsText(want4))
	}
	want5 := []mapread.Run{
		{Kind: mapread.Regular, Start: 1, End: 1, AniDBEpisodeIDs: []int{51}},
		{Kind: mapread.Special, Start: 2, End: 2, AniDBEpisodeIDs: []int{52}},
	}
	if a := animeOf(anime, 5); a == nil || !a.EpisodesListed || !reflect.DeepEqual(a.Episodes, want5) {
		t.Errorf("AniDB 5 with no node = %+v, want AniDB's list with no site, runs %s", a, runsText(want5))
	}
	want9 := []mapread.Run{
		{Kind: mapread.Regular, Start: 1, End: 2, AniDBEpisodeIDs: []int{91, 92}, TVDB: targets([]mapread.Target{{Season: 1, Episode: 1, Default: true}})},
		{Kind: mapread.Regular, Start: 3, End: 4, TVDB: targets([]mapread.Target{{Season: 1, Episode: 3, Default: true}})},
		{Kind: mapread.Regular, Start: 5, End: 5, AniDBEpisodeIDs: []int{95}},
	}
	if a := animeOf(anime, 9); a == nil || !a.EpisodesListed || a.Last.Regular != 4 || !reflect.DeepEqual(a.Episodes, want9) {
		t.Errorf("finished AniDB 9 listing 1, 2 and 5 = %+v, want last regular 4 (the database's count) and runs %s", a, runsText(want9))
	}
	if a := animeOf(anime, 6); a == nil || a.EpisodesListed || a.Episodes[0].AniDBEpisodeIDs != nil {
		t.Errorf("AniDB 6, which the mirror does not list = %+v, want no list and no ids", a)
	}
	doc := &mapread.Document{Version: mapread.Version, GeneratedAt: "2026-10-05T00:00:00Z", Sources: goldenSources, Attribution: release.DefaultAttribution, Anime: anime, Entries: []mapread.Entry{}}
	b, err := release.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mapread.Decode(bytes.NewReader(b), mapread.DefaultLimits()); err != nil {
		t.Errorf("Decode(the build) = %v", err)
	}
}

// A special a row names that the mirror does not list extends last, and
// its statement comes from the row.
func TestBuildRowNamedSpecials(t *testing.T) {
	in := placeFixture(t)
	in.Nodes[1].Rows = append(in.Nodes[1].Rows, animelists.Row{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";60-12;305-13;207-1;"})
	anime, _, _ := Build(in)
	a := animeOf(anime, 1)
	if a.Last.Parody != 5 || a.Last.Promotion != 7 {
		t.Errorf("last = %+v, want parody 5 and promotion 7 from the rows", a.Last)
	}
	x := mapread.NewIndex(&mapread.Document{Anime: anime})
	for _, tc := range []struct {
		ref  mapread.Ref
		want []mapread.Target
	}{
		{mapread.Ref{AniDB: 1, Kind: mapread.Special, Number: 60}, []mapread.Target{{Season: 0, Episode: 12}}},
		{mapread.Ref{AniDB: 1, Kind: mapread.Promotion, Number: 7}, []mapread.Target{{Season: 0, Episode: 1}}},
		{mapread.Ref{AniDB: 1, Kind: mapread.Parody, Number: 5}, []mapread.Target{{Season: 0, Episode: 13}}},
	} {
		ep, ok := x.Episode(tc.ref)
		if !ok || ep.TVDB == nil || ep.TVDB.State != mapread.Lands || !reflect.DeepEqual(ep.TVDB.Targets, tc.want) {
			t.Errorf("Episode(%+v) = %+v, %t; want stated %v", tc.ref, ep.TVDB, ok, tc.want)
		}
	}
}

func TestBuildLinks(t *testing.T) {
	in := placeFixture(t)
	in.Entries = []offlinedb.Entry{
		{Type: "TV", AniList: []int{78}, AniDB: []int{1}, IDs: mapread.IDs{AniDB: []int{1}}, Episodes: 9},
		{Type: "OVA", AniList: []int{79}, Episodes: 3},
		{Type: "ONA", AniList: []int{80}, Episodes: 4},
		{Type: "TV", AniList: []int{82}, AniDB: []int{1, 2}, IDs: mapread.IDs{AniDB: []int{1, 2}}, Episodes: 1},
	}
	mk := func(al int, parts []overlay.Part, targets int) Linked {
		return Linked{Link: &overlay.Link{AniListID: al, Parts: parts}, Compiled: overlay.Compiled{AniList: al, Targets: make([]int, targets)}}
	}
	linked := append(in.Links.Linked(),
		mk(78, []overlay.Part{{AniDBID: 1, Episodes: []int{1}}}, 1),
		mk(79, []overlay.Part{{AniDBID: 1, Episodes: []int{2, 3}}}, 2),
		mk(80, []overlay.Part{{AniDBID: 1, Episodes: []int{4, 5, 7}}, {AniDBID: 1, Whole: true}}, 4),
		mk(81, []overlay.Part{{AniDBID: 1, Specials: []int{1, 2, 4, 401}}}, 4),
		mk(82, []overlay.Part{{AniDBID: 1, Episodes: []int{1}}}, 1),
	)
	var skipped []Skipped
	in.Links, skipped = Applicable(in.Entries, nil, linked)
	anime, entries, st := Build(in)
	al := byAniList(entries)
	want := map[int][]mapread.Part{
		77: {
			{AniDBID: 1, Kind: mapread.Special, Start: 4, End: 4},
			{AniDBID: 1, Kind: mapread.Credit, Start: 50, End: 50},
			{AniDBID: 1, Kind: mapread.Promotion, Start: 1, End: 1},
			{AniDBID: 1, Kind: mapread.Parody, Start: 1, End: 1},
			{AniDBID: 1, Kind: mapread.Other, Start: 2, End: 2},
		},
		78: whole(1),
		80: {{AniDBID: 1, Kind: mapread.Regular, Start: 4, End: 5}, {AniDBID: 1, Kind: mapread.Regular, Start: 7, End: 7}, {AniDBID: 1, Kind: mapread.Regular, Combined: true}},
		81: {{AniDBID: 1, Kind: mapread.Special, Start: 1, End: 2}, {AniDBID: 1, Kind: mapread.Special, Start: 4, End: 4}, {AniDBID: 1, Kind: mapread.Other, Start: 1, End: 1}},
	}
	for id, parts := range want {
		if !reflect.DeepEqual(al[id].Parts, parts) {
			t.Errorf("AniList %d parts = %+v, want %+v", id, al[id].Parts, parts)
		}
	}
	if e := al[79]; e.Parts != nil || e.Type != "OVA" {
		t.Errorf("AniList 79 = %+v, want the link skipped for its count", e)
	}
	if e := al[82]; e.Parts != nil || !reflect.DeepEqual(e.IDs.AniDB, []int{1, 2}) {
		t.Errorf("AniList 82 = %+v, want both AniDB candidates and no parts: a link never picks one", e)
	}
	wantSkipped := []Skipped{
		{AniList: 78, Reason: "anime-offline-database now links AniDB 1"},
		{AniList: 79, Reason: "the entry has 3 episodes, the link 2"},
		{AniList: 82, Reason: "anime-offline-database gives it 2 AniDB ids"},
	}
	if !reflect.DeepEqual(skipped, wantSkipped) || in.Links.Len() != 4 || st.AmbiguousAniDB != 1 {
		t.Errorf("skipped = %+v, applied %d, ambiguous %d; want %+v, 4 applied and 1 ambiguous", skipped, in.Links.Len(), st.AmbiguousAniDB, wantSkipped)
	}
	if _, err := mapread.Decode(bytes.NewReader(encode(t, anime, entries)), mapread.DefaultLimits()); err != nil {
		t.Errorf("Decode(the build) = %v", err)
	}
}

// A link Applicable skips states nothing on the anime rows: AniDB 1's runs
// are the build's with no link, while the applied link changes them.
func TestSkippedLinkLeavesTheRuns(t *testing.T) {
	bare := placeFixture(t)
	bare.Links = Applied{}
	plain, _, _ := Build(bare)
	want := animeOf(plain, 1).Episodes
	linked, _, _ := Build(placeFixture(t))
	if reflect.DeepEqual(animeOf(linked, 1).Episodes, want) {
		t.Fatal("the applied link leaves AniDB 1's runs as they are, so this test proves nothing")
	}
	for _, tc := range []struct {
		name   string
		reason string
		entry  offlinedb.Entry
	}{
		{"one database AniDB id", "anime-offline-database now links AniDB 9", offlinedb.Entry{Type: "OVA", AniList: []int{77}, AniDB: []int{9}}},
		{"two database AniDB ids", "anime-offline-database gives it 2 AniDB ids", offlinedb.Entry{Type: "OVA", AniList: []int{77}, AniDB: []int{8, 9}}},
		{"another episode count", "the entry has 3 episodes, the link 5", offlinedb.Entry{Type: "OVA", AniList: []int{77}, Episodes: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := placeFixture(t)
			in.Entries = []offlinedb.Entry{tc.entry}
			var skipped []Skipped
			in.Links, skipped = Applicable(in.Entries, nil, in.Links.Linked())
			if want := []Skipped{{AniList: 77, Reason: tc.reason}}; !reflect.DeepEqual(skipped, want) || slices.ContainsFunc(in.Links.Linked(), func(l Linked) bool { return l.Link.AniListID == 77 }) {
				t.Errorf("Applicable = %+v skipped, %+v applied; want %+v and AniList 77 not applied", skipped, in.Links.Linked(), want)
			}
			anime, _, _ := Build(in)
			if got := animeOf(anime, 1).Episodes; !reflect.DeepEqual(got, want) {
				t.Errorf("AniDB 1 runs = %s, want the build's with no link, %s", runsText(got), runsText(want))
			}
		})
	}
}

func TestBuildIdentities(t *testing.T) {
	entries, nodes := fixtures(t)
	id := func(al, ad int) Identity { return Identity{Link: &overlay.Link{AniListID: al}, AniDB: ad} }
	ids := []Identity{id(999, 12), id(101, 14), id(100, 15), id(104, 12)}
	applied, skipped := Applicable(entries, ids, nil)
	_, rows, _ := Build(&Inputs{Entries: entries, Nodes: nodes, Facts: databaseFacts(entries, nil), Links: applied})
	al := byAniList(rows)
	if e := al[999]; !reflect.DeepEqual(e, mapread.Entry{AniListID: 999, Type: "OVA", Episodes: 2, Parts: whole(12)}) {
		t.Errorf("created AniList 999 = %+v, want AniDB 12's type and count with its part", e)
	}
	if e := al[101]; !reflect.DeepEqual(e.Parts, whole(14)) || e.Type != "SPECIAL" {
		t.Errorf("AniList 101 = %+v, want its own type with AniDB 14's part", e)
	}
	if !reflect.DeepEqual(al[100].Parts, whole(10)) {
		t.Errorf("AniList 100 = %+v, want its database identity kept", al[100])
	}
	if e := al[104]; e.Parts != nil || !slices.Equal(e.IDs.AniDB, []int{14, 15}) {
		t.Errorf("ambiguous AniList 104 = %+v, want both candidates and no parts", e)
	}
	want := []Skipped{{AniList: 100, Reason: "anime-offline-database now links AniDB 10"}, {AniList: 104, Reason: "anime-offline-database gives it 2 AniDB ids"}}
	if !reflect.DeepEqual(skipped, want) || applied.Len() != 2 {
		t.Errorf("skipped = %+v, applied %d; want %+v and 2 applied", skipped, applied.Len(), want)
	}
}

// Identities and Linked return copies of their slices, so writing an
// element of one does not change which links the build applies; the links
// themselves are shared.
func TestAppliedListsAreCopies(t *testing.T) {
	l := &overlay.Link{AniListID: 999}
	applied, _ := Applicable(nil, []Identity{{Link: l, AniDB: 12}}, []Linked{{Link: l}})
	applied.Identities()[0] = Identity{}
	applied.Linked()[0] = Linked{}
	if ids, linked := applied.Identities(), applied.Linked(); ids[0].Link != l || linked[0].Link != l {
		t.Errorf("Identities %+v, Linked %+v after writing to returned slices, want the applied links unchanged", ids, linked)
	}
}

// A build is a function of its inputs: two builds encode byte for byte.
func TestBuildIsDeterministic(t *testing.T) {
	in := placeFixture(t)
	a1, e1, _ := Build(in)
	a2, e2, _ := Build(placeFixture(t))
	if !bytes.Equal(encode(t, a1, e1), encode(t, a2, e2)) {
		t.Error("two builds from one input differ")
	}
	anime, entries, _ := buildMini(t)
	anime2, entries2, _ := buildMini(t)
	if !bytes.Equal(encode(t, anime, entries), encode(t, anime2, entries2)) {
		t.Error("two builds of the fixtures differ")
	}
}

func TestKindsAreAniDBs(t *testing.T) {
	for _, k := range []string{anidb.Special, anidb.Credit, anidb.Promotion, anidb.Parody, anidb.Other} {
		if !slices.Contains(mapread.Kinds(), mapread.Kind(k)) {
			t.Errorf("anidb kind %q is not a mapread kind", k)
		}
	}
}

// goldenSources are fixed sources, so the golden decodes as a release.
var goldenSources = mapread.Sources{
	AnimeOfflineDatabase: mapread.OfflineDatabaseSource{Repository: "https://github.com/cedya77/anime-offline-database", Release: "2026-40", Asset: "anime-offline-database.jsonl", SHA256: strings.Repeat("0", 64)},
	AnimeLists:           mapread.AnimeListsSource{Repository: "https://github.com/Anime-Lists/anime-lists", Commit: strings.Repeat("a", 40), File: "anime-list-master.xml"},
	AniDBMirror:          mapread.AniDBMirrorSource{Repository: anidb.Repository, Commit: strings.Repeat("a", 40)},
	Overlay:              mapread.OverlaySource{SHA256: strings.Repeat("1", 64), Entries: 1, Links: 1},
}

func encode(t *testing.T, anime []mapread.Anime, entries []mapread.Entry) []byte {
	t.Helper()
	b, err := release.Encode(&mapread.Document{
		Version: mapread.Version, GeneratedAt: "2026-10-05T00:00:00Z", Sources: goldenSources,
		Attribution: release.DefaultAttribution, Anime: anime, Entries: entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The goldens pin the whole document each fixture builds: the mini
// fixtures, and placeFixture with its database entries and links.
// mapread's fixtures are the same bytes, so its tests read this build.
// Regenerate with UPDATE_GOLDEN=1 go test ./internal/join -run TestGolden
// and review the diff.
func TestGolden(t *testing.T) {
	anime, entries, _ := buildMini(t)
	placed, linked, _ := Build(placeFixture(t))
	mini, place := encode(t, anime, entries), encode(t, placed, linked)
	for path, got := range map[string][]byte{
		"../../testdata/golden/animap.json":       mini,
		"../../testdata/golden/animap-place.json": place,
		"../../mapread/testdata/mini.json":        mini,
		"../../mapread/testdata/place.json":       place,
	} {
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(path, got, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read golden (run UPDATE_GOLDEN=1 go test ./internal/join -run TestGolden): %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("golden %s mismatch (run UPDATE_GOLDEN=1 go test ./internal/join -run TestGolden and review):\n--- want\n%s\n+++ got\n%s", path, want, got)
		}
	}
}

func TestCarriers(t *testing.T) {
	entries, _ := fixtures(t)
	got := Carriers(entries)
	if !slices.Equal(got[10], []int{100}) || !slices.Equal(got[13], []int{102, 103}) {
		t.Errorf("Carriers = %v, want AniDB 10 carried by AniList 100 and 13 by both 102 and 103", got)
	}
	if _, ok := got[12]; ok {
		t.Errorf("Carriers = %v, want no carrier for AniDB-only 12", got)
	}
}

// Every golden decodes through mapread, field for field and kind for kind,
// and re-encodes byte for byte.
func TestGoldenRoundTripsThroughMapread(t *testing.T) {
	paths, err := filepath.Glob("../../testdata/golden/*.json")
	if err != nil || len(paths) < 2 {
		t.Fatalf("goldens = %v, %v", paths, err)
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := mapread.Decode(bytes.NewReader(body), mapread.DefaultLimits())
		if err != nil {
			t.Errorf("Decode(%s) = %v", path, err)
			continue
		}
		again, err := mapread.Encode(doc)
		if err != nil || !bytes.Equal(again, body) {
			t.Errorf("%s does not re-encode as written (%v)", path, err)
		}
	}
}

// The place golden holds every member the JSON Schema names, every kind,
// every statement form on both sites and both entry key shapes, so the
// round trip through mapread covers the whole format.
func TestPlaceGoldenCoversTheSchema(t *testing.T) {
	var schema map[string]any
	readJSONFile(t, "../../docs/animap.schema.json", &schema)
	var doc map[string]any
	readJSONFile(t, "../../testdata/golden/animap-place.json", &doc)
	defs, _ := schema["$defs"].(map[string]any)
	want, seen := map[string]bool{}, map[string]bool{}
	schemaPaths(defs, schema, "", want)
	docPaths(defs, schema, doc, "", seen)
	for _, p := range slices.Sorted(maps.Keys(want)) {
		if !seen[p] {
			t.Errorf("the place golden has no %s", p)
		}
	}
	forms := map[string]bool{}
	for _, a := range doc["anime"].([]any) {
		row := a.(map[string]any)
		runs, _ := row["episodes"].([]any)
		for _, r := range runs {
			run := r.(map[string]any)
			for site, id := range map[string]string{"tvdb": "tvdb_id", "tmdb": "tmdb_tv_id"} {
				if row[id] != nil {
					forms[site+" "+statementForm(run[site])] = true
				}
			}
		}
	}
	for _, site := range []string{"tvdb", "tmdb"} {
		for _, form := range []string{"unstated", "nowhere", "stated", "default"} {
			if !forms[site+" "+form] {
				t.Errorf("the place golden has no %s run that is %s", site, form)
			}
		}
	}
	keyed := map[bool]bool{}
	for _, e := range doc["entries"].([]any) {
		_, ok := e.(map[string]any)["anilist_id"]
		keyed[ok] = true
	}
	if !keyed[true] || !keyed[false] {
		t.Errorf("entry key shapes = %v, want entries with and without an AniList id", keyed)
	}
}

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func resolve(defs, node map[string]any) map[string]any {
	if ref, ok := node["$ref"].(string); ok {
		def, _ := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		return resolve(defs, def)
	}
	return node
}

// schemaPaths records every member path node names below path, and each
// kind's enum value.
func schemaPaths(defs, node map[string]any, path string, out map[string]bool) {
	node = resolve(defs, node)
	if enum, ok := node["enum"].([]any); ok && strings.HasSuffix(path, ".kind") {
		for _, v := range enum {
			out[fmt.Sprint(path, "=", v)] = true
		}
	}
	props, _ := node["properties"].(map[string]any)
	for name, sub := range props {
		out[path+"."+name] = true
		schemaPaths(defs, sub.(map[string]any), path+"."+name, out)
	}
	if items, ok := node["items"].(map[string]any); ok {
		schemaPaths(defs, items, path+"[]", out)
	}
}

// docPaths records every member path and kind value v holds, as
// schemaPaths names them.
func docPaths(defs, node map[string]any, v any, path string, out map[string]bool) {
	node = resolve(defs, node)
	if _, ok := node["enum"]; ok && strings.HasSuffix(path, ".kind") {
		out[fmt.Sprint(path, "=", v)] = true
	}
	props, _ := node["properties"].(map[string]any)
	if obj, ok := v.(map[string]any); ok {
		for name, val := range obj {
			if sub, known := props[name].(map[string]any); known {
				out[path+"."+name] = true
				docPaths(defs, sub, val, path+"."+name, out)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		list, _ := v.([]any)
		for _, val := range list {
			docPaths(defs, items, val, path+"[]", out)
		}
	}
}

// statementForm names a run's site member: absent, [], or its targets.
func statementForm(site any) string {
	ts, ok := site.([]any)
	switch {
	case !ok:
		return "unstated"
	case len(ts) == 0:
		return "nowhere"
	}
	if _, ok := ts[0].(map[string]any)["default"]; ok {
		return "default"
	}
	return "stated"
}
