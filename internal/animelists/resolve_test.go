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
		row(map[string]string{"anidbseason": "0", "tvdbseason": "4", "tmdbseason": "5"}, ";401-1;413-13;"),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "6", "tmdbseason": "6", "start": "201", "end": "203", "offset": "-200"}, ""),
	}
	node := func(attrs map[string]string) *Node { return &Node{Attrs: attrs, Rows: rows} }
	season1 := node(map[string]string{"tvdbid": "10", "defaulttvdbseason": "1", "episodeoffset": "-2"})
	absolute := node(map[string]string{"tvdbid": "10", "defaulttvdbseason": "a", "episodeoffset": "5"})
	noOffset := node(map[string]string{"tvdbid": "10", "defaulttvdbseason": "1"})
	zeroOffset := node(map[string]string{"tvdbid": "10", "defaulttvdbseason": "1", "episodeoffset": "0"})
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
		{name: "an absent offset reads as zero", n: noOffset, s: 1, k: 6, want: Placement{Targets: [][2]int{{1, 6}}, Default: true, Implied: true}},
		{name: "an offset below episode 1", n: season1, s: 1, k: 2, want: Placement{Default: true, Below: true}},
		{name: "an absolute default", n: absolute, s: 1, k: 6, want: Placement{Absolute: 11, Default: true}},
		{name: "no default season", n: unset, s: 1, k: 6, want: Placement{Default: true}},
		{name: "a special through a row", n: unset, s: 0, k: 2, want: Placement{Targets: [][2]int{{1, 13}}}},
		{name: "a special by default", n: season1, s: 0, k: 5, want: Placement{Targets: [][2]int{{0, 5}}, Default: true, Implied: true}},
		{name: "an other episode through a row to a regular season", n: unset, s: 0, k: 413, want: Placement{Targets: [][2]int{{4, 13}}}},
		{name: "the same row on TMDB", n: tmdb, sd: TMDB, s: 0, k: 413, want: Placement{Targets: [][2]int{{5, 13}}}},
		{name: "a promotion through a range row", n: unset, s: 0, k: 202, want: Placement{Targets: [][2]int{{6, 2}}}},
		{name: "the range row on TMDB", n: tmdb, sd: TMDB, s: 0, k: 203, want: Placement{Targets: [][2]int{{6, 3}}}},
		{name: "TMDB's default season and offset past a TVDB row", n: tmdb, sd: TMDB, s: 1, k: 3, want: Placement{Targets: [][2]int{{2, 6}}, Default: true}},
		{name: "no absolute numbering on TMDB", n: tmdbAbsolute, sd: TMDB, s: 1, k: 3, want: Placement{Default: true}},
		{name: "a written offset of 0 is not implied", n: zeroOffset, s: 1, k: 6, want: Placement{Targets: [][2]int{{1, 6}}, Default: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.n.Place(tc.sd, tc.s, tc.k)
			if !slices.Equal(got.Targets, tc.want.Targets) || got.Absolute != tc.want.Absolute || got.Default != tc.want.Default ||
				got.Implied != tc.want.Implied || got.Below != tc.want.Below {
				t.Errorf("Place(%d, %d, %d) = %+v, want %+v", tc.sd, tc.s, tc.k, got, tc.want)
			}
		})
	}
}

func TestEpisodeLabel(t *testing.T) {
	if got := EpisodeLabel(0, 3); got != "S3" {
		t.Errorf("EpisodeLabel(0, 3) = %q, want S3", got)
	}
	if got := EpisodeLabel(1, 21); got != "ep21" {
		t.Errorf("EpisodeLabel(1, 21) = %q, want ep21", got)
	}
}
