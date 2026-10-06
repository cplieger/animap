package guard

import (
	"errors"
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
	return Collisions(overlay.Apply(m, entries), entries, nil, f)
}

// The 226/582 shape: the patched node's AniDB special defaults onto TVDB
// 0x1, which a sibling film filed under specials already claims.
func TestCollisionDefaultSpecialAgainstSibling(t *testing.T) {
	nodes := []*animelists.Node{
		node(226, "tvdbid=72922 defaulttvdbseason=1 tmdbtv=34165 tmdbseason=1"),
		node(582, "tvdbid=72922 defaulttvdbseason=0 tmdbtv=34165 tmdbseason=0"),
		node(583, "tvdbid=72922 defaulttvdbseason=1 episodeoffset=13"),
	}
	e := entry(226, overlay.Set{DefaultTVDBSeason: new("2"), TMDBSeason: new("2")})
	rep := run(nodes, []overlay.Entry{e}, facts(map[int]anidb.Anime{226: fin(13, 1)}, map[int]int{582: 2, 583: 25}))
	targets := map[string]bool{}
	for _, c := range rep.Blocking {
		targets[c.Target] = true
	}
	if !targets["TVDB 0x1"] || !targets["TMDB 34165 S0E1"] || len(rep.Blocking) != 2 {
		t.Errorf("Blocking = %+v, want TVDB 0x1 and TMDB 34165 S0E1", rep.Blocking)
	}
	if len(rep.Unresolved) == 0 {
		t.Error("siblings the mirror does not list, beside a season-0 claim, were not reported")
	}
}

// The Gall Force shape: the patched node is clean, but two untouched
// siblings on the series it lands on claim the same special.
func TestCollisionBetweenUntouchedSiblingsOnATouchedSeries(t *testing.T) {
	nodes := []*animelists.Node{
		node(2114, "tvdbid=138691 defaulttvdbseason=1"),
		node(2888, "tvdbid=138691 defaulttvdbseason=0"),
		node(2891, "tvdbid=138691 defaulttvdbseason=0"),
	}
	f := facts(map[int]anidb.Anime{2114: fin(1), 2888: fin(1), 2891: fin(1)}, nil)
	rep := run(nodes, []overlay.Entry{entry(2114, overlay.Set{DefaultTVDBSeason: new("2")})}, f)
	if len(rep.Blocking) != 1 || rep.Blocking[0].Target != "TVDB 0x1" {
		t.Errorf("Blocking = %+v, want TVDB 0x1 claimed by 2888 and 2891", rep.Blocking)
	}
	if len(rep.Unresolved) != 0 {
		t.Errorf("Unresolved = %+v, want none (every sibling's specials are known)", rep.Unresolved)
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
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(2, "tvdbid=30 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
		node(3, "tvdbid=40 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
	}
	// 1 is patched onto TMDB S2, 2 and 3 still collide on S1 but sit on
	// other TVDB series and are not the patched node: not blocking.
	ok := run(nodes, []overlay.Entry{entry(1, overlay.Set{TMDBSeason: new("2")})}, f)
	if len(ok.Blocking) != 0 {
		t.Errorf("Blocking = %+v, want none", ok.Blocking)
	}
	// Patched onto the S1 the others claim: the patched node is a claimant.
	bad := run(nodes, []overlay.Entry{entry(1, overlay.Set{TMDBSeason: new("1")})}, f)
	if len(bad.Blocking) != 3 {
		t.Errorf("Blocking = %+v, want TMDB 99 S1E1-E3", bad.Blocking)
	}
	// One untouched claimant on the patched node's series and one elsewhere
	// share a TMDB episode: neither is patched and only one is on the series.
	mixed := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1 tmdbtv=98 tmdbseason=1"),
		node(2, "tvdbid=10 defaulttvdbseason=2 tmdbtv=99 tmdbseason=1"),
		node(3, "tvdbid=40 defaulttvdbseason=1 tmdbtv=99 tmdbseason=1"),
	}
	two := run(mixed, []overlay.Entry{entry(1, overlay.Set{TMDBSeason: new("2")})}, f)
	if len(two.Blocking) != 0 {
		t.Errorf("Blocking = %+v, want none (one series claimant, none patched)", two.Blocking)
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

func TestCollisionRowsCoverAndRedirect(t *testing.T) {
	f := facts(map[int]anidb.Anime{1: fin(2, 1), 2: fin(1)}, nil)
	sp := animelists.Row{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-1;"}
	reg := animelists.Row{Attrs: map[string]string{"anidbseason": "1", "tvdbseason": "0"}, Text: ";1-2;"}
	nodes := []*animelists.Node{
		node(1, "tvdbid=10 defaulttvdbseason=1", sp),
		node(2, "tvdbid=10 defaulttvdbseason=0", reg),
	}
	e := entry(1, overlay.Set{DefaultTVDBSeason: new("1")})
	if rep := run(nodes, []overlay.Entry{e}, f); len(rep.Blocking) != 0 {
		t.Errorf("node 2's row moves its episode 1 to 0x2, away from node 1's 0x1: %+v", rep.Blocking)
	}
	nodes[1].Rows = nil
	if rep := run(nodes, []overlay.Entry{e}, f); len(rep.Blocking) != 1 || rep.Blocking[0].Target != "TVDB 0x1" {
		t.Errorf("without the row node 2 defaults onto 0x1: %+v, want one collision", rep.Blocking)
	}
	nodes[0].Rows[0].Text = ";1-0;"
	if rep := run(nodes, []overlay.Entry{e}, f); len(rep.Blocking) != 0 {
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

// On a touched series a node the mirror does not list is unresolved, one it
// lists is placed, and a special it lists can collide.
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
