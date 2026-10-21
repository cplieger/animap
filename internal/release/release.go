// Package release holds the checks a mapping export must pass before it is
// published, beyond the format mapread owns: its census against the latest
// release, and its freshness.
package release

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cplieger/animap/mapread"
)

// Populations are the counts the coverage guard compares across releases.
type Populations struct {
	Entries            int `json:"entries"`
	EntriesWithParts   int `json:"entries_with_parts"`
	RowsOnTVDB         int `json:"rows_on_tvdb"`
	EpisodesOnTVDB     int `json:"episodes_on_tvdb"`
	EpisodesWithMovies int `json:"episodes_with_movies"`
	AniDBEpisodeIDs    int `json:"anidb_episode_ids"`
}

// Census counts the populations: the entries and those with parts, the
// anime rows on any TVDB series, the AniDB episodes that land on TVDB or
// state a TMDB movie, and the AniDB episode ids listed.
func Census(doc *mapread.Document) Populations {
	p := Populations{Entries: len(doc.Entries)}
	for i := range doc.Entries {
		if len(doc.Entries[i].Parts) > 0 {
			p.EntriesWithParts++
		}
	}
	for i := range doc.Anime {
		a := &doc.Anime[i]
		if len(a.TVDBSeries()) > 0 {
			p.RowsOnTVDB++
		}
		for j := range a.Episodes {
			r := &a.Episodes[j]
			n := r.End - r.Start + 1
			if r.TVDB != nil && len(*r.TVDB) > 0 {
				p.EpisodesOnTVDB += n
			}
			if len(r.TMDBMovies) > 0 {
				p.EpisodesWithMovies += n
			}
			p.AniDBEpisodeIDs += len(r.AniDBEpisodeIDs)
		}
	}
	return p
}

// minRatio is the share of each previous population a release must keep,
// the share the export's own build keeps of the export before it.
const minRatio = 0.9

// Floors apply when there is no comparable previous release: about half
// of the census of the 2026-10-20 export.
var Floors = Populations{
	Entries: 10000, EntriesWithParts: 6500, RowsOnTVDB: 3500,
	EpisodesOnTVDB: 70000, EpisodesWithMovies: 1400, AniDBEpisodeIDs: 145000,
}

// ErrCoverage reports a population that shrank past the guard.
var ErrCoverage = errors.New("release: coverage collapsed")

// Coverage compares cur against prev (nil for no comparable release, which
// checks every population against Floors). With acceptShrink a deliberate
// shrink passes; extinction and a population below its floor never do.
func Coverage(prev *Populations, cur Populations, acceptShrink bool) error {
	if prev == nil {
		for _, p := range pops(&Floors, &cur) {
			if p.cur < p.prev {
				return fmt.Errorf("%w: %s is %d, below the floor %d", ErrCoverage, p.name, p.cur, p.prev)
			}
		}
		return nil
	}
	for _, p := range pops(prev, &cur) {
		if p.prev > 0 && p.cur == 0 {
			return fmt.Errorf("%w: %s went from %d to 0", ErrCoverage, p.name, p.prev)
		}
		if !acceptShrink && float64(p.cur) < minRatio*float64(p.prev) {
			return fmt.Errorf("%w: %s fell from %d to %d, below %.0f%%", ErrCoverage, p.name, p.prev, p.cur, minRatio*100)
		}
	}
	return nil
}

type pop struct {
	name      string
	prev, cur int
}

func pops(prev, cur *Populations) []pop {
	return []pop{
		{"entries", prev.Entries, cur.Entries},
		{"entries_with_parts", prev.EntriesWithParts, cur.EntriesWithParts},
		{"rows_on_tvdb", prev.RowsOnTVDB, cur.RowsOnTVDB},
		{"episodes_on_tvdb", prev.EpisodesOnTVDB, cur.EpisodesOnTVDB},
		{"episodes_with_movies", prev.EpisodesWithMovies, cur.EpisodesWithMovies},
		{"anidb_episode_ids", prev.AniDBEpisodeIDs, cur.AniDBEpisodeIDs},
	}
}

const (
	// maxExportAge bounds generated_at: the export is built once a week,
	// so a week plus a day for a late build.
	maxExportAge = 8 * 24 * time.Hour
	// maxDumpAge bounds sources.anidb.titles_dump: every weekly build
	// reads AniDB's title dump, so a dump older than two builds means the
	// anime list has stopped following AniDB.
	maxDumpAge = 14 * 24 * time.Hour
)

// Stale lists why doc is too old to publish at now, nil when it is fresh:
// generated_at more than maxExportAge before now, titles_dump more than
// maxDumpAge before now's UTC date. doc must be one mapread decoded; an
// error is a date it does not hold.
func Stale(doc *mapread.Document, now time.Time) ([]string, error) {
	built, err := instant(doc.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("release: generated_at: %w", err)
	}
	dump, err := time.Parse(time.DateOnly, doc.Sources.AniDB.TitlesDump)
	if err != nil {
		return nil, fmt.Errorf("release: sources.anidb.titles_dump: %w", err)
	}
	var out []string
	if age := now.Sub(built); age > maxExportAge {
		out = append(out, fmt.Sprintf("generated_at %s is %s old, over %s", doc.GeneratedAt, days(age), days(maxExportAge)))
	}
	y, m, d := now.UTC().Date()
	if age := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(dump); age > maxDumpAge {
		out = append(out, fmt.Sprintf("sources.anidb.titles_dump %s is %s old, over %s", doc.Sources.AniDB.TitlesDump, days(age), days(maxDumpAge)))
	}
	return out, nil
}

// instant reads an RFC 3339 time as mapread accepts it. RFC 3339 §5.6
// allows a lower-case t and z, and a leap second (§5.7), which time.Parse
// refuses; a leap second is read as the second before it.
func instant(s string) (time.Time, error) {
	s = strings.ToUpper(s)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil && len(s) > 19 && s[17:19] == "60" {
		return time.Parse(time.RFC3339, s[:17]+"59"+s[19:])
	}
	return t, err
}

// days writes d in days, to one decimal.
func days(d time.Duration) string {
	return fmt.Sprintf("%.1f days", d.Hours()/24)
}
