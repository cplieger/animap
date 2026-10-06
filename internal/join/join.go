// Package join builds the published records from offline-database entries
// and (overlay-patched) Anime-Lists nodes. The offline database supplies the
// identity fields and Anime-Lists fills everything else, the precedence
// Fribb/anime-lists-generator's merge uses; a record with an AniDB id takes
// its episode count from anidb.Facts.Count.
package join

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/schema"
)

// Stats counts what the join dropped or withheld, for the release notes.
type Stats struct {
	DroppedRows       int `json:"dropped_rows"`
	AmbiguousAniDB    int `json:"ambiguous_anidb"`
	AniListRecords    int `json:"anilist_records"`
	AniDBOnlyRecords  int `json:"anidb_only_records"`
	TypeOnlyRecords   int `json:"type_only_records"`
	NodesWithoutEntry int `json:"nodes_without_entry"`
}

type identity struct {
	anidb    map[int]struct{}
	mal      map[int]struct{}
	typ      string
	episodes int
	seen     bool
}

// Build returns AniList-keyed records by anilist_id, then AniDB-keyed records
// for every AniDB id no AniList record carries, by anidb_id. An AniList id
// whose entries name two or more AniDB ids gets no anidb_id: two candidates
// is not an answer, and each candidate keeps its own AniDB-keyed record.
func Build(entries []offlinedb.Entry, nodes map[int]*animelists.Node, facts *anidb.Facts) ([]schema.Record, Stats) {
	var st Stats
	byAniList := map[int]*identity{}
	byAniDB := map[int]*identity{}
	for i := range entries {
		e := &entries[i]
		for _, al := range e.AniList {
			absorb(byAniList, al, e, e.AniDB)
		}
		for _, ad := range e.AniDB {
			absorb(byAniDB, ad, e, nil)
		}
	}
	out, carried := aniListRecords(byAniList, facts, &st)
	st.AniListRecords = len(out)
	only := aniDBOnly(byAniDB, carried, nodes, &st)
	for _, ad := range slices.Sorted(maps.Keys(only)) {
		id := only[ad]
		out = append(out, schema.Record{AniDBID: ad, Type: id.typ, Episodes: facts.Count(ad), MALID: single(id.mal)})
	}
	st.AniDBOnlyRecords = len(out) - st.AniListRecords
	for i := range out {
		if n := nodes[out[i].AniDBID]; out[i].AniDBID > 0 && n != nil {
			fill(&out[i], n, &st)
		}
	}
	return out, st
}

func aniListRecords(byAniList map[int]*identity, facts *anidb.Facts, st *Stats) (out []schema.Record, carried map[int]bool) {
	out = make([]schema.Record, 0, len(byAniList))
	carried = map[int]bool{}
	for _, al := range slices.Sorted(maps.Keys(byAniList)) {
		id := byAniList[al]
		r := schema.Record{AniListID: al, Type: id.typ, Episodes: id.episodes, MALID: single(id.mal)}
		switch len(id.anidb) {
		case 0:
			st.TypeOnlyRecords++
		case 1:
			r.AniDBID = single(id.anidb)
			r.Episodes = facts.Count(r.AniDBID)
			carried[r.AniDBID] = true
		default:
			st.AmbiguousAniDB++
		}
		out = append(out, r)
	}
	return out, carried
}

// aniDBOnly is every AniDB id no AniList record carries: from the database,
// plus list nodes with no database entry that publish at least one fact.
func aniDBOnly(byAniDB map[int]*identity, carried map[int]bool, nodes map[int]*animelists.Node, st *Stats) map[int]*identity {
	out := map[int]*identity{}
	for ad, id := range byAniDB {
		if !carried[ad] {
			out[ad] = id
		}
	}
	for ad, n := range nodes {
		if carried[ad] || out[ad] != nil {
			continue
		}
		var probe schema.Record
		fill(&probe, n, &Stats{})
		if hasListFacts(&probe) {
			out[ad] = &identity{}
			st.NodesWithoutEntry++
		}
	}
	return out
}

// absorb folds one entry into the identity for key. The first entry seen
// supplies type and episodes, the count a record without an AniDB id
// publishes; AniDB and MAL candidates accumulate.
func absorb(m map[int]*identity, key int, e *offlinedb.Entry, aniDBIDs []int) {
	id := m[key]
	if id == nil {
		id = &identity{anidb: map[int]struct{}{}, mal: map[int]struct{}{}}
		m[key] = id
	}
	if !id.seen {
		id.typ, id.episodes, id.seen = e.Type, e.Episodes, true
	}
	for _, v := range aniDBIDs {
		id.anidb[v] = struct{}{}
	}
	for _, v := range e.MAL {
		id.mal[v] = struct{}{}
	}
}

func single(set map[int]struct{}) int {
	if len(set) != 1 {
		return 0
	}
	for k := range set {
		return k
	}
	return 0
}

func hasListFacts(r *schema.Record) bool {
	return r.TVDBID > 0 || r.TMDBTVID > 0 || len(r.TMDBMovieIDs) > 0 || len(r.IMDbIDs) > 0
}

var imdbRE = regexp.MustCompile(`^tt\d{7,10}$`)

// fill copies a node's published facts onto r. Each season and offset is
// conditional on its side's id, and tvdb_absolute excludes tvdb_season.
func fill(r *schema.Record, n *animelists.Node, st *Stats) {
	if v := positive(n.Attr("tvdbid")); v > 0 {
		r.TVDBID = v
		switch s := n.Attr("defaulttvdbseason"); {
		case s == "a":
			r.TVDBAbsolute = true
		case nonNegative(s) != nil:
			r.TVDBSeason = nonNegative(s)
		}
		r.TVDBEpisodeOffset = integer(n.Attr("episodeoffset"))
	}
	if v := positive(n.Attr("tmdbtv")); v > 0 {
		r.TMDBTVID = v
		r.TMDBSeason = nonNegative(n.Attr("tmdbseason"))
		r.TMDBEpisodeOffset = integer(n.Attr("tmdboffset"))
	}
	r.TMDBMovieIDs = positiveList(n.Attr("tmdbid"))
	r.IMDbIDs = imdbList(n.Attr("imdbid"))
	r.MappingList = nil
	if r.TVDBID == 0 && r.TMDBTVID == 0 {
		return
	}
	for _, row := range n.Rows {
		sr, err := row.Schema()
		if err != nil {
			st.DroppedRows++
			continue
		}
		r.MappingList = append(r.MappingList, sr)
	}
}

func positive(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func nonNegative(s string) *int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

func integer(s string) *int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
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
