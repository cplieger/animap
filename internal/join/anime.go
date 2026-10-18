package join

import (
	"cmp"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/mapread"
)

// claimKey is a linked AniDB episode: anime, Anime-Lists season, number.
type claimKey struct{ anidb, season, number int }

// placer states where each AniDB episode lands, from Node.Place, the
// applied links' claims and the overlay entries' captures.
type placer struct {
	in       *Inputs
	claims   map[claimKey][2]int
	captures map[int]*overlay.Entry
}

func newPlacer(in *Inputs) *placer {
	p := &placer{in: in, claims: map[claimKey][2]int{}, captures: map[int]*overlay.Entry{}}
	for _, l := range in.Links.Compiled() {
		for _, c := range l.Claims {
			p.claims[claimKey{c.Parent, c.AniDBSeason, c.Number}] = c.Target
		}
	}
	for i := range in.Overlay {
		p.captures[in.Overlay[i].AniDBID] = &in.Overlay[i]
	}
	return p
}

// rows are the anime rows of every AniDB id the database, the mirror, the
// list or a link names, keeping each row that says anything.
func (p *placer) rows(byAniDB map[int]*identity, st *Stats) []mapread.Anime {
	set := map[int]bool{}
	for ad := range byAniDB {
		set[ad] = true
	}
	for _, ad := range p.in.Facts.Listed() {
		set[ad] = true
	}
	for ad := range p.in.Nodes {
		set[ad] = true
	}
	linked := p.in.Links.Linked()
	for i := range linked {
		for _, ad := range linked[i].Link.AniDBIDs() {
			set[ad] = true
		}
	}
	var out []mapread.Anime
	for _, ad := range slices.Sorted(maps.Keys(set)) {
		a := mapread.Anime{AniDBID: ad}
		if id := byAniDB[ad]; id != nil {
			a.Type = id.typ
		}
		p.fill(&a, st)
		if says(&a) {
			out = append(out, a)
		}
	}
	return out
}

func says(a *mapread.Anime) bool {
	return a.Type != "" || a.TVDBID > 0 || a.TMDBTVID > 0 || len(a.TMDBMovieIDs) > 0 || len(a.IMDbIDs) > 0 || a.Last != (mapread.Last{}) || a.EpisodesListed
}

var imdbRE = regexp.MustCompile(`^tt\d{7,10}$`)

// fill copies the node's ids onto a, its last numbers, and the runs of
// its episodes.
func (p *placer) fill(a *mapread.Anime, st *Stats) {
	n := p.in.Nodes[a.AniDBID]
	mirror, listed := p.in.Facts.Anime(a.AniDBID)
	a.EpisodesListed = listed
	a.Last = a.Last.Set(mapread.Regular, p.in.Facts.Count(a.AniDBID))
	for _, k := range mapread.Kinds()[1:] {
		a.Last = a.Last.Set(k, mirror.Last(string(k)))
	}
	var named []numbered
	if n != nil {
		a.TVDBID = positive(n.Attr("tvdbid"))
		a.TMDBTVID = positive(n.Attr("tmdbtv"))
		a.TMDBMovieIDs = positiveList(n.Attr("tmdbid"))
		a.IMDbIDs = imdbList(n.Attr("imdbid"))
		named = rowNumbers(n, &mirror)
		for _, k := range named {
			if k.n > a.Last.Of(k.kind) {
				a.Last = a.Last.Set(k.kind, k.n)
			}
		}
	}
	if a.TVDBID > 0 || a.TMDBTVID > 0 {
		for _, r := range n.Rows {
			if _, err := r.Parse(); err != nil {
				st.UnparsedRows++
			}
		}
	}
	a.Episodes = p.runs(a, n, &mirror, named)
}

type numbered struct {
	kind mapread.Kind
	n    int
}

// numbers are the episodes of kind k the file names, ascending, each with
// its AniDB episode id or 0: every regular episode 1 to last and every one
// AniDB lists past it; of the other kinds, the ones AniDB lists and the
// ones a row names, which the collision guard claims.
func numbers(a *mapread.Anime, mirror *anidb.Anime, named []numbered, k mapread.Kind) []anidb.Listed {
	ids := map[int]int{}
	for _, e := range mirror.Episodes(string(k)) {
		ids[e.Number] = e.ID
	}
	if k == mapread.Regular {
		for num := 1; num <= a.Last.Regular; num++ {
			if _, ok := ids[num]; !ok {
				ids[num] = 0
			}
		}
	}
	for _, x := range named {
		if _, ok := ids[x.n]; x.kind == k && !ok {
			ids[x.n] = 0
		}
	}
	out := make([]anidb.Listed, 0, len(ids))
	for _, num := range slices.Sorted(maps.Keys(ids)) {
		out = append(out, anidb.Listed{Number: num, ID: ids[num]})
	}
	return out
}

// rowNumbers are the season 0 episodes the node's rows name, in AniDB's
// kinds and numbers.
func rowNumbers(n *animelists.Node, mirror *anidb.Anime) []numbered {
	var out []numbered
	for _, sd := range []animelists.Side{animelists.TVDB, animelists.TMDB} {
		for _, r := range n.SideRows(sd) {
			if r.AniDBSeason != 0 {
				continue
			}
			for _, k := range r.Sources() {
				if kind, num, ok := mirror.SeasonZeroOf(k); ok {
					out = append(out, numbered{mapread.Kind(kind), num})
				}
			}
		}
	}
	return out
}

// runs states every episode numbers gives on both sites, merging
// consecutive episodes whose listing and statements advance together. An
// episode AniDB does not list gets no run where every site lands nowhere.
func (p *placer) runs(a *mapread.Anime, n *animelists.Node, mirror *anidb.Anime, named []numbered) []mapread.Run {
	var out []mapread.Run
	for _, k := range mapread.Kinds() {
		for _, e := range numbers(a, mirror, named, k) {
			r, ok := p.episode(a, n, mirror, k, e)
			if !ok {
				continue
			}
			if m := len(out); m > 0 && out[m-1].ExtendedBy(&r) {
				out[m-1].End = e.Number
				out[m-1].AniDBEpisodeIDs = append(out[m-1].AniDBEpisodeIDs, r.AniDBEpisodeIDs...)
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

// episode is the one-episode run of episode e of kind k, false when AniDB
// does not list it and it lands nowhere on every site. A regular episode
// past last states nothing: the guard claims 1 to Facts.Count only.
func (p *placer) episode(a *mapread.Anime, n *animelists.Node, mirror *anidb.Anime, k mapread.Kind, e anidb.Listed) (mapread.Run, bool) {
	r := mapread.Run{Kind: k, Start: e.Number, End: e.Number}
	if e.ID > 0 {
		r.AniDBEpisodeIDs = []int{e.ID}
	}
	if k == mapread.Regular && e.Number > a.Last.Regular {
		return r, e.ID > 0
	}
	season, z := 1, e.Number
	if k != mapread.Regular {
		var ok bool
		if z, ok = mirror.SeasonZeroFor(string(k), e.Number); !ok {
			r.TVDB, r.TMDB = nowhereOn(a.TVDBID), nowhereOn(a.TMDBTVID)
			return r, e.ID > 0
		}
		season = 0
	}
	if a.TVDBID > 0 {
		r.TVDB = p.statement(n, animelists.TVDB, a, k, season, z)
	}
	if a.TMDBTVID > 0 {
		r.TMDB = p.statement(n, animelists.TMDB, a, k, season, z)
	}
	return r, e.ID > 0 || !nowhere(r.TVDB, a.TVDBID) || !nowhere(r.TMDB, a.TMDBTVID)
}

// nowhereOn is a site's lands-nowhere statement, nil with no site id.
func nowhereOn(id int) *[]mapread.Target {
	if id == 0 {
		return nil
	}
	return &[]mapread.Target{}
}

func nowhere(site *[]mapread.Target, id int) bool {
	return id == 0 || site != nil && len(*site) == 0
}

// statement is where Anime-Lists number z of AniDB season (1 regular, 0
// the rest) lands on sd, as Place gives it: nil states nothing, an empty
// list lands nowhere.
func (p *placer) statement(n *animelists.Node, sd animelists.Side, a *mapread.Anime, k mapread.Kind, season, z int) *[]mapread.Target {
	pl := n.Place(sd, season, z)
	if !pl.Default {
		ts := []mapread.Target{}
		for _, t := range pl.Targets {
			if t[1] < 1 {
				return nil
			}
			ts = append(ts, mapread.Target{Season: t[0], Episode: t[1]})
		}
		slices.SortFunc(ts, func(x, y mapread.Target) int {
			return cmp.Or(cmp.Compare(x.Season, y.Season), cmp.Compare(x.Episode, y.Episode))
		})
		ts = slices.Compact(ts)
		return &ts
	}
	if t, ok := p.claims[claimKey{a.AniDBID, season, z}]; ok && sd == animelists.TVDB {
		return &[]mapread.Target{{Season: t[0], Episode: t[1]}}
	}
	switch {
	case !TakesDefault(k):
		// Neither the collision guard nor the date check claims a default
		// of these kinds.
		return &[]mapread.Target{}
	case pl.Absolute > 0:
		if t, ok := p.captures[a.AniDBID].TVDBAt(a.TVDBID, pl.Absolute); ok {
			return &[]mapread.Target{{Season: t[0], Episode: t[1]}}
		}
		return nil
	case pl.Below:
		return &[]mapread.Target{}
	case len(pl.Targets) > 0:
		t := pl.Targets[0]
		return &[]mapread.Target{{Season: t[0], Episode: t[1], Default: pl.Implied}}
	}
	return nil
}

// TakesDefault reports whether episodes of kind k may use a node's default
// placement when no row names them: regular episodes and specials may;
// Anime-Lists has no default for the other kinds. On TVDB an applied
// link's claim comes first.
func TakesDefault(k mapread.Kind) bool { return k == mapread.Regular || k == mapread.Special }

func positive(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func positiveList(s string) []int {
	var out []int
	for p := range strings.SplitSeq(s, ",") {
		if n := positive(strings.TrimSpace(p)); n > 0 && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func imdbList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); imdbRE.MatchString(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}
