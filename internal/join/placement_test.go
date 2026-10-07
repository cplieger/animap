package join

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/schema"
)

func seg(start, end, season, episode int) schema.Segment {
	return schema.Segment{Start: start, End: end, Season: new(season), Episode: new(episode)}
}

func TestPlacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  schema.Record
		want []schema.Segment
	}{
		{
			name: "rows_place_every_episode",
			rec: schema.Record{TVDBID: 1, Episodes: 6, TVDBSeason: new(0), MappingList: []schema.Row{
				{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 7}, {2, 12}, {3}, {4, 13, 14}}},
				{AniDBSeason: 1, TVDBSeason: new(0), Start: 5, End: 6, Offset: 10},
			}},
			want: []schema.Segment{
				seg(1, 1, 0, 7), seg(2, 2, 0, 12), {Start: 3, End: 3}, seg(4, 4, 0, 13), seg(4, 6, 0, 14),
			},
		},
		{
			name: "an_explicit_offset_places_what_no_row_names",
			rec: schema.Record{TVDBID: 1, Episodes: 5, TVDBSeason: new(0), TVDBEpisodeOffset: new(8), MappingList: []schema.Row{
				{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{3, 20}}},
			}},
			want: []schema.Segment{seg(1, 2, 0, 9), seg(3, 3, 0, 20), seg(4, 5, 0, 12)},
		},
		{
			name: "a_run_breaks_at_a_season_change",
			rec: schema.Record{TVDBID: 1, Episodes: 2, TVDBSeason: new(0), MappingList: []schema.Row{
				{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 5}}},
				{AniDBSeason: 1, TVDBSeason: new(1), Episodes: [][]int{{2, 6}}},
			}},
			want: []schema.Segment{seg(1, 1, 0, 5), seg(2, 2, 1, 6)},
		},
		{
			name: "an_explicit_zero_offset_on_a_regular_season",
			rec:  schema.Record{TVDBID: 1, Episodes: 12, TVDBSeason: new(2), TVDBEpisodeOffset: new(0)},
			want: []schema.Segment{seg(1, 12, 2, 1)},
		},
		{
			name: "a_target_named_twice_counts_once",
			rec: schema.Record{TVDBID: 1, Episodes: 1, TVDBSeason: new(0), MappingList: []schema.Row{
				{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 5, 5}}},
			}},
			want: []schema.Segment{seg(1, 1, 0, 5)},
		},
		{
			name: "a_target_named_twice_apart_counts_once",
			rec: schema.Record{TVDBID: 1, Episodes: 1, TVDBSeason: new(0), MappingList: []schema.Row{
				{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 5, 6, 5}}},
			}},
			want: []schema.Segment{seg(1, 1, 0, 5), seg(1, 1, 0, 6)},
		},
		{
			name: "every_episode_without_a_counterpart",
			rec: schema.Record{TVDBID: 1, Episodes: 2, TVDBSeason: new(0), MappingList: []schema.Row{
				{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1}, {2}}},
			}},
			want: []schema.Segment{{Start: 1, End: 2}},
		},
		{
			name: "rows_cover_an_absolute_title",
			rec: schema.Record{TVDBID: 1, Episodes: 2, TVDBAbsolute: true, MappingList: []schema.Row{
				{AniDBSeason: 1, TVDBSeason: new(3), Start: 1, Offset: 4},
			}},
			want: []schema.Segment{seg(1, 2, 3, 5)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := placement(&tc.rec); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("placement(%+v) = %s, want %s", tc.rec, segString(got), segString(tc.want))
			}
		})
	}
}

func TestPlacementRefusesWhatItCannotPlace(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  schema.Record
	}{
		{"an_absent_offset_behind_an_uncovered_episode", schema.Record{TVDBID: 1, Episodes: 2, TVDBSeason: new(0), MappingList: []schema.Row{
			{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 4}}},
		}}},
		{"an_absolute_default", schema.Record{TVDBID: 1, Episodes: 3, TVDBAbsolute: true, TVDBEpisodeOffset: new(0)}},
		{"an_offset_landing_below_episode_1", schema.Record{TVDBID: 1, Episodes: 2, TVDBSeason: new(0), TVDBEpisodeOffset: new(-1)}},
		{"a_tmdb_row_places_nothing_on_tvdb", schema.Record{TVDBID: 1, Episodes: 1, TVDBSeason: new(0), MappingList: []schema.Row{
			{AniDBSeason: 1, TMDBSeason: new(0), Episodes: [][]int{{1, 4}}},
		}}},
		{"a_tvdb_row_without_a_tvdb_series", schema.Record{TMDBTVID: 2, Episodes: 1, MappingList: []schema.Row{
			{AniDBSeason: 1, TVDBSeason: new(0), Episodes: [][]int{{1, 4}}},
		}}},
		{"no_episode_count", schema.Record{TVDBID: 1, TVDBSeason: new(0), TVDBEpisodeOffset: new(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := placement(&tc.rec); got != nil {
				t.Errorf("placement(%+v) = %s, want nil", tc.rec, segString(got))
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
