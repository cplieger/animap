package guard

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
)

func TestCoverage(t *testing.T) {
	prev := schema.Populations{Records: 1000, AniListWithAniDB: 600, AniDBWithTVDB: 400, WithTMDB: 300, WithMappingList: 100}
	if err := Coverage(&prev, prev, false); err != nil {
		t.Errorf("equal populations: %v", err)
	}
	ok := prev
	ok.Records = 900
	if err := Coverage(&prev, ok, false); err != nil {
		t.Errorf("exactly 90%%: %v", err)
	}
	for _, mut := range []func(*schema.Populations){
		func(p *schema.Populations) { p.Records = 899 },
		func(p *schema.Populations) { p.AniListWithAniDB = 539 },
		func(p *schema.Populations) { p.AniDBWithTVDB = 359 },
		func(p *schema.Populations) { p.WithTMDB = 269 },
		func(p *schema.Populations) { p.WithMappingList = 89 },
	} {
		cur := prev
		mut(&cur)
		if err := Coverage(&prev, cur, false); !errors.Is(err, ErrCoverage) {
			t.Errorf("Coverage(%+v) = %v, want ErrCoverage", cur, err)
		}
		if err := Coverage(&prev, cur, true); err != nil {
			t.Errorf("accept-shrink still refused %+v: %v", cur, err)
		}
	}
	extinct := prev
	extinct.WithMappingList = 0
	if err := Coverage(&prev, extinct, true); !errors.Is(err, ErrCoverage) {
		t.Errorf("extinction with accept-shrink = %v, want ErrCoverage", err)
	}
	if err := Coverage(nil, schema.Populations{Records: 22169, AniListWithAniDB: 13444, AniDBWithTVDB: 7502}, false); err != nil {
		t.Errorf("first release at today's size: %v", err)
	}
	if err := Coverage(nil, schema.Populations{Records: 9999, AniListWithAniDB: 13444, AniDBWithTVDB: 7502}, false); !errors.Is(err, ErrCoverage) {
		t.Errorf("first release below the floor = %v, want ErrCoverage", err)
	}
}

func node(id int, attrs string, rows ...animelists.Row) *animelists.Node {
	n := &animelists.Node{AniDBID: id, Attrs: map[string]string{}, Rows: rows}
	for kv := range strings.FieldsSeq(attrs) {
		k, v, _ := strings.Cut(kv, "=")
		n.Attrs[k] = v
	}
	return n
}

func entry(id int, set overlay.Set) overlay.Entry {
	return overlay.Entry{AniDBID: id, Set: set}
}

// fin is a finished anime as the AniDB mirror lists it.
func fin(regular int, specials ...int) anidb.Anime {
	a := anidb.Anime{Regular: make([]string, regular), Finished: true}
	for _, k := range specials {
		a.Specials = append(a.Specials, anidb.Episode{Number: k})
	}
	return a
}

// facts is the mirror listing listed and the database counting database;
// an id in neither has no count and no known specials.
func facts(listed map[int]anidb.Anime, database map[int]int) *anidb.Facts {
	return anidb.NewFacts(&anidb.Snapshot{Commit: strings.Repeat("a", 40), Anime: listed}, nil, database)
}

func run(nodes []*animelists.Node, entries []overlay.Entry, f *anidb.Facts) CollisionReport {
	m := map[int]*animelists.Node{}
	for _, n := range nodes {
		m[n.AniDBID] = n
	}
	return Collisions(m, entries, nil, f)
}

// The baseline reads no mirror specials, so this collision is not upstream's either.
func TestCollisionOwnSpecialLeftAsMasterHasItDoesNotBlock(t *testing.T) {
	nodes := []*animelists.Node{
		node(226, "tvdbid=72922 defaulttvdbseason=1 tmdbtv=34165 tmdbseason=1"),
		node(582, "tvdbid=72922 defaulttvdbseason=0 tmdbtv=34165 tmdbseason=0"),
		node(583, "tvdbid=72922 defaulttvdbseason=1 episodeoffset=13"),
	}
	e := entry(226, overlay.Set{DefaultTVDBSeason: new("2"), TMDBSeason: new("2")})
	rep := run(nodes, []overlay.Entry{e}, facts(map[int]anidb.Anime{226: fin(13, 1)}, map[int]int{582: 2, 583: 25}))
	if len(rep.Blocking)+len(rep.Upstream)+len(rep.Unresolved) != 0 {
		t.Errorf("Blocking %+v, Upstream %+v, Unresolved %+v; want none", rep.Blocking, rep.Upstream, rep.Unresolved)
	}
}

func TestCollisionOwnSpecialMovedOntoATakenEpisodeBlocks(t *testing.T) {
	nodes := []*animelists.Node{
		node(226, "tvdbid=72922 defaulttvdbseason=1"),
		node(582, "tvdbid=72922 defaulttvdbseason=0"),
		node(583, "tvdbid=72922 defaulttvdbseason=1 episodeoffset=13"),
	}
	e := entry(226, overlay.Set{MappingList: &[]overlay.Row{{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 2}}}}})
	rep := run(nodes, []overlay.Entry{e}, facts(map[int]anidb.Anime{226: fin(13, 1)}, map[int]int{582: 2, 583: 25}))
	if len(rep.Blocking) != 1 || !slices.Equal(rep.Blocking[0].Claims, []string{"226 S1", "582 ep2"}) {
		t.Errorf("Blocking = %+v, want TVDB 0x2 claimed by 226 S1 and 582 ep2", rep.Blocking)
	}
	if len(rep.Unresolved) != 2 {
		t.Errorf("Unresolved = %+v, want AniDB 582 and 583, which the mirror does not list", rep.Unresolved)
	}
}

func TestCollisionBetweenUnchangedSiblingsOnATouchedSeriesIsBaselined(t *testing.T) {
	nodes := []*animelists.Node{
		node(2114, "tvdbid=138691 defaulttvdbseason=1"),
		node(2888, "tvdbid=138691 defaulttvdbseason=0"),
		node(2891, "tvdbid=138691 defaulttvdbseason=0"),
	}
	f := facts(map[int]anidb.Anime{2114: fin(1), 2888: fin(1), 2891: fin(1)}, nil)
	rep := run(nodes, []overlay.Entry{entry(2114, overlay.Set{DefaultTVDBSeason: new("2")})}, f)
	base := &Baseline{Version: BaselineVersion, Collisions: []BaselineEntry{{Series: 138691, Target: "TVDB 0x1", Nodes: []int{2888, 2891}}}}
	baselined, novel, _ := base.Split(rep.Upstream)
	if len(rep.Blocking) != 0 || len(baselined) != 1 || len(novel) != 0 {
		t.Errorf("Blocking = %+v, baselined %+v, novel %+v; want TVDB 0x1 (2888, 2891) baselined and nothing blocking", rep.Blocking, baselined, novel)
	}
	if len(rep.Unresolved) != 0 {
		t.Errorf("Unresolved = %+v, want none (every sibling's specials are known)", rep.Unresolved)
	}
}

func TestCollisionNewBetweenUnchangedSiblingsOnATouchedSeriesIsNovel(t *testing.T) {
	nodes := []*animelists.Node{
		node(2114, "tvdbid=138691 defaulttvdbseason=1"),
		node(2888, "tvdbid=138691 defaulttvdbseason=0"),
		node(2891, "tvdbid=138691 defaulttvdbseason=0"),
	}
	f := facts(map[int]anidb.Anime{2114: fin(1), 2888: fin(1), 2891: fin(1)}, nil)
	rep := run(nodes, []overlay.Entry{entry(2114, overlay.Set{DefaultTVDBSeason: new("2")})}, f)
	_, novel, _ := (&Baseline{Version: BaselineVersion}).Split(rep.Upstream)
	if len(novel) != 1 || novel[0].Target != "TVDB 0x1" || !slices.Equal(novel[0].Nodes, []int{2888, 2891}) {
		t.Errorf("novel = %+v, want TVDB 0x1 claimed by 2888 and 2891", novel)
	}
}

func TestCollisionOnANewClaimBlocksDespiteTheBaseline(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=1 episodeoffset=5"),
		node(3, "tvdbid=10 defaulttvdbseason=0"),
	}
	f := facts(map[int]anidb.Anime{1: fin(1), 2: fin(1, 1), 3: fin(1)}, nil)
	rep := run(nodes, []overlay.Entry{entry(1, overlay.Set{DefaultTVDBSeason: new("0")})}, f)
	base := &Baseline{Version: BaselineVersion, Collisions: []BaselineEntry{{Series: 10, Target: "TVDB 0x1", Nodes: []int{1, 2, 3}}}}
	baselined, _, _ := base.Split(rep.Upstream)
	want := []string{"1 ep1", "2 S1 (default)", "3 ep1"}
	if len(rep.Blocking) != 1 || !slices.Equal(rep.Blocking[0].Claims, want) || len(baselined) != 0 {
		t.Errorf("Blocking = %+v, baselined %+v; want TVDB 0x1 claimed by %v blocking and nothing baselined", rep.Blocking, baselined, want)
	}
}

func TestCollisionUnresolvedOnlyAgainstAnIntroducedClaim(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=1 episodeoffset=10"),
		node(3, "tvdbid=10 defaulttvdbseason=0"),
	}
	f := facts(map[int]anidb.Anime{1: fin(3, 1), 3: fin(1)}, map[int]int{2: 2})
	clean := run(nodes, []overlay.Entry{entry(1, overlay.Set{EpisodeOffset: new("1")})}, f)
	if len(clean.Unresolved) != 0 {
		t.Errorf("0x1 is claimed by AniDB 1 S1 as upstream and by unchanged AniDB 3: Unresolved = %+v, want none", clean.Unresolved)
	}
	moved := entry(1, overlay.Set{MappingList: &[]overlay.Row{{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 5}}}}})
	claimed := run(nodes, []overlay.Entry{moved}, f)
	if len(claimed.Unresolved) != 1 || claimed.Unresolved[0].AniDB != 2 || !slices.Equal(claimed.Unresolved[0].Episodes, []int{5}) {
		t.Errorf("AniDB 1 S1 moved onto 0x5: Unresolved = %+v, want AniDB 2 on episode 5", claimed.Unresolved)
	}
}

func TestCollisionAClaimTheEntryLeavesIsBaselined(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=1 episodeoffset=1"),
	}
	f := facts(map[int]anidb.Anime{1: fin(2), 2: fin(1)}, nil)
	rep := run(nodes, []overlay.Entry{entry(1, overlay.Set{TMDBTV: new("50")})}, f)
	base := &Baseline{Version: BaselineVersion, Collisions: []BaselineEntry{{Series: 10, Target: "TVDB 1x2", Nodes: []int{1, 2}}}}
	baselined, novel, _ := base.Split(rep.Upstream)
	if len(rep.Blocking) != 0 || len(baselined) != 1 || len(novel) != 0 {
		t.Errorf("Blocking %+v, baselined %+v, novel %+v; want TVDB 1x2 baselined and nothing blocking", rep.Blocking, baselined, novel)
	}
}

// Upstream, AniDB 1 already claims 1x2, with its episode 2.
func TestCollisionAMovedClaimBlocks(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=1 episodeoffset=1"),
	}
	f := facts(map[int]anidb.Anime{1: fin(2), 2: fin(1)}, nil)
	rep := run(nodes, []overlay.Entry{entry(1, overlay.Set{EpisodeOffset: new("1")})}, f)
	if len(rep.Blocking) != 1 || !slices.Equal(rep.Blocking[0].Claims, []string{"1 ep1", "2 ep1"}) {
		t.Errorf("Blocking = %+v, want TVDB 1x2 claimed by 1 ep1 and 2 ep1", rep.Blocking)
	}
}

func TestCollisionARowRestatingTheDefaultIntroducesNothing(t *testing.T) {
	sp := animelists.Row{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-5;"}
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1", sp),
		node(2, "tvdbid=10 defaulttvdbseason=2"),
	}
	f := facts(map[int]anidb.Anime{1: fin(1, 1, 2), 2: fin(1, 2)}, nil)
	e := entry(1, overlay.Set{MappingList: &[]overlay.Row{{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 5}, {2, 2}}}}})
	if rep := run(nodes, []overlay.Entry{e}, f); len(rep.Blocking)+len(rep.Upstream) != 0 {
		t.Errorf("S2 now rowed onto 0x2, where it and AniDB 2 S2 default: Blocking %+v, Upstream %+v; want none", rep.Blocking, rep.Upstream)
	}
}

// Without the mirror, the row is a new claim, so no baseline can hold the collision.
func TestCollisionARowRestatingTheDefaultOntoAClaimTheBaselineSeesBlocks(t *testing.T) {
	sp := animelists.Row{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-5;"}
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1", sp),
		node(2, "tvdbid=10 defaulttvdbseason=0 episodeoffset=1"),
	}
	f := facts(map[int]anidb.Anime{1: fin(1, 1, 2), 2: fin(1)}, nil)
	e := entry(1, overlay.Set{MappingList: &[]overlay.Row{{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 5}, {2, 2}}}}})
	rep := run(nodes, []overlay.Entry{e}, f)
	want := []Collision{{Target: "TVDB 0x2", Claims: []string{"1 S2", "2 ep1"}, Nodes: []int{1, 2}, Series: 10}}
	if !reflect.DeepEqual(rep.Blocking, want) || len(rep.Upstream) != 0 {
		t.Errorf("S2 rowed onto 0x2, where AniDB 2 ep1 sits: Blocking %+v, Upstream %+v; want %+v and nothing upstream", rep.Blocking, rep.Upstream, want)
	}
}

// Upstream, 0x2 shows S1's row label first and hides S2's default claim behind it.
func TestCollisionAnEpisodeHiddenBehindAnotherClaimIsNotNew(t *testing.T) {
	sp := animelists.Row{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-2;"}
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1", sp),
		node(2, "tvdbid=10 defaulttvdbseason=0 episodeoffset=1"),
	}
	f := facts(map[int]anidb.Anime{1: fin(1, 1, 2), 2: fin(1)}, nil)
	e := entry(1, overlay.Set{MappingList: &[]overlay.Row{{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 8}}}}})
	if rep := run(nodes, []overlay.Entry{e}, f); len(rep.Blocking) != 0 {
		t.Errorf("S2 defaults onto 0x2 as upstream: Blocking = %+v, want none", rep.Blocking)
	}
}

// A Target carries no series, so the moved node's 1x1 matches its old one.
func TestCollisionCreatedOrMovedNodeBlocks(t *testing.T) {
	sib := node(2, "tvdbid=10 defaulttvdbseason=1")
	f := facts(map[int]anidb.Anime{1: fin(1), 2: fin(1)}, nil)
	moved := run([]*animelists.Node{node(1, "tvdbid=20 defaulttvdbseason=1"), sib}, []overlay.Entry{entry(1, overlay.Set{TVDBID: new("10")})}, f)
	created := entry(1, overlay.Set{TVDBID: new("10"), DefaultTVDBSeason: new("1")})
	created.Create = true
	made := run([]*animelists.Node{sib}, []overlay.Entry{created}, f)
	for name, rep := range map[string]CollisionReport{"moved": moved, "created": made} {
		if len(rep.Blocking) != 1 || rep.Blocking[0].Target != "TVDB 1x1" || len(rep.Upstream) != 0 {
			t.Errorf("%s: Blocking %+v, Upstream %+v; want TVDB 1x1 blocking", name, rep.Blocking, rep.Upstream)
		}
	}
}

func TestCollisionUntouchedSeriesIsReportedNotBlocking(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=1"),
		node(3, "tvdbid=20 defaulttvdbseason=1"),
	}
	rep := run(nodes, []overlay.Entry{entry(3, overlay.Set{DefaultTVDBSeason: new("2")})}, facts(map[int]anidb.Anime{3: fin(5)}, map[int]int{1: 5, 2: 5}))
	if len(rep.Blocking) != 0 || len(rep.Upstream) != 5 || rep.Upstream[0].Series != 10 || rep.Checked != 2 {
		t.Errorf("report = %+v, want series 10's five shared episodes reported upstream, nothing blocking, two series checked", rep)
	}
}

func TestCollisionTMDBBlockingRule(t *testing.T) {
	f := facts(map[int]anidb.Anime{1: fin(3)}, map[int]int{2: 3, 3: 3})
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=3"),
		node(2, "tvdbid=30 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(3, "tvdbid=40 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
	}
	ok := run(nodes, []overlay.Entry{entry(1, overlay.Set{TMDBSeason: new("2")})}, f)
	if len(ok.Blocking) != 0 {
		t.Errorf("Blocking = %+v, want none", ok.Blocking)
	}
	bad := run(nodes, []overlay.Entry{entry(1, overlay.Set{TMDBSeason: new("1")})}, f)
	if len(bad.Blocking) != 3 {
		t.Errorf("Blocking = %+v, want TMDB 99 S1E1-E3", bad.Blocking)
	}
	mixed := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=98 tmdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=2 tmdbtv=99 tmdbseason=1"),
		node(3, "tvdbid=40 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
	}
	two := run(mixed, []overlay.Entry{entry(1, overlay.Set{TMDBSeason: new("2")})}, f)
	if len(two.Blocking) != 0 {
		t.Errorf("Blocking = %+v, want none (one series claimant, none changed)", two.Blocking)
	}
	apart := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(2, "tvdbid=30 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(4, "tvdbid=30 defaulttvdbseason=2"),
	}
	left := run(apart, []overlay.Entry{entry(1, overlay.Set{DefaultTVDBSeason: new("2")})}, facts(map[int]anidb.Anime{1: fin(3)}, map[int]int{2: 3, 4: 3}))
	if len(left.Blocking)+len(left.Upstream) != 0 {
		t.Errorf("Blocking %+v, Upstream %+v; want none (no TMDB claim introduced, one claimant per series)", left.Blocking, left.Upstream)
	}
}

func TestCollisionChangedNodeOffTVDBBlocksOnTMDB(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=2"),
		node(3, "tvdbid=movie defaulttvdbseason= tmdbtv=98 tmdbseason=1"),
	}
	f := facts(map[int]anidb.Anime{1: fin(1), 2: fin(1), 3: fin(1)}, nil)
	rep := run(nodes, []overlay.Entry{entry(3, overlay.Set{TMDBTV: new("99")})}, f)
	if len(rep.Blocking) != 1 || rep.Blocking[0].Target != "TMDB 99 S1E1" || len(rep.Upstream) != 0 || len(rep.Touched) != 0 {
		t.Errorf("report = %+v, want TMDB 99 S1E1 (1, 3) blocking, nothing upstream, no series touched", rep)
	}
}

func TestCollisionChangedNodeOffTVDBRowRestatingTheDefaultBlocks(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=movie tmdbtv=99 tmdbseason=0"),
		node(3, "tvdbid=movie defaulttvdbseason= tmdbtv=99 tmdbseason=1"),
	}
	f := facts(map[int]anidb.Anime{1: fin(1), 3: fin(1, 1)}, nil)
	e := entry(3, overlay.Set{MappingList: &[]overlay.Row{{AniDBSeason: 0, TMDBSeason: new(0), Episodes: [][]int{{1, 1}}}}})
	rep := run(nodes, []overlay.Entry{e}, f)
	want := []Collision{{Target: "TMDB 99 S0E1", Claims: []string{"1 ep1", "3 S1"}, Nodes: []int{1, 3}}}
	if !reflect.DeepEqual(rep.Blocking, want) {
		t.Errorf("S1 rowed onto TMDB 99 S0E1, where AniDB 1 ep1 sits: Blocking = %+v, want %+v", rep.Blocking, want)
	}
}

func TestCollisionChangedNodeOffTVDBChecksItsWholeTMDBShow(t *testing.T) {
	film := node(3, "tvdbid=movie defaulttvdbseason= tmdbtv=98 tmdbseason=1")
	onto99 := []overlay.Entry{entry(3, overlay.Set{TMDBTV: new("99")})}
	collision := func(series int) []Collision {
		return []Collision{{Target: "TMDB 99 S1E1", Claims: []string{"1 ep1", "3 ep1"}, Nodes: []int{1, 3}, Series: series}}
	}
	tests := []struct {
		name                  string
		others                []*animelists.Node
		entries               []overlay.Entry
		counted               []int
		wantBlocking          []Collision
		wantUncounted         []Uncounted
		wantUpstreamUncounted []Uncounted
	}{
		{
			name:    "lone_untouched_series",
			others:  []*animelists.Node{node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1")},
			entries: onto99, counted: []int{1}, wantBlocking: collision(0),
		},
		{
			name:    "another_film",
			others:  []*animelists.Node{node(1, "tvdbid=movie tmdbtv=99 tmdbseason=1")},
			entries: onto99, counted: []int{1}, wantBlocking: collision(0),
		},
		{
			name:    "uncounted_claimant",
			others:  []*animelists.Node{node(1, "tvdbid=movie tmdbtv=99 tmdbseason=1")},
			entries: onto99, wantUncounted: []Uncounted{{AniDB: 1}},
		},
		{
			name:    "claim_left_as_upstream",
			others:  []*animelists.Node{node(1, "tvdbid=movie tmdbtv=98 tmdbseason=1")},
			entries: []overlay.Entry{entry(3, overlay.Set{IMDbID: new("tt0000001")})}, counted: []int{1},
		},
		{
			name: "show_a_checked_series_reaches",
			others: []*animelists.Node{
				node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
				node(2, "tvdbid=10 defaulttvdbseason=2 tmdbtv=99 tmdbseason=2"),
			},
			entries: onto99, counted: []int{1}, wantBlocking: collision(0),
			wantUncounted:         []Uncounted{{AniDB: 2}},
			wantUpstreamUncounted: []Uncounted{{Series: 10, AniDB: 2}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listed := map[int]anidb.Anime{3: fin(1)}
			for _, id := range tc.counted {
				listed[id] = fin(1)
			}
			rep := run(append(slices.Clone(tc.others), film), tc.entries, facts(listed, nil))
			if !reflect.DeepEqual(rep.Blocking, tc.wantBlocking) || len(rep.Upstream) != 0 {
				t.Errorf("Blocking %+v, Upstream %+v; want %+v and nothing upstream", rep.Blocking, rep.Upstream, tc.wantBlocking)
			}
			if !slices.Equal(rep.Uncounted, tc.wantUncounted) || !slices.Equal(rep.UpstreamUncounted, tc.wantUpstreamUncounted) {
				t.Errorf("Uncounted %+v, UpstreamUncounted %+v; want %+v and %+v", rep.Uncounted, rep.UpstreamUncounted, tc.wantUncounted, tc.wantUpstreamUncounted)
			}
		})
	}
}

func TestCollisionTMDBPlacesEveryClaimantsSpecials(t *testing.T) {
	special := func(series int) []Collision {
		return []Collision{{Target: "TMDB 99 S0E1", Claims: []string{"1 ep1", "3 S1 (default)"}, Nodes: []int{1, 3}, Series: series}}
	}
	ontoSpecials := []overlay.Entry{entry(1, overlay.Set{TMDBSeason: new("0")})}
	tests := []struct {
		name           string
		nodes          []*animelists.Node
		entries        []overlay.Entry
		listed         map[int]anidb.Anime
		database       map[int]int
		wantBlocking   []Collision
		wantUnresolved []Unresolved
	}{
		{
			name: "own_special_with_no_series",
			nodes: []*animelists.Node{
				node(1, "tvdbid=movie tmdbtv=99 tmdbseason=0"),
				node(3, "tvdbid=movie defaulttvdbseason= tmdbtv=98 tmdbseason=1"),
			},
			entries: []overlay.Entry{entry(3, overlay.Set{TMDBTV: new("99")})},
			listed:  map[int]anidb.Anime{1: fin(1), 3: fin(1, 1)}, wantBlocking: special(0),
		},
		{
			name: "special_on_another_series",
			nodes: []*animelists.Node{
				node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
				node(3, "tvdbid=20 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
			},
			entries: ontoSpecials, listed: map[int]anidb.Anime{1: fin(1), 3: fin(1, 1)}, wantBlocking: special(10),
		},
		{
			name: "special_with_no_series",
			nodes: []*animelists.Node{
				node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
				node(3, "tvdbid=movie tmdbtv=99 tmdbseason=1"),
			},
			entries: ontoSpecials, listed: map[int]anidb.Anime{1: fin(1), 3: fin(1, 1)}, wantBlocking: special(10),
		},
		{
			name: "unlisted_on_another_series",
			nodes: []*animelists.Node{
				node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
				node(3, "tvdbid=20 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
			},
			entries: ontoSpecials, listed: map[int]anidb.Anime{1: fin(1)}, database: map[int]int{3: 1},
			wantUnresolved: []Unresolved{{Episodes: []int{1}, Series: 10, TMDB: 99, AniDB: 3}},
		},
		{
			name: "unlisted_with_its_special_rowed_elsewhere",
			nodes: []*animelists.Node{
				node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
				node(3, "tvdbid=20 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1",
					animelists.Row{Attrs: map[string]string{"anidbseason": "0", "tmdbseason": "0"}, Text: ";1-5;"}),
			},
			entries: ontoSpecials, listed: map[int]anidb.Anime{1: fin(1)}, database: map[int]int{3: 1},
		},
		{
			name: "unlisted_beside_a_node_with_no_series",
			nodes: []*animelists.Node{
				node(1, "tvdbid=movie tmdbtv=99 tmdbseason=1"),
				node(3, "tvdbid=movie defaulttvdbseason= tmdbtv=98 tmdbseason=0"),
			},
			entries: []overlay.Entry{entry(3, overlay.Set{TMDBTV: new("99")})},
			listed:  map[int]anidb.Anime{3: fin(1)}, database: map[int]int{1: 1},
			wantUnresolved: []Unresolved{{Episodes: []int{1}, TMDB: 99, AniDB: 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := run(tc.nodes, tc.entries, facts(tc.listed, tc.database))
			if !reflect.DeepEqual(rep.Blocking, tc.wantBlocking) {
				t.Errorf("Blocking = %+v, want %+v", rep.Blocking, tc.wantBlocking)
			}
			if !reflect.DeepEqual(rep.Unresolved, tc.wantUnresolved) {
				t.Errorf("Unresolved = %+v, want %+v", rep.Unresolved, tc.wantUnresolved)
			}
		})
	}
}

func TestCollisionAbsoluteResolvesThroughLayout(t *testing.T) {
	eps := []skyhook.Episode{{Season: 1, Number: 1, Absolute: 1}, {Season: 1, Number: 2, Absolute: 2}, {Season: 2, Number: 1, Absolute: 3}}
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=2"),
	}
	e := entry(1, overlay.Set{DefaultTVDBSeason: new("a")})
	e.Captured.TVDB = &overlay.CapturedTVDB{Series: 10, Episodes: eps}
	rep := run(nodes, []overlay.Entry{e}, facts(map[int]anidb.Anime{1: fin(3), 2: fin(1)}, nil))
	if len(rep.Blocking) != 1 || rep.Blocking[0].Target != "TVDB 2x1" {
		t.Errorf("Blocking = %+v, want TVDB 2x1 (absolute 3) claimed twice", rep.Blocking)
	}
}

func TestCollisionUntouchedAbsoluteNodeReachesTheBaseline(t *testing.T) {
	eps := []skyhook.Episode{{Season: 1, Number: 1, Absolute: 1}, {Season: 2, Number: 1, Absolute: 11}}
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=a"),
		node(2, "tvdbid=10 defaulttvdbseason=1"),
		node(3, "tvdbid=10 defaulttvdbseason=a episodeoffset=10"),
	}
	e := entry(3, overlay.Set{TMDBTV: new("50")})
	e.Captured.TVDB = &overlay.CapturedTVDB{Series: 10, Episodes: eps}
	rep := run(nodes, []overlay.Entry{e}, facts(map[int]anidb.Anime{1: fin(1), 2: fin(1), 3: fin(1)}, nil))
	_, novel, _ := (&Baseline{Version: BaselineVersion}).Split(rep.Upstream)
	if len(rep.Blocking) != 0 || len(novel) != 1 || novel[0].Target != "TVDB 1x1" || !slices.Equal(novel[0].Nodes, []int{1, 2}) {
		t.Errorf("Blocking %+v, novel %+v; want no blocking and TVDB 1x1 (1, 2) novel", rep.Blocking, novel)
	}
}

func TestCollisionRowsCoverAndRedirect(t *testing.T) {
	f := facts(map[int]anidb.Anime{1: fin(2, 1), 2: fin(1)}, nil)
	reg := animelists.Row{Attrs: map[string]string{"anidbseason": "1", "tvdbseason": "0"}, Text: ";1-3;"}
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=0 episodeoffset=1", reg),
	}
	onto := func(target int) []overlay.Entry {
		sp := overlay.Row{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, target}}}
		if target == 0 {
			sp.Episodes = [][]int{{1}}
		}
		return []overlay.Entry{entry(1, overlay.Set{MappingList: &[]overlay.Row{sp}})}
	}
	if rep := run(nodes, onto(2), f); len(rep.Blocking) != 0 {
		t.Errorf("node 2's row moves its episode 1 to 0x3, away from node 1's S1 on 0x2: %+v", rep.Blocking)
	}
	nodes[1].Rows = nil
	if rep := run(nodes, onto(2), f); len(rep.Blocking) != 1 || rep.Blocking[0].Target != "TVDB 0x2" {
		t.Errorf("without the row node 2's offset puts it on 0x2: %+v, want one collision", rep.Blocking)
	}
	if rep := run(nodes, onto(0), f); len(rep.Blocking) != 0 {
		t.Errorf("a ;1-0; row maps node 1's special to nothing: %+v, want no collision", rep.Blocking)
	}
}

// Two series share one TMDB show and both claim its first episode twice:
// each series sees the same collision, and it is reported once.
func TestCollisionSharedTMDBReportedOnce(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=2 tmdbtv=99 tmdbseason=1"),
		node(3, "tvdbid=20 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(4, "tvdbid=20 defaulttvdbseason=2 tmdbtv=99 tmdbseason=1"),
		node(5, "tvdbid=30 defaulttvdbseason=1"),
	}
	rep := run(nodes, []overlay.Entry{entry(5, overlay.Set{DefaultTVDBSeason: new("2")})}, facts(map[int]anidb.Anime{5: fin(1)}, map[int]int{1: 1, 2: 1, 3: 1, 4: 1}))
	if len(rep.Upstream) != 1 || rep.Upstream[0].Target != "TMDB 99 S1E1" || len(rep.Upstream[0].Claims) != 4 {
		t.Errorf("upstream = %+v, want TMDB 99 S1E1 once, claimed by all four", rep.Upstream)
	}
}

// A sibling on a touched series with no count anywhere places none of its
// episodes, so a collision through them is invisible: it is reported.
func TestCollisionUncountedSiblingOnATouchedSeries(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=2"),
	}
	e := entry(1, overlay.Set{DefaultTVDBSeason: new("2")})
	rep := run(nodes, []overlay.Entry{e}, facts(map[int]anidb.Anime{1: fin(3)}, nil))
	if len(rep.Blocking) != 0 || len(rep.Uncounted) != 1 || rep.Uncounted[0] != (Uncounted{Series: 10, AniDB: 2}) {
		t.Errorf("no count for AniDB 2: Blocking %+v, Uncounted %+v; want AniDB 2 on TVDB 10 uncounted", rep.Blocking, rep.Uncounted)
	}
	counted := run(nodes, []overlay.Entry{e}, facts(map[int]anidb.Anime{1: fin(3)}, map[int]int{2: 3}))
	if len(counted.Uncounted) != 0 || len(counted.Blocking) != 3 {
		t.Errorf("with AniDB 2 counted: Blocking %+v, Uncounted %+v; want TVDB 2x1-2x3 blocking", counted.Blocking, counted.Uncounted)
	}
}

// On an untouched shared series an uncounted node is reported for the
// baseline, and so is an uncounted node that shares only the TMDB show.
func TestCollisionUncountedOnAnUntouchedSeries(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=2"),
		node(3, "tvdbid=20 defaulttvdbseason=1"),
		node(4, "tmdbtv=99 tmdbseason=2"),
	}
	rep := run(nodes, []overlay.Entry{entry(3, overlay.Set{DefaultTVDBSeason: new("2")})}, facts(map[int]anidb.Anime{3: fin(1)}, map[int]int{1: 3}))
	want := []Uncounted{{Series: 10, AniDB: 2}, {Series: 10, AniDB: 4}}
	if len(rep.Uncounted) != 0 || !slices.Equal(rep.UpstreamUncounted, want) {
		t.Errorf("Uncounted %+v, UpstreamUncounted %+v; want none touched and %+v upstream", rep.Uncounted, rep.UpstreamUncounted, want)
	}
	counted := run(nodes, []overlay.Entry{entry(3, overlay.Set{DefaultTVDBSeason: new("2")})}, facts(map[int]anidb.Anime{3: fin(1)}, map[int]int{1: 3, 2: 3, 4: 3}))
	if len(counted.UpstreamUncounted) != 0 {
		t.Errorf("UpstreamUncounted with every node counted = %+v, want none", counted.UpstreamUncounted)
	}
}

// A bridge makes its parent's series touched, and there the check places
// the specials the mirror lists, so a sibling claiming the bridged
// special's episode blocks; on an untouched series specials are not placed.
func TestCollisionBridgeParentSpecials(t *testing.T) {
	nodes := map[int]*animelists.Node{
		1: node(1, "tvdbid=10 defaulttvdbseason=1"),
		2: node(2, "tvdbid=10 defaulttvdbseason=0"),
	}
	f := facts(map[int]anidb.Anime{1: fin(3, 1), 2: fin(1)}, nil)
	b := overlay.Bridge{AniListID: 9, ParentAniDBID: 1, Specials: []int{1}}
	without := Collisions(nodes, nil, nil, f)
	with := Collisions(nodes, nil, []overlay.Bridge{b}, f)
	if len(without.Blocking)+len(without.Upstream) != 0 || len(with.Blocking) != 1 || with.Blocking[0].Target != "TVDB 0x1" || len(with.Touched) != 1 {
		t.Errorf("without the bridge %+v, with it %+v; want TVDB 0x1 blocking only with it", without, with)
	}
}

func TestCollisionSiblingSpecialsComeFromTheMirror(t *testing.T) {
	nodes := map[int]*animelists.Node{
		1: node(1, "tvdbid=10 defaulttvdbseason=1"),
		2: node(2, "tvdbid=10 defaulttvdbseason=1 episodeoffset=3"),
	}
	b := []overlay.Bridge{{AniListID: 9, ParentAniDBID: 1, Specials: []int{1}}}
	unlisted := Collisions(nodes, nil, b, facts(map[int]anidb.Anime{1: fin(3, 1)}, map[int]int{2: 2}))
	if len(unlisted.Unresolved) != 1 || unlisted.Unresolved[0].AniDB != 2 {
		t.Fatalf("AniDB 2 not in the mirror: Unresolved = %+v, want AniDB 2", unlisted.Unresolved)
	}
	if got := Collisions(nodes, nil, b, facts(map[int]anidb.Anime{1: fin(3, 1), 2: fin(2)}, nil)); len(got.Unresolved)+len(got.Blocking) != 0 {
		t.Errorf("AniDB 2 listed with no specials: Unresolved = %+v, Blocking = %+v; want neither", got.Unresolved, got.Blocking)
	}
	if got := Collisions(nodes, nil, b, facts(map[int]anidb.Anime{1: fin(3, 1), 2: fin(2, 1)}, nil)); len(got.Blocking) != 1 || got.Blocking[0].Target != "TVDB 0x1" {
		t.Errorf("AniDB 2 listed with S1 beside the bridged S1: Blocking = %+v, want TVDB 0x1", got.Blocking)
	}
}

// The count the check places is anidb.Facts.Count: a finished anime's
// mirror count over the database's.
func TestCollisionPlacesTheMirrorCount(t *testing.T) {
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=1 episodeoffset=1"),
		node(3, "tvdbid=20 defaulttvdbseason=1"),
	}
	e := []overlay.Entry{entry(3, overlay.Set{DefaultTVDBSeason: new("2")})}
	byDatabase := run(nodes, e, facts(map[int]anidb.Anime{3: fin(1)}, map[int]int{1: 1, 2: 1}))
	byMirror := run(nodes, e, facts(map[int]anidb.Anime{1: fin(2), 3: fin(1)}, map[int]int{1: 1, 2: 1}))
	if len(byDatabase.Upstream) != 0 || len(byMirror.Upstream) != 1 || byMirror.Upstream[0].Target != "TVDB 1x2" {
		t.Errorf("database count 1: %+v; mirror count 2: %+v; want only the mirror's AniDB 1 ep2 on TVDB 1x2", byDatabase.Upstream, byMirror.Upstream)
	}
}
