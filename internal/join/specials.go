package join

import (
	"fmt"
	"slices"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/schema"
)

// Special says AniList entry AniList is the specials Specials of AniDB
// anime Parent, in episode order.
type Special struct {
	Specials []int
	AniList  int
	Parent   int
}

// Skipped is a Special the join did not apply, with why.
type Skipped struct {
	Reason  string `json:"reason"`
	AniList int    `json:"anilist_id"`
}

// ApplySpecials gives each bridged AniList record its parent's TVDB id and
// the TVDB episode each special lands on, as one mapping row. A bridge
// applies only to a record with no AniDB id of its own and the same
// episode count, whose specials all land on one TVDB season; anything
// else is skipped and reported, and the record stays as the join made it.
func ApplySpecials(records []schema.Record, specials []Special, nodes map[int]*animelists.Node) (applied int, skipped []Skipped) {
	for _, sp := range specials {
		i, found := slices.BinarySearchFunc(records, sp.AniList, func(r schema.Record, al int) int { return cmpAniList(r.AniListID, al) })
		if !found {
			skipped = append(skipped, Skipped{AniList: sp.AniList, Reason: "no record for the AniList id"})
			continue
		}
		if reason := bridge(&records[i], sp, nodes[sp.Parent]); reason != "" {
			skipped = append(skipped, Skipped{AniList: sp.AniList, Reason: reason})
			continue
		}
		applied++
	}
	return applied, skipped
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

func bridge(r *schema.Record, sp Special, parent *animelists.Node) string {
	switch {
	case r.AniDBID != 0:
		return fmt.Sprintf("anime-offline-database now links AniDB %d", r.AniDBID)
	case r.Episodes != len(sp.Specials):
		return fmt.Sprintf("the record has %d episodes, the bridge %d specials", r.Episodes, len(sp.Specials))
	case parent == nil || positive(parent.Attr("tvdbid")) == 0:
		return fmt.Sprintf("AniDB %d has no Anime-Lists node with a TVDB series", sp.Parent)
	}
	season := -1
	pairs := make([][]int, 0, len(sp.Specials))
	for i, k := range sp.Specials {
		s, ep, ok := parent.SpecialTVDB(k)
		switch {
		case !ok:
			return fmt.Sprintf("special S%d maps to no single TVDB episode", k)
		case season >= 0 && s != season:
			return "the specials land on more than one TVDB season"
		}
		season = s
		pairs = append(pairs, []int{i + 1, ep})
	}
	r.AniDBParent = &schema.ParentSpecials{AniDBID: sp.Parent, Specials: slices.Clone(sp.Specials)}
	r.TVDBID = positive(parent.Attr("tvdbid"))
	r.TVDBSeason = &season
	r.MappingList = []schema.Row{{AniDBSeason: 1, TVDBSeason: &season, Episodes: pairs}}
	return ""
}
