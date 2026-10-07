package animelists

import (
	"reflect"
	"slices"
	"testing"

	"github.com/cplieger/animap/internal/schema"
)

func TestPlace(t *testing.T) {
	rows := []SideRow{
		{AniDBSeason: 1, Season: 0, Pairs: [][]int{{1, 7}, {2}}},
		{AniDBSeason: 1, Season: 2, Start: 5, End: 6, Offset: -4},
	}
	layout := func(number int) (season, episode int, ok bool) { return 3, number - 100, number > 100 }
	for _, tc := range []struct {
		name    string
		d       Default
		k       int
		targets [][2]int
		src     Source
	}{
		{"a_row_wins_over_the_default", Default{Season: new(0), Offset: new(9)}, 1, [][2]int{{0, 7}}, ByRow},
		{"a_row_to_no_episode", Default{Season: new(0), Offset: new(9)}, 2, nil, ByRow},
		{"a_range_row", Default{Season: new(0)}, 6, [][2]int{{2, 2}}, ByRow},
		{"the_stated_offset", Default{Season: new(0), Offset: new(9)}, 3, [][2]int{{0, 12}}, ByOffset},
		{"an_absent_offset_reads_as_zero", Default{Season: new(1)}, 3, [][2]int{{1, 3}}, ByAbsentOffset},
		{"an_absolute_layout", Default{Offset: new(100), Absolute: layout}, 4, [][2]int{{3, 4}}, ByOffset},
		{"an_absolute_number_the_layout_lacks", Default{Absolute: layout}, 4, nil, Unplaced},
		{"no_default", Default{Offset: new(1)}, 3, nil, Unplaced},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets, src := Place(rows, tc.d, tc.k)
			if src != tc.src || !slices.Equal(targets, tc.targets) {
				t.Errorf("Place(rows, %+v, %d) = %v, %d; want %v, %d", tc.d, tc.k, targets, src, tc.targets, tc.src)
			}
		})
	}
}

func TestTVDBRowsMatchSideRows(t *testing.T) {
	n := &Node{Attrs: map[string]string{"tvdbid": "10"}, Rows: []Row{
		{Attrs: map[string]string{"anidbseason": "1", "tvdbseason": "0"}, Text: ";1-8;2-0;3-4+5;"},
		{Attrs: map[string]string{"anidbseason": "1", "tmdbseason": "1"}, Text: ";1-2;"},
		{Attrs: map[string]string{"anidbseason": "1", "tvdbseason": "1", "start": "4", "end": "9", "offset": "-3"}},
	}}
	var published []schema.Row
	for _, r := range n.Rows {
		sr, err := r.Schema()
		if err != nil {
			t.Fatalf("Setup: %v", err)
		}
		published = append(published, sr)
	}
	if got, want := TVDBRows(published), n.SideRows("tvdbseason"); !reflect.DeepEqual(got, want) {
		t.Errorf("TVDBRows(published) = %+v, want SideRows(tvdbseason) %+v", got, want)
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
