package datecheck

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/cplieger/animap/internal/skyhook"
)

// maxLayoutsBytes bounds the layouts file. The 4,291 series checked on
// 2026-10-06 took 10.6 MB.
const maxLayoutsBytes = 64 << 20

// RefetchAge is how old a layout grows before it is fetched again: a day
// short of a week, so the layouts one full pass fetched over its 72
// minutes all come due together on the daily run a week later, not partly
// on the run after it.
const RefetchAge = 6 * 24 * time.Hour

// Layout is one series' official order as SkyHook served it at Fetched,
// or Absent when SkyHook answered 404.
type Layout struct {
	Fetched  time.Time         `json:"fetched"`
	Slug     string            `json:"slug,omitempty"`
	Episodes []skyhook.Episode `json:"episodes,omitempty"`
	Absent   bool              `json:"absent,omitempty"`
}

// Layouts keeps every series' layout between runs, by TVDB series id. It
// holds episode numbers, air dates and the slug only; SkyHook's titles and
// overviews are never kept.
type Layouts map[int]Layout

// LoadLayouts reads a layouts file. A missing one is empty; an oversized or
// malformed one is empty with an error, because every layout in it can be
// fetched again; a file it cannot stat or read returns nil and the error.
func LoadLayouts(path string) (Layouts, error) {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Layouts{}, nil
	}
	if err != nil {
		return nil, err
	}
	if st.Size() > maxLayoutsBytes {
		return Layouts{}, fmt.Errorf("datecheck: %s is %d bytes, over %d; starting empty", path, st.Size(), maxLayoutsBytes)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	l := Layouts{}
	if err := json.Unmarshal(body, &l); err != nil {
		return Layouts{}, fmt.Errorf("datecheck: %s: %w; starting empty", path, err)
	}
	return l, nil
}

// LayoutOf keeps the part of a SkyHook show the check reads. An air date
// that is not YYYY-MM-DD is dropped, so every kept date is safe to compare
// and to print.
func LayoutOf(s *skyhook.Show, fetched time.Time) Layout {
	eps := skyhook.Layout(s, allSeasons(s))
	for i := range eps {
		if _, err := time.Parse(time.DateOnly, eps[i].AirDate); err != nil {
			eps[i].AirDate = ""
		}
	}
	return Layout{Fetched: fetched, Slug: s.Slug, Episodes: eps}
}

func allSeasons(s *skyhook.Show) []int {
	set := map[int]bool{}
	for _, e := range s.Episodes {
		set[e.Season] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// Due lists the series to fetch, in order: those with no layout, then those
// whose layout is older than maxAge, oldest first.
func (l Layouts) Due(series []int, maxAge time.Duration, now time.Time) []int {
	var missing, stale []int
	for _, id := range series {
		lay, ok := l[id]
		switch {
		case !ok:
			missing = append(missing, id)
		case now.Sub(lay.Fetched) > maxAge:
			stale = append(stale, id)
		}
	}
	slices.Sort(missing)
	slices.SortFunc(stale, func(a, b int) int { return cmp.Or(l[a].Fetched.Compare(l[b].Fetched), cmp.Compare(a, b)) })
	return append(missing, stale...)
}

// Prune drops the series no node maps any more.
func (l Layouts) Prune(keep []int) {
	set := make(map[int]bool, len(keep))
	for _, id := range keep {
		set[id] = true
	}
	maps.DeleteFunc(l, func(id int, _ Layout) bool { return !set[id] })
}
