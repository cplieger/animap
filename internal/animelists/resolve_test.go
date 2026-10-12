package animelists

import (
	"slices"
	"testing"
)

func TestPlace(t *testing.T) {
	row := func(attrs map[string]string, text string) Row { return Row{Attrs: attrs, Text: text} }
	rows := []Row{
		row(map[string]string{"anidbseason": "1", "tvdbseason": "2"}, ";3-7+8;4-0;"),
		row(map[string]string{"anidbseason": "1", "tvdbseason": "3", "start": "10", "end": "12", "offset": "-4"}, ""),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "1"}, ";2-13;"),
	}
	node := func(attrs map[string]string) *Node { return &Node{Attrs: attrs, Rows: rows} }
	season1 := node(map[string]string{"tvdbid": "10", "defaulttvdbseason": "1", "episodeoffset": "-2"})
	absolute := node(map[string]string{"tvdbid": "10", "defaulttvdbseason": "a", "episodeoffset": "5"})
	noOffset := node(map[string]string{"tvdbid": "10", "defaulttvdbseason": "1"})
	unset := node(map[string]string{"tvdbid": "10"})
	tmdb := node(map[string]string{"tmdbtv": "20", "tmdbseason": "2", "tmdboffset": "3"})
	tmdbAbsolute := node(map[string]string{"tmdbtv": "20", "tmdbseason": "a"})
	for _, tc := range []struct {
		n    *Node
		name string
		want Placement
		sd   Side
		s, k int
	}{
		{name: "a row to two episodes", n: season1, s: 1, k: 3, want: Placement{Targets: [][2]int{{2, 7}, {2, 8}}}},
		{name: "a row to no episode", n: season1, s: 1, k: 4, want: Placement{}},
		{name: "a range row", n: season1, s: 1, k: 11, want: Placement{Targets: [][2]int{{3, 7}}}},
		{name: "the default season and offset", n: season1, s: 1, k: 6, want: Placement{Targets: [][2]int{{1, 4}}, Default: true}},
		{name: "an absent offset reads as zero", n: noOffset, s: 1, k: 6, want: Placement{Targets: [][2]int{{1, 6}}, Default: true}},
		{name: "an offset below episode 1", n: season1, s: 1, k: 2, want: Placement{Default: true}},
		{name: "an absolute default", n: absolute, s: 1, k: 6, want: Placement{Absolute: 11, Default: true}},
		{name: "no default season", n: unset, s: 1, k: 6, want: Placement{Default: true}},
		{name: "a special through a row", n: unset, s: 0, k: 2, want: Placement{Targets: [][2]int{{1, 13}}}},
		{name: "a special by default", n: season1, s: 0, k: 5, want: Placement{Targets: [][2]int{{0, 5}}, Default: true}},
		{name: "TMDB's default season and offset past a TVDB row", n: tmdb, sd: TMDB, s: 1, k: 3, want: Placement{Targets: [][2]int{{2, 6}}, Default: true}},
		{name: "no absolute numbering on TMDB", n: tmdbAbsolute, sd: TMDB, s: 1, k: 3, want: Placement{Default: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.n.Place(tc.sd, tc.s, tc.k)
			if !slices.Equal(got.Targets, tc.want.Targets) || got.Absolute != tc.want.Absolute || got.Default != tc.want.Default {
				t.Errorf("Place(%d, %d, %d) = %+v, want %+v", tc.sd, tc.s, tc.k, got, tc.want)
			}
		})
	}
}

func TestSpecialTVDB(t *testing.T) {
	row := func(attrs map[string]string, text string) Row { return Row{Attrs: attrs, Text: text} }
	n := &Node{Attrs: map[string]string{"tvdbid": "10"}, Rows: []Row{
		row(map[string]string{"anidbseason": "0", "tvdbseason": "0"}, ";1-8;2-0;"),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "1"}, ";3-13;"),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "0", "start": "4", "end": "6", "offset": "-1"}, ""),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "0"}, ";9-1+2;"),
	}}
	for _, tc := range []struct {
		name    string
		k, s, e int
		ok      bool
	}{
		{"a pair row", 1, 0, 8, true},
		{"a row to a regular season", 3, 1, 13, true},
		{"a range with an offset", 5, 0, 4, true},
		{"no row, the default", 7, 0, 7, true},
		{"a row to no episode", 2, 0, 0, false},
		{"a row to two episodes", 9, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, ok := n.SpecialTVDB(tc.k)
			if ok != tc.ok || (ok && (s != tc.s || e != tc.e)) {
				t.Errorf("SpecialTVDB(%d) = %d, %d, %t; want %d, %d, %t", tc.k, s, e, ok, tc.s, tc.e, tc.ok)
			}
		})
	}
}
