// Package guard holds the checks that stop a bad build from publishing:
// coverage against the previous release, and the collision check over
// every TVDB series two nodes share or an overlay entry touches. Only a
// collision the overlay leaves as Anime-Lists has it, recorded in the
// tracked baseline, is tolerated; the baseline itself may only shrink.
package guard

import (
	"errors"
	"fmt"

	"github.com/cplieger/animap/internal/release"
)

// minRatio is the share of each previous population a build must keep.
// Weekly upstream churn is a few dozen ids, so 10% is far outside it.
const minRatio = 0.9

// Floors apply when there is no comparable previous release: about half of
// the 2026-10 measurement.
var Floors = release.Populations{
	Entries: 10000, EntriesWithParts: 6500, AnimeWithTVDB: 3500, AnimeWithTMDB: 4200,
	EpisodesOnTVDB: 71000, EpisodesOnTMDB: 65000, AniDBEpisodeIDs: 145000,
}

// ErrCoverage reports a population that shrank past the guard.
var ErrCoverage = errors.New("guard: coverage collapsed")

// Coverage compares cur against prev (nil for no comparable release, which
// checks every population against Floors). With acceptShrink a deliberate
// shrink passes; extinction never does.
func Coverage(prev *release.Populations, cur release.Populations, acceptShrink bool) error {
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

func pops(prev, cur *release.Populations) []pop {
	return []pop{
		{"entries", prev.Entries, cur.Entries},
		{"entries_with_parts", prev.EntriesWithParts, cur.EntriesWithParts},
		{"anime_with_tvdb", prev.AnimeWithTVDB, cur.AnimeWithTVDB},
		{"anime_with_tmdb", prev.AnimeWithTMDB, cur.AnimeWithTMDB},
		{"episodes_on_tvdb", prev.EpisodesOnTVDB, cur.EpisodesOnTVDB},
		{"episodes_on_tmdb", prev.EpisodesOnTMDB, cur.EpisodesOnTMDB},
		{"anidb_episode_ids", prev.AniDBEpisodeIDs, cur.AniDBEpisodeIDs},
	}
}
