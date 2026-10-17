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

	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/atomicfile/v4"
)

// acceptDoc is a review.json with AniDB 10's TVDB mismatch, AniDB 12's dead
// series, and two watched records on AniDB 20.
func acceptDoc(t *testing.T, dir string) string {
	t.Helper()
	dead := raisedDate(5001, datecheck.DateMismatch)
	dead.Findings, dead.Dead, dead.Digests = nil, true, map[string]string{"dead-series": strings.Repeat("a", 64)}
	w := raisedWatch(5)
	w.Findings = append(w.Findings, raisedWatch(6).Findings...)
	doc := &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{
		{Key: review.AniDBKey(10), Title: "Series with season 1", Sections: sections{DateTVDB: raisedDate(5000, datecheck.DateMismatch)}},
		{Key: review.AniDBKey(12), Title: "AniDB only", Sections: sections{DateTVDB: dead}},
		{Key: review.AniDBKey(20), Title: "Filed under specials", Sections: sections{Watch: w}},
	}}
	return writeFixture(t, dir, "review.json", doc)
}

func accept(t *testing.T, from, dir string, extra ...string) error {
	t.Helper()
	args := append([]string{"accept", "-from", from, "-dir", dir, "-reason", reviewReason, "-evidence", "https://anidb.net/anime/10"}, extra...)
	return run(t.Context(), args, &bytes.Buffer{})
}

func TestAcceptWritesTheFindingsDigests(t *testing.T) {
	dir, recs := t.TempDir(), t.TempDir()
	from := acceptDoc(t, dir)
	if err := accept(t, from, recs, "-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Fine); err != nil {
		t.Fatal(err)
	}
	got, err := review.LoadDir(recs)
	if err != nil {
		t.Fatal(err)
	}
	r := got[review.AniDBKey(10)]
	p := r.Parts.DateTVDB
	if p == nil || p.Findings["date-mismatch:2"] != strings.Repeat("a", 64) || p.Verdict != review.Fine || r.Title != "Series with season 1" || p.Date != time.Now().UTC().Format(time.DateOnly) {
		t.Errorf("record = %+v, want the finding's digest under a fine verdict", r)
	}
	if err := accept(t, from, recs, "-key", review.AniDBKey(20), "-part", review.Watch, "-anilist", "6", "-verdict", review.Unmappable, "-evidence", "https://anilist.co/anime/6"); err != nil {
		t.Fatal(err)
	}
	got, _ = review.LoadDir(recs)
	r20 := got[review.AniDBKey(20)]
	w := r20.WatchFor(6)
	if w == nil || w.RecordSHA256 != strings.Repeat("c", 64) || !slices.Equal(w.Gaps, []string{"no_tvdb"}) {
		t.Errorf("watch part = %+v, want AniList 6's gaps and record digest", w)
	}
}

// explain.json has review.json's shape, so a ruling can be taken from it.
func TestAcceptFromAnExplanation(t *testing.T) {
	c := newReviewCase(t)
	t.Setenv("TMDB_TOKEN", "")
	var out bytes.Buffer
	args := []string{"explain", "-key", review.AniDBKey(10), "-json", "-list", listFixture, "-overlay", c.ov, "-mirror", c.mirror, "-reviewed", c.reviewed, "-layouts-tvdb", c.tvdb, "-layouts-tmdb", c.tmdb}
	if err := run(t.Context(), args, &out); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(c.dir, "explain.json")
	if err := os.WriteFile(from, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accept(t, from, c.reviewed, "-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Fine); err != nil {
		t.Fatal(err)
	}
	doc := c.run(t)
	if m := subjectIn(doc, review.AniDBKey(10)).Sections.DateTVDB.Acceptance; m == nil || !m.Matches {
		t.Errorf("acceptance after accepting from explain.json = %+v, want it to cover the finding", m)
	}
}

func TestAcceptRefuses(t *testing.T) {
	dir := t.TempDir()
	from := acceptDoc(t, dir)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no such subject":                 {args: []string{"-key", review.AniDBKey(99), "-part", review.DateTVDB, "-verdict", review.Fine}, want: "raises nothing"},
		"no such lead":                    {args: []string{"-key", review.AniDBKey(10), "-part", review.DateTMDB, "-verdict", review.Fine}, want: "has no date-tmdb finding"},
		"no such gap":                     {args: []string{"-key", review.AniDBKey(10), "-part", review.Watch, "-verdict", review.Unmappable}, want: "has no watch gap"},
		"no such watched record":          {args: []string{"-key", review.AniDBKey(20), "-part", review.Watch, "-anilist", "7", "-verdict", review.Unmappable}, want: "no watch gap for AniList 7"},
		"two watched records, none named": {args: []string{"-key", review.AniDBKey(20), "-part", review.Watch, "-verdict", review.Unmappable}, want: "name one with -anilist"},
		"a dead series fine":              {args: []string{"-key", review.AniDBKey(12), "-part", review.DateTVDB, "-verdict", review.Fine}, want: "cannot be accepted"},
		"a dead series inexpressible":     {args: []string{"-key", review.AniDBKey(12), "-part", review.DateTVDB, "-verdict", review.Inexpressible, "-requires", "x"}, want: "cannot be accepted"},
		"inexpressible without requires":  {args: []string{"-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Inexpressible}, want: "names the format change"},
		"a date part unmappable":          {args: []string{"-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Unmappable}, want: "verdict"},
		"a watch part fine":               {args: []string{"-key", review.AniDBKey(20), "-part", review.Watch, "-anilist", "5", "-verdict", review.Fine}, want: "verdict"},
		"evidence on another host":        {args: []string{"-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Fine, "-evidence", "https://example.com/"}, want: "allowed host"},
		"an unknown part":                 {args: []string{"-key", review.AniDBKey(10), "-part", "drift", "-verdict", review.Fine}, want: "-part"},
		"a key that does not parse":       {args: []string{"-key", "lead:date:10", "-part", review.DateTVDB, "-verdict", review.Fine}, want: "not a subject key"},
	} {
		t.Run(name, func(t *testing.T) {
			recs := t.TempDir()
			err := accept(t, from, recs, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("accept = %v, want an error naming %q", err, tc.want)
			}
			if left, _ := filepath.Glob(filepath.Join(recs, "*")); len(left) != 0 {
				t.Errorf("a refused accept left %v", left)
			}
		})
	}
	t.Run("an existing record without -force", func(t *testing.T) {
		recs := t.TempDir()
		if err := accept(t, from, recs, "-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Fine); err != nil {
			t.Fatal(err)
		}
		err := accept(t, from, recs, "-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Inexpressible, "-requires", "x")
		if err == nil || !strings.Contains(err.Error(), "-force") {
			t.Errorf("a second accept = %v, want a refusal naming -force", err)
		}
		if err := accept(t, from, recs, "-key", review.AniDBKey(10), "-part", review.DateTVDB, "-verdict", review.Inexpressible, "-requires", "x", "-force"); err != nil {
			t.Fatalf("accept -force = %v", err)
		}
		got, _ := review.LoadDir(recs)
		if p := got[review.AniDBKey(10)].Parts.DateTVDB; p == nil || p.Verdict != review.Inexpressible {
			t.Errorf("after -force the part = %+v, want it replaced", p)
		}
	})
	t.Run("a usage error", func(t *testing.T) {
		if err := run(t.Context(), []string{"accept", "-key", review.AniDBKey(10)}, &bytes.Buffer{}); !errors.Is(err, errUsage) {
			t.Errorf("accept without -from = %v, want a usage error", err)
		}
	})
}

func fineRecord() review.Record {
	return review.Record{Key: review.AniDBKey(10), Title: "Series with season 1", Parts: review.Parts{DateTVDB: &review.DatePart{
		Findings: map[string]string{"date-mismatch:2": strings.Repeat("a", 64)},
		Verdict:  review.Fine, Date: "2026-10-15", Reason: reviewReason, Evidence: []string{"https://anidb.net/anime/10"},
	}}}
}

// Two accepts for one subject that both get past every check before either
// publishes: exactly one record lands and the other accept refuses.
func TestAcceptNeverReplacesARecordAnotherAcceptWrote(t *testing.T) {
	dir := t.TempDir()
	var arrived sync.WaitGroup
	arrived.Add(2)
	released := make(chan struct{})
	go func() { arrived.Wait(); close(released) }()
	swap(t, &beforePublish, func() {
		arrived.Done()
		select {
		case <-released:
		case <-time.After(10 * time.Second):
		}
	})
	var recs [2]review.Record
	for i := range recs {
		recs[i] = fineRecord()
		recs[i].Parts.DateTVDB.Reason = fmt.Sprintf("Reviewer %d: %s", i, reviewReason)
	}
	path := filepath.Join(dir, "anidb-10.json")
	var errs [2]error
	var wg sync.WaitGroup
	for i := range recs {
		wg.Go(func() { errs[i] = writeRecord(t.Context(), path, &recs[i], false, slog.New(slog.DiscardHandler)) })
	}
	wg.Wait()
	winner := slices.IndexFunc(errs[:], func(err error) bool { return err == nil })
	loser := 1 - winner
	if winner < 0 || errs[loser] == nil || !strings.Contains(errs[loser].Error(), "-force") {
		t.Fatalf("racing accepts returned %v, want one success and one refusal naming -force", errs)
	}
	got, err := review.LoadDir(dir)
	if err != nil || got[review.AniDBKey(10)].Parts.DateTVDB.Reason != recs[winner].Parts.DateTVDB.Reason {
		t.Errorf("the record holds %+v, %v; want the winner's", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 1 {
		t.Errorf("the directory holds %v, want the record only", left)
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
		if _, err := os.Stat(filepath.Join(d, "anidb-10.json")); err != nil {
			t.Errorf("synced %s before the record was in it: %v", d, err)
		}
		synced = append(synced, d)
		return nil
	})
	r := fineRecord()
	if err := writeRecord(t.Context(), filepath.Join(dir, "anidb-10.json"), &r, false, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if !slices.Equal(synced, []string{dir}) {
		t.Errorf("synced %v, want the records directory %s once", synced, dir)
	}
	swap(t, &syncDir, func(string) error { return errors.New("injected sync failure") })
	if err := writeRecord(t.Context(), filepath.Join(t.TempDir(), "anidb-10.json"), &r, false, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "injected sync failure") {
		t.Errorf("writeRecord with a failing directory sync = %v, want the sync failure", err)
	}
}

// A missing records directory is created by atomicfile, which syncs each
// directory it creates into its parent.
func TestAcceptCreatesTheRecordsDirectoryThroughAtomicfile(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%t", replace), func(t *testing.T) {
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
			if err := writeRecord(t.Context(), filepath.Join(dir, "anidb-10.json"), &r, replace, slog.New(slog.DiscardHandler)); err != nil {
				t.Fatalf("writeRecord into a missing directory: %v", err)
			}
			if got, err := review.LoadDir(dir); calls != 1 || err != nil || len(got) != 1 {
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
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%t", replace), func(t *testing.T) {
			dir := t.TempDir()
			r := fineRecord()
			if err := writeRecord(t.Context(), filepath.Join(dir, "anidb-10.json"), &r, replace, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "may not survive a crash") {
				t.Errorf("writeRecord with a write that is not durable = %v, want an error saying it may not survive a crash", err)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "*")); !replace && len(left) != 0 {
				t.Errorf("a refused accept left %v", left)
			}
		})
	}
}

func TestBuildValidatesTheAcceptanceRecords(t *testing.T) {
	noFloors(t)
	dir, ov, records := t.TempDir(), t.TempDir(), t.TempDir()
	r := fineRecord()
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(records, "anidb-10.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := build(t, dir, ov, "", "-reviewed", records); err != nil {
		t.Errorf("build with a valid record = %v, want it accepted", err)
	}
	if err := os.Rename(filepath.Join(records, "anidb-10.json"), filepath.Join(records, "anidb-11.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := build(t, dir, ov, "", "-reviewed", records); err == nil || !strings.Contains(err.Error(), "anidb-11.json") {
		t.Errorf("build with a record named for another key = %v, want a refusal naming the record", err)
	}
}
