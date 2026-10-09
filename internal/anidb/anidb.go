// Package anidb answers what episodes AniDB lists for an anime: the regular
// episodes, the specials with their numbers, and their air dates. It reads
// them from notseteve/AnimeAggregations, a twice-monthly mirror of AniDB's
// data, at a pinned commit, and Facts.Count is the one place a regular
// episode count is decided. Only numbers and dates leave the package; the
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

// Episode is one numbered AniDB episode. AirDate is YYYY-MM-DD, or "" when
// AniDB has no date.
type Episode struct {
	AirDate string `json:"air_date"`
	Number  int    `json:"number"`
}

// Anime is AniDB's episode list for one anime. Regular[i] is the air date
// of regular episode i+1; Specials are sorted by number. Finished means
// AniDB records an end date, which it can set before the last episode airs.
type Anime struct {
	Regular  []string  `json:"regular"`
	Specials []Episode `json:"specials"`
	Finished bool      `json:"finished"`
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
