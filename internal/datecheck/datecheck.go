// Package datecheck compares, for every AniDB episode an Anime-Lists node
// maps to TVDB, AniDB's air date with the air date of the TVDB episode it
// lands on in TVDB's official order. A node the dates contradict is a lead
// for a human to prove, never a verdict.
package datecheck

import (
	"slices"
	"strconv"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

// Kind is what is wrong with one mapped episode, or with the whole node.
type Kind string

const (
	// DeadSeries is a TVDB series id SkyHook answers 404 for.
	DeadSeries Kind = "dead-series"
	// MissingTarget is a TVDB season and episode the series does not have.
	MissingTarget Kind = "missing-target"
	// DateMismatch is a target that airs over a day away from the AniDB
	// episode, which mismatch judges a wrong target.
	DateMismatch Kind = "date-mismatch"
	// OutOfOrder is a date mismatch no carrier decides that takes part in a
	// revisit, as outOfOrder finds them.
	OutOfOrder Kind = "out-of-order"
)

// settle is how long after its AniDB air date an episode is checked: TVDB
// can lag a new episode by days.
const settle = 7 * 24 * time.Hour

const (
	// maxCarriers bounds the episodes listed as airing on an AniDB date.
	maxCarriers = 3
	// driftDays is the widest gap scheduleDrift reads as two broadcast
	// schedules rather than a wrong episode.
	driftDays = 31
)

// Episode is one AniDB episode. AirDate is "" when AniDB has none.
type Episode struct {
	AirDate string `json:"air_date,omitempty"`
	Number  int    `json:"number"`
	Special bool   `json:"special,omitempty"`
}

// Target is the TVDB episode a node maps an AniDB episode to. Absolute is
// set when the node numbers the series absolutely; Season and Number are
// then the episode that absolute number resolved to, 0 when none did.
// AirDate is "" when the episode is missing or TVDB has no date.
type Target struct {
	AirDate  string `json:"air_date,omitempty"`
	Season   int    `json:"season"`
	Number   int    `json:"number"`
	Absolute int    `json:"absolute,omitempty"`
}

// Finding is one mapped episode that fails. Carriers are the likeliest
// right targets, as carriers picks them. Default says no row names the
// episode, so the node's default placed it.
type Finding struct {
	Kind     Kind              `json:"kind"`
	Carriers []skyhook.Episode `json:"carriers,omitempty"`
	Episode  Episode           `json:"episode"`
	Target   Target            `json:"target"`
	Default  bool              `json:"default,omitempty"`
}

// Lead is a node that fails the check: its series is dead, or Findings
// lists every mapped episode that fails, regular episodes first.
type Lead struct {
	Findings []Finding `json:"findings,omitempty"`
	AniDB    int       `json:"anidb"`
	Series   int       `json:"series"`
	Dead     bool      `json:"dead,omitempty"`
}

// Series is the TVDB series id n maps to, and false when it has none.
func Series(n *animelists.Node) (int, bool) {
	id, err := strconv.Atoi(n.Attr("tvdbid"))
	return id, err == nil && id > 0
}

// Prefix starts every date-check issue key, and no other key.
const Prefix = "lead:date:"

// Key is the issue identity of the lead for AniDB id aid; it never changes.
func Key(aid int) string { return Prefix + strconv.Itoa(aid) }

// Check returns the lead for node n, with AniDB episode list a and the
// layout of n's series, nil when the node passes. Episodes that have not
// settled by now are skipped, and a date is compared only where both sides
// have one.
func Check(n *animelists.Node, a anidb.Anime, lay *Layout, now time.Time) *Lead {
	series, ok := Series(n)
	if !ok {
		return nil
	}
	if lay.Absent {
		return &Lead{AniDB: n.AniDBID, Series: series, Dead: true}
	}
	s := newShow(lay.Episodes, sharedDates(a))
	s.oneCalendar = s.keepsAniDBCalendar(a.Regular)
	var out, calendar []Finding
	for i, d := range a.Regular {
		bad, cal := s.check(n, Episode{Number: i + 1, AirDate: d}, a.Finished, now)
		out = append(out, bad...)
		calendar = append(calendar, cal...)
	}
	out = append(out, outOfOrder(calendar)...)
	for _, sp := range a.Specials {
		bad, _ := s.check(n, Episode{Number: sp.Number, AirDate: sp.AirDate, Special: true}, a.Finished, now)
		out = append(out, bad...)
	}
	if len(out) == 0 {
		return nil
	}
	return &Lead{AniDB: n.AniDBID, Series: series, Findings: out}
}

type show struct {
	byEpisode   map[[2]int]skyhook.Episode
	byAbsolute  map[int]skyhook.Episode
	shared      map[sharedKey]bool
	episodes    []skyhook.Episode
	oneCalendar bool
}

func newShow(layout []skyhook.Episode, shared map[sharedKey]bool) *show {
	s := &show{byEpisode: map[[2]int]skyhook.Episode{}, byAbsolute: map[int]skyhook.Episode{}, shared: shared, episodes: layout}
	for _, e := range layout {
		s.byEpisode[[2]int{e.Season, e.Number}] = e
		if e.Absolute > 0 && e.Season > 0 {
			s.byAbsolute[e.Absolute] = e
		}
	}
	return s
}

type verdict int

const (
	passes verdict = iota
	fails
	// otherCalendar is a date mismatch no carrier decides, which only
	// outOfOrder can still judge.
	otherCalendar
)

// keepsAniDBCalendar: some regular AniDB date, precise to the day and
// shared by no other episode, has a TVDB episode within a day, so the
// series keeps AniDB's calendar.
func (s *show) keepsAniDBCalendar(regular []string) bool {
	return slices.ContainsFunc(regular, func(d string) bool {
		return dayPrecise(d) && len(s.carriers(Episode{AirDate: d})) > 0
	})
}

// check returns ep's failing findings, and its findings on another
// calendar for outOfOrder.
func (s *show) check(n *animelists.Node, ep Episode, finished bool, now time.Time) (bad, calendar []Finding) {
	if !settled(ep.AirDate, finished, now) {
		return nil, nil
	}
	anidbSeason := 1
	if ep.Special {
		anidbSeason = 0
	}
	p := n.Place(animelists.TVDB, anidbSeason, ep.Number)
	ts := s.targets(p)
	sib := siblings(ep, ts)
	for _, t := range ts {
		switch f, v := s.judge(ep, t, p.Default, sib); v {
		case fails:
			bad = append(bad, f)
		case otherCalendar:
			calendar = append(calendar, f)
		}
	}
	return bad, calendar
}

// siblings are ep's targets that air on its date. The node itself puts the
// episode there, so they carry it even on a date sharedDates leaves
// without carriers.
func siblings(ep Episode, ts []Target) []skyhook.Episode {
	var out []skyhook.Episode
	for _, t := range ts {
		if ep.Special && t.Season != 0 {
			continue
		}
		if gap, ok := daysApart(ep.AirDate, t.AirDate); ok && gap <= 1 {
			out = append(out, skyhook.Episode{Season: t.Season, Number: t.Number, AirDate: t.AirDate})
		}
	}
	return out
}

// targets resolves a placement against the layout; an absolute number the
// series lacks stays a Target with Season and Number 0.
func (s *show) targets(p animelists.Placement) []Target {
	if p.Absolute > 0 {
		e := s.byAbsolute[p.Absolute]
		return []Target{{Absolute: p.Absolute, Season: e.Season, Number: e.Number, AirDate: e.AirDate}}
	}
	out := make([]Target, 0, len(p.Targets))
	for _, t := range p.Targets {
		out = append(out, Target{Season: t[0], Number: t[1], AirDate: s.byEpisode[t].AirDate})
	}
	return out
}

func (s *show) judge(ep Episode, t Target, byDefault bool, sib []skyhook.Episode) (Finding, verdict) {
	_, exists := s.byEpisode[[2]int{t.Season, t.Number}]
	if t.Absolute > 0 {
		exists = t.Number > 0
	}
	c := s.carriers(ep)
	if len(c) == 0 {
		c = sib
	}
	f := Finding{Kind: MissingTarget, Episode: ep, Target: t, Carriers: c, Default: byDefault}
	switch {
	case !exists && ep.Special && byDefault && len(c) == 0:
		// A special no row names defaults to season 0 episode k; TVDB not
		// having that special is no mapping error unless it files the
		// special elsewhere.
		return Finding{}, passes
	case !exists:
		return f, fails
	case ep.AirDate == "" || t.AirDate == "" || sameAiring(ep.AirDate, t.AirDate):
		return Finding{}, passes
	}
	f.Kind = DateMismatch
	return f, s.mismatch(ep, t, c, byDefault)
}

// mismatch judges a target whose date differs from ep's, c being ep's
// carriers.
func (s *show) mismatch(ep Episode, t Target, c []skyhook.Episode, byDefault bool) verdict {
	switch {
	case len(c) > 0 && !scheduleDrift(ep, t, c, byDefault):
		return fails
	case len(c) > 0 || s.shared[sharedKey{date: ep.AirDate, special: ep.Special}]:
		return passes
	case ep.Special && s.oneCalendar:
		// On a series that keeps AniDB's calendar, a regular episode no
		// episode meets is TVDB dating that one episode otherwise (a
		// pre-air, a stream, a typo); a special there is a wrong target.
		return fails
	}
	return otherCalendar
}

// outOfOrder are the findings of fs, all otherCalendar and in AniDB order,
// that take part in a revisit: two episodes land on one TVDB
// episode, and an episode numbered between them lands after it. TVDB
// merging a run of episodes into one is no revisit, and neither is a
// node that only reorders episodes, as a series aired out of order needs.
func outOfOrder(fs []Finding) []Finding {
	flagged := make([]bool, len(fs))
	last := map[[2]int]int{}
	for j := range fs {
		if !ordered(&fs[j]) {
			continue
		}
		t := [2]int{fs[j].Target.Season, fs[j].Target.Number}
		i, seen := last[t]
		last[t] = j
		if !seen {
			continue
		}
		for k := i + 1; k < j; k++ {
			between := fs[i].Episode.Number < fs[k].Episode.Number && fs[k].Episode.Number < fs[j].Episode.Number
			if between && ordered(&fs[k]) && after(fs[k].Target, fs[j].Target) {
				flagged[i], flagged[k], flagged[j] = true, true, true
			}
		}
	}
	var out []Finding
	for i, f := range fs {
		if flagged[i] {
			f.Kind = OutOfOrder
			out = append(out, f)
		}
	}
	return out
}

// ordered: f can be placed in airing order. Season 0 has no order, and a
// date on the 1st may stand for its whole month.
func ordered(f *Finding) bool {
	return f.Target.Season > 0 && f.Target.Number > 0 && dayPrecise(f.Episode.AirDate)
}

func after(x, y Target) bool {
	return x.Season > y.Season || x.Season == y.Season && x.Number > y.Number
}

func dayPrecise(date string) bool {
	d, err := time.Parse(time.DateOnly, date)
	return err == nil && d.Day() != 1
}

// scheduleDrift is a default-placed episode in a regular TVDB season, at
// most driftDays from its target, whose AniDB date another episode of that
// season carries. Two sources that number the season alike and keep
// different broadcast schedules (a pre-emption, a later station, a
// streaming batch) produce exactly that, so the dates decide nothing.
func scheduleDrift(ep Episode, t Target, carriers []skyhook.Episode, byDefault bool) bool {
	if !byDefault || t.Season < 1 {
		return false
	}
	if gap, ok := daysApart(ep.AirDate, t.AirDate); !ok || gap > driftDays {
		return false
	}
	return slices.ContainsFunc(carriers, func(c skyhook.Episode) bool { return c.Season == t.Season })
}

// carriers are the episodes that air within a day of ep's AniDB date, the
// candidates for its right target. A date of month or year precision, or
// one another episode of the same kind shares (a batch release), names no
// episode, and a special's candidates are TVDB specials only, since a
// regular episode airing the day of a special is no evidence.
func (s *show) carriers(ep Episode) []skyhook.Episode {
	if s.shared[sharedKey{date: ep.AirDate, special: ep.Special}] {
		return nil
	}
	var out []skyhook.Episode
	for _, e := range s.episodes {
		if ep.Special && e.Season != 0 {
			continue
		}
		if gap, ok := daysApart(ep.AirDate, e.AirDate); ok && gap <= 1 {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(x, y skyhook.Episode) int {
		if x.Season != y.Season {
			return x.Season - y.Season
		}
		return x.Number - y.Number
	})
	return out[:min(len(out), maxCarriers)]
}

// sharedDates are the dates two or more regular episodes, or two or more
// specials, of the anime carry, keyed by kind and date.
func sharedDates(a anidb.Anime) map[sharedKey]bool {
	seen := map[sharedKey]int{}
	for _, d := range a.Regular {
		seen[sharedKey{date: d}]++
	}
	for _, sp := range a.Specials {
		seen[sharedKey{date: sp.AirDate, special: true}]++
	}
	out := map[sharedKey]bool{}
	for k, n := range seen {
		if k.date != "" && n > 1 {
			out[k] = true
		}
	}
	return out
}

type sharedKey struct {
	date    string
	special bool
}

// settled: an episode is checked once its AniDB date is settle in the past,
// or, with no date, once AniDB records the anime's end.
func settled(date string, finished bool, now time.Time) bool {
	if date == "" {
		return finished
	}
	d, err := time.Parse(time.DateOnly, date)
	return err == nil && !d.After(now.Add(-settle))
}

// sameAiring reports whether AniDB date a and TVDB date b can be one
// airing: at most a day apart, for the time-zone shift; in the same month
// when either is the 1st, a month-precision date; in the same year when
// either is 1 January, a year-precision date. A date that does not parse
// matches nothing.
func sameAiring(a, b string) bool {
	gap, ok := daysApart(a, b)
	switch {
	case !ok:
		return false
	case gap <= 1:
		return true
	}
	x, _ := time.Parse(time.DateOnly, a)
	y, _ := time.Parse(time.DateOnly, b)
	switch {
	case yearPrecision(x) || yearPrecision(y):
		return x.Year() == y.Year()
	case x.Day() == 1 || y.Day() == 1:
		return x.Year() == y.Year() && x.Month() == y.Month()
	}
	return false
}

func yearPrecision(t time.Time) bool { return t.Month() == time.January && t.Day() == 1 }

// daysApart is how many whole days separate two YYYY-MM-DD dates, false
// when either does not parse.
func daysApart(a, b string) (int, bool) {
	x, errA := time.Parse(time.DateOnly, a)
	y, errB := time.Parse(time.DateOnly, b)
	if errA != nil || errB != nil {
		return 0, false
	}
	d := int(x.Sub(y).Hours() / 24)
	return max(d, -d), true
}
