package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/reviewed"
	"github.com/cplieger/atomicfile/v4"
)

const reviewReason = `AniDB 2 "Second" is TVDB 1x02 "Second" by title; TVDB dates it from a later broadcast.`

func checkDates(t *testing.T, dir, list, mirror, layouts string) string {
	t.Helper()
	out := filepath.Join(dir, "datecheck.json")
	args := []string{"datecheck", "-list", list, "-overlay", t.TempDir(), "-mirror", mirror, "-layouts", layouts, "-budget", "0s", "-out", out}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatalf("datecheck: %v", err)
	}
	return out
}

func accept(t *testing.T, datePath, recordDir string, extra ...string) error {
	t.Helper()
	args := []string{"datecheck", "accept", "-datecheck", datePath, "-dir", recordDir}
	return run(t.Context(), append(args, extra...), &bytes.Buffer{})
}

func planIssues(t *testing.T, dir, datePath, recordDir string, existing []issues.Issue) []issues.Action {
	t.Helper()
	out := filepath.Join(dir, "actions.json")
	args := []string{
		"issues", "-out", out, "-existing", writeFixture(t, dir, "existing.json", existing),
		"-datecheck", datePath, "-reviewed", recordDir,
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatalf("issues: %v", err)
	}
	return planned(t, out)
}

func replaceOnce(t *testing.T, src, dir, name, old, new string) string {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(b, []byte(old)); n != 1 {
		t.Fatalf("%s holds %q %d times, want once", src, old, n)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, bytes.Replace(b, []byte(old), []byte(new), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func actionsFor(actions []issues.Action, key string) []issues.Action {
	var out []issues.Action
	for _, a := range actions {
		if a.Key == key {
			out = append(out, a)
		}
	}
	return out
}

func TestIssuesCloseAReviewedLead(t *testing.T) {
	dir, records := t.TempDir(), t.TempDir()
	mirror, layouts := dateFixtures(t, dir, true)
	dc := checkDates(t, dir, listFixture, mirror, layouts)
	if err := accept(t, dc, records, "-id", "10", "-reason", reviewReason, "-evidence", "https://anidb.net/anime/10"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	key := datecheck.Key(10)
	t.Run("an open issue closes with a comment naming the record", func(t *testing.T) {
		existing := []issues.Issue{{Number: 5, State: "OPEN", Title: "lead", Body: issues.Marker(key)}}
		got := actionsFor(planIssues(t, dir, dc, records, existing), key)
		if len(got) != 1 || got[0].Kind != issues.Close || got[0].Number != 5 || !strings.Contains(got[0].Comment, "`reviewed/10.json`") {
			t.Errorf("planned %+v for the reviewed lead's open issue, want one close commenting on reviewed/10.json", got)
		}
	})
	t.Run("an issue closed by hand stays closed", func(t *testing.T) {
		existing := []issues.Issue{{Number: 5, State: "CLOSED", Title: "lead", Body: issues.Marker(key)}}
		if got := actionsFor(planIssues(t, dir, dc, records, existing), key); len(got) != 0 {
			t.Errorf("planned %+v for the reviewed lead's closed issue, want nothing", got)
		}
	})
	t.Run("without the record the closed issue reopens", func(t *testing.T) {
		existing := []issues.Issue{{Number: 5, State: "CLOSED", Title: "lead", Body: issues.Marker(key)}}
		got := actionsFor(planIssues(t, dir, dc, t.TempDir(), existing), key)
		if len(got) != 1 || got[0].Number != 5 || got[0].Kind == issues.Close || got[0].Kind == issues.Edit {
			t.Errorf("planned %+v with no record, want issue 5 reopened", got)
		}
	})
	t.Run("an open issue without the record stays open", func(t *testing.T) {
		existing := []issues.Issue{{Number: 5, State: "OPEN", Title: "lead", Body: issues.Marker(key)}}
		for _, a := range actionsFor(planIssues(t, dir, dc, t.TempDir(), existing), key) {
			if a.Kind == issues.Close {
				t.Errorf("planned %+v with no record, want the open lead kept open", a)
			}
		}
	})
}

// A one-byte change in any input the record fingerprints, or a finding the
// record does not list, raises the lead again and says why.
func TestIssuesRaiseAReviewedLeadAgainWhenItsInputsMove(t *testing.T) {
	dir, records := t.TempDir(), t.TempDir()
	mirror, layouts := dateFixtures(t, dir, true)
	dc := checkDates(t, dir, listFixture, mirror, layouts)
	if err := accept(t, dc, records, "-id", "10", "-reason", reviewReason, "-evidence", "https://anidb.net/anime/10"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	today := time.Now().UTC().Format(time.DateOnly)
	causes := map[string]string{
		"node": "the node changed", "tvdb": "TVDB's official order changed",
		"anidb": "AniDB's episode list changed", "findings": "a finding the record does not list appeared",
	}
	for _, tc := range []struct {
		name  string
		cause string
		redo  func(t *testing.T, dir string) string
	}{
		{"one byte of the node", "node", func(t *testing.T, d string) string {
			return checkDates(t, d, replaceOnce(t, listFixture, d, "list.xml", `tmdbtv="9000"`, `tmdbtv="9001"`), mirror, layouts)
		}},
		{"one byte of TVDB's order", "tvdb", func(t *testing.T, d string) string {
			return checkDates(t, d, listFixture, mirror, replaceOnce(t, layouts, d, "layouts.json", `"2020-01-06"`, `"2020-01-07"`))
		}},
		{"one byte of AniDB's list", "anidb", func(t *testing.T, d string) string {
			return checkDates(t, d, listFixture, replaceOnce(t, mirror, d, "mirror.json", `"2020-01-06"`, `"2020-01-07"`), layouts)
		}},
		{"one byte of TVDB's title digest", "tvdb", func(t *testing.T, d string) string {
			return checkDates(t, d, listFixture, mirror, replaceOnce(t, layouts, d, "layouts.json", fixtureTVDBTitles, fixtureTVDBTitles[1:]+"0"))
		}},
		{"one byte of AniDB's title digest", "anidb", func(t *testing.T, d string) string {
			return checkDates(t, d, listFixture, replaceOnce(t, mirror, d, "mirror.json", fixtureAniDBTitles, fixtureAniDBTitles[1:]+"0"), layouts)
		}},
		{"the series going dead", "tvdb", func(t *testing.T, d string) string {
			var l datecheck.Layouts
			if err := readJSON(layouts, &l); err != nil {
				t.Fatal(err)
			}
			l[5000] = datecheck.Layout{Fetched: time.Now().UTC(), Absent: true}
			return checkDates(t, d, listFixture, mirror, writeFixture(t, d, "layouts.json", l))
		}},
		{"a finding that settled", "findings", func(t *testing.T, d string) string {
			var res dateResult
			if err := readJSON(dc, &res); err != nil {
				t.Fatal(err)
			}
			for i := range res.Leads {
				if res.Leads[i].AniDB == 10 {
					res.Leads[i].Findings = append(res.Leads[i].Findings, datecheck.Finding{
						Kind: datecheck.MissingTarget, Episode: datecheck.Episode{Number: 3}, Target: datecheck.Target{Season: 1, Number: 4},
					})
				}
			}
			return writeFixture(t, d, "datecheck.json", res)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			existing := []issues.Issue{{Number: 5, State: "CLOSED", Title: "lead", Body: issues.Marker(datecheck.Key(10))}}
			got := actionsFor(planIssues(t, d, tc.redo(t, d), records, existing), datecheck.Key(10))
			if len(got) != 1 || got[0].Number != 5 || got[0].Kind == issues.Close || got[0].Kind == issues.Edit {
				t.Fatalf("planned %+v, want issue 5 reopened", got)
			}
			want := "`reviewed/10.json` marked this lead fine on " + today + ", and it is raised again because " + causes[tc.cause] + "."
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("reopened body lacks %q:\n%s", want, got[0].Body)
			}
			for name, phrase := range causes {
				if name != tc.cause && strings.Contains(got[0].Body, phrase) {
					t.Errorf("reopened body names %q, which did not change", phrase)
				}
			}
		})
	}
}

// The check looks for a regular episode's date on every season of the
// series, so a change on a season the node does not map onto raises the
// lead again even when its findings stay the same.
func TestIssuesRaiseAReviewedLeadAgainWhenASeasonItDoesNotMapMoves(t *testing.T) {
	dir, records := t.TempDir(), t.TempDir()
	mirror, kept := dateFixtures(t, dir, true)
	var l datecheck.Layouts
	if err := readJSON(kept, &l); err != nil {
		t.Fatal(err)
	}
	s := l[5000]
	s.Episodes = append(s.Episodes, tvdb(2, 1, 4, "2021-01-04"))
	season2 := strings.Repeat("b", 64)
	s.Titles[2] = season2
	l[5000] = s
	layouts := writeFixture(t, dir, "layouts.json", l)
	dc := checkDates(t, dir, listFixture, mirror, layouts)
	if err := accept(t, dc, records, "-id", "10", "-reason", reviewReason, "-evidence", "https://anidb.net/anime/10"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	for _, tc := range []struct{ name, old, new string }{
		{"a season-2 air date", `"2021-01-04"`, `"2021-01-05"`},
		{"season 2's title digest", season2, season2[1:] + "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			redo := checkDates(t, d, listFixture, mirror, replaceOnce(t, layouts, d, "layouts.json", tc.old, tc.new))
			var res dateResult
			if err := readJSON(redo, &res); err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(res.Leads, func(l dateLead) bool { return l.AniDB == 10 })
			if i < 0 || !slices.Equal(datecheck.FindingKeys(res.Leads[i].Findings), []string{"date-mismatch:2"}) {
				t.Fatalf("leads after the change = %+v, want AniDB 10 with its recorded finding only", res.Leads)
			}
			existing := []issues.Issue{{Number: 5, State: "CLOSED", Title: "lead", Body: issues.Marker(datecheck.Key(10))}}
			got := actionsFor(planIssues(t, d, redo, records, existing), datecheck.Key(10))
			if len(got) != 1 || got[0].Number != 5 || got[0].Kind == issues.Close || got[0].Kind == issues.Edit {
				t.Fatalf("planned %+v, want issue 5 reopened", got)
			}
			if want := "raised again because TVDB's official order changed."; !strings.Contains(got[0].Body, want) {
				t.Errorf("reopened body lacks %q:\n%s", want, got[0].Body)
			}
		})
	}
}

func TestLeadWantNamesWhatChangedSinceTheReview(t *testing.T) {
	l := &dateLead{Node: "anime tvdbid=79881", AniDB: 3195, Series: 79881, Findings: []datecheck.Finding{{Kind: datecheck.DateMismatch, Episode: datecheck.Episode{Number: 8}}}}
	rec := &reviewed.Record{AniDBID: 3195, Captured: reviewed.Captured{Date: "2026-10-13"}}
	for _, tc := range []struct {
		causes []reviewed.Cause
		want   string
	}{
		{nil, ""},
		{[]reviewed.Cause{reviewed.TVDB}, "because TVDB's official order changed."},
		{[]reviewed.Cause{reviewed.Node, reviewed.AniDB}, "because the node changed and AniDB's episode list changed."},
		{
			[]reviewed.Cause{reviewed.Node, reviewed.TVDB, reviewed.AniDB, reviewed.NewFinding},
			"because the node changed, TVDB's official order changed, AniDB's episode list changed and a finding the record does not list appeared.",
		},
	} {
		body := leadWant(l, rec, tc.causes).Body
		const opening = "`reviewed/3195.json` marked this lead fine on 2026-10-13, and it is raised again "
		switch {
		case tc.want == "" && strings.Contains(body, "marked this lead fine"):
			t.Errorf("leadWant with no cause names a review:\n%s", body)
		case tc.want != "" && !strings.Contains(body, opening+tc.want+"\n"):
			t.Errorf("leadWant(%v) lacks %q:\n%s", tc.causes, opening+tc.want, body)
		}
	}
	if body := leadWant(l, nil, nil).Body; strings.Contains(body, "marked this lead fine") {
		t.Errorf("leadWant with no record names a review:\n%s", body)
	}
}

func TestLeadWantTellsHowToMarkALeadFine(t *testing.T) {
	l := &dateLead{Node: "anime tvdbid=79881", Slug: "kamichu", AniDB: 3195, Series: 79881, Findings: []datecheck.Finding{
		{Kind: datecheck.DateMismatch, Episode: datecheck.Episode{Number: 8}, Target: datecheck.Target{Season: 1, Number: 9}},
	}}
	body := leadWant(l, nil, nil).Body
	for _, s := range []string{
		`go run ./cmd/animap datecheck accept -datecheck datecheck.json -id 3195 -reason "`,
		`" -evidence https://anidb.net/anime/3195 -evidence https://thetvdb.com/series/kamichu/seasons/official/1`,
		"Closing this issue by hand does not stick",
	} {
		if !strings.Contains(body, s) {
			t.Errorf("lead body lacks %q:\n%s", s, body)
		}
	}
	dead := lead(4571)
	if body := leadWant(&dead, nil, nil).Body; strings.Contains(body, "datecheck accept") || !strings.Contains(body, "Closing this issue by hand does not stick") {
		t.Errorf("dead-series body = %q, want no accept command and the note that closing by hand does not stick", body)
	}
}

func TestAcceptWritesTheLeadsFingerprints(t *testing.T) {
	dir, records := t.TempDir(), filepath.Join(t.TempDir(), "reviewed")
	mirror, layouts := dateFixtures(t, dir, true)
	dc := checkDates(t, dir, listFixture, mirror, layouts)
	evidence := []string{"https://anidb.net/anime/10", "https://thetvdb.com/series/series-5000/seasons/official/1"}
	if err := accept(t, dc, records, "-id", "10", "-reason", reviewReason, "-evidence", evidence[0], "-evidence", evidence[1]); err != nil {
		t.Fatalf("accept: %v", err)
	}
	var res dateResult
	if err := readJSON(dc, &res); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(res.Leads, func(l dateLead) bool { return l.AniDB == 10 })
	if i < 0 {
		t.Fatalf("AniDB 10 is no lead in %+v", res.Leads)
	}
	got, err := reviewed.LoadDir(records)
	if err != nil {
		t.Fatalf("the written record does not load: %v", err)
	}
	r := got[10]
	if r.Captured.Fingerprints != res.Leads[i].Fingerprints || r.Captured.Date != time.Now().UTC().Format(time.DateOnly) {
		t.Errorf("captured %+v, want the lead's fingerprints %+v dated today", r.Captured, res.Leads[i].Fingerprints)
	}
	if !slices.Equal(r.Findings, []string{"date-mismatch:2"}) || r.Verdict != reviewed.Verdict || r.Reason != reviewReason ||
		!slices.Equal(r.Evidence, evidence) || r.Title != "Series with season 1" {
		t.Errorf("record = %+v, want the lead's finding, the fine verdict, the reason, both links and the node's name", r)
	}
}

func TestAcceptRefuses(t *testing.T) {
	dir := t.TempDir()
	mirror, layouts := dateFixtures(t, dir, true)
	dc := checkDates(t, dir, listFixture, mirror, layouts)
	link := "https://anidb.net/anime/10"
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"an id that passes the check", []string{"-id", "19", "-reason", reviewReason, "-evidence", link}, "AniDB 19 is not a lead"},
		{"an id the list lacks", []string{"-id", "99", "-reason", reviewReason, "-evidence", link}, "AniDB 99 is not a lead"},
		{"a dead series", []string{"-id", "12", "-reason", reviewReason, "-evidence", link}, "SkyHook no longer serves"},
		{"an empty reason", []string{"-id", "10", "-reason", "", "-evidence", link}, "reason"},
		{"a blank reason", []string{"-id", "10", "-reason", " \n", "-evidence", link}, "reason"},
		{"no evidence", []string{"-id", "10", "-reason", reviewReason}, "evidence"},
		{"an http link", []string{"-id", "10", "-reason", reviewReason, "-evidence", "http://anidb.net/anime/10"}, "not an https link"},
		{"a link to another host", []string{"-id", "10", "-reason", reviewReason, "-evidence", "https://example.com/10"}, "not an https link"},
		{"a link with a user name", []string{"-id", "10", "-reason", reviewReason, "-evidence", "https://reviewer:pw@anidb.net/anime/10"}, "not an https link"},
		{"a reason over the record's size bound", []string{"-id", "10", "-reason", strings.Repeat("x", reviewed.MaxFileBytes), "-evidence", link}, "bytes, at most"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records := t.TempDir()
			err := accept(t, dc, records, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("accept %v = %v, want a refusal naming %q", tc.args, err, tc.want)
			}
			if left, _ := filepath.Glob(filepath.Join(records, "*")); len(left) != 0 {
				t.Errorf("a refused accept left %v", left)
			}
		})
	}
	t.Run("an existing record without -force", func(t *testing.T) {
		records := t.TempDir()
		args := []string{"-id", "10", "-reason", reviewReason, "-evidence", link}
		if err := accept(t, dc, records, args...); err != nil {
			t.Fatalf("first accept: %v", err)
		}
		before, err := os.ReadFile(filepath.Join(records, "10.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := accept(t, dc, records, "-id", "10", "-reason", "Another reason naming episode 2.", "-evidence", link); err == nil || !strings.Contains(err.Error(), "-force") {
			t.Errorf("second accept = %v, want a refusal naming -force", err)
		}
		after, err := os.ReadFile(filepath.Join(records, "10.json"))
		if err != nil || !bytes.Equal(before, after) {
			t.Errorf("a refused overwrite changed the record: %v", err)
		}
		if err := accept(t, dc, records, "-id", "10", "-reason", "Another reason naming episode 2.", "-evidence", link, "-force"); err != nil {
			t.Fatalf("accept -force: %v", err)
		}
		got, err := reviewed.LoadDir(records)
		if err != nil || got[10].Reason != "Another reason naming episode 2." {
			t.Errorf("after -force the record is %+v, %v; want the new reason", got[10], err)
		}
	})
	t.Run("no -datecheck", func(t *testing.T) {
		err := run(t.Context(), []string{"datecheck", "accept", "-id", "10", "-reason", reviewReason, "-evidence", link}, &bytes.Buffer{})
		if !errors.Is(err, errUsage) {
			t.Errorf("accept without -datecheck = %v, want a usage error", err)
		}
	})
}

// Two accepts for one id that both get past every check before either
// publishes: exactly one record lands and the other accept refuses.
func TestAcceptNeverReplacesARecordAnotherAcceptWrote(t *testing.T) {
	dir := t.TempDir()
	var arrived sync.WaitGroup
	arrived.Add(2)
	released := make(chan struct{})
	go func() { arrived.Wait(); close(released) }()
	prev := beforePublish
	beforePublish = func() {
		arrived.Done()
		select {
		case <-released:
		case <-time.After(10 * time.Second):
		}
	}
	t.Cleanup(func() { beforePublish = prev })
	fp := datecheck.Fingerprints{Node: strings.Repeat("a", 64), TVDB: strings.Repeat("b", 64), AniDB: strings.Repeat("c", 64)}
	var recs [2]reviewed.Record
	for i := range recs {
		recs[i] = reviewed.Record{
			AniDBID: 10, Title: "Series with season 1", Verdict: reviewed.Verdict, Findings: []string{"date-mismatch:2"},
			Reason: fmt.Sprintf("Reviewer %d: %s", i, reviewReason), Evidence: []string{"https://anidb.net/anime/10"},
			Captured: reviewed.Captured{Date: "2026-10-13", Fingerprints: fp},
		}
	}
	var errs [2]error
	var wg sync.WaitGroup
	for i := range recs {
		wg.Go(func() { errs[i] = writeRecord(t.Context(), dir, &recs[i], false, slog.New(slog.DiscardHandler)) })
	}
	wg.Wait()
	winner := slices.IndexFunc(errs[:], func(err error) bool { return err == nil })
	loser := 1 - winner
	if winner < 0 || errs[loser] == nil || !strings.Contains(errs[loser].Error(), "-force") {
		t.Fatalf("racing accepts returned %v, want one success and one refusal naming -force", errs)
	}
	got, err := reviewed.LoadDir(dir)
	if err != nil || got[10].Reason != recs[winner].Reason {
		t.Errorf("the record holds %q, %v; want the winner's reason %q", got[10].Reason, err, recs[winner].Reason)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 1 {
		t.Errorf("the directory holds %v, want the record only", left)
	}
}

func fineRecord() reviewed.Record {
	fp := datecheck.Fingerprints{Node: strings.Repeat("a", 64), TVDB: strings.Repeat("b", 64), AniDB: strings.Repeat("c", 64)}
	return reviewed.Record{
		AniDBID: 10, Title: "Series with season 1", Verdict: reviewed.Verdict, Findings: []string{"date-mismatch:2"},
		Reason: reviewReason, Evidence: []string{"https://anidb.net/anime/10"},
		Captured: reviewed.Captured{Date: "2026-10-13", Fingerprints: fp},
	}
}

func swap[T any](t *testing.T, v *T, with T) {
	t.Helper()
	prev := *v
	*v = with
	t.Cleanup(func() { *v = prev })
}

// The staged write's directory sync runs before the record's name exists,
// so accept reports success only after syncing the directory again.
func TestAcceptSyncsTheRecordsNameBeforeReportingSuccess(t *testing.T) {
	dir := t.TempDir()
	var synced []string
	swap(t, &syncDir, func(d string) error {
		if _, err := os.Stat(filepath.Join(d, "10.json")); err != nil {
			t.Errorf("synced %s before the record was in it: %v", d, err)
		}
		synced = append(synced, d)
		return nil
	})
	r := fineRecord()
	if err := writeRecord(t.Context(), dir, &r, false, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if !slices.Equal(synced, []string{dir}) {
		t.Errorf("synced %v, want the records directory %s once", synced, dir)
	}
	swap(t, &syncDir, func(string) error { return errors.New("injected sync failure") })
	if err := writeRecord(t.Context(), t.TempDir(), &r, false, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "injected sync failure") {
		t.Errorf("writeRecord with a failing directory sync = %v, want the sync failure", err)
	}
}

// A missing records directory is created by atomicfile, which syncs each
// directory it creates into its parent.
func TestAcceptCreatesTheRecordsDirectoryThroughAtomicfile(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "repo", "reviewed")
			calls := 0
			swap(t, &writeAtomic, func(ctx context.Context, path string, data []byte, opts ...atomicfile.Option) (atomicfile.Result, error) {
				calls++
				if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("the records directory existed before atomicfile wrote: %v", err)
				}
				return atomicfile.WriteFile(ctx, path, data, opts...)
			})
			r := fineRecord()
			if err := writeRecord(t.Context(), dir, &r, force, slog.New(slog.DiscardHandler)); err != nil {
				t.Fatalf("writeRecord into a missing directory: %v", err)
			}
			if got, err := reviewed.LoadDir(dir); calls != 1 || err != nil || got[10].Reason != r.Reason {
				t.Errorf("after %d atomicfile writes the directory holds %+v, %v; want the record from one write", calls, got, err)
			}
		})
	}
}

func TestAcceptFailsAWriteAtomicfileReportsNotDurable(t *testing.T) {
	swap(t, &writeAtomic, func(ctx context.Context, path string, data []byte, opts ...atomicfile.Option) (atomicfile.Result, error) {
		res, err := atomicfile.WriteFile(ctx, path, data, opts...)
		res.Durable = false
		return res, err
	})
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			dir := t.TempDir()
			r := fineRecord()
			if err := writeRecord(t.Context(), dir, &r, force, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "may not survive a crash") {
				t.Errorf("writeRecord with a write that is not durable = %v, want an error saying it may not survive a crash", err)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "*")); !force && len(left) != 0 {
				t.Errorf("a refused accept left %v", left)
			}
		})
	}
}

func TestBuildValidatesTheReviewedRecords(t *testing.T) {
	noFloors(t)
	dir, ov, records := t.TempDir(), t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", offsetEntry())
	counts := writeFixture(t, dir, "counts.json", []anidb.Row{countRow(30, 1)})
	fp := datecheck.Fingerprints{Node: strings.Repeat("a", 64), TVDB: strings.Repeat("b", 64), AniDB: strings.Repeat("c", 64)}
	rec := reviewed.Record{
		AniDBID: 20, Title: "Filed under specials", Verdict: reviewed.Verdict, Findings: []string{"date-mismatch:1"},
		Reason: reviewReason, Evidence: []string{"https://anidb.net/anime/20"}, Captured: reviewed.Captured{Date: "2026-10-13", Fingerprints: fp},
	}
	writeFixture(t, records, "20.json", rec)
	if _, err := build(t, dir, ov, "", "-counts", counts, "-reviewed", records); err != nil {
		t.Errorf("build with a record for a node an overlay entry patches = %v, want it accepted", err)
	}
	rec.Captured.Node = "not-a-digest"
	writeFixture(t, records, "20.json", rec)
	if _, err := build(t, dir, ov, "", "-counts", counts, "-reviewed", records); err == nil || !strings.Contains(err.Error(), "20.json") {
		t.Errorf("build with a malformed fingerprint = %v, want a refusal naming the record", err)
	}
	_ = os.Remove(filepath.Join(records, "20.json"))
	writeFixture(t, records, "21.json", reviewed.Record{AniDBID: 20})
	if _, err := build(t, dir, ov, "", "-counts", counts, "-reviewed", records); err == nil || !strings.Contains(err.Error(), "21.json") {
		t.Errorf("build with a record named for another id = %v, want a refusal naming the record", err)
	}
}
