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
	"github.com/cplieger/animap/internal/join"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

// target is one episode on one side: TV is 0 on the TVDB side and the TMDB
// tv id on the TMDB side.
type target struct {
	TV, Season, Episode int
}

func (t target) label() string {
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
// and every series an entry or an applied link lands on. Every regular
// count and special comes from facts.
func Collisions(upstream map[int]*animelists.Node, entries []overlay.Entry, applied join.Applied, facts *anidb.Facts) CollisionReport {
	var rep CollisionReport
	nodes := overlay.Apply(upstream, entries)
	onSeries, byTMDB := indexNodes(nodes)
	bySeries, byLink := touched(nodes, entries), map[int]bool{}
	base := seriesCheck{nodes: nodes, upstream: upstream, facts: facts, changed: map[int]bool{}, linked: map[int][]overlay.Claim{}}
	for i := range entries {
		base.changed[entries[i].AniDBID] = true
	}
	links := applied.Compiled()
	for i := range links {
		byLink[links[i].Series] = true
		for _, c := range links[i].Claims {
			base.changed[c.Parent] = true
			base.linked[c.Parent] = append(base.linked[c.Parent], c)
		}
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
		hit := bySeries[series] != nil || byLink[series]
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

// ClaimIndex answers the claims on any show of one list with entries
// applied, indexed once for every show it is asked about.
type ClaimIndex struct {
	onSeries, byTMDB map[int][]int
	touched          map[int][]*overlay.Entry
	base             seriesCheck
}

// NewClaimIndex indexes upstream, the list as Anime-Lists has it, with
// entries and the applied links; every regular count and special comes
// from facts.
func NewClaimIndex(upstream map[int]*animelists.Node, entries []overlay.Entry, applied join.Applied, facts *anidb.Facts) *ClaimIndex {
	nodes := overlay.Apply(upstream, entries)
	x := &ClaimIndex{
		touched: touched(nodes, entries),
		base:    seriesCheck{nodes: nodes, upstream: upstream, facts: facts, changed: map[int]bool{}, linked: map[int][]overlay.Claim{}},
	}
	x.onSeries, x.byTMDB = indexNodes(nodes)
	links := applied.Compiled()
	for i := range links {
		for _, c := range links[i].Claims {
			x.base.linked[c.Parent] = append(x.base.linked[c.Parent], c)
		}
	}
	return x
}

// Claims lists every claim on the TVDB series or TMDB show id on side, by
// [season, episode], as a touched check reads them: every node there with
// entries applied, AniDB specials included, applied links' claims on TVDB. Each
// label is "<aid> ep<k>" or "<aid> S<k>", with " (default)" when the
// node's default placed the special rather than a row or a link.
func (x *ClaimIndex) Claims(side animelists.Side, id int) map[[2]int][]string {
	ids := x.byTMDB[id]
	series := 0
	if side == animelists.TVDB {
		ids, series = x.onSeries[id], id
	}
	c := x.base.on(series, true, x.touched[series])
	out := map[[2]int][]string{}
	for _, id := range ids {
		for t, labels := range c.claims(id, c.nodes[id], side) {
			out[[2]int{t.Season, t.Episode}] = append(out[[2]int{t.Season, t.Episode}], labels...)
		}
		if side == animelists.TVDB {
			c.linkClaims(id, out)
		}
	}
	return out
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

type claim struct {
	label      string
	node       int
	introduced bool
}

func collide(series int, claims map[target][]claim, keep func([]claim) bool) (ours, theirs []Collision) {
	keys := slices.SortedFunc(maps.Keys(claims), func(a, b target) int {
		return cmp.Or(cmp.Compare(a.TV, b.TV), cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
	})
	for _, t := range keys {
		cl := claims[t]
		if distinctNodes(cl) < 2 || (keep != nil && !keep(cl)) {
			continue
		}
		col := Collision{Series: series, Target: t.label()}
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

type seriesCheck struct {
	nodes    map[int]*animelists.Node
	upstream map[int]*animelists.Node
	facts    *anidb.Facts
	changed  map[int]bool
	linked   map[int][]overlay.Claim
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
	tvdb := c.sideClaims(sibs, animelists.TVDB)
	r.ours, r.theirs = collide(c.series, tvdb, nil)
	r.unresolved = c.unresolved(sibs, tvdb, 0)
	claimants := slices.Clone(sibs)
	keep := func(cl []claim) bool { return reportsTMDB(sibs, cl) }
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
	claims := c.sideClaims(claimants, animelists.TMDB)
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

func (c *seriesCheck) sideClaims(ids []int, sd animelists.Side) map[target][]claim {
	out := map[target][]claim{}
	for _, id := range ids {
		now := c.claims(id, c.nodes[id], sd)
		put := c.introduced(id, now, sd)
		if sd == animelists.TVDB {
			put = c.addLinked(id, now, put)
		}
		for t, ls := range now {
			out[t] = append(out[t], claim{label: ls[0], node: id, introduced: put[t]})
		}
	}
	return out
}

// introduced marks the targets where the overlay put a changed node's
// claim: every target of a created node or one moved to another TVDB
// series, else each where upstream placed a different episode or none.
func (c *seriesCheck) introduced(id int, now map[target][]string, sd animelists.Side) map[target]bool {
	if !c.changed[id] {
		return nil
	}
	var was map[target][]string
	if up := c.upstream[id]; up != nil && up.Attr("tvdbid") == c.nodes[id].Attr("tvdbid") {
		was = c.claims(id, up, sd)
	}
	out := map[target]bool{}
	for t, ls := range now {
		for _, l := range ls {
			if !slices.ContainsFunc(was[t], func(w string) bool { return sameEpisode(w, l) }) {
				out[t] = true
			}
		}
	}
	return out
}

// addLinked claims each compiled link claim of node id at the target
// Compile published, introduced. It must not reuse this check's own
// placement: c.absolute flattens every capture on the series, so an
// absolute episode can land elsewhere than its parent's capture put it. put is
// never nil: Collisions marks every claim's parent changed, so introduced
// returns a map for it.
func (c *seriesCheck) addLinked(id int, now map[target][]string, put map[target]bool) map[target]bool {
	for _, l := range c.linked[id] {
		t, label := target{Season: l.Target[0], Episode: l.Target[1]}, linkLabel(id, l)
		if !slices.ContainsFunc(now[t], func(w string) bool { return sameEpisode(w, label) }) {
			now[t] = append(now[t], label)
		}
		put[t] = true
	}
	return put
}

// linkClaims adds node id's compiled link claims to out, each once.
func (c *seriesCheck) linkClaims(id int, out map[[2]int][]string) {
	for _, l := range c.linked[id] {
		t, label := [2]int{l.Target[0], l.Target[1]}, linkLabel(id, l)
		if !slices.Contains(out[t], label) {
			out[t] = append(out[t], label)
		}
	}
}

func linkLabel(id int, l overlay.Claim) string {
	if l.AniDBSeason != 0 {
		return fmt.Sprintf("%d ep%d", id, l.Number)
	}
	return fmt.Sprintf("%d S%d", id, l.Number)
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
func reportsTMDB(sibs []int, cl []claim) bool {
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
	out map[target][]string
	tv  int
}

func (cl claimer) add(season, ep int, label string) {
	t := target{TV: cl.tv, Season: season, Episode: ep}
	if !slices.Contains(cl.out[t], label) {
		cl.out[t] = append(cl.out[t], label)
	}
}

func (c *seriesCheck) claims(id int, n *animelists.Node, sd animelists.Side) map[target][]string {
	cl := claimer{out: map[target][]string{}}
	if sd == animelists.TMDB {
		if cl.tv = positiveAttr(n, "tmdbtv"); cl.tv == 0 {
			return nil
		}
	}
	for k := 1; k <= c.facts.Count(id); k++ {
		c.place(cl, n.Place(sd, 1, k), fmt.Sprintf("%d ep%d", id, k))
	}
	for _, k := range c.specialNumbers(id, n.SideRows(sd)) {
		p := n.Place(sd, 0, k)
		label := fmt.Sprintf("%d S%d", id, k)
		if p.Default {
			label += byDefault
		}
		c.place(cl, p, label)
	}
	return cl.out
}

// place claims a placement's targets; an absolute number resolves through
// the captured layouts, and claims nothing where they lack it.
func (c *seriesCheck) place(cl claimer, p animelists.Placement, label string) {
	for _, t := range p.Targets {
		cl.add(t[0], t[1], label)
	}
	if e, ok := c.absolute[p.Absolute]; ok {
		cl.add(e.Season, e.Number, label)
	}
}

// specialNumbers are the specials of node id a claim can come from: the
// ones its rows name, then the AniDB specials no row names.
func (c *seriesCheck) specialNumbers(id int, rows []animelists.SideRow) []int {
	covered := coveredSpecials(rows)
	var out []int
	for _, r := range rows {
		if r.AniDBSeason == 0 {
			out = append(out, r.Sources()...)
		}
	}
	numbers, _ := c.specials(id)
	for _, k := range numbers {
		if !covered[k] {
			out = append(out, k)
		}
	}
	return out
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
func (c *seriesCheck) unresolved(claimants []int, claims map[target][]claim, tv int) []Unresolved {
	if !c.touched {
		return nil
	}
	sd := animelists.TVDB
	if tv != 0 {
		sd = animelists.TMDB
	}
	var out []Unresolved
	for _, id := range claimants {
		if _, known := c.specials(id); known {
			continue
		}
		against := func(x claim) bool { return x.node != id && (c.changed[id] || x.introduced) }
		if hit := seasonZeroClaims(coveredSpecials(c.nodes[id].SideRows(sd)), claims, against); len(hit) > 0 {
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

func seasonZeroClaims(covered map[int]bool, tvdb map[target][]claim, against func(claim) bool) []int {
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
