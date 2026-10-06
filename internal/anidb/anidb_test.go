package anidb

import (
	"slices"
	"testing"
)

func finished(regular int, specials ...Episode) Anime {
	return Anime{Regular: make([]string, regular), Specials: specials, Finished: true}
}

func TestCount(t *testing.T) {
	snap := &Snapshot{Commit: commit, Anime: map[int]Anime{
		1: finished(6),
		2: {Regular: make([]string, 6)},
		3: finished(0),
		4: finished(6),
	}}
	rows := []Row{{AniDBID: 4, RegularEpisodes: 5}, {AniDBID: 9, RegularEpisodes: 2}}
	f := NewFacts(snap, rows, map[int]int{1: 1, 2: 3, 3: 1, 4: 6, 5: 12, 6: -1})
	for _, tc := range []struct {
		desc string
		aid  int
		want int
	}{
		{"a finished anime takes the mirror's count over the database's", 1, 6},
		{"an airing anime takes the database's count", 2, 3},
		{"a finished anime the mirror lists with no regular episode takes the database's", 3, 1},
		{"a counts row overrides the mirror", 4, 5},
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
