package guard

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

// Target is one episode on one side: TV is 0 on the TVDB side and the TMDB
// tv id on the TMDB side.
type Target struct {
	TV, Season, Episode int
}

func (t Target) String() string {
	if t.TV == 0 {
		return fmt.Sprintf("TVDB %dx%d", t.Season, t.Episode)
	}
	return fmt.Sprintf("TMDB %d S%dE%d", t.TV, t.Season, t.Episode)
}

// Collision is one target claimed by two or more nodes.
type Collision struct {
	Target string   `json:"target"`
	Claims []string `json:"claims"`
	Nodes  []int    `json:"nodes"`
	Series int      `json:"series"`
}

// Unresolved is a node on a touched series whose specials the AniDB mirror
// does not list, while another node claims a season-0 episode it could
// default onto.
type Unresolved struct {
	Episodes []int `json:"episodes"`
	Series   int   `json:"series"`
	AniDB    int   `json:"anidb"`
}

// Uncounted is a node on a checked series that anidb.Facts.Count has no
// regular episode count for, so none of its regular episodes can be placed
// and a collision through them would go unseen.
type Uncounted struct {
	Series int `json:"series"`
	AniDB  int `json:"anidb"`
}

// CollisionReport is the result over every TVDB series of the patched list.
// Blocking, Unresolved and Uncounted are on series an overlay entry lands
// on; Upstream and UpstreamUncounted on every other checked series, which
// predate the overlay. Specials are placed, and Unresolved known, only on
// touched series, the ones whose mapping the overlay vouches for. A caller
// must treat every Unresolved
// and Uncounted as blocking, and check the upstream halves against a
// baseline.
type CollisionReport struct {
	Blocking          []Collision  `json:"blocking"`
	Upstream          []Collision  `json:"upstream"`
	Unresolved        []Unresolved `json:"unresolved"`
	Uncounted         []Uncounted  `json:"uncounted"`
	UpstreamUncounted []Uncounted  `json:"upstream_uncounted"`
	Touched           []int        `json:"touched"`
	Checked           int          `json:"checked"`
}

// Collisions runs the series-wide check over every series two or more nodes
// share, and every series an entry or a bridge's parent lands on. nodes is
// the patched list; every regular count and special comes from facts.
func Collisions(nodes map[int]*animelists.Node, entries []overlay.Entry, bridges []overlay.Bridge, facts *anidb.Facts) CollisionReport {
	var rep CollisionReport
	onSeries, byTMDB := indexNodes(nodes)
	bySeries, byBridge := touched(nodes, entries), bridged(nodes, bridges)
	seen := map[string]bool{}
	for _, series := range slices.Sorted(maps.Keys(onSeries)) {
		sibs := onSeries[series]
		hit := bySeries[series] != nil || byBridge[series]
		if len(sibs) < 2 && !hit {
			continue
		}
		rep.Checked++
		c := newSeriesCheck(series, sibs, hit, bySeries[series], nodes, facts)
		tvdb, found, uncounted := c.check(sibs, byTMDB)
		dst, unc := &rep.Upstream, &rep.UpstreamUncounted
		if hit {
			dst, unc = &rep.Blocking, &rep.Uncounted
			rep.Touched = append(rep.Touched, series)
			rep.Unresolved = append(rep.Unresolved, c.unresolved(sibs, tvdb)...)
		}
		addOnce(seen, dst, found, func(c Collision) string { return c.Target + "|" + strings.Join(c.Claims, ",") })
		addOnce(seen, unc, uncounted, func(u Uncounted) string { return fmt.Sprint("uncounted|", u.Series, "|", u.AniDB) })
	}
	return rep
}

// addOnce appends each item whose key seen does not hold: a collision on a
// shared TMDB show is found from every TVDB series that reaches it.
func addOnce[T any](seen map[string]bool, dst *[]T, items []T, key func(T) string) {
	for _, it := range items {
		if k := key(it); !seen[k] {
			seen[k] = true
			*dst = append(*dst, it)
		}
	}
}

func indexNodes(nodes map[int]*animelists.Node) (onSeries, byTMDB map[int][]int) {
	onSeries, byTMDB = map[int][]int{}, map[int][]int{}
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		if s := positiveAttr(nodes[id], "tvdbid"); s > 0 {
			onSeries[s] = append(onSeries[s], id)
		}
		if tv := positiveAttr(nodes[id], "tmdbtv"); tv > 0 {
			byTMDB[tv] = append(byTMDB[tv], id)
		}
	}
	return onSeries, byTMDB
}

func touched(nodes map[int]*animelists.Node, entries []overlay.Entry) map[int][]*overlay.Entry {
	out := map[int][]*overlay.Entry{}
	for i := range entries {
		e := &entries[i]
		if p := nodes[e.AniDBID]; p != nil {
			if s := positiveAttr(p, "tvdbid"); s > 0 {
				out[s] = append(out[s], e)
			}
		}
	}
	return out
}

func bridged(nodes map[int]*animelists.Node, bridges []overlay.Bridge) map[int]bool {
	out := map[int]bool{}
	for i := range bridges {
		if p := nodes[bridges[i].ParentAniDBID]; p != nil {
			if s := positiveAttr(p, "tvdbid"); s > 0 {
				out[s] = true
			}
		}
	}
	return out
}

type claim struct {
	label string
	node  int
}

func collide(series int, claims map[Target][]claim, blocks func([]claim) bool) []Collision {
	var out []Collision
	keys := slices.SortedFunc(maps.Keys(claims), func(a, b Target) int {
		return cmp.Or(cmp.Compare(a.TV, b.TV), cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
	})
	for _, t := range keys {
		cl := claims[t]
		if distinctNodes(cl) < 2 || (blocks != nil && !blocks(cl)) {
			continue
		}
		col := Collision{Series: series, Target: t.String()}
		for _, x := range cl {
			col.Claims = append(col.Claims, x.label)
			if !slices.Contains(col.Nodes, x.node) {
				col.Nodes = append(col.Nodes, x.node)
			}
		}
		slices.Sort(col.Nodes)
		out = append(out, col)
	}
	return out
}

func distinctNodes(cl []claim) int {
	seen := map[int]bool{}
	for _, x := range cl {
		seen[x.node] = true
	}
	return len(seen)
}

type side int

const (
	sideTVDB side = iota
	sideTMDB
)

type seriesCheck struct {
	nodes    map[int]*animelists.Node
	facts    *anidb.Facts
	onSeries map[int]bool
	patched  map[int]bool
	absolute map[int]skyhook.Episode
	series   int
	touched  bool
}

func newSeriesCheck(series int, sibs []int, touched bool, entries []*overlay.Entry, nodes map[int]*animelists.Node, facts *anidb.Facts) *seriesCheck {
	c := &seriesCheck{
		series: series, nodes: nodes, facts: facts, touched: touched,
		onSeries: map[int]bool{}, patched: map[int]bool{}, absolute: map[int]skyhook.Episode{},
	}
	for _, id := range sibs {
		c.onSeries[id] = true
	}
	for _, e := range entries {
		c.patched[e.AniDBID] = true
		c.addLayout(e.Captured.TVDB)
	}
	return c
}

// specials are the special numbers AniDB lists for node id, read only for
// the nodes of a touched series; known is false where they are not read.
func (c *seriesCheck) specials(id int) (numbers []int, known bool) {
	if !c.touched || !c.onSeries[id] {
		return nil, false
	}
	a, ok := c.facts.Anime(id)
	for _, e := range a.Specials {
		numbers = append(numbers, e.Number)
	}
	return numbers, ok
}

// check returns the series' TVDB claims, its collisions on both sides, and
// every claimant, on either side, with no regular episode count.
func (c *seriesCheck) check(sibs []int, byTMDB map[int][]int) (tvdb map[Target][]claim, found []Collision, uncounted []Uncounted) {
	tvdb = c.sideClaims(sibs, sideTVDB)
	found = collide(c.series, tvdb, nil)
	claimants := slices.Clone(sibs)
	for _, tv := range c.tmdbIDs(sibs) {
		blocks := func(cl []claim) bool { return c.tmdbBlocks(sibs, cl) }
		found = append(found, collide(c.series, c.sideClaims(byTMDB[tv], sideTMDB), blocks)...)
		claimants = append(claimants, byTMDB[tv]...)
	}
	return tvdb, found, c.uncounted(claimants)
}

// addLayout indexes a captured layout by absolute number, which is how an
// absolute-numbered node's episodes resolve to a season and episode.
func (c *seriesCheck) addLayout(t *overlay.CapturedTVDB) {
	if t == nil || t.Series != c.series {
		return
	}
	for _, ep := range t.Episodes {
		if ep.Absolute > 0 && ep.Season > 0 {
			c.absolute[ep.Absolute] = ep
		}
	}
}

func (c *seriesCheck) sideClaims(ids []int, sd side) map[Target][]claim {
	out := map[Target][]claim{}
	for _, id := range ids {
		for t, l := range c.claims(id, sd) {
			out[t] = append(out[t], claim{label: l, node: id})
		}
	}
	return out
}

func (c *seriesCheck) tmdbIDs(sibs []int) []int {
	set := map[int]bool{}
	for _, id := range sibs {
		if tv := positiveAttr(c.nodes[id], "tmdbtv"); tv > 0 {
			set[tv] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// tmdbBlocks is the TMDB rule: a shared TMDB episode blocks only when two
// claimants sit on this TVDB series or one of them is a patched node.
func (c *seriesCheck) tmdbBlocks(sibs []int, cl []claim) bool {
	on := 0
	for _, x := range cl {
		if c.patched[x.node] {
			return true
		}
		if slices.Contains(sibs, x.node) {
			on++
		}
	}
	return on > 1
}

type claimer struct {
	out map[Target]string
	tv  int
}

func (cl claimer) add(season, ep int, label string) {
	t := Target{TV: cl.tv, Season: season, Episode: ep}
	if _, ok := cl.out[t]; !ok {
		cl.out[t] = label
	}
}

func (c *seriesCheck) claims(id int, sd side) map[Target]string {
	n := c.nodes[id]
	seasonAttr, dfltAttr, offAttr := "tvdbseason", "defaulttvdbseason", "episodeoffset"
	cl := claimer{out: map[Target]string{}}
	if sd == sideTMDB {
		seasonAttr, dfltAttr, offAttr = "tmdbseason", "tmdbseason", "tmdboffset"
		if cl.tv = positiveAttr(n, "tmdbtv"); cl.tv == 0 {
			return nil
		}
	}
	rows := n.SideRows(seasonAttr)
	off, _ := strconv.Atoi(n.Attr(offAttr))
	dflt := n.Attr(dfltAttr)
	for k := 1; k <= c.facts.Count(id); k++ {
		label := fmt.Sprintf("%d ep%d", id, k)
		if covered, targets := animelists.Targets(rows, 1, k); covered {
			for _, t := range targets {
				cl.add(t[0], t[1], label)
			}
			continue
		}
		if s, ep, ok := c.defaultTarget(dflt, sd, k+off); ok {
			cl.add(s, ep, label)
		}
	}
	c.specialClaims(cl, id, rows)
	return cl.out
}

func (c *seriesCheck) defaultTarget(dflt string, sd side, ep int) (season, episode int, ok bool) {
	if s, err := strconv.Atoi(dflt); err == nil && s >= 0 {
		return s, ep, true
	}
	if dflt == "a" && sd == sideTVDB {
		if e, found := c.absolute[ep]; found {
			return e.Season, e.Number, true
		}
	}
	return 0, 0, false
}

// specialClaims adds the AniDB specials: through their rows, else each
// known special k defaults onto season 0 episode k.
func (c *seriesCheck) specialClaims(cl claimer, id int, rows []animelists.SideRow) {
	for _, r := range rows {
		if r.AniDBSeason != 0 {
			continue
		}
		for _, k := range r.Sources() {
			_, targets := animelists.Targets([]animelists.SideRow{r}, 0, k)
			for _, t := range targets {
				cl.add(t[0], t[1], fmt.Sprintf("%d S%d", id, k))
			}
		}
	}
	covered := coveredSpecials(rows)
	numbers, _ := c.specials(id)
	for _, k := range numbers {
		if !covered[k] {
			cl.add(0, k, fmt.Sprintf("%d S%d (default)", id, k))
		}
	}
}

func coveredSpecials(rows []animelists.SideRow) map[int]bool {
	out := map[int]bool{}
	for _, r := range rows {
		if r.AniDBSeason == 0 {
			for _, k := range r.Sources() {
				out[k] = true
			}
		}
	}
	return out
}

func (c *seriesCheck) unresolved(sibs []int, tvdb map[Target][]claim) []Unresolved {
	var out []Unresolved
	for _, id := range sibs {
		if _, known := c.specials(id); known {
			continue
		}
		if hit := othersSeasonZero(id, coveredSpecials(c.nodes[id].SideRows("tvdbseason")), tvdb); len(hit) > 0 {
			out = append(out, Unresolved{Series: c.series, AniDB: id, Episodes: hit})
		}
	}
	return out
}

func (c *seriesCheck) uncounted(sibs []int) []Uncounted {
	var out []Uncounted
	for _, id := range sibs {
		if c.facts.Count(id) <= 0 {
			out = append(out, Uncounted{Series: c.series, AniDB: id})
		}
	}
	return out
}

// othersSeasonZero lists the season-0 episodes another node claims that
// node id's rows do not cover, which an unread special of id could default onto.
func othersSeasonZero(id int, covered map[int]bool, tvdb map[Target][]claim) []int {
	var hit []int
	for t, cl := range tvdb {
		if t.Season != 0 || covered[t.Episode] {
			continue
		}
		if slices.ContainsFunc(cl, func(x claim) bool { return x.node != id }) {
			hit = append(hit, t.Episode)
		}
	}
	slices.Sort(hit)
	return hit
}

func positiveAttr(n *animelists.Node, name string) int {
	v, err := strconv.Atoi(n.Attr(name))
	if err != nil || v <= 0 {
		return 0
	}
	return v
}
