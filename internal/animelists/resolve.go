package animelists

import (
	"slices"
	"strconv"
	"strings"
)

// Side is the database a node maps to: TVDB or TMDB.
type Side int

// The zero Side is TVDB.
const (
	TVDB Side = iota
	TMDB
)

// attrs are the attributes that place an episode on sd: the row season,
// the node's default season and its episode offset. Only TVDB numbers a
// series absolutely.
func (sd Side) attrs() (rowSeason, defaultSeason, offset string, absolute bool) {
	if sd == TMDB {
		return "tmdbseason", "tmdbseason", "tmdboffset", false
	}
	return "tvdbseason", "defaulttvdbseason", "episodeoffset", true
}

// SideRow is a parsed row that names a season on one side.
type SideRow struct {
	Pairs       [][]int
	Season      int
	AniDBSeason int
	Start, End  int
	Offset      int
}

// SideRows are n's rows that name a season on sd; a row the list syntax
// cannot parse claims nothing.
func (n *Node) SideRows(sd Side) []SideRow {
	seasonAttr, _, _, _ := sd.attrs()
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

// throughRows resolves AniDB episode k of anidbSeason through the first row
// that covers it. A pair row covers k when it names k; a "-0" pair covers
// it with no target. A regular range with no end is open; a specials range
// with no end covers its start only.
func throughRows(rows []SideRow, anidbSeason, k int) (covered bool, targets [][2]int) {
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

// Placement is where a node puts one AniDB episode on one side. Absolute
// is the TVDB absolute number when the node's default season is "a" and no
// row names the episode; otherwise Targets holds [season, episode] pairs,
// none when the node maps the episode nowhere. Default says no row named
// the episode.
type Placement struct {
	Targets  [][2]int
	Absolute int
	Default  bool
}

// Place is where n puts AniDB episode k of anidbSeason (1 regular, 0
// specials) on sd: through the first row that covers it, else the node's
// default. A regular episode defaults to the default season at k plus the
// offset, and to nothing when that is below 1 or the season is unset; a
// special defaults to season 0 episode k.
func (n *Node) Place(sd Side, anidbSeason, k int) Placement {
	_, dfltAttr, offAttr, absolute := sd.attrs()
	if covered, t := throughRows(n.SideRows(sd), anidbSeason, k); covered {
		return Placement{Targets: t}
	}
	if anidbSeason == 0 {
		return Placement{Targets: [][2]int{{0, k}}, Default: true}
	}
	off, _ := strconv.Atoi(n.Attr(offAttr))
	ep := k + off
	if ep < 1 {
		return Placement{Default: true}
	}
	s := n.Attr(dfltAttr)
	if s == "a" && absolute {
		return Placement{Absolute: ep, Default: true}
	}
	if season, err := strconv.Atoi(s); err == nil && season >= 0 {
		return Placement{Targets: [][2]int{{season, ep}}, Default: true}
	}
	return Placement{Default: true}
}

// EpisodeLabel names AniDB episode k of anidbSeason: S<k> for a special,
// ep<k> for a regular episode.
func EpisodeLabel(anidbSeason, k int) string {
	if anidbSeason == 0 {
		return "S" + strconv.Itoa(k)
	}
	return "ep" + strconv.Itoa(k)
}
