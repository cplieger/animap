package mapread

import (
	"slices"
	"strings"
	"testing"
)

func TestKindsReturnsACopy(t *testing.T) {
	ks := Kinds()
	ks[0] = Other
	if got := Kinds(); got[0] != Regular {
		t.Errorf("Kinds() after a caller changed its result = %v, want regular first", got)
	}
	if _, err := Decode(strings.NewReader(encodeSample(t)), DefaultLimits()); err != nil {
		t.Errorf("Decode after a caller changed Kinds()' result = %v, want the document", err)
	}
}

func TestPartIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		part Part
		want bool
	}{
		{"the rangeless regular part", Part{AniDBID: 1, Kind: Regular}, true},
		{"a ranged regular part", Part{AniDBID: 1, Kind: Regular, Start: 1, End: 12}, false},
		{"a start alone", Part{AniDBID: 1, Kind: Regular, Start: 3}, false},
		{"an end alone", Part{AniDBID: 1, Kind: Regular, End: 12}, false},
		{"a combined part", Part{AniDBID: 1, Kind: Regular, Combined: true}, false},
		{"rangeless specials", Part{AniDBID: 1, Kind: Special}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.part.Identity(); got != tc.want {
				t.Errorf("%+v.Identity() = %t, want %t", tc.part, got, tc.want)
			}
		})
	}
}

func TestRunExtendedBy(t *testing.T) {
	run := Run{Kind: Regular, Start: 1, End: 2, AniDBEpisodeIDs: []int{1, 2}, TVDB: targets(at(1, 1), dflt(2, 5), on(20, 0, 3)), TMDBMovies: []int{7}}
	next := func(ts *[]Target, movies ...int) Run {
		return Run{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: ts, TMDBMovies: movies}
	}
	for _, tc := range []struct {
		name string
		next Run
		want bool
	}{
		{"the next episode on every target", Run{Kind: Regular, Start: 3, End: 4, AniDBEpisodeIDs: []int{3, 4}, TVDB: targets(at(1, 3), dflt(2, 7), on(20, 0, 5)), TMDBMovies: []int{7}}, true},
		{"another kind", Run{Kind: Special, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 3), dflt(2, 7), on(20, 0, 5)), TMDBMovies: []int{7}}, false},
		{"a gap in the numbers", Run{Kind: Regular, Start: 4, End: 4, AniDBEpisodeIDs: []int{4}, TVDB: targets(at(1, 4), dflt(2, 8), on(20, 0, 6)), TMDBMovies: []int{7}}, false},
		{"not listed", Run{Kind: Regular, Start: 3, End: 3, TVDB: targets(at(1, 3), dflt(2, 7), on(20, 0, 5)), TMDBMovies: []int{7}}, false},
		{"a target not one on", next(targets(at(1, 4), dflt(2, 7), on(20, 0, 5)), 7), false},
		{"a target on another season", next(targets(at(1, 3), dflt(3, 7), on(20, 0, 5)), 7), false},
		{"a target's default flag differs", next(targets(at(1, 3), at(2, 7), on(20, 0, 5)), 7), false},
		{"a target on another series", next(targets(at(1, 3), dflt(2, 7), on(21, 0, 5)), 7), false},
		{"a target moved onto the row's series", next(targets(at(1, 3), dflt(2, 7), at(0, 5)), 7), false},
		{"fewer targets", next(targets(at(1, 3), dflt(2, 7)), 7), false},
		{"TVDB stating nothing", next(nil, 7), false},
		{"other movies", next(targets(at(1, 3), dflt(2, 7), on(20, 0, 5)), 8), false},
		{"another alias of the movie", next(targets(at(1, 3), dflt(2, 7), on(20, 0, 5)), 7, 9), false},
		{"no movie", next(targets(at(1, 3), dflt(2, 7), on(20, 0, 5))), false},
		{"TVDB episode ids on one target of the next only", next(targets(at(1, 3), dflt(2, 7), withIDs(on(20, 0, 5), 9)), 7), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := run.ExtendedBy(&tc.next); got != tc.want {
				t.Errorf("ExtendedBy(%+v) = %t, want %t", tc.next, got, tc.want)
			}
		})
	}
	withIDsRun := Run{Kind: Regular, Start: 1, End: 1, TVDB: targets(withIDs(at(1, 1), 5))}
	if !withIDsRun.ExtendedBy(&Run{Kind: Regular, Start: 2, End: 2, TVDB: targets(withIDs(at(1, 2), 6))}) {
		t.Error("ExtendedBy of two runs whose target holds TVDB episode ids on each = false, want true")
	}
	if withIDsRun.ExtendedBy(&Run{Kind: Regular, Start: 2, End: 2, TVDB: targets(at(1, 2))}) {
		t.Error("ExtendedBy of a run with TVDB episode ids by one without = true, want false")
	}
	unstated := Run{Kind: Other, Start: 1, End: 1}
	if !unstated.ExtendedBy(&Run{Kind: Other, Start: 2, End: 2}) {
		t.Error("ExtendedBy of two runs stating nothing = false, want true")
	}
}

func TestTVDBSeries(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    Anime
		want []int
	}{
		{"the row's series alone", Anime{TVDBID: 10, Episodes: []Run{{TVDB: targets(at(1, 1))}, {TVDB: targets()}}}, []int{10}},
		{"the row's series first, then each other in run order, once", Anime{TVDBID: 10, Episodes: []Run{
			{TVDB: targets(on(30, 0, 1))}, {TVDB: targets(at(0, 1), on(20, 0, 1), on(30, 0, 2))}, {TMDBMovies: []int{5}}, {TVDB: targets(on(20, 0, 2))},
		}}, []int{10, 30, 20}},
		{"a row on no series of its own", Anime{Episodes: []Run{{TVDB: targets(on(20, 1, 1))}, {TVDB: targets(on(20, 0, 1), on(40, 0, 1))}}}, []int{20, 40}},
		{"a row on no series", Anime{Episodes: []Run{{TMDBMovies: []int{5}}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.TVDBSeries(); !slices.Equal(got, tc.want) {
				t.Errorf("TVDBSeries() = %v, want %v", got, tc.want)
			}
		})
	}
}
