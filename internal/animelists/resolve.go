package animelists

import (
	"slices"
	"strconv"
	"strings"
)

// SideRow is a parsed row that names a season on one side, TVDB or TMDB.
type SideRow struct {
	Pairs       [][]int
	Season      int
	AniDBSeason int
	Start, End  int
	Offset      int
}

// SideRows are n's rows that name a season in seasonAttr ("tvdbseason" or
// "tmdbseason"); a row the list syntax cannot parse claims nothing.
func (n *Node) SideRows(seasonAttr string) []SideRow {
	var out []SideRow
	for _, r := range n.Rows {
		s, err := strconv.Atoi(strings.TrimSpace(r.Attrs[seasonAttr]))
		if err != nil || s < 0 {
			continue
		}
		sr, err := r.Schema()
		if err != nil {
			continue
		}
		out = append(out, SideRow{Pairs: sr.Episodes, Season: s, AniDBSeason: sr.AniDBSeason, Start: sr.Start, End: sr.End, Offset: sr.Offset})
	}
	return out
}

// Sources are the AniDB episode numbers the row names.
func (r SideRow) Sources() []int {
	var out []int
	for _, p := range r.Pairs {
		out = append(out, p[0])
	}
	if r.Start > 0 {
		for k := r.Start; k <= max(r.End, r.Start); k++ {
			out = append(out, k)
		}
	}
	return out
}

// Targets resolves AniDB episode k of anidbSeason through the first row
// that covers it. A pair row covers k when it names k; a "-0" pair covers
// it with no target. A regular range with no end is open; a specials range
// with no end covers its start only.
func Targets(rows []SideRow, anidbSeason, k int) (covered bool, targets [][2]int) {
	for _, r := range rows {
		if r.AniDBSeason != anidbSeason {
			continue
		}
		if i := slices.IndexFunc(r.Pairs, func(p []int) bool { return p[0] == k }); i >= 0 {
			for _, t := range r.Pairs[i][1:] {
				targets = append(targets, [2]int{r.Season, t})
			}
			return true, targets
		}
		if r.inRange(k) {
			return true, [][2]int{{r.Season, k + r.Offset}}
		}
	}
	return false, nil
}

func (r SideRow) inRange(k int) bool {
	if r.Start == 0 || k < r.Start {
		return false
	}
	switch {
	case r.End > 0:
		return k <= r.End
	case r.AniDBSeason == 0:
		return k == r.Start
	default:
		return true
	}
}

// SpecialTVDB is where AniDB special k of n lands on TVDB: through a row,
// else the list's default of season 0 episode k. ok is false when a row
// maps it to no episode or to more than one.
func (n *Node) SpecialTVDB(k int) (season, episode int, ok bool) {
	covered, t := Targets(n.SideRows("tvdbseason"), 0, k)
	switch {
	case !covered:
		return 0, k, true
	case len(t) == 1:
		return t[0][0], t[0][1], true
	default:
		return 0, 0, false
	}
}
