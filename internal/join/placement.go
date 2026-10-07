package join

import (
	"slices"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/schema"
)

// placement is where r's regular episodes land on TVDB, from r's own
// published rows, season and offset through animelists.Place. It is nil
// unless every episode 1..r.Episodes is placed by a row or by an offset
// r states: an absent offset says nothing, and an absolute default has no
// layout to read here.
func placement(r *schema.Record) []schema.Segment {
	if r.TVDBID == 0 || r.Episodes <= 0 {
		return nil
	}
	rows := animelists.TVDBRows(r.MappingList)
	d := animelists.Default{Season: r.TVDBSeason, Offset: r.TVDBEpisodeOffset}
	var segs []schema.Segment
	for k := 1; k <= r.Episodes; k++ {
		targets, src := animelists.Place(rows, d, k)
		if src != animelists.ByRow && src != animelists.ByOffset {
			return nil
		}
		var ok bool
		if segs, ok = placeTargets(segs, k, targets); !ok {
			return nil
		}
	}
	return segs
}

// placeTargets is false when a target lies below TVDB episode 1.
func placeTargets(segs []schema.Segment, k int, targets [][2]int) ([]schema.Segment, bool) {
	if len(targets) == 0 {
		return extend(segs, k, nil), true
	}
	for i, t := range targets {
		if t[1] < 1 {
			return nil, false
		}
		if slices.Contains(targets[:i], t) {
			continue
		}
		segs = extend(segs, k, &t)
	}
	return segs, true
}

// A nil t places k on no TVDB episode.
func extend(segs []schema.Segment, k int, t *[2]int) []schema.Segment {
	if n := len(segs); n > 0 && continues(&segs[n-1], k, t) {
		segs[n-1].End = k
		return segs
	}
	seg := schema.Segment{Start: k, End: k}
	if t != nil {
		s, e := t[0], t[1]
		seg.Season, seg.Episode = &s, &e
	}
	return append(segs, seg)
}

func continues(last *schema.Segment, k int, t *[2]int) bool {
	if t == nil || last.Season == nil {
		return t == nil && last.Season == nil
	}
	return *last.Season == t[0] && *last.Episode+k-last.Start == t[1]
}
