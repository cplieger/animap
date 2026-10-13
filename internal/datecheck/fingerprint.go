package datecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

// Fingerprints are SHA-256 digests of one node's inputs to Check. Node is
// the overlay-capture digest of the node as checked, which leaves its title
// and notes out. TVDB covers the official order on every season Check can
// read for the node and AniDB the episode list, each with its episode-title
// digests.
type Fingerprints struct {
	Node  string `json:"node_sha256"`
	TVDB  string `json:"tvdb_sha256"`
	AniDB string `json:"anidb_sha256"`
}

// FingerprintsOf fingerprints patched node n with AniDB list a and its
// series layout. The node digest is the one overlay capture writes.
func FingerprintsOf(n *animelists.Node, a anidb.Anime, lay *Layout) Fingerprints {
	return Fingerprints{Node: animelists.HashOf(n), TVDB: lay.hash(fingerprintSeasons(n, a, lay)), AniDB: a.Hash()}
}

// fingerprintSeasons are every season Check can read for n. A regular AniDB
// episode makes that the whole series, because carriers and
// keepsAniDBCalendar look for its date on every season. Otherwise they are
// the seasons the node maps onto with 0 for specials, or every season for
// an absolute node, whose specials Check places on season 0.
func fingerprintSeasons(n *animelists.Node, a anidb.Anime, lay *Layout) []int {
	touched := overlay.TouchedSeasons(n, len(a.Specials) > 0)
	if touched != nil && len(a.Regular) == 0 {
		return touched
	}
	set := map[int]bool{}
	for _, s := range touched {
		set[s] = true
	}
	for _, e := range lay.Episodes {
		set[e.Season] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// hash digests whether SkyHook serves the series, then the episodes and
// title digests of seasons, so an absent series never hashes like an empty
// official order.
func (lay *Layout) hash(seasons []int) string {
	h := sha256.New()
	eps := skyhook.Layout(&skyhook.Show{Episodes: lay.Episodes}, seasons)
	fmt.Fprintf(h, "absent=%t\n%s\n", lay.Absent, skyhook.Hash(eps))
	for _, s := range seasons {
		fmt.Fprintf(h, "T%d %s\n", s, lay.Titles[s])
	}
	return hex.EncodeToString(h.Sum(nil))
}

var findingKeyRE = regexp.MustCompile(`^(` + string(MissingTarget) + `|` + string(DateMismatch) + `|` + string(OutOfOrder) + `):S?[1-9]\d*$`)

// Key names f by kind and AniDB episode, S before a special's number, so
// two targets of one episode failing alike share a key.
func (f *Finding) Key() string {
	ep := strconv.Itoa(f.Episode.Number)
	if f.Episode.Special {
		ep = "S" + ep
	}
	return string(f.Kind) + ":" + ep
}

// FindingKeys are the keys of fs, sorted, each once.
func FindingKeys(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for i := range fs {
		out = append(out, fs[i].Key())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ValidFindingKey reports whether k has the shape Key writes.
func ValidFindingKey(k string) bool { return findingKeyRE.MatchString(k) }
