package join

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/schema"
)

func seg(start, end, season, episode int) schema.Segment {
	return schema.Segment{Start: start, End: end, Season: new(season), Episode: new(episode)}
}

func placed(n *animelists.Node, episodes int) []schema.Segment {
	r := schema.Record{AniDBID: 1, Episodes: episodes}
	fill(&r, n, &Stats{})
	return placement(&r, n)
}

func node(attrs map[string]string, rows ...animelists.Row) *animelists.Node {
	return &animelists.Node{AniDBID: 1, Attrs: attrs, Rows: rows}
}

func row(attrs map[string]string, text string) animelists.Row {
	return animelists.Row{Attrs: attrs, Text: text}
}

func TestPlacement(t *testing.T) {
	tvdb0 := map[string]string{"anidbseason": "1", "tvdbseason": "0"}
	for _, tc := range []struct {
		n        *animelists.Node
		name     string
		want     []schema.Segment
		episodes int
	}{
		{
			name: "rows_place_every_episode", episodes: 6,
			n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0"},
				row(tvdb0, ";1-7;2-12;3-0;4-13+14;"),
				row(map[string]string{"anidbseason": "1", "tvdbseason": "0", "start": "5", "end": "6", "offset": "10"}, ""),
			),
			want: []schema.Segment{
				seg(1, 1, 0, 7), seg(2, 2, 0, 12), {Start: 3, End: 3}, seg(4, 4, 0, 13), seg(4, 6, 0, 14),
			},
		},
		{
			name: "an_explicit_offset_places_what_no_row_names", episodes: 5,
			n:    node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0", "episodeoffset": "8"}, row(tvdb0, ";3-20;")),
			want: []schema.Segment{seg(1, 2, 0, 9), seg(3, 3, 0, 20), seg(4, 5, 0, 12)},
		},
		{
			name: "a_run_breaks_at_a_season_change", episodes: 2,
			n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0"},
				row(tvdb0, ";1-5;"),
				row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";2-6;"),
			),
			want: []schema.Segment{seg(1, 1, 0, 5), seg(2, 2, 1, 6)},
		},
		{
			name: "an_explicit_zero_offset_on_a_regular_season", episodes: 12,
			n:    node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "2", "episodeoffset": "0"}),
			want: []schema.Segment{seg(1, 12, 2, 1)},
		},
		{
			name: "a_target_named_twice_counts_once", episodes: 1,
			n:    node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0"}, row(tvdb0, ";1-5+5;")),
			want: []schema.Segment{seg(1, 1, 0, 5)},
		},
		{
			name: "a_target_named_twice_apart_counts_once", episodes: 1,
			n:    node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0"}, row(tvdb0, ";1-5+6+5;")),
			want: []schema.Segment{seg(1, 1, 0, 5), seg(1, 1, 0, 6)},
		},
		{
			name: "every_episode_without_a_counterpart", episodes: 2,
			n:    node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0"}, row(tvdb0, ";1-0;2-0;")),
			want: []schema.Segment{{Start: 1, End: 2}},
		},
		{
			name: "rows_cover_an_absolute_title", episodes: 2,
			n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "a"},
				row(map[string]string{"anidbseason": "1", "tvdbseason": "3", "start": "1", "offset": "4"}, ""),
			),
			want: []schema.Segment{seg(1, 2, 3, 5)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := placed(tc.n, tc.episodes); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("placement(%+v) = %s, want %s", tc.n, segString(got), segString(tc.want))
			}
		})
	}
}

func TestPlacementRefusesWhatItCannotPlace(t *testing.T) {
	tvdb0 := map[string]string{"anidbseason": "1", "tvdbseason": "0"}
	for _, tc := range []struct {
		n        *animelists.Node
		name     string
		episodes int
	}{
		{name: "an_absent_offset_behind_an_uncovered_episode", n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0"}, row(tvdb0, ";1-4;")), episodes: 2},
		{name: "an_absolute_default", n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "a", "episodeoffset": "0"}), episodes: 3},
		{name: "an_offset_landing_below_episode_1", n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0", "episodeoffset": "-1"}), episodes: 2},
		{name: "a_tmdb_row_places_nothing_on_tvdb", n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0"},
			row(map[string]string{"anidbseason": "1", "tmdbseason": "0"}, ";1-4;")), episodes: 1},
		{name: "a_tvdb_row_without_a_tvdb_series", n: node(map[string]string{"tmdbtv": "2"}, row(tvdb0, ";1-4;")), episodes: 1},
		{name: "no_episode_count", n: node(map[string]string{"tvdbid": "1", "defaulttvdbseason": "0", "episodeoffset": "0"}), episodes: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := placed(tc.n, tc.episodes); got != nil {
				t.Errorf("placement(%+v) = %s, want nil", tc.n, segString(got))
			}
		})
	}
}

func segString(segs []schema.Segment) string {
	var b strings.Builder
	for _, s := range segs {
		if s.Season == nil {
			fmt.Fprintf(&b, " %d-%d:none", s.Start, s.End)
			continue
		}
		fmt.Fprintf(&b, " %d-%d:S%dE%d", s.Start, s.End, *s.Season, *s.Episode)
	}
	return "[" + b.String() + " ]"
}
