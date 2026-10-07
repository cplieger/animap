package guard

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/animap/internal/strictjson"
)

// ErrBaselineGrew reports a baseline entry the previous baseline did not
// have: the baseline only shrinks.
var ErrBaselineGrew = errors.New("guard: the collision baseline gained an entry")

// BaselineVersion is the file format version.
const BaselineVersion = 1

// Baseline is the tracked set of collisions the overlay leaves as
// Anime-Lists has them, and of nodes with no regular episode count on
// series no overlay entry touches. An item in it is
// reported and does not block; every other one blocks. Basis names the
// episode-count precedence that measured it: a collision exists only
// relative to the counts that placed it, so a baseline is re-derived when
// the basis changes and only shrinks while it does not.
type Baseline struct {
	AnimeListsCommit string          `json:"anime_lists_commit"`
	Basis            string          `json:"basis"`
	Collisions       []BaselineEntry `json:"collisions"`
	Uncounted        []Uncounted     `json:"uncounted"`
	Version          int             `json:"version"`
}

// BaselineEntry is one baselined collision: a target on a series and the
// nodes that claimed it.
type BaselineEntry struct {
	Target string `json:"target"`
	Nodes  []int  `json:"nodes"`
	Series int    `json:"series"`
}

func (e BaselineEntry) key() string { return fmt.Sprint(e.Series, "|", e.Target) }

// covers reports whether e accepts c: the same target on the same series,
// claimed by no node e did not record.
func (e BaselineEntry) covers(c Collision) bool {
	if e.Series != c.Series || e.Target != c.Target {
		return false
	}
	for _, n := range c.Nodes {
		if !slices.Contains(e.Nodes, n) {
			return false
		}
	}
	return true
}

var basisRE = regexp.MustCompile(`^[a-z0-9-]+(/[a-z0-9-]+)*$`)

// LoadBaseline reads a baseline strictly; a missing file is an empty one.
// An empty basis is a file written before the field existed.
func LoadBaseline(path string) (*Baseline, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Baseline{Version: BaselineVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var b Baseline
	if err := strictjson.Decode(body, &b); err != nil {
		return nil, fmt.Errorf("guard: baseline %s: %w", path, err)
	}
	if b.Version != BaselineVersion {
		return nil, fmt.Errorf("guard: baseline %s: version %d, want %d", path, b.Version, BaselineVersion)
	}
	if b.Basis != "" && !basisRE.MatchString(b.Basis) {
		return nil, fmt.Errorf("guard: baseline %s: basis %q is not lowercase words joined by - and /", path, b.Basis)
	}
	return &b, nil
}

// NewBaseline records cols and unc, measured under basis, sorted by series.
func NewBaseline(cols []Collision, unc []Uncounted, commit, basis string) *Baseline {
	b := &Baseline{Version: BaselineVersion, AnimeListsCommit: commit, Basis: basis, Uncounted: slices.Clone(unc)}
	for _, c := range cols {
		b.Collisions = append(b.Collisions, BaselineEntry{Series: c.Series, Target: c.Target, Nodes: slices.Clone(c.Nodes)})
	}
	slices.SortFunc(b.Collisions, func(x, y BaselineEntry) int {
		return cmp.Or(cmp.Compare(x.Series, y.Series), strings.Compare(x.Target, y.Target))
	})
	slices.SortFunc(b.Uncounted, compareUncounted)
	b.Uncounted = slices.Compact(b.Uncounted)
	return b
}

func compareUncounted(x, y Uncounted) int {
	return cmp.Or(cmp.Compare(x.Series, y.Series), cmp.Compare(x.AniDB, y.AniDB))
}

// Split sorts upstream collisions into those the baseline accepts and
// those it does not, and lists the baseline entries no longer seen, which
// are fixed and can leave the file.
func (b *Baseline) Split(upstream []Collision) (baselined, novel []Collision, stale []BaselineEntry) {
	byKey := map[string]BaselineEntry{}
	for _, e := range b.Collisions {
		byKey[e.key()] = e
	}
	seen := map[string]bool{}
	for _, c := range upstream {
		k := BaselineEntry{Series: c.Series, Target: c.Target}.key()
		if e, ok := byKey[k]; ok && e.covers(c) {
			baselined = append(baselined, c)
			seen[k] = true
			continue
		}
		novel = append(novel, c)
	}
	for _, e := range b.Collisions {
		if !seen[e.key()] {
			stale = append(stale, e)
		}
	}
	return baselined, novel, stale
}

// SplitUncounted is Split for nodes with no regular episode count.
func (b *Baseline) SplitUncounted(upstream []Uncounted) (baselined, novel, stale []Uncounted) {
	seen := map[Uncounted]bool{}
	for _, u := range upstream {
		if slices.Contains(b.Uncounted, u) {
			baselined = append(baselined, u)
			seen[u] = true
			continue
		}
		novel = append(novel, u)
	}
	for _, u := range b.Uncounted {
		if !seen[u] {
			stale = append(stale, u)
		}
	}
	return baselined, novel, stale
}

// Prune keeps the entries that are still seen in upstream and unc, each
// collision narrowed to the nodes that still claim it.
func (b *Baseline) Prune(upstream []Collision, unc []Uncounted) *Baseline {
	baselined, _, _ := b.Split(upstream)
	counted, _, _ := b.SplitUncounted(unc)
	return NewBaseline(baselined, counted, b.AnimeListsCommit, b.Basis)
}

// CheckShrink fails when head has an entry base does not, or names a
// node base did not record for that entry. A head measured under another
// basis than base is a re-derivation, and any entry may differ.
func CheckShrink(base, head *Baseline) error {
	if base.Basis != head.Basis {
		return nil
	}
	byKey := map[string]BaselineEntry{}
	for _, e := range base.Collisions {
		byKey[e.key()] = e
	}
	var grew []string
	for _, e := range head.Collisions {
		prev, ok := byKey[e.key()]
		if !ok || !prev.covers(Collision{Series: e.Series, Target: e.Target, Nodes: e.Nodes}) {
			grew = append(grew, e.key())
		}
	}
	for _, u := range head.Uncounted {
		if !slices.Contains(base.Uncounted, u) {
			grew = append(grew, fmt.Sprint(u.Series, "|uncounted AniDB ", u.AniDB))
		}
	}
	if len(grew) > 0 {
		return fmt.Errorf("%w: %s", ErrBaselineGrew, strings.Join(grew, ", "))
	}
	return nil
}
