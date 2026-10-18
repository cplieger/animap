// Package leftovers pairs, on one provider show, the AniDB specials and
// other episodes a node puts on no episode of the show with the show's
// season 0 episodes no AniDB episode claims. AniDB's episode list is the
// reference for what exists and the show for where it lands, so one side
// of a pair may be the other: a lead for a person, never a verdict.
package leftovers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/join"
	"github.com/cplieger/animap/mapread"
)

// Episode is one AniDB episode that lands on no episode of the show: its
// kind (anidb.Special, anidb.Other or anidb.Promotion), its number in that
// kind, and its Anime-Lists season 0 number, 0 when none addresses it.
type Episode struct {
	Kind       string `json:"kind"`
	Number     int    `json:"number"`
	SeasonZero int    `json:"season_zero,omitempty"`
}

// Finding is one node's leftovers on one show: the specials and other
// episodes that land nowhere on it; the promotions that land nowhere too,
// listed because a promotion can be a prologue; and the show's season 0
// episode numbers no claim names that could be an unplaced episode, as
// pairable decides. Inputs digests the show and those three sets; Slug is
// the TVDB series' URL slug, if any.
type Finding struct {
	Inputs     string          `json:"inputs"`
	Slug       string          `json:"slug,omitempty"`
	Unplaced   []Episode       `json:"unplaced"`
	Promotions []Episode       `json:"promotions,omitempty"`
	Unclaimed  []int           `json:"unclaimed"`
	Series     int             `json:"series"`
	Side       animelists.Side `json:"side"`
}

// Find returns n's leftovers on series, its show on side, with AniDB
// episode list a and the show's layout lay; nil when no special or other
// episode lands nowhere, or no season 0 episode of the show is unclaimed
// and could be one of them, as pairable decides. An episode lands where
// the published map puts it (landing.lands) on an episode lay has. claims
// returns the collision guard's claims on the show
// (guard.ClaimIndex.Claims); Find calls it at most once.
func Find(n *animelists.Node, side animelists.Side, series int, a *anidb.Anime, lay *datecheck.Layout, claims func() map[[2]int][]string) *Finding {
	l := &landing{n: n, side: side, read: claims, laid: map[[2]int]bool{}}
	for i := range lay.Episodes {
		l.laid[[2]int{lay.Episodes[i].Season, lay.Episodes[i].Number}] = true
	}
	f := &Finding{Side: side, Series: series, Slug: lay.Slug}
	cands := candidates(a)
	for _, e := range cands {
		if e.Kind != anidb.Promotion && !l.lands(e) {
			f.Unplaced = append(f.Unplaced, e)
		}
	}
	if len(f.Unplaced) == 0 {
		return nil
	}
	for _, e := range cands {
		if e.Kind == anidb.Promotion && !l.lands(e) {
			f.Promotions = append(f.Promotions, e)
		}
	}
	f.Unclaimed = unclaimed(lay, l.claims(), pairable(a, f.Unplaced))
	if len(f.Unclaimed) == 0 {
		return nil
	}
	f.Inputs = f.digest()
	return f
}

// landing places one node's episodes on one show and reads the guard's
// claims at most once, on first need (a default placement on TVDB, or the
// unclaimed-slot scan).
type landing struct {
	n       *animelists.Node
	laid    map[[2]int]bool
	read    func() map[[2]int][]string
	claimed map[[2]int][]string
	side    animelists.Side
	got     bool
}

func (l *landing) claims() map[[2]int][]string {
	if !l.got {
		l.claimed, l.got = l.read(), true
	}
	return l.claimed
}

// lands reports whether e lands on an episode the show has, as the
// published map places it: n.Place, with join.TakesDefault owning which
// kinds take its default, and on TVDB a default episode an applied link
// claims where the link puts it.
func (l *landing) lands(e Episode) bool {
	if e.SeasonZero == 0 {
		return false
	}
	p := l.n.Place(l.side, 0, e.SeasonZero)
	if !p.Default {
		return landsOn(p.Targets, l.laid)
	}
	if l.side == animelists.TVDB {
		if on, linked := l.linkedOn(e.SeasonZero); linked {
			return on
		}
	}
	return join.TakesDefault(mapread.Kind(e.Kind)) && landsOn(p.Targets, l.laid)
}

// linkedOn reports whether an applied link claims Anime-Lists season 0
// number z of the node, and whether on an episode the show has. On a
// default episode only a link's claim carries the bare "<aid> S<z>" label
// (guard.ClaimIndex.Claims).
func (l *landing) linkedOn(z int) (on, linked bool) {
	label := fmt.Sprintf("%d S%d", l.n.AniDBID, z)
	for t, labels := range l.claims() {
		if slices.Contains(labels, label) {
			linked, on = true, on || l.laid[t]
		}
	}
	return on, linked
}

// unclaimed are lay's season 0 numbers no claim names that pair says
// could be an unplaced episode.
func unclaimed(lay *datecheck.Layout, claims map[[2]int][]string, pair func(date string) bool) []int {
	var out []int
	for i := range lay.Episodes {
		if e := lay.Episodes[i]; e.Season == 0 && e.Number > 0 && len(claims[[2]int{0, e.Number}]) == 0 && pair(e.AirDate) {
			out = append(out, e.Number)
		}
	}
	return out
}

// candidates are a's specials, promotions and other episodes. Credits and
// parodies are left out, and promotions never raise alone, because
// openings, endings, trailers and commercials are nearly never on TVDB and
// would pair with nearly every unclaimed episode.
func candidates(a *anidb.Anime) []Episode {
	out := make([]Episode, 0, len(a.Specials))
	for _, sp := range a.Specials {
		out = append(out, Episode{Kind: anidb.Special, Number: sp.Number, SeasonZero: sp.Number})
	}
	for _, x := range a.Extras {
		if x.Kind == anidb.Other || x.Kind == anidb.Promotion {
			out = append(out, Episode{Kind: x.Kind, Number: x.Number, SeasonZero: x.SeasonZero})
		}
	}
	return out
}

// spanSlack widens an anime's span by a month either side, for a
// provider that dates an airing a little before or after AniDB.
const spanSlack = 31 * 24 * time.Hour

// pairable reports whether an unclaimed season 0 episode aired on date
// could be one of unplaced. Any can be an undated one, and an other
// episode, which the mirror never dates, can be any. A dated special can
// be only one datecheck.SameAiring matches; an undated special only one
// within the span of a's dated regular episodes and specials, widened by
// spanSlack, or any when AniDB dates nothing of a. Most unclaimed
// episodes outside those are another anime's on the same show (a sequel's
// special, a later film) or the provider's own extras.
func pairable(a *anidb.Anime, unplaced []Episode) func(date string) bool {
	first, last := span(a)
	dated := map[int]string{}
	for _, sp := range a.Specials {
		if _, err := time.Parse(time.DateOnly, sp.AirDate); err == nil {
			dated[sp.Number] = sp.AirDate
		}
	}
	return func(date string) bool {
		d, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return true
		}
		inSpan := first.IsZero() || !d.Before(first.Add(-spanSlack)) && !d.After(last.Add(spanSlack))
		return slices.ContainsFunc(unplaced, func(e Episode) bool {
			at, ok := dated[e.Number]
			switch {
			case e.Kind != anidb.Special:
				return true
			case ok:
				return datecheck.SameAiring(at, date)
			}
			return inSpan
		})
	}
}

// span is the first and last date of a's dated regular episodes and
// specials, zero when AniDB dates none.
func span(a *anidb.Anime) (first, last time.Time) {
	for _, e := range slices.Concat(a.Regular, a.Specials) {
		d, err := time.Parse(time.DateOnly, e.AirDate)
		if err != nil {
			continue
		}
		if first.IsZero() || d.Before(first) {
			first = d
		}
		if d.After(last) {
			last = d
		}
	}
	return first, last
}

func landsOn(targets [][2]int, laid map[[2]int]bool) bool {
	return slices.ContainsFunc(targets, func(t [2]int) bool { return laid[t] })
}

// Provider names side in a finding key: "tvdb" or "tmdb".
func Provider(side animelists.Side) string {
	if side == animelists.TMDB {
		return "tmdb"
	}
	return "tvdb"
}

// digest hashes the show and the three sets, so an acceptance lapses when
// any of them changes and never on a slug or a title.
func (f *Finding) digest() string {
	lines := make([]string, 0, 1+len(f.Unplaced)+len(f.Promotions)+len(f.Unclaimed))
	lines = append(lines, fmt.Sprintf("show %s %d", Provider(f.Side), f.Series))
	for _, e := range f.Unplaced {
		lines = append(lines, fmt.Sprintf("unplaced %s %d S%d", e.Kind, e.Number, e.SeasonZero))
	}
	for _, e := range f.Promotions {
		lines = append(lines, fmt.Sprintf("unplaced %s %d S%d", e.Kind, e.Number, e.SeasonZero))
	}
	for _, k := range f.Unclaimed {
		lines = append(lines, "unclaimed "+strconv.Itoa(k))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// Key names f by its show: "tvdb:<series>" or "tmdb:<show>".
func (f *Finding) Key() string { return Provider(f.Side) + ":" + strconv.Itoa(f.Series) }

var findingKeyRE = regexp.MustCompile(`^(tvdb|tmdb):[1-9]\d*$`)

// ValidFindingKey reports whether k has the shape Key writes.
func ValidFindingKey(k string) bool { return findingKeyRE.MatchString(k) }
