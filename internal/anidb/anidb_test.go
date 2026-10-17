package anidb

import (
	"slices"
	"testing"
)

// numbered is regular episodes 1 to n.
func numbered(n int) []Episode {
	out := make([]Episode, n)
	for i := range out {
		out[i].Number = i + 1
	}
	return out
}

func finished(regular int, specials ...Episode) Anime {
	return Anime{Regular: numbered(regular), Specials: specials, Finished: true}
}

func TestCount(t *testing.T) {
	snap := &Snapshot{Commit: commit, Anime: map[int]Anime{
		1: finished(6),
		2: {Regular: numbered(6)},
		3: finished(0),
		4: finished(6),
		8: {Regular: []Episode{{Number: 1}, {Number: 3}}, Finished: true},
	}}
	rows := []Row{{AniDBID: 4, RegularEpisodes: 5}, {AniDBID: 9, RegularEpisodes: 2}}
	f := NewFacts(snap, rows, map[int]int{1: 1, 2: 3, 3: 1, 4: 6, 5: 12, 6: -1, 8: 4})
	for _, tc := range []struct {
		desc string
		aid  int
		want int
	}{
		{"a finished anime takes the mirror's count over the database's", 1, 6},
		{"an airing anime takes the database's count", 2, 3},
		{"a finished anime the mirror lists with no regular episode takes the database's", 3, 1},
		{"a counts row overrides the mirror", 4, 5},
		{"a finished anime whose regular list has a gap takes the database's", 8, 4},
		{"an anime the mirror does not list takes the database's", 5, 12},
		{"a counts row counts what no source does", 9, 2},
		{"a negative database count is no count", 6, 0},
		{"no source", 7, 0},
	} {
		if got := f.Count(tc.aid); got != tc.want {
			t.Errorf("Count(%d) = %d, want %d (%s)", tc.aid, got, tc.want, tc.desc)
		}
	}
}

func TestRedundantRows(t *testing.T) {
	snap := &Snapshot{Commit: commit, Anime: map[int]Anime{1: finished(6), 2: finished(6)}}
	rows := []Row{{AniDBID: 1, RegularEpisodes: 6}, {AniDBID: 2, RegularEpisodes: 5}, {AniDBID: 3, RegularEpisodes: 4}, {AniDBID: 4, RegularEpisodes: 2}}
	f := NewFacts(snap, rows, map[int]int{3: 4})
	if got, want := f.RedundantRows(), []int{1, 3}; !slices.Equal(got, want) {
		t.Errorf("RedundantRows() = %v, want %v: the rows equal to the mirror's and the database's count", got, want)
	}
}

func TestAnime(t *testing.T) {
	sp := []Episode{{Number: 1, AirDate: "2005-04-21"}}
	f := NewFacts(&Snapshot{Commit: commit, Anime: map[int]Anime{1: finished(2, sp...)}}, nil, nil)
	if a, ok := f.Anime(1); !ok || !slices.Equal(a.Specials, sp) || len(a.Regular) != 2 {
		t.Errorf("Anime(1) = %+v, %t; want 2 regular episodes and %v", a, ok, sp)
	}
	if a, ok := f.Anime(2); ok || a.Specials != nil {
		t.Errorf("Anime(2) = %+v, %t; want nothing for an anime the mirror does not list", a, ok)
	}
	if f.Commit() != commit {
		t.Errorf("Commit() = %q, want %q", f.Commit(), commit)
	}
}

func TestSeasonZero(t *testing.T) {
	a := Anime{
		Specials: []Episode{{Number: 1, Titles: "s1"}, {Number: 201, Titles: "s201"}},
		Extras:   []Extra{{Kind: Promotion, Number: 1, SeasonZero: 0, Titles: "t1"}, {Kind: Other, Number: 1, SeasonZero: 401, Titles: "o1"}},
	}
	for _, tc := range []struct {
		titles string
		k      int
		extra  bool
		ok     bool
	}{
		{"s1", 1, false, true},
		{"s201", 201, false, true},
		{"o1", 401, true, true},
		{"", 402, false, false},
		{"", 0, false, false},
	} {
		if e, extra, ok := a.SeasonZero(tc.k); e.Titles != tc.titles || extra != tc.extra || ok != tc.ok {
			t.Errorf("SeasonZero(%d) = %+v, %t, %t; want titles %q, %t, %t", tc.k, e, extra, ok, tc.titles, tc.extra, tc.ok)
		}
	}
}

func TestSeasonZeroNumbering(t *testing.T) {
	for _, tc := range []struct {
		kind       string
		number, k  int
		addressing bool
	}{
		{Special, 1, 1, true},
		{Special, 100, 100, true},
		{Special, 101, 101, false},
		{Credit, 1, 101, true},
		{Credit, 100, 200, true},
		{Credit, 101, 201, false},
		{Promotion, 1, 201, true},
		{Parody, 100, 400, true},
		{Parody, 101, 401, false},
		{Other, 1, 401, true},
		{Other, 600, 1000, true},
	} {
		k, ok := seasonZeroNumber(tc.kind, tc.number)
		if ok != tc.addressing || ok && k != tc.k {
			t.Errorf("seasonZeroNumber(%s, %d) = %d, %t; want %d, %t", tc.kind, tc.number, k, ok, tc.k, tc.addressing)
		}
		if !ok {
			continue
		}
		if kind, n, ok := seasonZeroKind(k); !ok || kind != tc.kind || n != tc.number {
			t.Errorf("seasonZeroKind(%d) = %s %d, %t; want %s %d", k, kind, n, ok, tc.kind, tc.number)
		}
	}
	for _, bad := range []struct {
		kind   string
		number int
	}{{Credit, 0}, {"trailer", 1}} {
		if k, ok := seasonZeroNumber(bad.kind, bad.number); ok {
			t.Errorf("seasonZeroNumber(%s, %d) = %d, want none", bad.kind, bad.number, k)
		}
	}
	if _, _, ok := seasonZeroKind(0); ok {
		t.Error("seasonZeroKind(0) decodes, want none below 1")
	}
}

// SeasonZeroFor and SeasonZeroOf answer through the listed episodes first,
// so an unlisted extra number a listed episode holds addresses nothing; a
// special always has its own number, listed or not.
func TestSeasonZeroForTheListedEpisodes(t *testing.T) {
	a := Anime{
		Specials: []Episode{{Number: 101}, {Number: 3}},
		Extras:   []Extra{{Kind: Credit, Number: 1}, {Kind: Credit, Number: 2, SeasonZero: 102}, {Kind: Other, Number: 2, SeasonZero: 402}},
	}
	for _, tc := range []struct {
		kind      string
		number, k int
		ok        bool
	}{
		{Special, 101, 101, true},
		{Special, 3, 3, true},
		{Special, 5, 5, true},
		{Credit, 1, 0, false},
		{Credit, 2, 102, true},
		{Credit, 3, 103, true},
		{Other, 2, 402, true},
		{Special, 102, 102, true},
		{Special, 0, 0, false},
	} {
		if k, ok := a.SeasonZeroFor(tc.kind, tc.number); k != tc.k || ok != tc.ok {
			t.Errorf("SeasonZeroFor(%s, %d) = %d, %t; want %d, %t", tc.kind, tc.number, k, ok, tc.k, tc.ok)
		}
	}
	for k, want := range map[int][2]any{101: {Special, 101}, 102: {Credit, 2}, 103: {Credit, 3}, 402: {Other, 2}, 7: {Special, 7}} {
		if kind, n, ok := a.SeasonZeroOf(k); !ok || kind != want[0] || n != want[1] {
			t.Errorf("SeasonZeroOf(%d) = %s %d, %t; want %v", k, kind, n, ok, want)
		}
	}
	if a.Last(Special) != 101 || a.Last(Credit) != 2 || a.Last(Other) != 2 || a.Last(Parody) != 0 {
		t.Errorf("Last = special %d, credit %d, other %d, parody %d; want 101, 2, 2, 0", a.Last(Special), a.Last(Credit), a.Last(Other), a.Last(Parody))
	}
}

func TestEpisodesListsEachKindWithItsIDs(t *testing.T) {
	a := Anime{
		Regular:  []Episode{{Number: 1, ID: 11}, {Number: 3, ID: 12}},
		Specials: []Episode{{Number: 2, ID: 21}, {Number: 45, ID: 22}},
		Extras:   []Extra{{Kind: Credit, Number: 1, ID: 31}, {Kind: Other, Number: 3, ID: 41}, {Kind: Other, Number: 7, ID: 42}},
	}
	for kind, want := range map[string][]Listed{
		Regular:   {{Number: 1, ID: 11}, {Number: 3, ID: 12}},
		Special:   {{Number: 2, ID: 21}, {Number: 45, ID: 22}},
		Credit:    {{Number: 1, ID: 31}},
		Promotion: nil,
		Other:     {{Number: 3, ID: 41}, {Number: 7, ID: 42}},
	} {
		if got := a.Episodes(kind); !slices.Equal(got, want) {
			t.Errorf("Episodes(%s) = %v, want %v", kind, got, want)
		}
	}
}

func TestRegularEpisodeFindsTheListedNumber(t *testing.T) {
	a := Anime{Regular: []Episode{{Number: 1, ID: 11}, {Number: 3, ID: 13}, {Number: 7, ID: 17}}}
	for n, want := range map[int]int{0: 0, 1: 11, 2: 0, 3: 13, 6: 0, 7: 17, 8: 0} {
		e, ok := a.RegularEpisode(n)
		if ok != (want > 0) || e.ID != want || ok && e.Number != n {
			t.Errorf("RegularEpisode(%d) = %+v, %t; want id %d", n, e, ok, want)
		}
	}
}
