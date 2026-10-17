// Package join builds the published tables. The anime rows come from the
// (overlay-patched) Anime-Lists nodes, anidb.Facts and every placement
// Node.Place gives; the entries from anime-offline-database's entries and
// the overlay links, which say which AniDB episodes an entry is.
package join

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/mapread"
)

// Inputs are what Build joins. Nodes are patched by the overlay; Overlay
// is its entries, whose TVDB captures resolve absolute numbers; Links are
// the overlay links the build applies.
type Inputs struct {
	Nodes   map[int]*animelists.Node
	Facts   *anidb.Facts
	Entries []offlinedb.Entry
	Overlay []overlay.Entry
	Links   Applied
}

// Stats counts what the join built, withheld or could not read, for the
// release notes.
type Stats struct {
	AnimeRows      int `json:"anime_rows"`
	AniListEntries int `json:"anilist_entries"`
	OtherEntries   int `json:"other_entries"`
	AmbiguousAniDB int `json:"ambiguous_anidb"`
	UnparsedRows   int `json:"unparsed_rows"`
	UnknownSources int `json:"unknown_sources"`
}

type identity struct {
	anidb    map[int]struct{}
	parts    []mapread.Part
	typ      string
	ids      mapread.IDs
	episodes int
	seen     bool
}

// Build returns the anime rows by AniDB id and the entries: one per AniList
// id, ascending, then one per database entry with no AniList id that names
// a published id, by its encoded ids. An AniList id whose entries name two
// or more AniDB ids has no parts: two candidates is not an answer.
func Build(in *Inputs) (anime []mapread.Anime, entries []mapread.Entry, st Stats) {
	byAniList, byAniDB := index(in.Entries)
	addIdentities(byAniList, byAniDB, in.Links.Identities())
	linked := in.Links.Linked()
	for i := range linked {
		addLink(byAniList, &linked[i], in.Facts)
	}
	entries = aniListEntries(byAniList, &st)
	st.AniListEntries = len(entries)
	others := otherEntries(in.Entries)
	st.OtherEntries = len(others)
	entries = append(entries, others...)
	for i := range in.Entries {
		st.UnknownSources += len(in.Entries[i].Unknown)
	}
	anime = newPlacer(in).rows(byAniDB, &st)
	st.AnimeRows = len(anime)
	return anime, entries, st
}

func index(entries []offlinedb.Entry) (byAniList, byAniDB map[int]*identity) {
	byAniList, byAniDB = map[int]*identity{}, map[int]*identity{}
	for i := range entries {
		e := &entries[i]
		for _, al := range e.AniList {
			absorb(byAniList, al, e)
		}
		for _, ad := range e.AniDB {
			absorb(byAniDB, ad, e)
		}
	}
	return byAniList, byAniDB
}

// Carriers maps each AniDB id an AniList entry is to every such AniList id,
// ascending, as Build decides it: the AniList id's entries name that AniDB
// id alone.
func Carriers(entries []offlinedb.Entry) map[int][]int {
	byAniList, _ := index(entries)
	out := map[int][]int{}
	for _, al := range slices.Sorted(maps.Keys(byAniList)) {
		if ad := single(byAniList[al].anidb); ad > 0 {
			out[ad] = append(out[ad], al)
		}
	}
	return out
}

// absorb folds one entry into the identity for key. The first entry seen
// supplies type and episodes; AniDB candidates and service ids accumulate.
func absorb(m map[int]*identity, key int, e *offlinedb.Entry) {
	id := m[key]
	if id == nil {
		id = &identity{anidb: map[int]struct{}{}}
		m[key] = id
	}
	if !id.seen {
		id.typ, id.episodes, id.seen = e.Type, e.Episodes, true
	}
	for _, v := range e.AniDB {
		id.anidb[v] = struct{}{}
	}
	mergeIDs(&id.ids, &e.IDs)
}

func aniListEntries(byAniList map[int]*identity, st *Stats) []mapread.Entry {
	out := make([]mapread.Entry, 0, len(byAniList))
	for _, al := range slices.Sorted(maps.Keys(byAniList)) {
		id := byAniList[al]
		e := mapread.Entry{AniListID: al, Type: id.typ, Episodes: id.episodes, IDs: idsOrNil(&id.ids)}
		switch {
		case id.parts != nil:
			e.Parts = id.parts
		case len(id.anidb) == 1:
			e.Parts = []mapread.Part{{AniDBID: single(id.anidb), Kind: mapread.Regular}}
		case len(id.anidb) > 1:
			st.AmbiguousAniDB++
		}
		out = append(out, e)
	}
	return out
}

// otherEntries are the database entries with no AniList id that name an id
// on a published service, ordered by their encoded ids, ties in file order.
func otherEntries(entries []offlinedb.Entry) []mapread.Entry {
	type keyed struct {
		key []byte
		e   mapread.Entry
	}
	var out []keyed
	for i := range entries {
		src := &entries[i]
		ids := idsOrNil(&src.IDs)
		if len(src.AniList) > 0 || ids == nil {
			continue
		}
		e := mapread.Entry{Type: src.Type, Episodes: src.Episodes, IDs: ids}
		if len(src.AniDB) == 1 {
			e.Parts = []mapread.Part{{AniDBID: src.AniDB[0], Kind: mapread.Regular}}
		}
		out = append(out, keyed{key: encodeIDs(ids), e: e})
	}
	slices.SortStableFunc(out, func(a, b keyed) int { return bytes.Compare(a.key, b.key) })
	res := make([]mapread.Entry, 0, len(out))
	for _, k := range out {
		res = append(res, k.e)
	}
	return res
}

// encodeIDs is the producer's sort key for entries with no AniList id: ids
// as mapread.Encode writes them.
func encodeIDs(ids *mapread.IDs) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ids); err != nil {
		panic("join: ids do not encode: " + err.Error())
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func intLists(ids *mapread.IDs) []*[]int {
	return []*[]int{&ids.AniDB, &ids.AnimeCountdown, &ids.AniSearch, &ids.ANN, &ids.Kitsu, &ids.LiveChart, &ids.MAL, &ids.Simkl}
}

func keyLists(ids *mapread.IDs) []*[]string {
	return []*[]string{&ids.AnimePlanet, &ids.NotifyMoe}
}

// mergeIDs adds src's ids to dst, each list kept sorted and unique.
func mergeIDs(dst, src *mapread.IDs) {
	d, s := intLists(dst), intLists(src)
	for i := range d {
		*d[i] = slices.Compact(slices.Sorted(slices.Values(append(*d[i], *s[i]...))))
	}
	dk, sk := keyLists(dst), keyLists(src)
	for i := range dk {
		*dk[i] = slices.Compact(slices.Sorted(slices.Values(append(*dk[i], *sk[i]...))))
	}
}

// idsOrNil is a copy of ids, nil when every list is empty.
func idsOrNil(ids *mapread.IDs) *mapread.IDs {
	var out mapread.IDs
	mergeIDs(&out, ids)
	for _, l := range intLists(&out) {
		if len(*l) == 0 {
			*l = nil
		}
	}
	for _, l := range keyLists(&out) {
		if len(*l) == 0 {
			*l = nil
		}
	}
	if encoded := encodeIDs(&out); string(encoded) == "{}" {
		return nil
	}
	return &out
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
