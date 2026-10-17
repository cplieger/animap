package join

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
)

// Identity gives Link's AniList entry the AniDB id AniDB, as an overlay
// link overlay.Link.Identity classes as one does.
type Identity struct {
	Link  *overlay.Link
	AniDB int
}

// Linked is an overlay link that is not an identity, with its compiled
// placement: the link's parts become the entry's, and its claims state
// where its AniDB episodes land.
type Linked struct {
	Link     *overlay.Link
	Compiled overlay.Compiled
}

// Skipped is a link the join did not apply, with why.
type Skipped struct {
	Reason  string `json:"reason"`
	AniList int    `json:"anilist_id"`
}

// Applied are the overlay links a build applies. Applicable is the only
// constructor, so a reader of link claims that takes an Applied cannot see
// a skipped link. The zero value applies none.
type Applied struct {
	ids    []Identity
	linked []Linked
}

// Identities are the applied identity links.
func (a Applied) Identities() []Identity { return slices.Clone(a.ids) }

// Linked are the applied links that are not identities.
func (a Applied) Linked() []Linked { return slices.Clone(a.linked) }

// Compiled are Linked's compiled claims, in the same order.
func (a Applied) Compiled() []overlay.Compiled {
	out := make([]overlay.Compiled, 0, len(a.linked))
	for i := range a.linked {
		out = append(out, a.linked[i].Compiled)
	}
	return out
}

// Links are every applied link, identities included, by AniList id.
func (a Applied) Links() []*overlay.Link {
	out := make([]*overlay.Link, 0, a.Len())
	for _, l := range a.ids {
		out = append(out, l.Link)
	}
	for i := range a.linked {
		out = append(out, a.linked[i].Link)
	}
	slices.SortStableFunc(out, func(x, y *overlay.Link) int { return cmp.Compare(x.AniListID, y.AniListID) })
	return out
}

// Len is the number of applied links.
func (a Applied) Len() int { return len(a.ids) + len(a.linked) }

// Applicable splits ids and links into those the build applies and the
// rest, with why, against anime-offline-database's entries; nil entries
// apply every one. A link applies only to an AniList id they give no AniDB
// id, so an ambiguous one keeps no parts, and a non-identity link only
// when they count its episodes as the link does, or not at all.
func Applicable(entries []offlinedb.Entry, ids []Identity, links []Linked) (Applied, []Skipped) {
	var out Applied
	var skipped []Skipped
	byAniList, _ := index(entries)
	for _, l := range ids {
		if reason := identified(byAniList[l.Link.AniListID]); reason != "" {
			skipped = append(skipped, Skipped{AniList: l.Link.AniListID, Reason: reason})
			continue
		}
		out.ids = append(out.ids, l)
	}
	for i := range links {
		l := &links[i]
		id := byAniList[l.Link.AniListID]
		reason := identified(id)
		if n := len(l.Compiled.Targets); reason == "" && id != nil && id.episodes != 0 && id.episodes != n {
			reason = fmt.Sprintf("the entry has %d episodes, the link %d", id.episodes, n)
		}
		if reason != "" {
			skipped = append(skipped, Skipped{AniList: l.Link.AniListID, Reason: reason})
			continue
		}
		out.linked = append(out.linked, *l)
	}
	return out, skipped
}

// identified is why a link cannot apply to id: the database already gives
// it one AniDB id or several; "" when it gives none.
func identified(id *identity) string {
	if id == nil {
		return ""
	}
	switch n := len(id.anidb); {
	case n == 1:
		return fmt.Sprintf("anime-offline-database now links AniDB %d", single(id.anidb))
	case n > 1:
		return fmt.Sprintf("anime-offline-database gives it %d AniDB ids", n)
	}
	return ""
}

// addIdentities folds each applied identity into byAniList before the
// entries are made, so a linked entry takes the ordinary path. An AniList
// id the database lacks takes the type and count of the anime's own
// database entry.
func addIdentities(byAniList, byAniDB map[int]*identity, ids []Identity) {
	for _, l := range ids {
		id := byAniList[l.Link.AniListID]
		if id == nil {
			id = &identity{anidb: map[int]struct{}{}, seen: true}
			if src := byAniDB[l.AniDB]; src != nil {
				id.typ, id.episodes = src.typ, src.episodes
			}
			byAniList[l.Link.AniListID] = id
		}
		id.anidb[l.AniDB] = struct{}{}
	}
}

// addLink gives an applied link's AniList entry the link's parts.
func addLink(byAniList map[int]*identity, l *Linked, facts *anidb.Facts) {
	id := byAniList[l.Link.AniListID]
	if id == nil {
		id = &identity{anidb: map[int]struct{}{}, seen: true}
		byAniList[l.Link.AniListID] = id
	}
	id.parts = l.Link.Published(facts)
}
