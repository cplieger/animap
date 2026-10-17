// Package anidb answers what episodes AniDB lists for an anime: every
// episode's kind, number and AniDB episode id, and the air dates of the
// regular episodes and specials. It reads them from
// notseteve/AnimeAggregations, a twice-monthly mirror of AniDB's data, at a
// pinned commit, and Facts.Count is the one place a regular episode count
// is decided; Anime.SeasonZeroOf and Anime.SeasonZeroFor own Anime-Lists'
// season 0 numbers. Only numbers, ids, dates and a digest of the episode
// titles leave the package; the mirror's titles and descriptions are never
// kept.
package anidb

import (
	"cmp"
	"errors"
	"maps"
	"slices"
)

// errInvalid wraps every refusal of a mirror file, a snapshot file or a
// counts file.
var errInvalid = errors.New("anidb: invalid")

// Episode is one numbered AniDB episode. AirDate is YYYY-MM-DD, or "" when
// AniDB has no date; the Episode SeasonZero gives for an extra has none.
// Titles is the hex SHA-256 of the episode's titles, and ID its AniDB
// episode id.
type Episode struct {
	AirDate string `json:"air_date"`
	Titles  string `json:"titles"`
	Number  int    `json:"number"`
	ID      int    `json:"id"`
}

// Anime is AniDB's episode list for one anime. Regular and Specials are
// sorted by number, each number once and from 1, and AniDB can skip a
// number in either; Extras (credits, promotions, parodies and other
// episodes) are sorted by kind, then number. Finished means AniDB records
// an end date, which it can set before the last episode airs. Titles is
// the hex SHA-256 of the regular episodes' and specials' titles;
// LoadSnapshot refuses an anime without them, the ids or Extras.
type Anime struct {
	Titles   string    `json:"titles"`
	Regular  []Episode `json:"regular"`
	Specials []Episode `json:"specials"`
	Extras   []Extra   `json:"extras"`
	Finished bool      `json:"finished"`
}

// Extra is a credit, promotion, parody or other episode: Kind and its
// AniDB Number in that kind, its AniDB episode ID, and SeasonZero, the
// Anime-Lists season 0 number seasonZeroNumber gives it, or 0 when that
// number cannot address it (past its block's 100th or on a listed
// special's number). Titles is the hex SHA-256 of its titles.
type Extra struct {
	Kind       string `json:"kind"`
	Titles     string `json:"titles"`
	Number     int    `json:"number"`
	ID         int    `json:"id"`
	SeasonZero int    `json:"season_zero"`
}

// Listed is one episode AniDB lists: its number in its kind and its AniDB
// episode id.
type Listed struct{ Number, ID int }

// Episodes are the episodes AniDB lists in kind, Regular, Special or an
// extra's kind, ascending by number; nil for an unknown kind.
func (a *Anime) Episodes(kind string) []Listed {
	var out []Listed
	switch kind {
	case Regular:
		for _, e := range a.Regular {
			out = append(out, Listed{Number: e.Number, ID: e.ID})
		}
	case Special:
		for _, e := range a.Specials {
			out = append(out, Listed{Number: e.Number, ID: e.ID})
		}
	default:
		for _, x := range a.Extras {
			if x.Kind == kind {
				out = append(out, Listed{Number: x.Number, ID: x.ID})
			}
		}
	}
	return out
}

// RegularEpisode is the regular episode AniDB lists as number n, false when
// it lists none.
func (a *Anime) RegularEpisode(n int) (Episode, bool) {
	i, ok := slices.BinarySearchFunc(a.Regular, n, func(e Episode, n int) int { return cmp.Compare(e.Number, n) })
	if !ok {
		return Episode{}, false
	}
	return a.Regular[i], true
}

// SeasonZero is the episode Anime-Lists numbers k in season 0: the special
// k, else the extra whose SeasonZero is k, with Number k; extra reports the
// second.
func (a *Anime) SeasonZero(k int) (e Episode, extra, ok bool) {
	if i := slices.IndexFunc(a.Specials, func(e Episode) bool { return e.Number == k }); i >= 0 {
		return a.Specials[i], false, true
	}
	if i := slices.IndexFunc(a.Extras, func(x Extra) bool { return x.SeasonZero == k && k > 0 }); i >= 0 {
		return Episode{Number: k, Titles: a.Extras[i].Titles, ID: a.Extras[i].ID}, true, true
	}
	return Episode{}, false, false
}

// Last is the highest number AniDB lists in kind, which is "special" or
// an extra's kind; 0 when it lists none. Facts.Count owns the regular
// episodes.
func (a *Anime) Last(kind string) int {
	n := 0
	if kind == Special {
		for _, e := range a.Specials {
			n = max(n, e.Number)
		}
		return n
	}
	for _, x := range a.Extras {
		if x.Kind == kind {
			n = max(n, x.Number)
		}
	}
	return n
}

// SeasonZeroOf is the kind and AniDB number of the episode a and
// Anime-Lists number k in season 0: a listed special or extra, else the
// episode seasonZeroKind decodes. ok is false for a number below 1.
func (a *Anime) SeasonZeroOf(k int) (kind string, number int, ok bool) {
	if i := slices.IndexFunc(a.Specials, func(e Episode) bool { return e.Number == k }); i >= 0 {
		return Special, k, true
	}
	if i := slices.IndexFunc(a.Extras, func(x Extra) bool { return x.SeasonZero == k && k > 0 }); i >= 0 {
		return a.Extras[i].Kind, a.Extras[i].Number, true
	}
	return seasonZeroKind(k)
}

// SeasonZeroFor is the Anime-Lists season 0 number of AniDB episode number
// of kind, special or an extra's kind: a special's own number, listed or
// not, a listed extra's SeasonZero, else seasonZeroNumber's when no listed
// episode holds it. ok is false when no number addresses the episode.
func (a *Anime) SeasonZeroFor(kind string, number int) (k int, ok bool) {
	if kind == Special {
		return number, number >= 1
	}
	if i := slices.IndexFunc(a.Extras, func(x Extra) bool { return x.Kind == kind && x.Number == number }); i >= 0 {
		return a.Extras[i].SeasonZero, a.Extras[i].SeasonZero > 0
	}
	if k, ok = seasonZeroNumber(kind, number); !ok {
		return 0, false
	}
	if kk, nn, _ := a.SeasonZeroOf(k); kk != kind || nn != number {
		return 0, false
	}
	return k, true
}

// CountBasis names the precedence in Count. A collision baseline records
// the basis it was measured under, so any change to Count changes it.
const CountBasis = "anidb-mirror-first/aod-airing-fallback/v1"

// Facts is AniDB's episode lists at one mirror snapshot, with the
// checks/counts.json rows and anime-offline-database's regular counts the
// precedence in Count falls back on.
type Facts struct {
	snapshot  *Snapshot
	overrides map[int]int
	database  map[int]int
}

// NewFacts joins a snapshot, the counts rows and the database's count per
// AniDB id.
func NewFacts(s *Snapshot, rows []Row, database map[int]int) *Facts {
	overrides := make(map[int]int, len(rows))
	for i := range rows {
		overrides[rows[i].AniDBID] = rows[i].RegularEpisodes
	}
	return &Facts{snapshot: s, overrides: overrides, database: database}
}

// Count is AniDB's regular episode count for aid, 0 when no source has one:
// a checks/counts.json row; else the mirror's count when it records the
// anime's end date and lists regular episodes 1 to N with no gap; else
// anime-offline-database's, because the mirror's record of an anime with no
// end date can be two weeks old, and a list with a gap is not a count. An
// anime with an end date and no regular episode also falls through, since
// zero places nothing.
func (f *Facts) Count(aid int) int {
	if n, ok := f.overrides[aid]; ok {
		return n
	}
	return f.sourceCount(aid)
}

func (f *Facts) sourceCount(aid int) int {
	a, ok := f.snapshot.Anime[aid]
	if n := len(a.Regular); ok && a.Finished && n > 0 && a.Regular[n-1].Number == n {
		return n
	}
	return max(f.database[aid], 0)
}

// RedundantRows are the counts rows that equal what the sources give
// without them, sorted; each can be deleted.
func (f *Facts) RedundantRows() []int {
	var out []int
	for _, id := range slices.Sorted(maps.Keys(f.overrides)) {
		if f.overrides[id] == f.sourceCount(id) {
			out = append(out, id)
		}
	}
	return out
}

// Anime is AniDB's episode list for aid, and false when the mirror lists
// none: no file, a file with no episodes object, or a refused file.
func (f *Facts) Anime(aid int) (Anime, bool) {
	a, ok := f.snapshot.Anime[aid]
	return a, ok
}

// Listed are the AniDB ids the mirror lists episodes for, ascending.
func (f *Facts) Listed() []int { return slices.Sorted(maps.Keys(f.snapshot.Anime)) }

// Commit is the mirror commit the facts were read at.
func (f *Facts) Commit() string { return f.snapshot.Commit }
