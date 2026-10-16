package drift

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
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

func causes(fs []Finding) []cause {
	var out []cause
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
		want      []cause
		evaluated []string
	}{
		{"unchanged", Observation{Node: upstream(), Layout: layout, LayoutRead: true}, nil, all},
		{"landed", Observation{Node: landed, Layout: layout, LayoutRead: true}, []cause{Landed}, all},
		{"node changed", Observation{Node: moved, Layout: layout, LayoutRead: true}, []cause{NodeChanged}, all},
		{"node removed", Observation{Layout: layout, LayoutRead: true}, []cause{NodeChanged}, all},
		{"layout changed", Observation{Node: upstream(), Layout: changedLayout, LayoutRead: true}, []cause{TVDBLayout}, all},
		{"series gone", Observation{Node: upstream(), LayoutAbsent: true}, []cause{TVDBLayout}, all},
		{"layout unreadable", Observation{Node: moved}, []cause{NodeChanged}, all[:2]},
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
	if f[0].Gone {
		t.Errorf("a moved layout reads as a series gone: %+v", f[0])
	}
	gone, _ := Decide(OfEntry(entry()), &Observation{Node: upstream(), LayoutAbsent: true})
	if !gone[0].Gone || gone[0].After != nil || !strings.HasPrefix(gone[0].Key, "drift:tvdb:") {
		t.Errorf("series-gone finding = %+v, want Gone and nil After", gone[0])
	}
	emptied, _ := Decide(OfEntry(entry()), &Observation{Node: upstream(), LayoutRead: true})
	if len(emptied) != 1 || emptied[0].Gone {
		t.Errorf("a season TVDB emptied = %+v, want one finding, the series still served", emptied)
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
	if f, _ := Decide(OfEntry(e), &Observation{Node: appeared, Layout: layout, LayoutRead: true}); !slices.Equal(causes(f), []cause{Landed}) {
		t.Errorf("node added upstream with the entry's values: %v, want landed", causes(f))
	}
}

func link() *overlay.Link {
	return &overlay.Link{
		AniListID: 2269, Title: "Compilation",
		Parts:    []overlay.Part{{AniDBID: 4169, Whole: true}, {AniDBID: 4168, Whole: true}, {AniDBID: 4169, Specials: []int{1}}},
		Captured: overlay.LinkCaptured{AniDBSHA256: "captured digest"},
	}
}

func TestDecideLink(t *testing.T) {
	both := []string{"drift:link:2269:landed", "drift:link:2269:anidb"}
	for _, tc := range []struct {
		name      string
		obs       Observation
		want      []cause
		evaluated []string
	}{
		{"unchanged", Observation{AODRead: true, AniDB: "captured digest", AniDBRead: true}, nil, both},
		{"landed", Observation{AOD: []int{4169}, AODRead: true, AniDB: "captured digest", AniDBRead: true}, []cause{Landed}, both},
		{"an AniDB episode changed", Observation{AODRead: true, AniDB: "new digest", AniDBRead: true}, []cause{AniDBChanged}, both},
		{"landed and changed", Observation{AOD: []int{9}, AODRead: true, AniDB: "new digest", AniDBRead: true}, []cause{Landed, AniDBChanged}, both},
		{"anime-offline-database unread", Observation{AniDB: "new digest", AniDBRead: true}, []cause{AniDBChanged}, both[1:]},
		{"the mirror unread", Observation{AOD: []int{9}, AODRead: true}, []cause{Landed}, both[:1]},
		{"nothing read", Observation{AOD: []int{9}, AniDB: "new digest"}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ev := Decide(OfLink(link()), &tc.obs)
			if got := causes(f); !slices.Equal(got, tc.want) {
				t.Errorf("Decide(link) causes = %v, want %v", got, tc.want)
			}
			if !slices.Equal(ev, tc.evaluated) {
				t.Errorf("Decide(link) evaluated = %v, want %v", ev, tc.evaluated)
			}
			for _, x := range f {
				if x.Path != "overlay/links/2269.json" || x.AniList != 2269 || x.AniDB != 4169 || !slices.Equal(x.AniDBIDs, []int{4169, 4168}) {
					t.Errorf("finding %+v does not name the link file and its anime", x)
				}
				if x.Cause == Landed && x.Linked != tc.obs.AOD[0] {
					t.Errorf("landed finding links AniDB %d, want %d", x.Linked, tc.obs.AOD[0])
				}
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
