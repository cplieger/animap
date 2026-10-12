package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/watch"
)

func tvdb(s, e, abs int, date string) skyhook.Episode {
	return skyhook.Episode{Season: s, Number: e, Absolute: abs, AirDate: date}
}

// dateFixtures writes a mirror listing AniDB 10, 12 and 19 of the fixture
// list, and fresh layouts: AniDB 10's episode 2 lands on the wrong date,
// AniDB 19 matches, and AniDB 12's series 5001 is dead unless dead is false,
// in which case it has no layout at all.
func dateFixtures(t *testing.T, dir string, dead bool) (mirror, layouts string) {
	t.Helper()
	snap := anidb.Snapshot{Commit: mirrorCommit, Refused: []int{}, Anime: map[int]anidb.Anime{
		10: {Regular: []string{"2020-01-06", "2020-01-13"}, Finished: true},
		12: {Regular: []string{"2020-03-02"}, Finished: true},
		19: {Regular: []string{"2020-02-03"}, Finished: true},
	}}
	fresh := time.Now().UTC()
	l := datecheck.Layouts{
		5000: {Fetched: fresh, Slug: "series-5000", Episodes: []skyhook.Episode{
			tvdb(1, 1, 1, "2020-01-06"), tvdb(1, 2, 2, "2020-03-02"), tvdb(1, 3, 3, "2020-01-13"),
		}},
		5004: {Fetched: fresh, Episodes: []skyhook.Episode{tvdb(1, 1, 1, "2020-02-03")}},
	}
	if dead {
		l[5001] = datecheck.Layout{Fetched: fresh, Absent: true}
	}
	return writeFixture(t, dir, "mirror.json", snap), writeFixture(t, dir, "layouts.json", l)
}

func runDate(t *testing.T, dir, mirror, layouts string) dateResult {
	t.Helper()
	out := filepath.Join(dir, "datecheck.json")
	args := []string{"datecheck", "-list", listFixture, "-overlay", t.TempDir(), "-mirror", mirror, "-layouts", layouts, "-budget", "0s", "-out", out}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatalf("datecheck: %v", err)
	}
	var res dateResult
	if err := readJSON(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDateCheckFlagsTheNodesTheDatesContradict(t *testing.T) {
	dir := t.TempDir()
	mirror, layouts := dateFixtures(t, dir, true)
	res := runDate(t, dir, mirror, layouts)
	byID := map[int]dateLead{}
	for _, l := range res.Leads {
		byID[l.AniDB] = l
	}
	if len(byID) != 2 || !byID[12].Dead {
		t.Fatalf("leads = %+v, want AniDB 10 and a dead AniDB 12", res.Leads)
	}
	l10 := byID[10]
	if len(l10.Findings) != 1 || l10.Findings[0].Kind != datecheck.DateMismatch || l10.Findings[0].Target.Number != 2 || l10.Slug != "series-5000" {
		t.Errorf("AniDB 10 lead = %+v, want one date mismatch on S1E2 with the series slug", l10)
	}
	if !slices.Contains(res.Evaluated, "lead:date:19") || !slices.Contains(res.Evaluated, "lead:date:18") ||
		slices.Contains(res.Evaluated, "lead:date:30") || !slices.Contains(res.Owned, "lead:date:30") {
		t.Errorf("evaluated %v, owned %v; want 18 and 19 evaluated, 30 (no AniDB list) owned only", res.Evaluated, res.Owned)
	}
}

func TestDateCheckLeavesANodeWithoutALayoutUnjudged(t *testing.T) {
	dir := t.TempDir()
	mirror, layouts := dateFixtures(t, dir, false)
	res := runDate(t, dir, mirror, layouts)
	if slices.Contains(res.Evaluated, "lead:date:12") || res.Stats.NoLayout != 1 || res.Stats.Due != 1 || res.Stats.Requests != 0 {
		t.Errorf("evaluated %v, stats %+v; want AniDB 12 unevaluated and its series due but not fetched", res.Evaluated, res.Stats)
	}
}

func TestDateCheckLeavesASeriesItCouldNotRefreshUnjudged(t *testing.T) {
	dir := t.TempDir()
	mirror, layouts := dateFixtures(t, dir, true)
	l, err := datecheck.LoadLayouts(layouts)
	if err != nil {
		t.Fatal(err)
	}
	old := l[5004]
	old.Fetched = time.Now().Add(-156 * time.Hour)
	l[5004] = old
	res := runDate(t, dir, mirror, writeFixture(t, dir, "layouts.json", l))
	if slices.Contains(res.Evaluated, "lead:date:19") || res.Stats.Stale != 1 {
		t.Fatalf("evaluated %v, stats %+v; want AniDB 19 unjudged on its stale layout", res.Evaluated, res.Stats)
	}
	existing := []issues.Issue{{Number: 7, State: "OPEN", Title: "lead", Body: issues.Marker("lead:date:19")}}
	args := []string{
		"issues", "-out", filepath.Join(dir, "actions.json"), "-existing", writeFixture(t, dir, "existing.json", existing),
		"-datecheck", writeFixture(t, dir, "result.json", res),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, a := range planned(t, filepath.Join(dir, "actions.json")) {
		if a.Number == 7 {
			t.Errorf("planned %+v for AniDB 19's open lead, want it untouched while its series is stale", a)
		}
	}
}

func TestIssuesSpreadLeadEditsAndClosesOverRuns(t *testing.T) {
	dir := t.TempDir()
	var existing []issues.Issue
	dr := dateResult{}
	for i := range 6 {
		k := datecheck.Key(i + 1)
		existing = append(existing, issues.Issue{Number: i + 1, State: "OPEN", Title: "old", Body: "old\n\n" + issues.Marker(k)})
		dr.Evaluated = append(dr.Evaluated, k)
		if i < 3 {
			dr.Leads = append(dr.Leads, lead(i+1))
		}
	}
	args := []string{
		"issues", "-lead-limit", "2", "-out", filepath.Join(dir, "actions.json"),
		"-existing", writeFixture(t, dir, "existing.json", existing), "-datecheck", writeFixture(t, dir, "datecheck.json", dr),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	count := map[issues.Kind]int{}
	for _, a := range planned(t, filepath.Join(dir, "actions.json")) {
		count[a.Kind]++
	}
	if count[issues.Edit] != 2 || count[issues.Close] != 2 || len(count) != 2 {
		t.Errorf("planned %v, want 2 edits and 2 closes of the 3 each due under a lead limit of 2", count)
	}
}

func lead(aid int) dateLead {
	return dateLead{Node: "anime tvdbid=1", AniDB: aid, Series: 1, Dead: true}
}

func TestIssuesPlanLeadsUnderTheirOwnLimit(t *testing.T) {
	dir := t.TempDir()
	var gaps []watch.Entry
	for i := range 30 {
		gaps = append(gaps, watch.Entry{AniListID: i + 1, Type: "TV", Gaps: []watch.Gap{watch.NoAniDB}})
	}
	wr := watchResult{Set: "seadex", EntryURL: "https://releases.moe/{id}", Complete: true, Watched: 30, Gaps: gaps, Classification: watch.Classification{New: gaps}}
	var dr dateResult
	for i := range 30 {
		dr.Leads = append(dr.Leads, lead(100+i))
		dr.Evaluated = append(dr.Evaluated, datecheck.Key(100+i))
	}
	args := []string{
		"issues", "-limit", "10", "-lead-limit", "5", "-out", filepath.Join(dir, "actions.json"),
		"-existing", writeFixture(t, dir, "existing.json", []issues.Issue{}),
		"-watch", writeFixture(t, dir, "watch.json", wr), "-datecheck", writeFixture(t, dir, "datecheck.json", dr),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var leads, others int
	for _, a := range planned(t, filepath.Join(dir, "actions.json")) {
		switch {
		case a.Kind != issues.Create, a.Key == dashboardKey:
		case strings.HasPrefix(a.Key, datecheck.Prefix):
			leads++
		default:
			others++
		}
	}
	if leads != 5 || others != 10 {
		t.Errorf("created %d leads and %d other issues, want 5 and 10: each limit is spent apart", leads, others)
	}
}

func TestIssuesOpenTheSurestLeadsFirst(t *testing.T) {
	dir := t.TempDir()
	mismatch, missing, dead := lead(1), lead(2), lead(3)
	mismatch.Dead, missing.Dead = false, false
	mismatch.Findings = []datecheck.Finding{{Kind: datecheck.DateMismatch}}
	missing.Findings = []datecheck.Finding{{Kind: datecheck.DateMismatch}, {Kind: datecheck.MissingTarget}}
	dr := dateResult{Leads: []dateLead{mismatch, missing, dead}}
	args := []string{
		"issues", "-lead-limit", "2", "-out", filepath.Join(dir, "actions.json"),
		"-existing", writeFixture(t, dir, "existing.json", []issues.Issue{}), "-datecheck", writeFixture(t, dir, "datecheck.json", dr),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var created []string
	for _, a := range planned(t, filepath.Join(dir, "actions.json")) {
		created = append(created, a.Key)
	}
	if want := []string{"lead:date:3", "lead:date:2"}; !slices.Equal(created, want) {
		t.Errorf("created %v, want %v: a dead series, then a missing target, before a date mismatch", created, want)
	}
}

func TestIssuesCloseALeadOnlyWhereTheDateCheckLooked(t *testing.T) {
	open := func(n int, key string) issues.Issue {
		return issues.Issue{Number: n, State: "OPEN", Title: key, Body: issues.Marker(key)}
	}
	existing := []issues.Issue{
		open(1, "lead:date:1"), open(2, "lead:date:2"), open(3, "lead:date:3"), open(4, "drift:node:4"),
	}
	dir := t.TempDir()
	dr := dateResult{Evaluated: []string{"lead:date:1"}, Owned: []string{"lead:date:1", "lead:date:2"}}
	args := []string{
		"issues", "-out", filepath.Join(dir, "actions.json"), "-existing", writeFixture(t, dir, "existing.json", existing),
		"-datecheck", writeFixture(t, dir, "datecheck.json", dr),
		"-drift", writeFixture(t, dir, "drift.json", driftResult{Evaluated: []string{"drift:node:9"}}),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var closed []int
	for _, a := range planned(t, filepath.Join(dir, "actions.json")) {
		if a.Kind == issues.Close {
			closed = append(closed, a.Number)
		}
	}
	slices.Sort(closed)
	if want := []int{1, 3}; !slices.Equal(closed, want) {
		t.Errorf("closed %v, want %v: a lead closes when its node passed or left the list, never while its node went unjudged, and never a drift issue", closed, want)
	}
}

func TestLeadWantBody(t *testing.T) {
	l := &dateLead{
		Node: "anime defaulttvdbseason=1 tvdbid=79881\nmapping anidbseason=1 tvdbseason=1 text=;8-9;`@here`", Overlay: "overlay/3195.json", Slug: "kamichu",
		AniDB: 3195, Series: 79881, Findings: []datecheck.Finding{
			{
				Kind: datecheck.DateMismatch, Episode: datecheck.Episode{Number: 8, AirDate: "2005-08-31"},
				Target: datecheck.Target{Season: 1, Number: 9, AirDate: "2005-09-07"}, Carriers: []skyhook.Episode{tvdb(1, 8, 8, "2005-08-31")},
			},
			{Kind: datecheck.MissingTarget, Episode: datecheck.Episode{Number: 1, AirDate: "2005-11-23", Special: true}, Target: datecheck.Target{Season: 1, Number: 11}},
			{Kind: datecheck.OutOfOrder, Episode: datecheck.Episode{Number: 23, AirDate: "1984-06-24"}, Target: datecheck.Target{Season: 1, Number: 23, AirDate: "1983-08-23"}},
		},
	}
	w := leadWant(l)
	for _, s := range []string{
		"| 8 | 2005-08-31 | S1E9 | 2005-09-07 | dates differ | S1E8 |",
		"| S1 | 2005-11-23 | S1E11 | none | not on TVDB | none |",
		"| 23 | 1984-06-24 | S1E23 | 1983-08-23 | out of order | none |",
		"https://anidb.net/anime/3195", "https://thetvdb.com/series/kamichu/seasons/official/1",
		"with `overlay/3195.json` applied", "text=;8-9;'@here'", "docs/date-check.md",
	} {
		if !strings.Contains(w.Body, s) {
			t.Errorf("leadWant body lacks %q:\n%s", s, w.Body)
		}
	}
	if w.Key != "lead:date:3195" || !slices.Equal(w.Labels, []string{labelAnimap, labelLead}) || strings.Contains(w.Body, "`@here`") {
		t.Errorf("leadWant = key %q labels %v, body %q; want the lead key, both labels and no live mention", w.Key, w.Labels, w.Body)
	}
	l.Slug = "../evil"
	if body := leadWant(l).Body; !strings.Contains(body, "https://thetvdb.com/dereferrer/series/79881") || strings.Contains(body, "evil") {
		t.Errorf("leadWant with a bad slug links %q, want the id link only", body)
	}
}

func TestLeadWantDeadSeries(t *testing.T) {
	l := lead(4571)
	l.Series = 178381
	w := leadWant(&l)
	if !strings.Contains(w.Title, "TVDB 178381, which SkyHook no longer serves") || !strings.Contains(w.Body, "https://thetvdb.com/dereferrer/series/178381") ||
		strings.Contains(w.Body, "| AniDB episode |") {
		t.Errorf("dead-series want = %q / %q, want the dead title, the id link and no table", w.Title, w.Body)
	}
}

func TestLeadWantListsAtMostTheCap(t *testing.T) {
	l := lead(1)
	l.Dead = false
	for i := range maxListedLines + 5 {
		l.Findings = append(l.Findings, datecheck.Finding{Kind: datecheck.MissingTarget, Episode: datecheck.Episode{Number: i + 1}, Target: datecheck.Target{Season: 1, Number: i + 1}})
	}
	body := leadWant(&l).Body
	if n := strings.Count(body, "| not on TVDB |"); n != maxListedLines || !strings.Contains(body, fmt.Sprintf("... %d more", 5)) {
		t.Errorf("listed %d findings, want %d and a line naming the 5 more", n, maxListedLines)
	}
}
