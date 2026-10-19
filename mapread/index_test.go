package mapread

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"
)

func landsOn(ps ...Placement) *Statement { return &Statement{State: Lands, Placements: ps} }

func landsNowhere() *Statement { return &Statement{State: Nowhere} }

func statesNothing() *Statement { return &Statement{State: Unstated} }

// placedDflt is a placement on series resting on a default alone.
func placedDflt(series, season, episode int) Placement {
	p := placed(series, season, episode)
	p.Default = true
	return p
}

func anidbEpisode(aid int, k Kind, n, id int) AniDBEpisode {
	return AniDBEpisode{AniDB: aid, Kind: k, Number: n, ID: id}
}

func landing(aid int, k Kind, n, id int, byDefault bool) Landing {
	return Landing{AniDBEpisode: anidbEpisode(aid, k, n, id), Default: byDefault}
}

// golden decodes the golden at path and indexes it.
func golden(t *testing.T, path string) *Index {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Decode(bytes.NewReader(b), DefaultLimits())
	if err != nil {
		t.Fatalf("Decode(%s) = %v", path, err)
	}
	return NewIndex(doc)
}

// entryOf is x's entry of AniList id al, failing t when there is none.
func entryOf(t *testing.T, x *Index, al int) Entry {
	t.Helper()
	e, ok := x.Entry(al)
	if !ok {
		t.Fatalf("Entry(%d) not found", al)
	}
	return e
}

func TestEpisodeAppliesTheReadingRule(t *testing.T) {
	x := NewIndex(sample())
	for _, tc := range []struct {
		name    string
		ref     Ref
		tvdb    *Statement
		movies  []int
		listing Listing
		id      int
		ok      bool
	}{
		{"stated", Ref{AniDB: 1, Kind: Regular, Number: 2}, landsOn(placedID(10, 1, 2, 7002)), nil, Listed, 102, true},
		{"one episode on two", Ref{AniDB: 1, Kind: Regular, Number: 3}, landsOn(placed(10, 1, 3), placed(10, 1, 4)), nil, Listed, 103, true},
		{"a listed episode landing nowhere", Ref{AniDB: 1, Kind: Regular, Number: 4}, landsNowhere(), nil, Listed, 104, true},
		{"a listed episode past last", Ref{AniDB: 1, Kind: Regular, Number: 5}, statesNothing(), nil, Listed, 105, true},
		{"a number past AniDB's list", Ref{AniDB: 1, Kind: Regular, Number: 6}, landsNowhere(), nil, NotListed, 0, true},
		{"a special that is a movie", Ref{AniDB: 1, Kind: Special, Number: 1}, landsOn(placedID(10, 0, 5, 7005)), []int{40}, Listed, 201, true},
		{"a run's absent tvdb states nothing", Ref{AniDB: 1, Kind: Special, Number: 2}, statesNothing(), []int{41}, Listed, 202, true},
		{"a gap in AniDB's numbering", Ref{AniDB: 1, Kind: Special, Number: 3}, landsNowhere(), nil, NotListed, 0, true},
		{"a special on another series", Ref{AniDB: 1, Kind: Special, Number: 4}, landsOn(placedID(11, 0, 1, 7101)), nil, Listed, 204, true},
		{"a listed extra landing nowhere", Ref{AniDB: 1, Kind: Credit, Number: 1}, landsNowhere(), nil, Listed, 301, true},
		{"a number a row names that AniDB does not list", Ref{AniDB: 1, Kind: Other, Number: 1}, landsOn(placed(10, 0, 5)), nil, NotListed, 0, true},
		{"a kind AniDB lists none of", Ref{AniDB: 1, Kind: Promotion, Number: 1}, landsNowhere(), nil, NotListed, 0, true},
		{"a listed film on no series", Ref{AniDB: 3, Kind: Regular, Number: 1}, nil, []int{30, 31}, Listed, 900, true},
		{"a row with no AniDB list", Ref{AniDB: 2, Kind: Regular, Number: 1}, landsOn(placedDflt(10, 2, 1)), nil, ListingUnknown, 0, true},
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
			if ok != tc.ok || !reflect.DeepEqual(ep.TVDB, tc.tvdb) || !slices.Equal(ep.TMDBMovies, tc.movies) || ep.Listing != tc.listing || ep.ID != tc.id {
				t.Errorf("Episode(%+v) = %+v movies %v, listing %d id %d, %t; want %+v movies %v, listing %d id %d, %t",
					tc.ref, ep.TVDB, ep.TMDBMovies, ep.Listing, ep.ID, ok, tc.tvdb, tc.movies, tc.listing, tc.id, tc.ok)
			}
		})
	}
}

// Every episode of every kind in the sample and the goldens, past last
// included, reads in one of Statement's closed states, every target on its
// series, with an id exactly when Listed, and a reference naming no
// episode reads false.
func TestEpisodeStatementsAreClosed(t *testing.T) {
	xs := []*Index{NewIndex(sample())}
	for _, path := range goldens {
		xs = append(xs, golden(t, path))
	}
	for _, x := range xs {
		for _, a := range x.doc.Anime {
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
		{"a special on another series", x.AtTVDB(TVDBEpisode{11, 0, 1}), []Landing{landing(1, Special, 4, 204, false)}},
		{"the row's series at another series' slot", x.AtTVDB(TVDBEpisode{10, 0, 1}), nil},
		{"nothing there", x.AtTVDB(TVDBEpisode{10, 1, 5}), nil},
		{"another series", x.AtTVDB(TVDBEpisode{12, 1, 1}), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("got %+v, want %+v", tc.got, tc.want)
			}
		})
	}
	want := []Span{
		{AniDB: 1, First: TVDBEpisode{10, 1, 1}, Last: TVDBEpisode{10, 1, 4}, Exact: true},
		{AniDB: 2, First: TVDBEpisode{10, 2, 1}, Last: TVDBEpisode{10, 2, 2}},
	}
	if got := x.Series(10); !reflect.DeepEqual(got, want) {
		t.Errorf("Series(10) = %+v, want %+v", got, want)
	}
	if got := x.Series(11); got != nil {
		t.Errorf("Series(11) = %+v, want none: only a special lands there", got)
	}
}

// A run whose targets overlap lands two of its episodes on one TVDB
// episode; AtTVDB still lists them by number.
func TestSiteLookupsOrderOverlappingTargets(t *testing.T) {
	doc := sample()
	doc.Anime = []Anime{{
		AniDBID: 1, Type: "TV", TVDBID: 10, Last: Last{Regular: 2},
		Episodes: []Run{{Kind: Regular, Start: 1, End: 2, TVDB: targets(at(1, 1), at(1, 2))}},
	}}
	x := NewIndex(doc)
	want := []Landing{landing(1, Regular, 1, 0, false), landing(1, Regular, 2, 0, false)}
	if got := x.AtTVDB(TVDBEpisode{10, 1, 2}); !reflect.DeepEqual(got, want) {
		t.Errorf("AtTVDB(S1E2) = %+v, want %+v", got, want)
	}
}

func TestEntryLookups(t *testing.T) {
	x := NewIndex(sample())
	if _, ok := x.Entry(0); ok {
		t.Error("Entry(0) found an entry")
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
		{"a special on another series", 6, []EntryEpisode{{Number: 1, Refs: r(1, Special, 4, 204)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.Episodes(entryOf(t, x, tc.al)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Episodes = %+v, want %+v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		parts []Part
		want  Routes
	}{
		{"a whole series", entryOf(t, x, 1).Parts, Routes{TVDB: []int{10}}},
		{"a whole film", entryOf(t, x, 4).Parts, Routes{TMDBMovies: []int{30, 31}, IMDb: []string{"tt0000003"}}},
		{"an ambiguous entry", entryOf(t, x, 3).Parts, Routes{}},
		{"specials that are two movies", entryOf(t, x, 2).Parts, Routes{TVDB: []int{10}}},
		{"a combined part, then an anime with no count", entryOf(t, x, 5).Parts, Routes{TVDB: []int{10}}},
		{"a special on another series", entryOf(t, x, 6).Parts, Routes{TVDB: []int{11}}},
		{"a part landing nowhere", []Part{{AniDBID: 1, Kind: Regular, Start: 4, End: 4}}, Routes{TVDB: []int{10}}},
		{"a part stating nothing", []Part{{AniDBID: 1, Kind: Special, Start: 2, End: 2}}, Routes{TVDB: []int{10}, TMDBMovies: []int{41}}},
		{"a ranged part of a film", []Part{{AniDBID: 3, Kind: Regular, Start: 1, End: 1}}, Routes{TMDBMovies: []int{30, 31}}},
		{"a combined part of a film", []Part{{AniDBID: 3, Kind: Regular, Combined: true}}, Routes{TMDBMovies: []int{30, 31}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.Routes(Entry{Parts: tc.parts}); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Routes = %+v, want %+v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name string
		al   int
		want []SeriesSeasons
	}{
		{"a whole series", 1, []SeriesSeasons{{Series: 10, Seasons: []SeasonEpisodes{{Season: 1, Stated: []int{1, 2, 3}}}, Nowhere: 1}}},
		{"specials", 2, []SeriesSeasons{{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1, 3}}}, Unstated: 1}}},
		{"a combined part", 5, []SeriesSeasons{{Series: 10, Seasons: []SeasonEpisodes{{Season: 2, Default: []int{1}}}}}},
		{"a film on no series", 4, []SeriesSeasons{}},
		{"a special on another series", 6, []SeriesSeasons{{Series: 10, Nowhere: 1}, {Series: 11, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.TVDBSeasons(entryOf(t, x, tc.al)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TVDBSeasons = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A combined part names one entry episode only when its anime counts
// regular episodes; on a decoded document it otherwise names none, as
// every rangeless part does.
func TestCombinedPartWithNoCountNamesNothing(t *testing.T) {
	doc := sample()
	doc.Entries = append(doc.Entries, Entry{AniListID: 7, Type: "OVA", Parts: []Part{{AniDBID: 4, Kind: Regular, Combined: true}}})
	b, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(b), DefaultLimits())
	if err != nil {
		t.Fatalf("Decode = %v", err)
	}
	x := NewIndex(decoded)
	e := entryOf(t, x, 7)
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
	unstated := func(n int) Run { return Run{Kind: Regular, Start: n, End: n, TMDBMovies: []int{n}} }
	for _, tc := range []struct {
		name     string
		runs     []Run
		reading  SeriesReading
		seasons  SeriesSeasons
		unstated bool
	}{
		{"default then stated", []Run{one(1, dflt(0, 1)), one(2, at(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Default: []int{1}}}}, false},
		{"stated then default", []Run{one(1, at(0, 1)), one(2, dflt(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Default: []int{1}}}}, false},
		{"stated only", []Run{one(1, at(0, 1)), one(2, at(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}}, false},
		{"default only", []Run{one(1, dflt(0, 1)), one(2, dflt(0, 2))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Default: []int{1}}}}, false},
		{"two seasons, each on its own basis", []Run{one(1, at(0, 1)), one(2, dflt(1, 1))}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0}, {Season: 1, Default: true}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}, {Season: 1, Default: []int{1}}}}, false},
		{"nowhere", nil, SeriesReading{Series: 10}, SeriesSeasons{Series: 10, Nowhere: 1}, false},
		{"unstated", []Run{unstated(1), unstated(2)}, SeriesReading{Series: 10, Unstated: true}, SeriesSeasons{Series: 10, Unstated: 1}, true},
		{"nowhere and unstated", []Run{unstated(2)}, SeriesReading{Series: 10, Unstated: true}, SeriesSeasons{Series: 10, Unstated: 1}, true},
		{"stated and unstated", []Run{one(1, at(0, 1)), unstated(2)}, SeriesReading{Series: 10, Seasons: []SeasonBasis{{Season: 0}}}, SeriesSeasons{Series: 10, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := NewIndex(&Document{
				Anime:   []Anime{{AniDBID: 1, TVDBID: 10, Last: Last{Regular: 2}, Episodes: tc.runs}},
				Entries: []Entry{{AniListID: 1, Parts: []Part{{AniDBID: 1, Kind: Regular, Combined: true}}}},
			})
			e := entryOf(t, x, 1)
			eps := x.Episodes(e)
			if len(eps) != 1 {
				t.Fatalf("Episodes = %+v, want one combined entry episode", eps)
			}
			if got, want := x.OnTVDB(eps[0]), (TVDBReading{Series: []SeriesReading{tc.reading}, Unstated: tc.unstated}); !reflect.DeepEqual(got, want) {
				t.Errorf("OnTVDB = %+v, want %+v", got, want)
			}
			if got := x.TVDBSeasons(e); !reflect.DeepEqual(got, []SeriesSeasons{tc.seasons}) {
				t.Errorf("TVDBSeasons = %+v, want %+v", got, []SeriesSeasons{tc.seasons})
			}
		})
	}
}

// An entry episode unstated on its anime's series and landing only on
// another reads unstated there, not nowhere.
func TestOnTVDBKeepsUnstatedBesideALandingElsewhere(t *testing.T) {
	x := NewIndex(&Document{
		Anime: []Anime{{AniDBID: 1, TVDBID: 10, Last: Last{Regular: 2}, Episodes: []Run{
			{Kind: Regular, Start: 1, End: 1, TMDBMovies: []int{1}},
			{Kind: Regular, Start: 2, End: 2, TVDB: targets(on(20, 0, 2))},
		}}},
	})
	ep := EntryEpisode{Number: 1, Refs: []AniDBEpisode{anidbEpisode(1, Regular, 1, 0), anidbEpisode(1, Regular, 2, 0)}}
	want := TVDBReading{Series: []SeriesReading{{Series: 10, Unstated: true}, {Series: 20, Seasons: []SeasonBasis{{Season: 0}}}}, Unstated: true}
	if got := x.OnTVDB(ep); !reflect.DeepEqual(got, want) {
		t.Errorf("OnTVDB = %+v, want %+v", got, want)
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

// A row with no tvdb_id whose episodes land on series Y reads on Y alone,
// and is off TVDB where no run lands.
func TestZeroDefaultRow(t *testing.T) {
	x := golden(t, "testdata/series.json")
	if ep, ok := x.Episode(Ref{AniDB: 90001, Kind: Regular, Number: 2}); !ok || !reflect.DeepEqual(ep.TVDB, landsOn(placed(9200, 1, 2))) {
		t.Errorf("Episode(regular 2) = %+v, %t; want 9200 S1E2", ep.TVDB, ok)
	}
	if ep, ok := x.Episode(Ref{AniDB: 90001, Kind: Special, Number: 2}); !ok || ep.TVDB != nil {
		t.Errorf("Episode(uncovered special 2) = %+v, %t; want off TVDB", ep.TVDB, ok)
	}
	e := entryOf(t, x, 11)
	eps := x.Episodes(e)
	if len(eps) != 3 {
		t.Fatalf("Episodes = %+v, want 3", eps)
	}
	if got, want := x.OnTVDB(eps[0]), (TVDBReading{Series: []SeriesReading{{Series: 9200, Seasons: []SeasonBasis{{Season: 1}}}}}); !reflect.DeepEqual(got, want) {
		t.Errorf("OnTVDB(entry episode 1) = %+v, want %+v", got, want)
	}
	if got, want := x.AtTVDB(TVDBEpisode{9200, 1, 3}), []Landing{landing(90001, Regular, 3, 0, false)}; !reflect.DeepEqual(got, want) {
		t.Errorf("AtTVDB(9200 S1E3) = %+v, want %+v", got, want)
	}
	if got, want := x.AtTVDB(TVDBEpisode{9200, 0, 1}), []Landing{landing(90001, Special, 1, 0, false)}; !reflect.DeepEqual(got, want) {
		t.Errorf("AtTVDB(9200 S0E1) = %+v, want %+v", got, want)
	}
	if got, want := x.Series(9200), []Span{{AniDB: 90001, First: TVDBEpisode{9200, 1, 1}, Last: TVDBEpisode{9200, 1, 3}, Exact: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Series(9200) = %+v, want %+v", got, want)
	}
	if got := x.Routes(e).TVDB; !slices.Equal(got, []int{9200}) {
		t.Errorf("Routes(identity).TVDB = %v, want [9200]", got)
	}
}

// One special on S0E1 of series X and of series Y is found once on each.
func TestSpecialOnTwoSeries(t *testing.T) {
	x := golden(t, "testdata/series.json")
	want := []Landing{landing(90002, Special, 1, 0, false)}
	for _, series := range []int{9100, 9300} {
		if got := x.AtTVDB(TVDBEpisode{series, 0, 1}); !reflect.DeepEqual(got, want) {
			t.Errorf("AtTVDB(%d S0E1) = %+v, want %+v", series, got, want)
		}
	}
	eps := x.Episodes(entryOf(t, x, 13))
	if len(eps) != 1 {
		t.Fatalf("Episodes = %+v, want 1", eps)
	}
	reading := TVDBReading{Series: []SeriesReading{{Series: 9100, Seasons: []SeasonBasis{{Season: 0}}}, {Series: 9300, Seasons: []SeasonBasis{{Season: 0}}}}}
	if got := x.OnTVDB(eps[0]); !reflect.DeepEqual(got, reading) {
		t.Errorf("OnTVDB = %+v, want %+v", got, reading)
	}
	if got := x.Routes(entryOf(t, x, 13)).TVDB; !slices.Equal(got, []int{9100, 9300}) {
		t.Errorf("Routes(the special).TVDB = %v, want [9100 9300]", got)
	}
	if got := x.Routes(entryOf(t, x, 12)).TVDB; !slices.Equal(got, []int{9100}) {
		t.Errorf("Routes(identity).TVDB = %v, want [9100]", got)
	}
	b, err := os.ReadFile("testdata/series.json")
	if err != nil {
		t.Fatal(err)
	}
	pair := []byte(`[{"season":0,"episode":1},{"series":9300,"season":0,"episode":1}]`)
	if !bytes.Contains(b, pair) {
		t.Fatalf("Setup: the golden lacks %s", pair)
	}
	swapped := bytes.Replace(b, pair, []byte(`[{"series":9300,"season":0,"episode":1},{"season":0,"episode":1}]`), 1)
	if _, err := Decode(bytes.NewReader(swapped), DefaultLimits()); !errors.Is(err, ErrInvalid) {
		t.Errorf("Decode(the two targets out of order) = %v, want ErrInvalid", err)
	}
}

// A special of a row on X filed on Y reads nowhere on X and lands on Y.
func TestSpecialFiledElsewhere(t *testing.T) {
	x := golden(t, "testdata/series.json")
	alone := entryOf(t, x, 14)
	eps := x.Episodes(alone)
	if len(eps) != 1 {
		t.Fatalf("Episodes = %+v, want 1", eps)
	}
	reading := TVDBReading{Series: []SeriesReading{{Series: 9400}, {Series: 9500, Seasons: []SeasonBasis{{Season: 0}}}}}
	if got := x.OnTVDB(eps[0]); !reflect.DeepEqual(got, reading) {
		t.Errorf("OnTVDB = %+v, want %+v", got, reading)
	}
	seasons := []SeriesSeasons{{Series: 9400, Nowhere: 1}, {Series: 9500, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}}}
	if got := x.TVDBSeasons(alone); !reflect.DeepEqual(got, seasons) {
		t.Errorf("TVDBSeasons = %+v, want %+v", got, seasons)
	}
	if got := x.Routes(alone).TVDB; !slices.Equal(got, []int{9500}) {
		t.Errorf("Routes(the special alone).TVDB = %v, want [9500]", got)
	}
	if got := x.Routes(entryOf(t, x, 15)).TVDB; !slices.Equal(got, []int{9400, 9500}) {
		t.Errorf("Routes(it and a special on 9400).TVDB = %v, want [9400 9500]", got)
	}
}

func TestRoutesMixedShapes(t *testing.T) {
	x := golden(t, "testdata/series.json")
	for _, tc := range []struct {
		name string
		al   int
		want []int
	}{
		{"an identity entry with one regular episode on another series", 16, []int{9600, 9700}},
		{"a link part of one episode on Y and one nowhere on X", 17, []int{9900}},
		{"a link part of one episode on Y and one unstated on X", 18, []int{9900}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.Routes(entryOf(t, x, tc.al)).TVDB; !slices.Equal(got, tc.want) {
				t.Errorf("Routes.TVDB = %v, want %v", got, tc.want)
			}
		})
	}
}

// moviesDoc is an anime whose regular episodes 1-2 and 5-6 are one movie
// (5 and its alias 6), 3 another and 4 none, and a film sharing movie 5.
func moviesDoc() *Document {
	return &Document{Anime: []Anime{
		{AniDBID: 1, Type: "TV", TVDBID: 10, Last: Last{Regular: 6}, Episodes: []Run{
			{Kind: Regular, Start: 1, End: 2, TMDBMovies: []int{5, 6}},
			{Kind: Regular, Start: 3, End: 3, TMDBMovies: []int{7}},
			{Kind: Regular, Start: 5, End: 6, TMDBMovies: []int{5, 6}},
		}},
		{AniDBID: 2, Type: "MOVIE", IMDbIDs: []string{"tt0000002"}, Last: Last{Regular: 1}, EpisodesListed: true, Episodes: []Run{
			{Kind: Regular, Start: 1, End: 1, AniDBEpisodeIDs: []int{21}, TMDBMovies: []int{5, 8}},
		}},
	}}
}

func regulars(aid, start, end int) Part {
	return Part{AniDBID: aid, Kind: Regular, Start: start, End: end}
}

func TestRoutesTMDBMovies(t *testing.T) {
	x := NewIndex(moviesDoc())
	for _, tc := range []struct {
		name  string
		parts []Part
		want  []int
	}{
		{"one list on every episode", []Part{regulars(1, 1, 2)}, []int{5, 6}},
		{"one list on episodes of two runs", []Part{regulars(1, 1, 2), regulars(1, 5, 6)}, []int{5, 6}},
		{"two lists", []Part{regulars(1, 2, 3)}, nil},
		{"one episode with none", []Part{regulars(1, 3, 4)}, nil},
		{"an episode with none first", []Part{regulars(1, 4, 5)}, nil},
		{"only an episode with none", []Part{regulars(1, 4, 4)}, nil},
		{"two movies, never their union", []Part{regulars(1, 1, 3)}, nil},
		{"a whole film", []Part{{AniDBID: 2, Kind: Regular}}, []int{5, 8}},
		{"episodes of two anime on lists sharing an id", []Part{regulars(1, 1, 1), {AniDBID: 2, Kind: Regular}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.Routes(Entry{Parts: tc.parts}).TMDBMovies; !slices.Equal(got, tc.want) {
				t.Errorf("Routes.TMDBMovies = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIndexMovies(t *testing.T) {
	x := NewIndex(moviesDoc())
	for _, tc := range []struct {
		name  string
		parts []Part
		want  [][]int
	}{
		{"each distinct work once, in episode order", []Part{{AniDBID: 1, Kind: Regular}}, [][]int{{5, 6}, {7}}},
		{"episode order across parts", []Part{regulars(1, 3, 3), regulars(1, 1, 1)}, [][]int{{7}, {5, 6}}},
		{"an episode with none", []Part{regulars(1, 4, 4)}, nil},
		{"two anime", []Part{{AniDBID: 2, Kind: Regular}, regulars(1, 5, 6)}, [][]int{{5, 8}, {5, 6}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.Movies(Entry{Parts: tc.parts}); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Movies = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAtTMDBMovie(t *testing.T) {
	x := NewIndex(moviesDoc())
	for _, tc := range []struct {
		name string
		id   int
		want []Landing
	}{
		{"every episode of every run on it, across anime", 5, []Landing{
			landing(1, Regular, 1, 0, false), landing(1, Regular, 2, 0, false), landing(1, Regular, 5, 0, false), landing(1, Regular, 6, 0, false), landing(2, Regular, 1, 21, false),
		}},
		{"an alias", 6, []Landing{landing(1, Regular, 1, 0, false), landing(1, Regular, 2, 0, false), landing(1, Regular, 5, 0, false), landing(1, Regular, 6, 0, false)}},
		{"one episode", 7, []Landing{landing(1, Regular, 3, 0, false)}},
		{"a film's alias", 8, []Landing{landing(2, Regular, 1, 21, false)}},
		{"no episode", 9, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.AtTMDBMovie(tc.id); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("AtTMDBMovie(%d) = %+v, want %+v", tc.id, got, tc.want)
			}
		})
	}
	s := NewIndex(sample())
	if got, want := s.AtTMDBMovie(40), []Landing{landing(1, Special, 1, 201, false)}; !reflect.DeepEqual(got, want) {
		t.Errorf("AtTMDBMovie(40) = %+v, want %+v", got, want)
	}
}

// Each special filed under another show is found there with its AniDB
// episode id, and its anime's identity entry still routes one series.
func TestCrossSeriesCases(t *testing.T) {
	x := golden(t, "testdata/series.json")
	for _, tc := range []struct {
		name   string
		at     TVDBEpisode
		want   Landing
		al     int
		series int
	}{
		{"AniDB 65 special 2", TVDBEpisode{72462, 0, 4}, landing(65, Special, 2, 650020, false), 1, 79714},
		{"AniDB 153 special 1", TVDBEpisode{330514, 0, 1}, landing(153, Special, 1, 1530010, false), 2, 78923},
		{"AniDB 2186 special 4", TVDBEpisode{254931, 0, 1}, landing(2186, Special, 4, 21860040, false), 3, 74890},
		{"AniDB 2186 special 7", TVDBEpisode{254931, 0, 2}, landing(2186, Special, 7, 21860070, false), 3, 74890},
		{"AniDB 3513 special 1", TVDBEpisode{77062, 0, 1}, landing(3513, Special, 1, 35130010, false), 4, 73887},
		{"AniDB 3513 special 2", TVDBEpisode{77062, 0, 2}, landing(3513, Special, 2, 35130020, false), 4, 73887},
		{"AniDB 9216 special 2", TVDBEpisode{383905, 3, 1}, landing(9216, Special, 2, 92160020, false), 5, 76703},
		{"AniDB 9216 special 3", TVDBEpisode{383905, 3, 2}, landing(9216, Special, 3, 92160030, false), 5, 76703},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := x.AtTVDB(tc.at); !reflect.DeepEqual(got, []Landing{tc.want}) {
				t.Errorf("AtTVDB(%+v) = %+v, want %+v", tc.at, got, tc.want)
			}
			if got := x.Routes(entryOf(t, x, tc.al)).TVDB; !slices.Equal(got, []int{tc.series}) {
				t.Errorf("Routes(identity).TVDB = %v, want [%d]", got, tc.series)
			}
		})
	}
}

// A TVDB episode id reaches every AniDB episode on its TVDB episode,
// those whose target holds no id included, on any series.
func TestAtTVDBEpisodeID(t *testing.T) {
	x := NewIndex(sample())
	place := golden(t, "testdata/place.json")
	series := golden(t, "testdata/series.json")
	for _, tc := range []struct {
		name string
		got  []Landing
		want []Landing
	}{
		{"a slot two targets share, one holding the id", x.AtTVDBEpisodeID(7005), []Landing{landing(1, Special, 1, 201, false), landing(1, Other, 1, 0, false)}},
		{"the second episode of a run", x.AtTVDBEpisodeID(7002), []Landing{landing(1, Regular, 2, 102, false)}},
		{"another series", x.AtTVDBEpisodeID(7101), []Landing{landing(1, Special, 4, 204, false)}},
		{"an id no target holds", x.AtTVDBEpisodeID(7003), nil},
		{"id 0", x.AtTVDBEpisodeID(0), nil},
		{"a golden's slot of two runs", place.AtTVDBEpisodeID(5021), []Landing{landing(1, Regular, 6, 1006, false), landing(1, Regular, 7, 1007, false)}},
		{"a golden's default placement", place.AtTVDBEpisodeID(5005), []Landing{landing(1, Special, 5, 2005, true)}},
		{"a golden's special filed under another show", series.AtTVDBEpisodeID(254931002), []Landing{landing(2186, Special, 7, 21860070, false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("got %+v, want %+v", tc.got, tc.want)
			}
		})
	}
}

// On answers for any series: a series the reading has, as it has it, and
// any other with nothing landing there, unstated exactly when an AniDB
// episode of the entry episode states nothing.
func TestTVDBReadingOn(t *testing.T) {
	r := TVDBReading{Series: []SeriesReading{{Series: 10, Unstated: true}, {Series: 20, Seasons: []SeasonBasis{{Season: 0, Default: true}}}}, Unstated: true}
	for _, tc := range []struct {
		name    string
		reading TVDBReading
		series  int
		want    SeriesReading
	}{
		{"a series it lands on", r, 20, SeriesReading{Series: 20, Seasons: []SeasonBasis{{Season: 0, Default: true}}}},
		{"its anime's series", r, 10, SeriesReading{Series: 10, Unstated: true}},
		{"another series, with nothing stated", r, 30, SeriesReading{Series: 30, Unstated: true}},
		{"another series, everything stated", TVDBReading{Series: []SeriesReading{{Series: 10}}}, 30, SeriesReading{Series: 30}},
		{"no series at all", TVDBReading{Off: true}, 30, SeriesReading{Series: 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.reading.On(tc.series); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("On(%d) = %+v, want %+v", tc.series, got, tc.want)
			}
		})
	}
	got := r.On(20)
	got.Seasons[0].Season = 9
	if r.Series[1].Seasons[0].Season != 0 {
		t.Error("On returned seasons the reading shares")
	}
}

// An entry routed to series Y whose other episode states nothing on its
// anime's series X reads unstated on Y too, and one landing nowhere on X
// reads nowhere on Y: every entry episode counts on every series.
func TestTVDBSeasonsReadsEverySeries(t *testing.T) {
	x := golden(t, "testdata/series.json")
	for _, tc := range []struct {
		name    string
		al      int
		reading SeriesReading
		want    []SeriesSeasons
	}{
		{"a special on Y and one stating nothing on X", 18, SeriesReading{Series: 9900, Unstated: true}, []SeriesSeasons{
			{Series: 9800, Nowhere: 1, Unstated: 1},
			{Series: 9900, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}, Unstated: 1},
		}},
		{"a special on Y and one landing nowhere on X", 17, SeriesReading{Series: 9900}, []SeriesSeasons{
			{Series: 9800, Nowhere: 2},
			{Series: 9900, Seasons: []SeasonEpisodes{{Season: 0, Stated: []int{1}}}, Nowhere: 1},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := entryOf(t, x, tc.al)
			eps := x.Episodes(e)
			if len(eps) != 2 {
				t.Fatalf("Episodes = %+v, want 2", eps)
			}
			if got := x.Routes(e).TVDB; !slices.Equal(got, []int{9900}) {
				t.Errorf("Routes.TVDB = %v, want [9900]", got)
			}
			if got := x.OnTVDB(eps[1]).On(9900); !reflect.DeepEqual(got, tc.reading) {
				t.Errorf("OnTVDB(entry episode 2).On(9900) = %+v, want %+v", got, tc.reading)
			}
			if got := x.TVDBSeasons(e); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TVDBSeasons = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An entry episode off TVDB reads nowhere on the entry's series Y, whether
// or not a run covers it: a run stating no tvdb on a row with no tvdb_id
// states no landing, as no run does.
func TestTVDBSeasonsReadsAnEpisodeOffTVDBAsNowhere(t *testing.T) {
	x := NewIndex(&Document{
		Anime: []Anime{
			{AniDBID: 1, TVDBID: 5000, Last: Last{Regular: 1}, Episodes: []Run{{Kind: Regular, Start: 1, End: 1, TVDB: targets(at(1, 1))}}},
			{AniDBID: 2, Type: "MOVIE", Last: Last{Regular: 1}},
			{AniDBID: 3, Type: "MOVIE", Last: Last{Regular: 1}, Episodes: []Run{{Kind: Regular, Start: 1, End: 1, TMDBMovies: []int{555}}}},
		},
		Entries: []Entry{
			{AniListID: 1, Parts: []Part{{AniDBID: 1, Kind: Regular}, {AniDBID: 2, Kind: Regular}}},
			{AniListID: 2, Parts: []Part{{AniDBID: 1, Kind: Regular}, {AniDBID: 3, Kind: Regular}}},
		},
	})
	want := []SeriesSeasons{{Series: 5000, Seasons: []SeasonEpisodes{{Season: 1, Stated: []int{1}}}, Nowhere: 1}}
	for _, tc := range []struct {
		name string
		al   int
	}{
		{"no run", 1},
		{"a run with movies only", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := entryOf(t, x, tc.al)
			eps := x.Episodes(e)
			if len(eps) != 2 {
				t.Fatalf("Episodes = %+v, want 2", eps)
			}
			r := x.OnTVDB(eps[1])
			if want := (TVDBReading{Off: true}); !reflect.DeepEqual(r, want) {
				t.Errorf("OnTVDB(entry episode 2) = %+v, want %+v", r, want)
			}
			if got, want := r.On(5000), (SeriesReading{Series: 5000}); !reflect.DeepEqual(got, want) {
				t.Errorf("OnTVDB(entry episode 2).On(5000) = %+v, want %+v", got, want)
			}
			if got := x.TVDBSeasons(e); !reflect.DeepEqual(got, want) {
				t.Errorf("TVDBSeasons = %+v, want %+v", got, want)
			}
		})
	}
}

// A result is the caller's: changing it leaves the index as it was.
func TestLookupsReturnCopies(t *testing.T) {
	x := NewIndex(sample())
	e, _ := x.Entry(1)
	e.Parts[0].AniDBID = 9
	e.IDs.MAL[0] = 99
	e.IDs.AniDB[0] = 99
	a, _ := x.Anime(1)
	(*a.Episodes[0].TVDB)[0].Episode = 99
	(*a.Episodes[6].TVDB)[0].Series = 99
	(*a.Episodes[0].TVDB)[0].EpisodeIDs[0] = 99
	a.Episodes[0].AniDBEpisodeIDs[0] = 99
	a.Episodes[4].TMDBMovies[0] = 99
	a.Episodes[1].Start = 7
	a.MALIDs[0] = 99
	a.TVDBSeries()[0] = 99
	ep, _ := x.Episode(Ref{AniDB: 1, Kind: Special, Number: 1})
	ep.TMDBMovies[0] = 99
	ep.TVDB.Placements[0].Series = 99
	m := x.Movies(entryOf(t, x, 2))
	m[0][0] = 99
	if e2, _ := x.Entry(1); !reflect.DeepEqual(e2, sample().Entries[0]) {
		t.Errorf("Entry(1) after editing a result = %+v", e2)
	}
	if a2, _ := x.Anime(1); !reflect.DeepEqual(a2, sample().Anime[0]) {
		t.Errorf("Anime(1) after editing a result = %+v", a2)
	}
	if got, _ := x.Episode(Ref{AniDB: 1, Kind: Special, Number: 1}); !slices.Equal(got.TMDBMovies, []int{40}) || got.TVDB.Placements[0].Series != 10 {
		t.Errorf("Episode(special 1) after editing a result = %+v movies %v", got.TVDB, got.TMDBMovies)
	}
	if got := x.Movies(entryOf(t, x, 2)); !reflect.DeepEqual(got, [][]int{{40}, {41}}) {
		t.Errorf("Movies(AniList 2) after editing a result = %v", got)
	}
}
