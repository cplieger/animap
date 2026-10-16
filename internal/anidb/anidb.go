// Package anidb answers what episodes AniDB lists for an anime: the regular
// episodes and specials with their numbers and air dates, and the other
// kinds with their numbers. It reads them from notseteve/AnimeAggregations,
// a twice-monthly mirror of AniDB's data, at a pinned commit, and
// Facts.Count is the one place a regular episode count is decided. Only
// numbers, dates and a digest of the episode titles leave the package; the
// mirror's titles and descriptions are AniDB text under CC BY-NC-SA and are
// never kept.
package anidb

import (
	"errors"
	"maps"
	"slices"
)

// errInvalid wraps every refusal of a mirror file, a snapshot file or a
// counts file.
var errInvalid = errors.New("anidb: invalid")

// Episode is one numbered AniDB episode. A special's AirDate is YYYY-MM-DD,
// or "" when AniDB has no date; Extract leaves an extra's empty and
// LoadSnapshot does not check it, since nothing reads it. Titles is the hex
// SHA-256 of the episode's titles.
type Episode struct {
	AirDate string `json:"air_date"`
	Titles  string `json:"titles"`
	Number  int    `json:"number"`
}

// Anime is AniDB's episode list for one anime. Regular[i] is the air date
// of regular episode i+1; Specials and Extras (credits, promotions, parodies
// and other episodes, under their Anime-Lists season 0 numbers from 101)
// are sorted by number. Finished means AniDB records an
// end date, which it can set before the last episode airs. Titles is the
// hex SHA-256 of the regular episodes' and specials' titles,
// RegularTitles[i] that of regular episode i+1 alone; LoadSnapshot refuses
// an anime without them or Extras.
type Anime struct {
	Titles        string    `json:"titles"`
	Regular       []string  `json:"regular"`
	RegularTitles []string  `json:"regular_titles"`
	Specials      []Episode `json:"specials"`
	Extras        []Episode `json:"extras"`
	Finished      bool      `json:"finished"`
}

// SeasonZero is the episode Anime-Lists numbers k in season 0: the special
// k, else the extra k; extra reports the second.
func (a *Anime) SeasonZero(k int) (e Episode, extra, ok bool) {
	if i := slices.IndexFunc(a.Specials, func(e Episode) bool { return e.Number == k }); i >= 0 {
		return a.Specials[i], false, true
	}
	if i := slices.IndexFunc(a.Extras, func(e Episode) bool { return e.Number == k }); i >= 0 {
		return a.Extras[i], true, true
	}
	return Episode{}, false, false
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
// anime's end date; else anime-offline-database's, because the mirror's
// record of an anime with no end date can be two weeks old. An anime with an
// end date and no regular episode also falls through, since zero places
// nothing.
func (f *Facts) Count(aid int) int {
	if n, ok := f.overrides[aid]; ok {
		return n
	}
	return f.sourceCount(aid)
}

func (f *Facts) sourceCount(aid int) int {
	if a, ok := f.snapshot.Anime[aid]; ok && a.Finished && len(a.Regular) > 0 {
		return len(a.Regular)
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
// none: no file, a file with no episodes, or a refused file.
func (f *Facts) Anime(aid int) (Anime, bool) {
	a, ok := f.snapshot.Anime[aid]
	return a, ok
}

// Commit is the mirror commit the facts were read at.
func (f *Facts) Commit() string { return f.snapshot.Commit }
