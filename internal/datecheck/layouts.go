package datecheck

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/strictjson"
	"github.com/cplieger/animap/internal/tmdb"
)

// maxStoreBytes bounds one provider's layouts file. The 4,291 TVDB series
// checked on 2026-10-06 took 10.6 MB without title digests.
const maxStoreBytes = 64 << 20

// RefetchAge is how old a TVDB layout grows before it is fetched again: a
// day short of a week, so the layouts one full pass fetched over its 72
// minutes all come due together on the daily run a week later, not partly
// on the run after it.
const RefetchAge = 6 * 24 * time.Hour

// staggerDays spreads the staggered refetch over a week: an id is due on
// the day its remainder by staggerDays matches the day of the year's.
const staggerDays = 7

// Aired is one provider episode with the digest of its title. Its JSON
// form is the compact [season, episode, absolute, air date, title digest]
// array.
type Aired struct {
	Title string
	skyhook.Episode
}

// MarshalJSON writes the compact array form.
func (a Aired) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{a.Season, a.Number, a.Absolute, a.AirDate, a.Title})
}

// UnmarshalJSON reads the compact array form.
func (a *Aired) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw) != 5 {
		return fmt.Errorf("datecheck: layout episode has %d elements, want 5", len(raw))
	}
	for i, dst := range []*int{&a.Season, &a.Number, &a.Absolute} {
		if err := json.Unmarshal(raw[i], dst); err != nil {
			return err
		}
	}
	if err := json.Unmarshal(raw[3], &a.AirDate); err != nil {
		return err
	}
	return json.Unmarshal(raw[4], &a.Title)
}

// TitleDigest is the first 16 hex characters of the SHA-256 of title,
// trimmed: enough to see a retitle, and no provider text.
func TitleDigest(title string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(title)))
	return hex.EncodeToString(sum[:8])
}

// Layout is one show's episode order as its provider served it at
// Fetched, or Absent when the provider answered 404. A movie's layout has
// no episodes and only says whether it exists.
type Layout struct {
	Fetched  time.Time `json:"fetched"`
	Slug     string    `json:"slug,omitempty"`
	Episodes []Aired   `json:"episodes,omitempty"`
	Absent   bool      `json:"absent,omitempty"`
}

// Official is the layout's episodes without their title digests.
func (l *Layout) Official() []skyhook.Episode {
	out := make([]skyhook.Episode, 0, len(l.Episodes))
	for i := range l.Episodes {
		out = append(out, l.Episodes[i].Episode)
	}
	return out
}

// Layouts keeps every show's layout between runs, by provider id. It holds
// episode numbers, air dates, title digests and the slug; a provider's
// titles and overviews are never kept.
type Layouts map[int]Layout

// Store is one provider's layouts file: its shows, and for TMDB the movies,
// whose ids are another namespace.
type Store struct {
	Series Layouts `json:"series"`
	Movies Layouts `json:"movies,omitempty"`
}

// NewStore is an empty store.
func NewStore() *Store { return &Store{Series: Layouts{}, Movies: Layouts{}} }

// LoadStore reads a layouts file. A missing one is empty; an oversized,
// malformed or older-shaped one is empty with an error, because every
// layout in it can be fetched again; a file it cannot stat or read returns
// nil and the error.
func LoadStore(path string) (*Store, error) {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewStore(), nil
	}
	if err != nil {
		return nil, err
	}
	if st.Size() > maxStoreBytes {
		return NewStore(), fmt.Errorf("datecheck: %s is %d bytes, over %d; starting empty", path, st.Size(), maxStoreBytes)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := NewStore()
	if err := strictjson.Decode(body, maxStoreBytes, s); err != nil {
		return NewStore(), fmt.Errorf("datecheck: %s: %w; starting empty", path, err)
	}
	if s.Series == nil {
		s.Series = Layouts{}
	}
	if s.Movies == nil {
		s.Movies = Layouts{}
	}
	return s, nil
}

// Encode is the store as its file holds it, refused over the file's bound.
func (s *Store) Encode() ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if len(b) > maxStoreBytes {
		return nil, fmt.Errorf("datecheck: layouts are %d bytes, over %d", len(b), maxStoreBytes)
	}
	return b, nil
}

// LayoutOf keeps the part of a SkyHook show the check reads.
func LayoutOf(s *skyhook.Show, fetched time.Time) Layout {
	eps := make([]Aired, 0, len(s.Episodes))
	for _, e := range s.Episodes {
		eps = append(eps, Aired{Episode: e, Title: TitleDigest(s.Titles[[2]int{e.Season, e.Number}])})
	}
	return newLayout(eps, s.Slug, fetched)
}

// LayoutOfTMDB keeps the part of a TMDB show the check reads. TMDB numbers
// no episode absolutely.
func LayoutOfTMDB(s *tmdb.Show, fetched time.Time) Layout {
	eps := make([]Aired, 0, len(s.Episodes))
	for _, e := range s.Episodes {
		eps = append(eps, Aired{Season: e.Season, Number: e.Number, AirDate: e.AirDate, Title: TitleDigest(e.Title)})
	}
	return newLayout(eps, "", fetched)
}

// newLayout sorts eps and drops an air date that is not YYYY-MM-DD, so
// every kept date is safe to compare and to print.
func newLayout(eps []Aired, slug string, fetched time.Time) Layout {
	for i := range eps {
		if _, err := time.Parse(time.DateOnly, eps[i].AirDate); err != nil {
			eps[i].AirDate = ""
		}
	}
	slices.SortFunc(eps, func(x, y Aired) int { return cmp.Or(cmp.Compare(x.Season, y.Season), cmp.Compare(x.Number, y.Number)) })
	return Layout{Fetched: fetched, Slug: slug, Episodes: eps}
}

// Due lists the ids to fetch, in order: those with no layout; then those
// in always, revalidated on every run whatever their age; then those
// older than maxAge, oldest first.
func (l Layouts) Due(ids []int, always map[int]bool, maxAge time.Duration, now time.Time) []int {
	var missing, every, stale []int
	for _, id := range ids {
		lay, ok := l[id]
		switch {
		case !ok:
			missing = append(missing, id)
		case always[id]:
			every = append(every, id)
		case now.Sub(lay.Fetched) > maxAge:
			stale = append(stale, id)
		}
	}
	return l.order(missing, every, stale)
}

// DueStaggered lists the ids to fetch so that no day carries the weekly
// refetch whole: those with no layout; then those whose day it is
// (id mod 7 equals the day of the year mod 7) and that were fetched over a
// day ago; then those a missed day left over a week old, oldest first.
func (l Layouts) DueStaggered(ids []int, now time.Time) []int {
	var missing, today, missed []int
	day := now.UTC().YearDay() % staggerDays
	for _, id := range ids {
		lay, ok := l[id]
		age := now.Sub(lay.Fetched)
		switch {
		case !ok:
			missing = append(missing, id)
		case id%staggerDays == day && age > 24*time.Hour:
			today = append(today, id)
		case age > (staggerDays+1)*24*time.Hour:
			missed = append(missed, id)
		}
	}
	return l.order(missing, today, missed)
}

func (l Layouts) order(missing, next, stale []int) []int {
	slices.Sort(missing)
	slices.Sort(next)
	slices.SortFunc(stale, func(a, b int) int { return cmp.Or(l[a].Fetched.Compare(l[b].Fetched), cmp.Compare(a, b)) })
	return slices.Concat(missing, next, stale)
}

// Prune drops the ids no node maps any more.
func (l Layouts) Prune(keep []int) {
	set := make(map[int]bool, len(keep))
	for _, id := range keep {
		set[id] = true
	}
	maps.DeleteFunc(l, func(id int, _ Layout) bool { return !set[id] })
}
