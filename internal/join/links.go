package join

import (
	"fmt"
	"slices"

	"github.com/cplieger/animap/internal/schema"
)

// Identity gives AniList entry AniList the AniDB id AniDB, as an overlay
// link that names every regular episode of one anime does.
type Identity struct {
	AniList, AniDB int
}

// Linked is an overlay link placed on TVDB: AniList episode i+1 lands on
// episode Targets[i] of season Season of series TVDBID. Specials is set
// for a link to season-zero episodes of one anime, the one shape
// anidb_parent states.
type Linked struct {
	Specials *schema.ParentSpecials
	Targets  []int
	AniList  int
	TVDBID   int
	Season   int
}

// Skipped is a link the join did not apply, with why.
type Skipped struct {
	Reason  string `json:"reason"`
	AniList int    `json:"anilist_id"`
}

// addIdentities folds each identity into byAniList before the records are
// made, so a linked record takes the ordinary path: its AniDB id, count
// and node facts, and the AniDB-only record of that id is not published.
// An AniList id anime-offline-database already links is skipped.
func addIdentities(byAniList, byAniDB map[int]*identity, ids []Identity) (skipped []Skipped) {
	for _, l := range ids {
		id := byAniList[l.AniList]
		switch {
		case id != nil && len(id.anidb) > 0:
			skipped = append(skipped, Skipped{AniList: l.AniList, Reason: fmt.Sprintf("anime-offline-database now links AniDB %d", firstOf(id.anidb))})
			continue
		case id == nil:
			id = &identity{anidb: map[int]struct{}{}, mal: map[int]struct{}{}, seen: true}
			if src := byAniDB[l.AniDB]; src != nil {
				id.typ, id.episodes = src.typ, src.episodes
				for m := range src.mal {
					id.mal[m] = struct{}{}
				}
			}
			byAniList[l.AniList] = id
		}
		id.anidb[l.AniDB] = struct{}{}
	}
	return skipped
}

func firstOf(set map[int]struct{}) int {
	ids := make([]int, 0, len(set))
	for k := range set {
		ids = append(ids, k)
	}
	return slices.Min(ids)
}

// ApplyLinks gives each linked AniList record its TVDB series, season, one
// mapping row and the placement, inserting a record where the join made
// none. records must be sorted as Build returns them; the result is too.
// A link applies only to a record with no AniDB id of its own and, when it
// has one, the same episode count; anything else is skipped and reported,
// and the record stays as the join made it.
func ApplyLinks(records []schema.Record, links []Linked) (out []schema.Record, applied int, skipped []Skipped) {
	for i := range links {
		l := &links[i]
		at, found := slices.BinarySearchFunc(records, l.AniList, func(r schema.Record, al int) int { return cmpAniList(r.AniListID, al) })
		if !found {
			records = slices.Insert(records, at, schema.Record{AniListID: l.AniList, Episodes: len(l.Targets)})
		}
		if reason := link(&records[at], l); reason != "" {
			skipped = append(skipped, Skipped{AniList: l.AniList, Reason: reason})
			continue
		}
		applied++
	}
	return records, applied, skipped
}

// cmpAniList orders AniList-keyed records first, then AniDB-only ones
// (AniListID 0), which never match a search.
func cmpAniList(have, want int) int {
	switch {
	case have == 0:
		return 1
	case have < want:
		return -1
	case have > want:
		return 1
	}
	return 0
}

func link(r *schema.Record, l *Linked) string {
	switch {
	case r.AniDBID != 0:
		return fmt.Sprintf("anime-offline-database now links AniDB %d", r.AniDBID)
	case r.Episodes != 0 && r.Episodes != len(l.Targets):
		return fmt.Sprintf("the record has %d episodes, the link %d", r.Episodes, len(l.Targets))
	}
	season := l.Season
	pairs := make([][]int, 0, len(l.Targets))
	for i, t := range l.Targets {
		pairs = append(pairs, []int{i + 1, t})
	}
	if l.Specials != nil {
		r.AniDBParent = &schema.ParentSpecials{AniDBID: l.Specials.AniDBID, Specials: slices.Clone(l.Specials.Specials)}
	}
	r.TVDBID = l.TVDBID
	r.TVDBSeason = &season
	r.MappingList = []schema.Row{{AniDBSeason: 1, TVDBSeason: &season, Episodes: pairs}}
	r.TVDBPlacement = segments(len(pairs), func(k int) ([][2]int, bool) {
		return [][2]int{{season, pairs[k-1][1]}}, true
	})
	return ""
}
