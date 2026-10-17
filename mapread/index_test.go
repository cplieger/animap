package mapread

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func landsOn(ts ...Target) *Statement { return &Statement{State: Lands, Targets: ts} }

func landsNowhere() *Statement { return &Statement{State: Nowhere} }

func statesNothing() *Statement { return &Statement{State: Unstated} }

func anidbEpisode(aid int, k Kind, n, id int) AniDBEpisode {
	return AniDBEpisode{AniDB: aid, Kind: k, Number: n, ID: id}
}

func landing(aid int, k Kind, n, id int, byDefault bool) Landing {
	return Landing{AniDBEpisode: anidbEpisode(aid, k, n, id), Default: byDefault}
}

func TestEpisodeAppliesTheReadingRule(t *testing.T) {
	x := NewIndex(sample())
	for _, tc := range []struct {
		name       string
		ref        Ref
		tvdb, tmdb *Statement
		listing    Listing
		id         int
		ok         bool
	}{
		{"stated, and a default", Ref{AniDB: 1, Kind: Regular, Number: 2}, landsOn(at(1, 2)), landsOn(dflt(1, 2)), Listed, 102, true},
		{"one episode on two, and a run's []", Ref{AniDB: 1, Kind: Regular, Number: 3}, landsOn(at(1, 3), at(1, 4)), landsNowhere(), Listed, 103, true},
		{"a listed episode landing nowhere", Ref{AniDB: 1, Kind: Regular, Number: 4}, landsNowhere(), landsNowhere(), Listed, 104, true},
		{"a listed episode past last", Ref{AniDB: 1, Kind: Regular, Number: 5}, statesNothing(), statesNothing(), Listed, 105, true},
		{"a number past AniDB's list", Ref{AniDB: 1, Kind: Regular, Number: 6}, landsNowhere(), landsNowhere(), NotListed, 0, true},
		{"a run's absent member states nothing", Ref{AniDB: 1, Kind: Special, Number: 2}, statesNothing(), landsOn(dflt(0, 2)), Listed, 202, true},
		{"a gap in AniDB's numbering", Ref{AniDB: 1, Kind: Special, Number: 3}, landsNowhere(), landsNowhere(), NotListed, 0, true},
		{"a listed extra landing nowhere", Ref{AniDB: 1, Kind: Credit, Number: 1}, landsNowhere(), landsNowhere(), Listed, 301, true},
		{"a number a row names that AniDB does not list", Ref{AniDB: 1, Kind: Other, Number: 1}, landsOn(at(0, 5)), statesNothing(), NotListed, 0, true},
		{"a kind AniDB lists none of", Ref{AniDB: 1, Kind: Promotion, Number: 1}, landsNowhere(), landsNowhere(), NotListed, 0, true},
		{"a listed episode with no site id", Ref{AniDB: 3, Kind: Regular, Number: 1}, nil, nil, Listed, 900, true},
		{"a row with no AniDB list", Ref{AniDB: 2, Kind: Regular, Number: 1}, landsOn(dflt(2, 1)), nil, ListingUnknown, 0, true},
		{"past last on a row with no AniDB list", Ref{AniDB: 2, Kind: Regular, Number: 3}, nil, nil, ListingUnknown, 0, false},
		{"a kind with no last on a row with no AniDB list", Ref{AniDB: 2, Kind: Credit, Number: 1}, nil, nil, ListingUnknown, 0, false},
		{"no row", Ref{AniDB: 9, Kind: Regular, Number: 1}, nil, nil, ListingUnknown, 0, false},
		{"an unknown kind on a row with AniDB's list", Ref{AniDB: 1, Kind: "bogus", Number: 1}, nil, nil, ListingUnknown, 0, false},
		{"an empty kind", Ref{AniDB: 1, Number: 1}, nil, nil, ListingUnknown, 0, false},
		{"number 0 on a row with AniDB's list", Ref{AniDB: 1, Kind: Regular, Number: 0}, nil, nil, ListingUnknown, 0, false},
		{"a negative number on a row with AniDB's list", Ref{AniDB: 1, Kind: Special, Number: -1}, nil, nil, ListingUnknown, 0, false},
		{"number 0 on a row with no AniDB list", Ref{AniDB: 2, Kind: Regular, Number: 0}, nil, nil, ListingUnknown, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ep, ok := x.Episode(tc.ref)
			if ok != tc.ok || !reflect.DeepEqual(ep.TVDB, tc.tvdb) || !reflect.DeepEqual(ep.TMDB, tc.tmdb) || ep.Listing != tc.listing || ep.ID != tc.id {
				t.Errorf("Episode(%+v) = %+v %+v, listing %d id %d, %t; want %+v %+v, listing %d id %d, %t",
					tc.ref, ep.TVDB, ep.TMDB, ep.Listing, ep.ID, ok, tc.tvdb, tc.tmdb, tc.listing, tc.id, tc.ok)
			}
		})
	}
}

// Every episode of every kind in the sample and the goldens, past last
// included, reads in one of Statement's closed states, with an id exactly
// when Listed, and a reference naming no episode reads false.
func TestEpisodeStatementsAreClosed(t *testing.T) {
	docs := []*Document{sample()}
	for _, path := range []string{"testdata/mini.json", "testdata/place.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Decode(bytes.NewReader(b), DefaultLimits())
		if err != nil {
			t.Fatalf("Decode(%s) = %v", path, err)
		}
		docs = append(docs, doc)
	}
	for _, doc := range docs {
		x := NewIndex(doc)
		for _, a := range doc.Anime {
			for _, k := range append(Kinds(), "", "bogus") {
				for n := -1; n <= a.Last.Of(k)+2; n++ {
					checkEpisode(t, x, Ref{AniDB: a.AniDBID, Kind: k, Number: n})
				}
			}
		}
	}
}

func TestSiteLookups(t *testing.T) {
	x := NewIndex(sample())
	for _, tc := range []struct {
		name string
		got  []Landing
		want []Landing
	}{
		{"two AniDB episodes on one TVDB episode", x.AtTVDB(TVDBEpisode{10, 0, 5}), []Landing{landing(1, Special, 1, 201, false), landing(1, Other, 1, 0, false)}},
		{"one AniDB episode on two, first", x.AtTVDB(TVDBEpisode{10, 1, 3}), []Landing{landing(1, Regular, 3, 103, false)}},
		{"one AniDB episode on two, second", x.AtTVDB(TVDBEpisode{10, 1, 4}), []Landing{landing(1, Regular, 3, 103, false)}},
		{"inside a run", x.AtTVDB(TVDBEpisode{10, 1, 2}), []Landing{landing(1, Regular, 2, 102, false)}},
		{"a default landing on a row with no AniDB list", x.AtTVDB(TVDBEpisode{10, 2, 2}), []Landing{landing(2, Regular, 2, 0, true)}},
		{"nothing there", x.AtTVDB(TVDBEpisode{10, 1, 5}), nil},
		{"another series", x.AtTVDB(TVDBEpisode{11, 1, 1}), nil},
		{"TMDB", x.AtTMDB(TMDBEpisode{20, 1, 2}), []Landing{landing(1, Regular, 2, 102, true)}},
	} {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s = %+v, want %+v", tc.name, tc.got, tc.want)
		}
	}
	if got := x.TMDBMovie(31); !reflect.DeepEqual(got, []int{3}) {
		t.Errorf("TMDBMovie(31) = %v, want [3]", got)
	}
	want := []Span{
		{AniDB: 1, First: TVDBEpisode{10, 1, 1}, Last: TVDBEpisode{10, 1, 4}, Exact: true},
		{AniDB: 2, First: TVDBEpisode{10, 2, 1}, Last: TVDBEpisode{10, 2, 2}},
	}
	if got := x.Series(10); !reflect.DeepEqual(got, want) {
		t.Errorf("Series(10) = %+v, want %+v", got, want)
	}
}

// A run whose targets overlap lands two of its episodes on one site
// episode; both lookups still list them by number.
func TestSiteLookupsOrderOverlappingTargets(t *testing.T) {
	doc := sample()
	doc.Anime = []Anime{{
		AniDBID: 1, Type: "TV", TVDBID: 10, TMDBTVID: 20, Last: Last{Regular: 2},
		Episodes: []Run{{Kind: Regular, Start: 1, End: 2, TVDB: targets(at(1, 1), at(1, 2)), TMDB: targets(at(1, 1), at(1, 2))}},
	}}
	x := NewIndex(doc)
	want := []Landing{landing(1, Regular, 1, 0, false), landing(1, Regular, 2, 0, false)}
	if got := x.AtTVDB(TVDBEpisode{10, 1, 2}); !reflect.DeepEqual(got, want) {
		t.Errorf("AtTVDB(S1E2) = %+v, want %+v", got, want)
	}
	if got := x.AtTMDB(TMDBEpisode{20, 1, 2}); !reflect.DeepEqual(got, want) {
		t.Errorf("AtTMDB(S1E2) = %+v, want %+v", got, want)
	}
}

func TestEntryLookups(t *testing.T) {
	x := NewIndex(sample())
	entry := func(al int) Entry {
		e, ok := x.Entry(al)
		if !ok {
			t.Fatalf("Entry(%d) not found", al)
		}
		return e
	}
	if _, ok := x.Entry(0); ok {
		t.Error("Entry(0) found an entry with no AniList id")
	}
	r := func(aid int, k Kind, n, id int) []AniDBEpisode { return []AniDBEpisode{anidbEpisode(aid, k, n, id)} }
	for _, tc := range []struct {
		name string
		al   int
		want []EntryEpisode
	}{
		{"a whole anime, to last", 1, []EntryEpisode{{Number: 1, Refs: r(1, Regular, 1, 101)}, {Number: 2, Refs: r(1, Regular, 2, 102)}, {Number: 3, Refs: r(1, Regular, 3, 103)}, {Number: 4, Refs: r(1, Regular, 4, 104)}}},
		{"a link to specials and an unlisted other episode", 2, []EntryEpisode{{Number: 1, Refs: r(1, Special, 1, 201)}, {Number: 2, Refs: r(1, Special, 2, 202)}, {Number: 3, Refs: r(1, Other, 1, 0)}}},
		{"an ambiguous entry", 3, nil},
		{"a combined part, then an anime with no count", 5, []EntryEpisode{{Number: 1, Refs: []AniDBEpisode{anidbEpisode(2, Regular, 1, 0), anidbEpisode(2, Regular, 2, 0)}}}},
	} {
		if got := x.Episodes(entry(tc.al)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Episodes(%s) = %+v, want %+v", tc.name, got, tc.want)
		}
	}
	if got := x.Routes(entry(1)); !reflect.DeepEqual(got, Routes{TVDB: []int{10}, TMDBShows: []int{20}}) {
		t.Errorf("Routes(1) = %+v", got)
	}
	if got := x.Routes(entry(4)); !reflect.DeepEqual(got, Routes{TMDBMovies: []int{30, 31}, IMDb: []string{"tt0000003"}}) {
		t.Errorf("Routes(4) = %+v, want the film's ids", got)
	}
	if got := x.Routes(entry(3)); !reflect.DeepEqual(got, Routes{}) {
		t.Errorf("Routes(ambiguous) = %+v, want none", got)
	}
	for name, parts := range map[string][]Part{
		"a ranged part":   {{AniDBID: 3, Kind: Regular, Start: 1, End: 1}},
		"a combined part": {{AniDBID: 3, Kind: Regular, Combined: true}},
	} {
		if got := x.Routes(Entry{Parts: parts}); !reflect.DeepEqual(got, Routes{}) {
			t.Errorf("Routes(%s of a film) = %+v, want no film ids: they name the whole anime", name, got)
		}
	}
	for _, tc := range []struct {
		al   int
		want []SeriesSeasons
	}{
		{1, []SeriesSeasons{{Series: 10, Seasons: []SeasonEpisodes{{Season: 1, Stated: []int{1, 2, 3}}}, Nowhere: 1}}},
		{2, []SeriesSeasons{{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1, 3}}}, Unstated: 1}}},
		{5, []SeriesSeasons{{Series: 10, Seasons: []SeasonEpisodes{{Season: 2, Default: []int{1}}}}}},
		{4, []SeriesSeasons{}},
	} {
		if got := x.TVDBSeasons(entry(tc.al)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("TVDBSeasons(%d) = %+v, want %+v", tc.al, got, tc.want)
		}
	}
}

// A combined part names one entry episode only when its anime counts
// regular episodes; on a decoded document it otherwise names none, as
// every rangeless part does.
func TestCombinedPartWithNoCountNamesNothing(t *testing.T) {
	doc := sample()
	doc.Entries = append(doc.Entries[:5:5], append([]Entry{{AniListID: 6, Type: "OVA", Parts: []Part{{AniDBID: 4, Kind: Regular, Combined: true}}}}, doc.Entries[5:]...)...)
	b, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(b), DefaultLimits())
	if err != nil {
		t.Fatalf("Decode = %v", err)
	}
	x := NewIndex(decoded)
	e, _ := x.Entry(6)
	if got := x.Episodes(e); got != nil {
		t.Errorf("Episodes(a combined part of an anime with no regular last) = %+v, want none", got)
	}
	if got := x.TVDBSeasons(e); len(got) != 0 {
		t.Errorf("TVDBSeasons = %+v, want none", got)
	}
}

// One combined entry episode of two AniDB episodes reads one way whatever
// their order: a season is Default when any landing there is a default.
func TestOnTVDBAggregatesEveryLanding(t *testing.T) {
	one := func(n int, ts ...Target) Run { return Run{Kind: Regular, Start: n, End: n, TVDB: targets(ts...)} }
	unstated := func(n int) Run { return Run{Kind: Regular, Start: n, End: n, TMDB: targets(at(1, n))} }
	for _, tc := range []struct {
		name    string
		runs    []Run
		reading SeriesReading
		seasons SeriesSeasons
	}{
		{"default then stated", []Run{one(1, dflt(0, 1)), one(2, at(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Default: []int{1}}}}},
		{"stated then default", []Run{one(1, at(0, 1)), one(2, dflt(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Default: []int{1}}}}},
		{"stated only", []Run{one(1, at(0, 1)), one(2, at(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}}},
		{"default only", []Run{one(1, dflt(0, 1)), one(2, dflt(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Default: []int{1}}}}},
		{"two seasons, each on its own basis", []Run{one(1, at(0, 1)), one(2, dflt(1, 1))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0}, {Season: 1, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}, {Season: 1, Default: []int{1}}}}},
		{"nowhere", nil, SeriesReading{Series: 10}, SeriesSeasons{Series: 10, Nowhere: 1}},
		{"unstated", []Run{unstated(1), unstated(2)}, SeriesReading{Series: 10, Unstated: true}, SeriesSeasons{Series: 10, Unstated: 1}},
		{"nowhere and unstated", []Run{unstated(2)}, SeriesReading{Series: 10, Unstated: true}, SeriesSeasons{Series: 10, Unstated: 1}},
		{"stated and unstated", []Run{one(1, at(0, 1)), unstated(2)}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := NewIndex(&Document{
				Anime:   []Anime{{AniDBID: 1, TVDBID: 10, TMDBTVID: 20, Last: Last{Regular: 2}, Episodes: tc.runs}},
				Entries: []Entry{{AniListID: 1, Parts: []Part{{AniDBID: 1, Kind: Regular, Combined: true}}}},
			})
			e, _ := x.Entry(1)
			eps := x.Episodes(e)
			if len(eps) != 1 {
				t.Fatalf("Episodes = %+v, want one combined entry episode", eps)
			}
			if got, want := x.OnTVDB(eps[0]), (TVDBReading{Series: []SeriesReading{tc.reading}}); !reflect.DeepEqual(got, want) {
				t.Errorf("OnTVDB = %+v, want %+v", got, want)
			}
			if got := x.TVDBSeasons(e); !reflect.DeepEqual(got, []SeriesSeasons{tc.seasons}) {
				t.Errorf("TVDBSeasons = %+v, want %+v", got, []SeriesSeasons{tc.seasons})
			}
		})
	}
}

// An AniDB episode of an anime on no TVDB series marks the reading Off.
func TestOnTVDBMarksAnAnimeOffTVDB(t *testing.T) {
	x := NewIndex(sample())
	got := x.OnTVDB(EntryEpisode{Number: 1, Refs: []AniDBEpisode{anidbEpisode(3, Regular, 1, 900), anidbEpisode(2, Regular, 1, 0)}})
	want := TVDBReading{Off: true, Series: []SeriesReading{{Series: 10, Seasons: []SeasonBasis{{Season: 2, Default: true}}}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OnTVDB = %+v, want %+v", got, want)
	}
}

// A result is the caller's: changing it leaves the index as it was.
func TestLookupsReturnCopies(t *testing.T) {
	x := NewIndex(sample())
	e, _ := x.Entry(1)
	e.Parts[0].AniDBID = 9
	e.IDs.MAL[0] = 99
	a, _ := x.Anime(1)
	(*a.Episodes[0].TVDB)[0].Episode = 99
	a.Episodes[0].AniDBEpisodeIDs[0] = 99
	a.Episodes[1].Start = 7
	a.TMDBMovieIDs = append(a.TMDBMovieIDs, 5)
	if e2, _ := x.Entry(1); !reflect.DeepEqual(e2, sample().Entries[0]) {
		t.Errorf("Entry(1) after editing a result = %+v", e2)
	}
	if a2, _ := x.Anime(1); !reflect.DeepEqual(a2, sample().Anime[0]) {
		t.Errorf("Anime(1) after editing a result = %+v", a2)
	}
	m := x.TMDBMovie(30)
	m[0] = 7
	if got := x.TMDBMovie(30); !reflect.DeepEqual(got, []int{3}) {
		t.Errorf("TMDBMovie(30) after editing a result = %v", got)
	}
}
