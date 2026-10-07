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

// Collision is one target claimed by two or more nodes. Series is the TVDB
// series it was found from, or 0 when it was found from the TMDB show of a
// changed node with no TVDB series.
type Collision struct {
	Target string   `json:"target"`
	Claims []string `json:"claims"`
	Nodes  []int    `json:"nodes"`
	Series int      `json:"series"`
}

// Unresolved is a node whose specials the AniDB mirror does not list, while
// another node claims a season-0 episode it could default onto, and either
// that claim is introduced or the node is changed. TMDB is the show those
// episodes are on, or 0 for TVDB season 0. Series is as on Collision.
type Unresolved struct {
	Episodes []int `json:"episodes"`
	Series   int   `json:"series"`
	TMDB     int   `json:"tmdb,omitempty"`
	AniDB    int   `json:"anidb"`
}

// Uncounted is a claimant anidb.Facts.Count has no regular episode count
// for, so none of its regular episodes can be placed and a collision
// through them would go unseen. Series is as on Collision.
type Uncounted struct {
	Series int `json:"series"`
	AniDB  int `json:"anidb"`
}

// CollisionReport is the result over every TVDB series of the patched list.
// Blocking holds the collisions with an introduced claim (see introduced),
// Upstream the rest, measured without the mirror's specials and with absolute
// numbers placed through the series' captured layouts. Uncounted holds the
// uncounted claimants on a touched series or on the TMDB show of a changed
// node with no TVDB series, UpstreamUncounted the others. A caller must treat
// every Unresolved and Uncounted as blocking, and check Upstream and
// UpstreamUncounted against a baseline.
type CollisionReport struct {
	Blocking          []Collision  `json:"blocking"`
	Upstream          []Collision  `json:"upstream"`
	Unresolved        []Unresolved `json:"unresolved"`
	Uncounted         []Uncounted  `json:"uncounted"`
	UpstreamUncounted []Uncounted  `json:"upstream_uncounted"`
	Touched           []int        `json:"touched"`
	Checked           int          `json:"checked"`
}

// Collisions applies entries to upstream, the list as Anime-Lists has it,
// and runs the series-wide check over every series two or more nodes share
// and every series an entry or a bridge's parent lands on. Every regular
// count and special comes from facts.
func Collisions(upstream map[int]*animelists.Node, entries []overlay.Entry, bridges []overlay.Bridge, facts *anidb.Facts) CollisionReport {
	var rep CollisionReport
	nodes := overlay.Apply(upstream, entries)
	onSeries, byTMDB := indexNodes(nodes)
	bySeries, byBridge := touched(nodes, entries), bridged(nodes, bridges)
	base := seriesCheck{nodes: nodes, upstream: upstream, facts: facts, changed: map[int]bool{}, bridged: map[int][]int{}}
	for i := range entries {
		base.changed[entries[i].AniDBID] = true
	}
	for i := range bridges {
		b := &bridges[i]
		base.changed[b.ParentAniDBID] = true
		base.bridged[b.ParentAniDBID] = append(base.bridged[b.ParentAniDBID], b.Specials...)
	}
	seen, seenUp := map[string]bool{}, map[string]bool{}
	colKey := func(c Collision) string { return c.Target + "|" + strings.Join(c.Claims, ",") }
	uncKey := func(u Uncounted) string { return fmt.Sprint("uncounted|", u.Series, "|", u.AniDB) }
	unrKey := func(u Unresolved) string { return fmt.Sprint("unresolved|", u.TMDB, "|", u.AniDB, "|", u.Episodes) }
	report := func(r pass) {
		addOnce(seen, &rep.Blocking, r.ours, colKey)
		addOnce(seen, &rep.Uncounted, r.uncounted, uncKey)
		addOnce(seen, &rep.Unresolved, r.unresolved, unrKey)
	}
	for _, series := range slices.Sorted(maps.Keys(onSeries)) {
		sibs := onSeries[series]
		hit := bySeries[series] != nil || byBridge[series]
		if len(sibs) < 2 && !hit {
			continue
		}
		rep.Checked++
		up := base.on(series, false, bySeries[series]).check(sibs, byTMDB)
		addOnce(seenUp, &rep.Upstream, up.theirs, colKey)
		if !hit {
			addOnce(seen, &rep.UpstreamUncounted, up.uncounted, uncKey)
			continue
		}
		rep.Touched = append(rep.Touched, series)
		p := base.on(series, true, bySeries[series]).check(sibs, byTMDB)
		p.ours = withBaselineOnly(p.ours, up.ours)
		report(p)
	}
	report(base.offTVDB(byTMDB))
	return rep
}

// withBaselineOnly adds to ours each collision the baseline's own measure
// calls introduced on a target ours lacks: a row restating a special's
// mirror default is unchanged beside the mirror, but no baseline could
// ever hold the collision it makes.
func withBaselineOnly(ours, baseline []Collision) []Collision {
	for _, col := range baseline {
		if !slices.ContainsFunc(ours, func(o Collision) bool { return o.Target == col.Target }) {
			ours = append(ours, col)
		}
	}
	return ours
}

// One TMDB collision can be reached from several touched series.
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
	label      string
	node       int
	introduced bool
}

func collide(series int, claims map[Target][]claim, keep func([]claim) bool) (ours, theirs []Collision) {
	keys := slices.SortedFunc(maps.Keys(claims), func(a, b Target) int {
		return cmp.Or(cmp.Compare(a.TV, b.TV), cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
	})
	for _, t := range keys {
		cl := claims[t]
		if distinctNodes(cl) < 2 || (keep != nil && !keep(cl)) {
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
		if slices.ContainsFunc(cl, func(x claim) bool { return x.introduced }) {
			ours = append(ours, col)
		} else {
			theirs = append(theirs, col)
		}
	}
	return ours, theirs
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

func (sd side) attrs() (season, dflt, off string) {
	if sd == sideTMDB {
		return "tmdbseason", "tmdbseason", "tmdboffset"
	}
	return "tvdbseason", "defaulttvdbseason", "episodeoffset"
}

type seriesCheck struct {
	nodes    map[int]*animelists.Node
	upstream map[int]*animelists.Node
	facts    *anidb.Facts
	changed  map[int]bool
	bridged  map[int][]int
	absolute map[int]skyhook.Episode
	series   int
	touched  bool
}

type pass struct {
	ours, theirs []Collision
	uncounted    []Uncounted
	unresolved   []Unresolved
}

func (c *seriesCheck) on(series int, touched bool, entries []*overlay.Entry) *seriesCheck {
	s := *c
	s.series, s.touched = series, touched
	s.absolute = map[int]skyhook.Episode{}
	for _, e := range entries {
		s.addLayout(e.Captured.TVDB)
	}
	return &s
}

// specials are the special numbers AniDB lists for node id, read for every
// node on a touched check and none otherwise; known is false where they are
// not read.
func (c *seriesCheck) specials(id int) (numbers []int, known bool) {
	if !c.touched {
		return nil, false
	}
	a, ok := c.facts.Anime(id)
	for _, e := range a.Specials {
		numbers = append(numbers, e.Number)
	}
	return numbers, ok
}

func (c *seriesCheck) check(sibs []int, byTMDB map[int][]int) pass {
	var r pass
	tvdb := c.sideClaims(sibs, sideTVDB)
	r.ours, r.theirs = collide(c.series, tvdb, nil)
	r.unresolved = c.unresolved(sibs, tvdb, 0)
	claimants := slices.Clone(sibs)
	keep := func(cl []claim) bool { return c.reportsTMDB(sibs, cl) }
	for _, tv := range c.tmdbIDs(sibs) {
		s := c.show(tv, byTMDB[tv], keep)
		r.ours, r.theirs = append(r.ours, s.ours...), append(r.theirs, s.theirs...)
		r.unresolved = append(r.unresolved, s.unresolved...)
		claimants = append(claimants, byTMDB[tv]...)
	}
	r.uncounted = c.uncounted(claimants)
	return r
}

func (c *seriesCheck) show(tv int, claimants []int, keep func([]claim) bool) pass {
	var r pass
	claims := c.sideClaims(claimants, sideTMDB)
	r.ours, r.theirs = collide(c.series, claims, keep)
	r.unresolved = c.unresolved(claimants, claims, tv)
	return r
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
		now := c.claims(id, c.nodes[id], sd)
		put := c.introduced(id, now, sd)
		for t, ls := range now {
			out[t] = append(out[t], claim{label: ls[0], node: id, introduced: put[t]})
		}
	}
	return out
}

// introduced marks the targets where the overlay put a changed node's
// claim: every target of a created node or one moved to another TVDB
// series, else each where upstream placed a different episode or none, and
// on TVDB each target of a bridged special.
func (c *seriesCheck) introduced(id int, now map[Target][]string, sd side) map[Target]bool {
	if !c.changed[id] {
		return nil
	}
	var was map[Target][]string
	if up := c.upstream[id]; up != nil && up.Attr("tvdbid") == c.nodes[id].Attr("tvdbid") {
		was = c.claims(id, up, sd)
	}
	out := map[Target]bool{}
	for t, ls := range now {
		for _, l := range ls {
			if !slices.ContainsFunc(was[t], func(w string) bool { return sameEpisode(w, l) }) {
				out[t] = true
			}
		}
	}
	if sd == sideTVDB {
		for _, t := range specialTargets(c.nodes[id].SideRows("tvdbseason"), c.bridged[id]) {
			out[t] = true
		}
	}
	return out
}

// specialTargets must place specials as specialClaims does, or a bridged
// special's claim is never marked introduced.
func specialTargets(rows []animelists.SideRow, ks []int) []Target {
	var out []Target
	for _, k := range ks {
		named := false
		for _, r := range rows {
			if r.AniDBSeason != 0 || !slices.Contains(r.Sources(), k) {
				continue
			}
			named = true
			_, targets := animelists.Targets([]animelists.SideRow{r}, 0, k)
			for _, t := range targets {
				out = append(out, Target{Season: t[0], Episode: t[1]})
			}
		}
		if !named {
			out = append(out, Target{Episode: k})
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

// offTVDB checks the TMDB show of each changed node with no TVDB series,
// since such a node touches no series check.
func (c *seriesCheck) offTVDB(byTMDB map[int][]int) pass {
	set := map[int]bool{}
	for id := range c.changed {
		if n := c.nodes[id]; n != nil && positiveAttr(n, "tvdbid") == 0 {
			if tv := positiveAttr(n, "tmdbtv"); tv > 0 {
				set[tv] = true
			}
		}
	}
	var r pass
	s, rows := c.on(0, true, nil), c.on(0, false, nil)
	for _, tv := range slices.Sorted(maps.Keys(set)) {
		p := s.show(tv, byTMDB[tv], nil)
		p.ours = withBaselineOnly(p.ours, rows.show(tv, byTMDB[tv], nil).ours)
		r.ours, r.unresolved = append(r.ours, p.ours...), append(r.unresolved, p.unresolved...)
		r.uncounted = append(r.uncounted, s.uncounted(byTMDB[tv])...)
	}
	return r
}

// reportsTMDB is the TMDB rule: this series reports a shared TMDB episode
// only when two claimants sit on it or a claim on it is introduced.
func (c *seriesCheck) reportsTMDB(sibs []int, cl []claim) bool {
	on := 0
	for _, x := range cl {
		if x.introduced {
			return true
		}
		if slices.Contains(sibs, x.node) {
			on++
		}
	}
	return on > 1
}

const byDefault = " (default)"

// sameEpisode reports whether two claim labels name one episode, so a row
// restating a special's default introduces nothing.
func sameEpisode(a, b string) bool {
	return strings.TrimSuffix(a, byDefault) == strings.TrimSuffix(b, byDefault)
}

// claimer collects each target's labels, the first one being the label the
// target's collision shows.
type claimer struct {
	out map[Target][]string
	tv  int
}

func (cl claimer) add(season, ep int, label string) {
	t := Target{TV: cl.tv, Season: season, Episode: ep}
	if !slices.Contains(cl.out[t], label) {
		cl.out[t] = append(cl.out[t], label)
	}
}

func (c *seriesCheck) claims(id int, n *animelists.Node, sd side) map[Target][]string {
	seasonAttr, dfltAttr, offAttr := sd.attrs()
	cl := claimer{out: map[Target][]string{}}
	if sd == sideTMDB {
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
			cl.add(0, k, fmt.Sprintf("%d S%d", id, k)+byDefault)
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

// unresolved finds the claimants whose unread specials could land on a
// season-0 claim of tv, 0 being the TVDB side; only a touched check reads
// specials, so only it can tell.
func (c *seriesCheck) unresolved(claimants []int, claims map[Target][]claim, tv int) []Unresolved {
	if !c.touched {
		return nil
	}
	sd := sideTVDB
	if tv != 0 {
		sd = sideTMDB
	}
	rowsAttr, _, _ := sd.attrs()
	var out []Unresolved
	for _, id := range claimants {
		if _, known := c.specials(id); known {
			continue
		}
		against := func(x claim) bool { return x.node != id && (c.changed[id] || x.introduced) }
		if hit := seasonZeroClaims(coveredSpecials(c.nodes[id].SideRows(rowsAttr)), claims, against); len(hit) > 0 {
			out = append(out, Unresolved{Series: c.series, TMDB: tv, AniDB: id, Episodes: hit})
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

func seasonZeroClaims(covered map[int]bool, tvdb map[Target][]claim, against func(claim) bool) []int {
	var hit []int
	for t, cl := range tvdb {
		if t.Season != 0 || covered[t.Episode] {
			continue
		}
		if slices.ContainsFunc(cl, against) {
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
