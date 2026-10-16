package join

import (
	"reflect"
	"testing"

	"github.com/cplieger/animap/internal/schema"
)

func TestApplyLinks(t *testing.T) {
	records := []schema.Record{
		{AniListID: 376, Type: "OVA", Episodes: 1},
		{AniListID: 1563, Type: "TV", Episodes: 29},
		{AniListID: 2269, Type: "SPECIAL", Episodes: 3},
		{AniListID: 2593, Type: "MOVIE", Episodes: 1, MALID: 2593},
		{AniListID: 3000, AniDBID: 300, Episodes: 1},
		{AniListID: 3001, Type: "OVA", Episodes: 2},
		{AniDBID: 10},
	}
	nums := func(from, to int) []int {
		var out []int
		for k := from; k <= to; k++ {
			out = append(out, k)
		}
		return out
	}
	links := []Linked{
		{AniList: 376, TVDBID: 75941, Season: 0, Targets: []int{1}, Specials: &schema.ParentSpecials{AniDBID: 1544, Specials: []int{1}}},
		{AniList: 1563, TVDBID: 71634, Season: 2, Targets: nums(1, 29)},
		{AniList: 2269, TVDBID: 254931, Season: 0, Targets: []int{2, 3, 4}},
		{AniList: 2593, TVDBID: 82099, Season: 1, Targets: []int{1}},
		{AniList: 3000, TVDBID: 1, Season: 1, Targets: []int{1}},
		{AniList: 3001, TVDBID: 1, Season: 1, Targets: []int{1}},
		{AniList: 2000, TVDBID: 5, Season: 1, Targets: []int{3, 4}},
	}
	out, applied, skipped := ApplyLinks(records, links)
	if applied != 5 {
		t.Errorf("applied = %d, want 5", applied)
	}
	wantSkipped := []Skipped{
		{AniList: 3000, Reason: "anime-offline-database now links AniDB 300"},
		{AniList: 3001, Reason: "the record has 2 episodes, the link 1"},
	}
	if !reflect.DeepEqual(skipped, wantSkipped) {
		t.Errorf("skipped = %+v, want %+v", skipped, wantSkipped)
	}
	byID := map[int]schema.Record{}
	var order []int
	for _, r := range out {
		byID[r.AniListID] = r
		order = append(order, r.AniListID)
	}
	if want := []int{376, 1563, 2000, 2269, 2593, 3000, 3001, 0}; !reflect.DeepEqual(order, want) {
		t.Errorf("record order = %v, want %v: the created record in AniList order", order, want)
	}
	pairs := func(targets ...int) [][]int {
		out := make([][]int, 0, len(targets))
		for i, t := range targets {
			out = append(out, []int{i + 1, t})
		}
		return out
	}
	for _, tc := range []struct {
		want schema.Record
		name string
	}{
		{name: "a specials link, as anidb_parent states it", want: schema.Record{
			AniListID: 376, Type: "OVA", Episodes: 1, AniDBParent: &schema.ParentSpecials{AniDBID: 1544, Specials: []int{1}},
			TVDBID: 75941, TVDBSeason: new(0), MappingList: []schema.Row{{AniDBSeason: 1, TVDBSeason: new(0), Episodes: pairs(1)}},
			TVDBPlacement: []schema.Segment{seg(1, 1, 0, 1)},
		}},
		{name: "a regular run filling a season", want: schema.Record{
			AniListID: 1563, Type: "TV", Episodes: 29, TVDBID: 71634, TVDBSeason: new(2),
			MappingList:   []schema.Row{{AniDBSeason: 1, TVDBSeason: new(2), Episodes: pairs(nums(1, 29)...)}},
			TVDBPlacement: []schema.Segment{seg(1, 29, 2, 1)},
		}},
		{name: "whole parts of three anime", want: schema.Record{
			AniListID: 2269, Type: "SPECIAL", Episodes: 3, TVDBID: 254931, TVDBSeason: new(0),
			MappingList:   []schema.Row{{AniDBSeason: 1, TVDBSeason: new(0), Episodes: pairs(2, 3, 4)}},
			TVDBPlacement: []schema.Segment{seg(1, 3, 0, 2)},
		}},
		{name: "one film of a multi-film anime", want: schema.Record{
			AniListID: 2593, Type: "MOVIE", Episodes: 1, MALID: 2593, TVDBID: 82099, TVDBSeason: new(1),
			MappingList:   []schema.Row{{AniDBSeason: 1, TVDBSeason: new(1), Episodes: pairs(1)}},
			TVDBPlacement: []schema.Segment{seg(1, 1, 1, 1)},
		}},
		{name: "a record the join did not make", want: schema.Record{
			AniListID: 2000, Episodes: 2, TVDBID: 5, TVDBSeason: new(1),
			MappingList:   []schema.Row{{AniDBSeason: 1, TVDBSeason: new(1), Episodes: pairs(3, 4)}},
			TVDBPlacement: []schema.Segment{seg(1, 2, 1, 3)},
		}},
		{name: "a skipped record, unchanged", want: schema.Record{AniListID: 3001, Type: "OVA", Episodes: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := byID[tc.want.AniListID]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("record = %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// A record with no count of its own takes the link, as a created one does.
func TestApplyLinksToARecordWithoutACount(t *testing.T) {
	out, applied, skipped := ApplyLinks([]schema.Record{{AniListID: 5, Type: "OVA"}}, []Linked{{AniList: 5, TVDBID: 1, Season: 0, Targets: []int{7}}})
	if applied != 1 || skipped != nil || out[0].TVDBID != 1 || out[0].Episodes != 0 {
		t.Errorf("ApplyLinks = %+v, %d, %+v; want the link applied and the count left as the join made it", out, applied, skipped)
	}
}
