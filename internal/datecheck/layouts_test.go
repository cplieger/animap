package datecheck

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/skyhook"
)

func TestDueFetchesMissingFirstThenOldest(t *testing.T) {
	l := Layouts{
		1: {Fetched: now.Add(-1 * time.Hour)},
		2: {Fetched: now.Add(-10 * 24 * time.Hour)},
		3: {Fetched: now.Add(-30 * 24 * time.Hour), Absent: true},
		4: {Fetched: now.Add(-8 * 24 * time.Hour)},
	}
	got := l.Due([]int{1, 2, 3, 4, 7, 5}, 7*24*time.Hour, now)
	if want := []int{5, 7, 3, 2, 4}; !slices.Equal(got, want) {
		t.Errorf("Due = %v, want %v (missing by id, then stale oldest first, fresh left out)", got, want)
	}
}

// A fresh layout kept without title digests cannot be fingerprinted like a
// new fetch, so it is fetched again before any of its nodes is judged.
func TestDueFetchesAFreshLayoutWithoutTitleDigests(t *testing.T) {
	eps := []skyhook.Episode{ep(1, 1, 1, "2005-08-10")}
	l := Layouts{
		1: {Fetched: now, Episodes: eps},
		2: {Fetched: now, Episodes: eps, Titles: map[int]string{1: "digest"}},
		3: {Fetched: now, Absent: true},
		4: {Fetched: now},
		6: {Fetched: now.Add(-8 * 24 * time.Hour), Episodes: eps, Titles: map[int]string{1: "digest"}},
	}
	if got := l.Due([]int{1, 2, 3, 4, 5, 6}, RefetchAge, now); !slices.Equal(got, []int{1, 5, 6}) {
		t.Errorf("Due = %v, want [1 5 6]: the layout with episodes and no title digests among the missing, then the stale", got)
	}
}

func TestDueRefetchesAFullPassOnTheDailyRunAWeekLater(t *testing.T) {
	l := Layouts{}
	var series []int
	for i := range 73 {
		l[i] = Layout{Fetched: now.Add(time.Duration(i) * time.Minute)}
		series = append(series, i)
	}
	if got := l.Due(series, RefetchAge, now.Add(6*24*time.Hour)); len(got) != 0 {
		t.Errorf("Due six days after the pass = %d series, want none", len(got))
	}
	if got := l.Due(series, RefetchAge, now.Add(7*24*time.Hour)); len(got) != len(series) {
		t.Errorf("Due seven days after the pass = %d series, want all %d", len(got), len(series))
	}
}

func TestPruneKeepsOnlyMappedSeries(t *testing.T) {
	l := Layouts{1: {}, 2: {}, 3: {}}
	l.Prune([]int{2, 9})
	if _, ok := l[2]; len(l) != 1 || !ok {
		t.Errorf("Prune([2 9]) left %v, want only series 2", l)
	}
}

func TestLayoutOfDropsAnUnparseableDate(t *testing.T) {
	s := &skyhook.Show{Slug: "kamichu", Episodes: []skyhook.Episode{
		ep(1, 2, 2, "2005-08-17"), ep(0, 1, 0, "| injected |"), ep(1, 1, 1, "2005-08-10"),
	}}
	got := LayoutOf(s, now)
	want := []skyhook.Episode{ep(0, 1, 0, ""), ep(1, 1, 1, "2005-08-10"), ep(1, 2, 2, "2005-08-17")}
	if !slices.Equal(got.Episodes, want) || got.Slug != "kamichu" || !got.Fetched.Equal(now) {
		t.Errorf("LayoutOf = %+v, want episodes %v sorted with the bad date dropped", got, want)
	}
}

func TestLayoutOfKeepsTitleDigestsNotTitles(t *testing.T) {
	s := &skyhook.Show{
		Episodes: []skyhook.Episode{ep(0, 1, 0, ""), ep(1, 1, 1, "2005-08-10")},
		Titles:   map[[2]int]string{{0, 1}: "Kamichu Special", {1, 1}: "Kamichu First"},
	}
	got := LayoutOf(s, now)
	if want := s.TitleHashes(); !maps.Equal(got.Titles, want) || len(want) != 2 {
		t.Errorf("LayoutOf titles = %v, want the show's season digests %v", got.Titles, want)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("Kamichu")) {
		t.Errorf("a kept layout holds a TVDB title: %s", b)
	}
}

func TestLoadLayouts(t *testing.T) {
	dir := t.TempDir()
	if l, err := LoadLayouts(filepath.Join(dir, "absent.json")); err != nil || len(l) != 0 {
		t.Errorf("LoadLayouts(absent) = %v, %v; want empty, nil", l, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := LoadLayouts(bad); err == nil || l == nil || len(l) != 0 {
		t.Errorf("LoadLayouts(garbage) = %v, %v; want empty layouts and an error", l, err)
	}
	good := filepath.Join(dir, "good.json")
	in := Layouts{79881: {Fetched: now, Slug: "kamichu", Episodes: []skyhook.Episode{ep(1, 1, 1, "2005-08-10")}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, b, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := LoadLayouts(good)
	if err != nil || len(l) != 1 || l[79881].Slug != "kamichu" || !slices.Equal(l[79881].Episodes, in[79881].Episodes) {
		t.Errorf("LoadLayouts(round trip) = %+v, %v; want %+v", l, err, in)
	}
}
