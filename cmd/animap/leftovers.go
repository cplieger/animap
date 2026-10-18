package main

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/join"
	"github.com/cplieger/animap/internal/leftovers"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/mapread"
)

// addLeftovers lists every node whose leftovers raise, whose shows the run
// could not judge while both date sources could, or that an acceptance
// part rules on.
func (r *reviewRun) addLeftovers(subjects map[string]*subject) {
	h := r.health[review.Leftovers]
	for _, name := range []string{review.DateTVDB, review.DateTMDB} {
		if src := r.health[name]; !src.Evaluated && h.Evaluated {
			h.Evaluated, h.UnevaluatedReason = false, providerName(sideOf(name))+" is not checked: "+src.UnevaluatedReason
		}
	}
	if r.facts == nil {
		return
	}
	paths := make(map[int]string, len(r.entries))
	for i := range r.entries {
		paths[r.entries[i].AniDBID] = r.entries[i].Path()
	}
	for _, aid := range slices.Sorted(maps.Keys(r.patched)) {
		key := review.AniDBKey(aid)
		l := r.leftovers(aid)
		if l == nil || !l.raised() && (l.Evaluated || !h.Evaluated) && r.records[key].Parts.Leftovers == nil {
			continue
		}
		if l.raised() {
			l.Node, l.Overlay = r.patched[aid].Canonical(), paths[aid]
		}
		r.subjectOf(subjects, key).Sections.Leftovers = l
	}
}

// leftovers judges node aid on each show it maps to, nil when it maps to
// none or AniDB lists no episode of it.
func (r *reviewRun) leftovers(aid int) *leftoversSection {
	n := r.patched[aid]
	a, listed := r.facts.Anime(aid)
	if n == nil || !listed {
		return nil
	}
	l := &leftoversSection{}
	var unread []string
	for _, name := range []string{review.DateTVDB, review.DateTMDB} {
		if reason := r.judgeShow(l, n, &a, name); reason != "" {
			unread = append(unread, reason)
		}
	}
	if !l.Applicable {
		return nil
	}
	l.Evaluated = l.raised() || len(unread) == 0
	l.Reason = strings.Join(unread, "; ")
	if l.raised() {
		l.Digests = map[string]string{}
		for i := range l.Findings {
			l.Digests[l.Findings[i].Key()] = l.Findings[i].Inputs
		}
	}
	return l
}

// judgeShow adds n's finding on the show of date section name to l, and
// returns why the run could not judge it, "" when it could or n maps to
// no show there.
func (r *reviewRun) judgeShow(l *leftoversSection, n *animelists.Node, a *anidb.Anime, name string) string {
	side := sideOf(name)
	series, ok := datecheck.Series(n, side)
	if !ok {
		return ""
	}
	l.Applicable = true
	if reason := r.layoutUnread(name, series); reason != "" {
		if l.Unchecked == nil {
			l.Unchecked = map[string]string{}
		}
		l.Unchecked[leftovers.Provider(side)+":"+strconv.Itoa(series)] = reason
		return fmt.Sprintf("%s %d: %s", showName(side), series, reason)
	}
	lay := r.layoutsOf(side).Series[series]
	if f := leftovers.Find(n, side, series, a, &lay, func() map[[2]int][]string { return r.claimsOn(side, series) }); f != nil {
		l.Findings = append(l.Findings, *f)
	}
	return ""
}

// layoutUnread is why the run cannot read series' layout for date section
// name: its source did not judge, the run did not refresh it, or the
// store has none; "" when it can.
func (r *reviewRun) layoutUnread(name string, series int) string {
	if h := r.health[name]; !h.Evaluated {
		return h.UnevaluatedReason
	}
	stale := r.tvdbStale
	if name == review.DateTMDB {
		stale = r.tmdbStale
	}
	_, stored := r.layoutsOf(sideOf(name)).Series[series]
	return unreadReason(stale[series], stored, "no layout fetched yet")
}

func (r *reviewRun) layoutsOf(side animelists.Side) *datecheck.Store {
	if side == animelists.TMDB {
		return r.tmdb
	}
	return r.tvdb
}

// fillLeftovers is the leftovers section of a listed subject the run did
// not list it for: node aid judged, or, with the mirror unread, not
// checked wherever the node maps to a show.
func (r *reviewRun) fillLeftovers(aid int, h *sourceHealth) *leftoversSection {
	if r.facts != nil {
		return r.leftovers(aid)
	}
	if !mapsToShow(r.patched[aid]) {
		return nil
	}
	return &leftoversSection{Applicable: true, Reason: h.UnevaluatedReason}
}

// mapsToShow reports whether n, nil when the list lacks the node, maps to
// a TVDB series or a TMDB show.
func mapsToShow(n *animelists.Node) bool {
	if n == nil {
		return false
	}
	_, tv := datecheck.Series(n, animelists.TVDB)
	_, tm := datecheck.Series(n, animelists.TMDB)
	return tv || tm
}

func providerName(side animelists.Side) string {
	if side == animelists.TMDB {
		return tmdbName
	}
	return "TVDB"
}

// showName is how an issue names a show on side: "TVDB series" or "TMDB
// show".
func showName(side animelists.Side) string {
	if side == animelists.TMDB {
		return "TMDB show"
	}
	return "TVDB series"
}

// leftoversText is a raised leftovers section. It carries AniDB episode
// kinds and numbers and provider season 0 numbers, never a title.
func leftoversText(s *subject, l *leftoversSection) string {
	var b strings.Builder
	sub, _ := review.ParseKey(s.Key)
	b.WriteString("### Season 0 leftovers\n\n")
	fmt.Fprintf(&b, "AniDB %d has specials or other episodes that land on no episode of a show it maps to, and that show has season 0 episodes no AniDB episode claims. One may be the other. This is a lead, not a verdict.\n\n", sub.ID)
	if m := l.Acceptance; m != nil && !m.Matches && len(m.Causes) > 0 {
		fmt.Fprintf(&b, "`reviewed/%s` `%s` accepted these leftovers, and they are raised again because %s.\n\n", m.File, m.Part, causesText(m.Causes))
	}
	for i := range l.Findings {
		f := &l.Findings[i]
		link := seasonPage(f.Side, f.Series, f.Slug, 0)
		fmt.Fprintf(&b, "On %s %d:\n\n", showName(f.Side), f.Series)
		fmt.Fprintf(&b, "- AniDB episodes that land nowhere: %s\n", episodesText(f.Unplaced))
		if len(f.Promotions) > 0 {
			fmt.Fprintf(&b, "- AniDB promotions that land nowhere: %s\n", episodesText(f.Promotions))
		}
		fmt.Fprintf(&b, "- Season 0 episodes no AniDB episode claims: %s\n", slotsText(f.Unclaimed))
		fmt.Fprintf(&b, "- %s: %s\n\n", providerName(f.Side), link)
	}
	for _, k := range slices.Sorted(maps.Keys(l.Unchecked)) {
		provider, id, _ := strings.Cut(k, ":")
		side := animelists.TVDB
		if provider == leftovers.Provider(animelists.TMDB) {
			side = animelists.TMDB
		}
		fmt.Fprintf(&b, "%s %s: not checked this run (%s).\n\n", showName(side), id, l.Unchecked[k])
	}
	page := seasonPage(l.Findings[0].Side, l.Findings[0].Series, l.Findings[0].Slug, 0)
	accept := func(verdict string) {
		fmt.Fprintf(&b, "```sh\ngo run ./cmd/animap accept -from review.json -key %s -part %s %s -evidence https://anidb.net/anime/%d -evidence %s\n```\n\n", s.Key, review.Leftovers, verdict, sub.ID, page)
	}
	b.WriteString("If an episode that lands nowhere is one of the unclaimed episodes, prove it by title on AniDB and the provider's season 0 page. Map one with an Anime-Lists number with a row on the node's overlay entry (docs/overlay.md), and send the same row to Anime-Lists in a pull request.\n\n")
	if linkable(l.Findings) {
		b.WriteString("On TVDB an overlay link can instead map an other episode or promotion no row names, when an AniList entry is that episode and the unclaimed episode carries its Anime-Lists number (docs/review.md, \"Season 0 leftovers\").\n\n")
	}
	if unaddressed(l.Findings) {
		b.WriteString("No row can map one with no Anime-Lists number: accept the pair as inexpressible from the run's `review.json`, naming the format change it needs (docs/review.md, \"Accepting a finding\"), and commit the file it writes:\n\n")
		accept(`-verdict inexpressible -requires "<the format change>" -reason "<which episode is which, by title>"`)
	}
	b.WriteString("If none is, accept the pair as fine from the run's `review.json` (docs/review.md) and commit the file it writes:\n\n")
	accept(`-verdict fine -reason "<why no episode that lands nowhere is an unclaimed one, by title>"`)
	return b.String()
}

// unaddressed reports whether any finding lists an episode no Anime-Lists
// number addresses.
func unaddressed(fs []leftovers.Finding) bool {
	return slices.ContainsFunc(fs, func(f leftovers.Finding) bool {
		return slices.ContainsFunc(slices.Concat(f.Unplaced, f.Promotions), func(e leftovers.Episode) bool { return e.SeasonZero == 0 })
	})
}

// linkable reports whether any TVDB finding lists an episode whose kind
// join.TakesDefault gives no default and whose Anime-Lists number is one
// of the finding's unclaimed episodes, where a link lands it unless a row
// names it. A finding does not record rows, so the remedy text states that
// condition.
func linkable(fs []leftovers.Finding) bool {
	return slices.ContainsFunc(fs, func(f leftovers.Finding) bool {
		return f.Side == animelists.TVDB && slices.ContainsFunc(slices.Concat(f.Unplaced, f.Promotions), func(e leftovers.Episode) bool {
			return slices.Contains(f.Unclaimed, e.SeasonZero) && !join.TakesDefault(mapread.Kind(e.Kind))
		})
	})
}

// episodesText lists AniDB episodes as "special 3 (S0 3), other 1 (S0 401)".
func episodesText(eps []leftovers.Episode) string {
	return joinCapped(eps, func(e leftovers.Episode) string {
		s0 := "no Anime-Lists number"
		if e.SeasonZero > 0 {
			s0 = "S0 " + strconv.Itoa(e.SeasonZero)
		}
		return fmt.Sprintf("%s %d (%s)", kindLabel(e.Kind), e.Number, s0)
	})
}

// kindLabel lets only AniDB's kind names into an issue, whatever wrote the
// review it came from.
func kindLabel(kind string) string {
	switch kind {
	case anidb.Special, anidb.Other, anidb.Promotion:
		return kind
	}
	return "episode"
}

func slotsText(slots []int) string {
	return joinCapped(slots, func(k int) string { return "S0E" + strconv.Itoa(k) })
}

// joinCapped joins the first maxListedLines of items as text writes each,
// then how many more there are.
func joinCapped[T any](items []T, text func(T) string) string {
	n := min(len(items), maxListedLines)
	parts := make([]string, 0, n+1)
	for _, x := range items[:n] {
		parts = append(parts, text(x))
	}
	if len(items) > n {
		parts = append(parts, fmt.Sprintf("%d more", len(items)-n))
	}
	return strings.Join(parts, ", ")
}

// leftoversRank ranks a section with a TVDB pair after the watch, and one
// whose pairs are all on TMDB with what only TMDB disagrees with.
func leftoversRank(l *leftoversSection) int {
	if slices.ContainsFunc(l.Findings, func(f leftovers.Finding) bool { return f.Side == animelists.TVDB }) {
		return rankLeftovers
	}
	return rankTMDBOnly
}
