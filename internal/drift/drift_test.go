package drift

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/counts"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

var layout = []skyhook.Episode{{Season: 2, Number: 1, AirDate: "2000-01-27"}, {Season: 2, Number: 2, AirDate: "2000-02-03"}}

func upstream() *animelists.Node {
	return &animelists.Node{AniDBID: 544, Attrs: map[string]string{"tvdbid": "91391", "defaulttvdbseason": "1"}}
}

func entry() *overlay.Entry {
	return &overlay.Entry{
		AniDBID: 544, Title: "Oh! Super Milk-chan", Set: overlay.Set{DefaultTVDBSeason: new("2")},
		Captured: overlay.Captured{
			NodeSHA256: upstream().Hash(),
			TVDB:       &overlay.CapturedTVDB{Series: 91391, Seasons: []int{2}, Episodes: layout, SHA256: skyhook.Hash(layout)},
		},
	}
}

func causes(fs []Finding) []Cause {
	var out []Cause
	for _, f := range fs {
		out = append(out, f.Cause)
	}
	return out
}

func TestDecide(t *testing.T) {
	landed := upstream()
	landed.Attrs["defaulttvdbseason"] = "2"
	moved := upstream()
	moved.Attrs["tvdbid"] = "1"
	changedLayout := append(slices.Clone(layout), skyhook.Episode{Season: 2, Number: 3})
	all := []string{"drift:node:544", "drift:landed:544", "drift:tvdb:544"}
	for _, tc := range []struct {
		name      string
		obs       Observation
		want      []Cause
		evaluated []string
	}{
		{"unchanged", Observation{Node: upstream(), Layout: layout, LayoutRead: true}, nil, all},
		{"landed", Observation{Node: landed, Layout: layout, LayoutRead: true}, []Cause{Landed}, all},
		{"node changed", Observation{Node: moved, Layout: layout, LayoutRead: true}, []Cause{NodeChanged}, all},
		{"node removed", Observation{Layout: layout, LayoutRead: true}, []Cause{NodeChanged}, all},
		{"layout changed", Observation{Node: upstream(), Layout: changedLayout, LayoutRead: true}, []Cause{TVDBLayout}, all},
		{"series gone", Observation{Node: upstream(), LayoutAbsent: true}, []Cause{TVDBLayout}, all},
		{"layout unreadable", Observation{Node: moved}, []Cause{NodeChanged}, all[:2]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ev := Decide(OfEntry(entry()), &tc.obs)
			if got := causes(f); !slices.Equal(got, tc.want) {
				t.Errorf("Decide causes = %v, want %v", got, tc.want)
			}
			if !slices.Equal(ev, tc.evaluated) {
				t.Errorf("Decide evaluated = %v, want %v", ev, tc.evaluated)
			}
		})
	}
}

func TestDecideLayoutFindingCarriesBothLists(t *testing.T) {
	after := []skyhook.Episode{{Season: 2, Number: 1, AirDate: "2000-01-28"}}
	f, _ := Decide(OfEntry(entry()), &Observation{Node: upstream(), Layout: after, LayoutRead: true})
	if len(f) != 1 || !reflect.DeepEqual(f[0].Before, layout) || !reflect.DeepEqual(f[0].After, after) {
		t.Fatalf("finding = %+v", f)
	}
	gone, _ := Decide(OfEntry(entry()), &Observation{Node: upstream(), LayoutAbsent: true})
	if gone[0].After != nil || !strings.HasPrefix(gone[0].Key, "drift:tvdb:") {
		t.Errorf("series-gone finding = %+v, want nil After", gone[0])
	}
}

func TestDecideCreatedEntry(t *testing.T) {
	e := entry()
	e.Create, e.Captured.NodeSHA256 = true, animelists.HashAbsent
	if f, _ := Decide(OfEntry(e), &Observation{Layout: layout, LayoutRead: true}); len(f) != 0 {
		t.Errorf("absent node for a create entry: %v, want no finding", causes(f))
	}
	appeared := upstream()
	appeared.Attrs["defaulttvdbseason"] = "2"
	if f, _ := Decide(OfEntry(e), &Observation{Node: appeared, Layout: layout, LayoutRead: true}); !slices.Equal(causes(f), []Cause{Landed}) {
		t.Errorf("node added upstream with the entry's values: %v, want landed", causes(f))
	}
}

func bridge() *overlay.Bridge {
	sp := []overlay.AniDBEpisode{{Kind: overlay.AniDBSpecial, Number: 1, AirDate: "2005-04-21"}}
	return &overlay.Bridge{
		AniListID: 376, ParentAniDBID: 544, Specials: []int{1}, Title: "OVA", ParentAniDBSpecials: sp,
		Captured: overlay.BridgeCaptured{
			NodeSHA256: upstream().Hash(),
			TVDB:       &overlay.CapturedTVDB{Series: 91391, Seasons: []int{2}, Episodes: layout, SHA256: skyhook.Hash(layout)},
		},
	}
}

func TestDecideSpecial(t *testing.T) {
	moved := upstream()
	moved.Attrs["tvdbid"] = "1"
	var base Observation
	base.Node, base.Layout, base.LayoutRead = upstream(), layout, true
	all := []string{"drift:special:376:node", "drift:special:376:tvdb"}
	withLanded := []string{"drift:special:376:node", "drift:special:376:landed", "drift:special:376:tvdb"}
	for _, tc := range []struct {
		name      string
		mut       func(*Observation)
		want      []Cause
		evaluated []string
	}{
		{"unchanged", func(*Observation) {}, nil, all},
		{"parent node changed", func(o *Observation) { o.Node = moved }, []Cause{NodeChanged}, all},
		{"layout changed", func(o *Observation) { o.Layout = layout[:1] }, []Cause{TVDBLayout}, all},
		{"linked in the release", func(o *Observation) { o.Linked, o.LinkedRead = 999, true }, []Cause{Landed}, withLanded},
		{"read in the release, not linked", func(o *Observation) { o.LinkedRead = true }, nil, withLanded},
		{"nothing read but the node", func(o *Observation) { o.LayoutRead = false }, nil, all[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.mut(&o)
			f, ev := Decide(OfBridge(bridge()), &o)
			if got := causes(f); !slices.Equal(got, tc.want) {
				t.Errorf("Decide(bridge) causes = %v, want %v", got, tc.want)
			}
			if !slices.Equal(ev, tc.evaluated) {
				t.Errorf("Decide(bridge) evaluated = %v, want %v", ev, tc.evaluated)
			}
			for _, x := range f {
				if x.Path != "overlay/special-of-parent/376.json" || x.AniList != 376 {
					t.Errorf("finding %+v does not name the bridge file", x)
				}
			}
		})
	}
}

func TestDecideCount(t *testing.T) {
	row := &counts.Row{AniDBID: 17281, RegularEpisodes: 2}
	carried := func(n int) []Finding {
		return []Finding{{Key: "drift:counts:17281", Cause: CountInDatabase, AniDB: 17281, Path: "checks/counts.json", Counted: 2, DatabaseEpisodes: n}}
	}
	key := []string{"drift:counts:17281"}
	for _, tc := range []struct {
		name      string
		obs       Observation
		want      []Finding
		evaluated []string
	}{
		{"no database read", Observation{InDatabase: true, DatabaseEpisodes: 2}, nil, nil},
		{"the database lacks it", Observation{DatabaseRead: true}, nil, key},
		{"the database carries it with no count", Observation{DatabaseRead: true, InDatabase: true}, carried(0), key},
		{"the database carries it, counts agree", Observation{DatabaseRead: true, InDatabase: true, DatabaseEpisodes: 2}, carried(2), key},
		{"the database carries it, counts differ", Observation{DatabaseRead: true, InDatabase: true, DatabaseEpisodes: 3}, carried(3), key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ev := Decide(OfRow(row), &tc.obs)
			if !reflect.DeepEqual(f, tc.want) {
				t.Errorf("Decide(row, %+v) findings = %+v, want %+v", tc.obs, f, tc.want)
			}
			if !slices.Equal(ev, tc.evaluated) {
				t.Errorf("Decide(row, %+v) evaluated = %v, want %v", tc.obs, ev, tc.evaluated)
			}
		})
	}
}

// An entry re-captured onto a TMDB-only route keeps its TVDB key
// evaluated, so a TVDB-layout issue from before the re-capture closes.
func TestDecideEntryWithoutTVDBCapture(t *testing.T) {
	e := entry()
	e.Captured.TVDB = nil
	for _, obs := range []Observation{{Node: upstream()}, {Node: upstream(), LayoutAbsent: true}} {
		f, ev := Decide(OfEntry(e), &obs)
		if len(f) != 0 || !slices.Contains(ev, "drift:tvdb:544") {
			t.Errorf("Decide(no TVDB capture, %+v): findings %v, evaluated %v; want no finding and the TVDB key evaluated", obs, causes(f), ev)
		}
	}
}
