// Package guard holds the checks that stop a bad build from publishing:
// coverage against the previous release, and the collision check over
// every TVDB series two nodes share or an overlay entry touches. Only a
// collision on an untouched series that the tracked baseline records is
// tolerated; the baseline itself may only shrink.
package guard

import (
	"errors"
	"fmt"

	"github.com/cplieger/animap/internal/schema"
)

// MinRatio is the share of each previous population a build must keep.
// Weekly upstream churn is a few dozen ids, so 10% is far outside it.
const MinRatio = 0.9

// Floors apply when there is no previous release: half of the 2026-10
// measurement.
var Floors = schema.Populations{Records: 10000, AniListWithAniDB: 6500, AniDBWithTVDB: 3500}

// ErrCoverage reports a population that shrank past the guard.
var ErrCoverage = errors.New("guard: coverage collapsed")

// Coverage compares cur against prev (nil for a first release). With
// acceptShrink a deliberate shrink passes; extinction never does.
func Coverage(prev *schema.Populations, cur schema.Populations, acceptShrink bool) error {
	type pop struct {
		name      string
		prev, cur int
	}
	if prev == nil {
		for _, p := range []pop{
			{"records", Floors.Records, cur.Records},
			{"anilist_with_anidb", Floors.AniListWithAniDB, cur.AniListWithAniDB},
			{"anidb_with_tvdb", Floors.AniDBWithTVDB, cur.AniDBWithTVDB},
		} {
			if p.cur < p.prev {
				return fmt.Errorf("%w: %s is %d, below the first-release floor %d", ErrCoverage, p.name, p.cur, p.prev)
			}
		}
		return nil
	}
	for _, p := range []pop{
		{"records", prev.Records, cur.Records},
		{"anilist_with_anidb", prev.AniListWithAniDB, cur.AniListWithAniDB},
		{"anidb_with_tvdb", prev.AniDBWithTVDB, cur.AniDBWithTVDB},
		{"with_tmdb", prev.WithTMDB, cur.WithTMDB},
		{"with_mapping_list", prev.WithMappingList, cur.WithMappingList},
	} {
		if p.prev > 0 && p.cur == 0 {
			return fmt.Errorf("%w: %s went from %d to 0", ErrCoverage, p.name, p.prev)
		}
		if !acceptShrink && float64(p.cur) < MinRatio*float64(p.prev) {
			return fmt.Errorf("%w: %s fell from %d to %d, below %.0f%%", ErrCoverage, p.name, p.prev, p.cur, MinRatio*100)
		}
	}
	return nil
}
