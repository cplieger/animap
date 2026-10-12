package join

import (
	"slices"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/schema"
)

// placement is where r's regular episodes land on TVDB, as n.Place puts
// them; r is the record fill made from n. It is nil unless every episode
// 1..r.Episodes is placed by a row or by an offset r states: an absent
// offset says nothing, and an absolute default has no layout to read here.
func placement(r *schema.Record, n *animelists.Node) []schema.Segment {
	if r.TVDBID == 0 || r.Episodes <= 0 {
		return nil
	}
	return segments(r.Episodes, func(k int) ([][2]int, bool) {
		p := n.Place(animelists.TVDB, 1, k)
		return p.Targets, !p.Default || (r.TVDBEpisodeOffset != nil && len(p.Targets) > 0)
	})
}

// segments runs episodes 1..count through at. It is nil when at refuses
// an episode or a target lies below TVDB episode 1.
func segments(count int, at func(k int) (targets [][2]int, ok bool)) []schema.Segment {
	var segs []schema.Segment
	for k := 1; k <= count; k++ {
		t, ok := at(k)
		if !ok {
			return nil
		}
		if segs, ok = placeTargets(segs, k, t); !ok {
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
