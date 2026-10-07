package join

import (
	"reflect"
	"slices"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/schema"
)

func TestApplySpecials(t *testing.T) {
	parent := &animelists.Node{AniDBID: 100, Attrs: map[string]string{"tvdbid": "500", "defaulttvdbseason": "1", "tmdbtv": "600", "tmdbseason": "1"}, Rows: []animelists.Row{
		{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-4;2-5;3-0;"},
		{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "1"}, Text: ";6-13;"},
	}}
	nodes := map[int]*animelists.Node{100: parent, 200: {AniDBID: 200, Attrs: map[string]string{"tvdbid": "movie"}}}
	records := []schema.Record{
		{AniListID: 1, Episodes: 2},
		{AniListID: 2, Episodes: 1},
		{AniListID: 3, Episodes: 1, AniDBID: 7},
		{AniListID: 4, Episodes: 3},
		{AniListID: 5, Episodes: 1},
		{AniListID: 6, Episodes: 1},
		{AniListID: 8, Episodes: 2},
		{AniDBID: 900},
	}
	applied, skipped := ApplySpecials(records, []Special{
		{AniList: 1, Parent: 100, Specials: []int{1, 2}},
		{AniList: 2, Parent: 100, Specials: []int{3}},
		{AniList: 3, Parent: 100, Specials: []int{1}},
		{AniList: 4, Parent: 100, Specials: []int{1, 2}},
		{AniList: 5, Parent: 200, Specials: []int{1}},
		{AniList: 6, Parent: 100, Specials: []int{6}},
		{AniList: 8, Parent: 100, Specials: []int{5, 6}},
		{AniList: 9, Parent: 100, Specials: []int{1}},
	}, nodes)
	if applied != 2 {
		t.Errorf("applied = %d, want 2 (AniList 1 and 6)", applied)
	}
	var why []int
	for _, s := range skipped {
		why = append(why, s.AniList)
	}
	if want := []int{2, 3, 4, 5, 8, 9}; !slices.Equal(why, want) {
		t.Errorf("skipped = %+v, want AniList %v: a special to no episode, an own AniDB id, a count mismatch, a parent with no series, two seasons, no record", skipped, want)
	}
	r := records[0]
	if r.AniDBParent == nil || r.AniDBParent.AniDBID != 100 || r.TVDBID != 500 || r.TVDBSeason == nil || *r.TVDBSeason != 0 ||
		len(r.MappingList) != 1 || !slices.EqualFunc(r.MappingList[0].Episodes, [][]int{{1, 4}, {2, 5}}, slices.Equal) {
		t.Errorf("bridged record = %+v, want parent 100, TVDB 500 season 0, row [[1 4] [2 5]]", r)
	}
	if r.TMDBTVID != 0 || r.TMDBSeason != nil || r.MappingList[0].TMDBSeason != nil {
		t.Errorf("bridged record = %+v, want no TMDB value although the parent maps TMDB 600", r)
	}
	if s := records[5]; s.TVDBSeason == nil || *s.TVDBSeason != 1 || !slices.Equal(s.MappingList[0].Episodes[0], []int{1, 13}) {
		t.Errorf("a special a row places on season 1 = %+v, want season 1, [1 13]", s)
	}
	if got, want := r.TVDBPlacement, []schema.Segment{{Start: 1, End: 2, Season: new(0), Episode: new(4)}}; !reflect.DeepEqual(got, want) {
		t.Errorf("bridged record placement = %s, want %s", segString(got), segString(want))
	}
	if records[2].AniDBParent != nil || records[1].TVDBID != 0 {
		t.Error("a skipped bridge changed its record")
	}
}
