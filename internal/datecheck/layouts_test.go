package datecheck

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/tmdb"
)

func TestDueFetchesMissingFirstThenAlwaysThenOldest(t *testing.T) {
	l := Layouts{
		1: {Fetched: now.Add(-1 * time.Hour)},
		2: {Fetched: now.Add(-10 * 24 * time.Hour)},
		3: {Fetched: now.Add(-30 * 24 * time.Hour), Absent: true},
		4: {Fetched: now.Add(-8 * 24 * time.Hour)},
		6: {Fetched: now.Add(-2 * time.Hour)},
		8: {Fetched: now.Add(-9 * 24 * time.Hour)},
		9: {Fetched: now.Add(-time.Minute)},
	}
	got := l.Due([]int{1, 2, 3, 4, 7, 5, 6, 8, 9}, map[int]bool{6: true, 8: true, 9: true}, 7*24*time.Hour, now)
	if want := []int{5, 7, 6, 8, 9, 3, 2, 4}; !slices.Equal(got, want) {
		t.Errorf("Due = %v, want %v (missing by id, then every revalidated one by id however fresh, then stale oldest first; fresh left out)", got, want)
	}
}

func TestDueRefetchesAFullPassOnTheDailyRunAWeekLater(t *testing.T) {
	l := Layouts{}
	var series []int
	for i := range 73 {
		l[i] = Layout{Fetched: now.Add(time.Duration(i) * time.Minute)}
		series = append(series, i)
	}
	if got := l.Due(series, nil, RefetchAge, now.Add(6*24*time.Hour)); len(got) != 0 {
		t.Errorf("Due six days after the pass = %d series, want none", len(got))
	}
	if got := l.Due(series, nil, RefetchAge, now.Add(7*24*time.Hour)); len(got) != len(series) {
		t.Errorf("Due seven days after the pass = %d series, want all %d", len(got), len(series))
	}
}

func TestDueStaggeredSpreadsAWeekOverItsDays(t *testing.T) {
	l := Layouts{}
	var ids []int
	for id := 1; id <= 70; id++ {
		l[id] = Layout{Fetched: now.Add(-time.Hour)}
		ids = append(ids, id)
	}
	seen := map[int]int{}
	for day := 1; day <= 7; day++ {
		at := now.Add(time.Duration(day) * 24 * time.Hour)
		due := l.DueStaggered(ids, at)
		if len(due) != 10 {
			t.Errorf("day %d: %d due, want a seventh of 70", day, len(due))
		}
		for _, id := range due {
			seen[id]++
			l[id] = Layout{Fetched: at}
		}
	}
	if len(seen) != 70 {
		t.Errorf("a week refetched %d of 70 ids, want every one once", len(seen))
	}
	t.Run("missing first, then a missed day", func(t *testing.T) {
		at := now.Add(30 * 24 * time.Hour)
		today := at.YearDay()%staggerDays + staggerDays*10
		missed, fresh := today+1, today+2
		m := Layouts{
			today:  {Fetched: at.Add(-2 * 24 * time.Hour)},
			missed: {Fetched: at.Add(-9 * 24 * time.Hour)},
			fresh:  {Fetched: at.Add(-2 * 24 * time.Hour)},
		}
		got := m.DueStaggered([]int{missed, fresh, 999, today}, at)
		if want := []int{999, today, missed}; !slices.Equal(got, want) {
			t.Errorf("DueStaggered = %v, want %v", got, want)
		}
	})
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
	if !slices.Equal(got.Official(), want) || got.Slug != "kamichu" || !got.Fetched.Equal(now) {
		t.Errorf("LayoutOf = %+v, want episodes %v sorted with the bad date dropped", got, want)
	}
}

func TestLayoutsKeepTitleDigestsNotTitles(t *testing.T) {
	s := &skyhook.Show{
		Episodes: []skyhook.Episode{ep(0, 1, 0, ""), ep(1, 1, 1, "2005-08-10")},
		Titles:   map[[2]int]string{{0, 1}: "Kamichu Special", {1, 1}: "Kamichu First"},
	}
	tv := LayoutOf(s, now)
	if tv.Episodes[1].Title != TitleDigest("Kamichu First") {
		t.Errorf("LayoutOf title = %q, want the digest of the episode's title", tv.Episodes[1].Title)
	}
	tm := LayoutOfTMDB(&tmdb.Show{Episodes: []tmdb.Episode{{Season: 1, Number: 1, AirDate: "2005-08-10", Title: "Kamichu First"}}}, now)
	if tm.Episodes[0].Title != TitleDigest("Kamichu First") || tm.Episodes[0].Absolute != 0 {
		t.Errorf("LayoutOfTMDB = %+v, want the title digest and no absolute number", tm.Episodes[0])
	}
	st := NewStore()
	st.Series[1], st.Movies[1] = tv, tm
	b, err := st.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("Kamichu")) {
		t.Errorf("a kept layout holds a provider title: %s", b)
	}
}

func TestLoadStore(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if s, err := LoadStore(filepath.Join(dir, "absent.json")); err != nil || len(s.Series) != 0 || s.Movies == nil {
		t.Errorf("LoadStore(absent) = %v, %v; want empty, nil", s, err)
	}
	for name, body := range map[string]string{
		"garbage":        "{not json",
		"older shape":    `{"79881":{"fetched":"2026-10-06T00:00:00Z","titles":{"1":"x"}}}`,
		"four-item rows": `{"series":{"1":{"fetched":"2026-10-06T00:00:00Z","episodes":[[1,1,1,"2005-08-10"]]}}}`,
	} {
		if s, err := LoadStore(write(name, []byte(body))); err == nil || s == nil || len(s.Series) != 0 {
			t.Errorf("LoadStore(%s) = %v, %v; want empty and an error", name, s, err)
		}
	}
	in := NewStore()
	in.Series[79881] = Layout{Fetched: now, Slug: "kamichu", Episodes: []Aired{{Episode: ep(1, 1, 1, "2005-08-10"), Title: "0123456789abcdef"}}}
	in.Movies[5] = Layout{Fetched: now, Absent: true}
	b, err := in.Encode()
	if err != nil {
		t.Fatal(err)
	}
	s, err := LoadStore(write("good.json", b))
	if err != nil || s.Series[79881].Slug != "kamichu" || !slices.Equal(s.Series[79881].Episodes, in.Series[79881].Episodes) || !s.Movies[5].Absent {
		t.Errorf("LoadStore(round trip) = %+v, %v; want %+v", s, err, in)
	}
}
