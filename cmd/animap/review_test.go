package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
	"github.com/cplieger/httpx/v5"
)

var fixtureOtherTitles = strings.Repeat("d", 64)

const reviewReason = "AniDB 2 \"Second\" is TVDB 1x3 \"Second\"; TVDB dates it from the DVD."

func tvdb(s, e, abs int, date string) skyhook.Episode {
	return skyhook.Episode{Season: s, Number: e, Absolute: abs, AirDate: date}
}

func aired(eps ...skyhook.Episode) []datecheck.Aired {
	out := make([]datecheck.Aired, 0, len(eps))
	for _, e := range eps {
		out = append(out, datecheck.Aired{Episode: e, Title: datecheck.TitleDigest(e.Line())})
	}
	return out
}

// reviewFixtures writes a mirror listing AniDB 10, 12 and 19 of the
// fixture list and fresh stores: on TVDB, AniDB 10's episode 2 lands on the
// wrong date, 19 matches, and 12's series 5001 is dead; on TMDB, 10's show
// 9000 matches and AniDB 18's movie 555 is gone while 13's 77 and 78 exist.
func reviewFixtures(t *testing.T, dir string) (mirror, tvdbStore, tmdbStore string) {
	t.Helper()
	snap := anidb.Snapshot{Commit: mirrorCommit, Refused: []int{}, Anime: map[int]anidb.Anime{
		10: titled(anidb.Anime{Regular: []string{"2020-01-06", "2020-01-13"}, Finished: true, Titles: fixtureOtherTitles}),
		12: titled(anidb.Anime{Regular: []string{"2020-03-02"}, Finished: true, Titles: fixtureOtherTitles}),
		19: titled(anidb.Anime{Regular: []string{"2020-02-03"}, Finished: true, Titles: fixtureOtherTitles}),
	}}
	fresh := time.Now().UTC()
	tv := datecheck.NewStore()
	tv.Series[5000] = datecheck.Layout{Fetched: fresh, Slug: "series-5000", Episodes: aired(tvdb(1, 1, 1, "2020-01-06"), tvdb(1, 2, 2, "2020-03-02"), tvdb(1, 3, 3, "2020-01-13"))}
	tv.Series[5004] = datecheck.Layout{Fetched: fresh, Episodes: aired(tvdb(1, 1, 1, "2020-02-03"))}
	tv.Series[5001] = datecheck.Layout{Fetched: fresh, Absent: true}
	tm := datecheck.NewStore()
	tm.Series[9000] = datecheck.Layout{Fetched: fresh, Episodes: aired(tvdb(1, 1, 0, "2020-01-06"), tvdb(1, 2, 0, "2020-01-13"))}
	tm.Movies[555] = datecheck.Layout{Fetched: fresh, Absent: true}
	tm.Movies[77] = datecheck.Layout{Fetched: fresh}
	tm.Movies[78] = datecheck.Layout{Fetched: fresh}
	return writeFixture(t, dir, "mirror.json", snap), writeStore(t, dir, "layouts-tvdb.json", tv), writeStore(t, dir, "layouts-tmdb.json", tm)
}

func writeStore(t *testing.T, dir, name string, s *datecheck.Store) string {
	t.Helper()
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type reviewCase struct {
	dir, ov, reviewed, mirror, tvdb, tmdb string
}

func newReviewCase(t *testing.T) *reviewCase {
	t.Helper()
	c := &reviewCase{dir: t.TempDir(), ov: t.TempDir(), reviewed: t.TempDir()}
	c.mirror, c.tvdb, c.tmdb = reviewFixtures(t, c.dir)
	return c
}

func (c *reviewCase) run(t *testing.T, extra ...string) *reviewDoc {
	t.Helper()
	out := filepath.Join(c.dir, "review.json")
	args := append([]string{
		"review", "-list", listFixture, "-overlay", c.ov, "-mirror", c.mirror, "-reviewed", c.reviewed,
		"-layouts-tvdb", c.tvdb, "-layouts-tmdb", c.tmdb, "-budget-tvdb", "0s", "-budget-tmdb", "0s", "-out", out,
	}, extra...)
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatalf("review: %v", err)
	}
	var doc reviewDoc
	if err := readJSON(out, &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

func subjectIn(doc *reviewDoc, key string) *subject {
	for _, s := range doc.Subjects {
		if s.Key == key {
			return s
		}
	}
	return nil
}

func TestReviewJudgesBothSides(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "test-key")
	doc := newReviewCase(t).run(t)
	for _, name := range review.Sections[:3] {
		if h := doc.Sources[name]; h == nil || !h.Evaluated {
			t.Errorf("source %s = %+v, want evaluated", name, h)
		}
	}
	s10 := subjectIn(doc, review.AniDBKey(10))
	if s10 == nil || s10.Title != "Series with season 1" {
		t.Fatalf("subjects = %+v, want AniDB 10 titled from its node", doc.Subjects)
	}
	d := s10.Sections.DateTVDB
	if d == nil || !d.Evaluated || len(d.Findings) != 1 || d.Findings[0].Kind != datecheck.DateMismatch || d.Slug != "series-5000" {
		t.Errorf("AniDB 10 date-tvdb = %+v, want one date mismatch with the slug", d)
	}
	if d != nil && (len(d.Digests) != 1 || len(d.Digests["date-mismatch:2"]) != 64 || !strings.Contains(d.Node, "tvdbid=5000")) {
		t.Errorf("AniDB 10 date-tvdb digests %v, node %q; want the finding's digest and the node text", d.Digests, d.Node)
	}
	if s10.Sections.DateTMDB != nil {
		t.Errorf("AniDB 10 date-tmdb = %+v, want it clear and so left out", s10.Sections.DateTMDB)
	}
	if s12 := subjectIn(doc, review.AniDBKey(12)); s12 == nil || s12.Sections.DateTVDB == nil || !s12.Sections.DateTVDB.Dead {
		t.Errorf("AniDB 12 = %+v, want a dead TVDB series", s12)
	}
	s18 := subjectIn(doc, review.AniDBKey(18))
	if s18 == nil || s18.Sections.DateTMDB == nil || len(s18.Sections.DateTMDB.Findings) != 1 || s18.Sections.DateTMDB.Findings[0].Movie != 555 {
		t.Errorf("AniDB 18 = %+v, want TMDB movie 555 dead", s18)
	}
	if s := subjectIn(doc, review.AniDBKey(19)); s != nil {
		t.Errorf("AniDB 19 = %+v, want it clear and so not listed", s)
	}
	if s := subjectIn(doc, review.AniDBKey(13)); s != nil {
		t.Errorf("AniDB 13 = %+v, want its movies found and so not listed", s)
	}
}

// A missing token leaves every TMDB section unevaluated, never clear.
func TestReviewWithoutATokenLeavesTMDBUnevaluated(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	doc := newReviewCase(t).run(t)
	h := doc.Sources[review.DateTMDB]
	if h == nil || h.Evaluated || h.UnevaluatedReason != "TMDB_TOKEN is not set" {
		t.Fatalf("TMDB source = %+v, want unevaluated for the missing token", h)
	}
	s10 := subjectIn(doc, review.AniDBKey(10))
	if tm := s10.Sections.DateTMDB; tm == nil || tm.Evaluated || !tm.Applicable || tm.Reason != "TMDB_TOKEN is not set" {
		t.Errorf("AniDB 10 date-tmdb = %+v, want applicable and not checked", tm)
	}
	if s := subjectIn(doc, review.AniDBKey(18)); s != nil {
		t.Errorf("AniDB 18 = %+v, want no TMDB finding without a token", s)
	}
}

func TestReviewRefusesAMalformedToken(t *testing.T) {
	for name, v := range map[string]string{"whitespace": "a b", "a newline": "ab\n", "a control character": "a\x01", "oversized": strings.Repeat("a", maxTokenBytes+1)} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TMDB_TOKEN", v)
			c := newReviewCase(t)
			err := run(t.Context(), []string{"review", "-list", listFixture, "-overlay", c.ov, "-out", filepath.Join(c.dir, "r.json")}, &bytes.Buffer{})
			if !errors.Is(err, errBadToken) || strings.Contains(err.Error(), v) {
				t.Errorf("review with a malformed token = %v, want errBadToken without the value", err)
			}
		})
	}
}

// A due series the budget leaves unfetched is not judged on its old layout.
func TestReviewLeavesASeriesItCouldNotRefreshUnevaluated(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	st, err := datecheck.LoadStore(c.tvdb)
	if err != nil {
		t.Fatal(err)
	}
	old := st.Series[5004]
	old.Fetched = time.Now().Add(-156 * time.Hour)
	st.Series[5004] = old
	c.tvdb = writeStore(t, c.dir, "layouts-tvdb.json", st)
	doc := c.run(t)
	s19 := subjectIn(doc, review.AniDBKey(19))
	if s19 == nil || s19.Sections.DateTVDB == nil || s19.Sections.DateTVDB.Evaluated || !strings.Contains(s19.Sections.DateTVDB.Reason, "budget") {
		t.Fatalf("AniDB 19 = %+v, want its date-tvdb unevaluated for the spent budget", s19)
	}
	if h := doc.Sources[review.DateTVDB]; h.Stale != 1 || h.Due != 1 || h.Requests != 0 {
		t.Errorf("TVDB source = %+v, want one due, one stale, no request", h)
	}
}

func TestReviewWithoutAMirrorLeavesTheDatesUnevaluated(t *testing.T) {
	c := newReviewCase(t)
	out := filepath.Join(c.dir, "review.json")
	if err := run(t.Context(), []string{"review", "-list", listFixture, "-overlay", c.ov, "-reviewed", c.reviewed, "-budget-tvdb", "0s", "-out", out}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var doc reviewDoc
	if err := readJSON(out, &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{review.DateTVDB, review.DateTMDB} {
		if h := doc.Sources[name]; h.Evaluated || h.UnevaluatedReason == "" {
			t.Errorf("%s = %+v, want unevaluated with a reason", name, h)
		}
	}
	if len(doc.Subjects) != 0 {
		t.Errorf("subjects = %+v, want none: nothing raised and no source judged a node", doc.Subjects)
	}
	tvdb := filepath.Join(c.dir, "kept.json")
	if err := os.Rename(c.tvdb, tvdb); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), []string{"review", "-list", listFixture, "-overlay", c.ov, "-reviewed", c.reviewed, "-layouts-tvdb", tvdb, "-budget-tvdb", "0s", "-out", out}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if st, err := datecheck.LoadStore(tvdb); err != nil || len(st.Series) != 3 {
		t.Errorf("TVDB store after a run without the mirror = %v, %v; want all 3 series kept", st, err)
	}
}

func TestReviewMatchesAcceptanceParts(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "test-key")
	c := newReviewCase(t)
	doc := c.run(t)
	digest := subjectIn(doc, review.AniDBKey(10)).Sections.DateTVDB.Digests["date-mismatch:2"]
	part := func(findings map[string]string) *review.DatePart {
		return &review.DatePart{Findings: findings, Verdict: review.Fine, Date: "2026-10-15", Reason: reviewReason, Evidence: []string{"https://anidb.net/anime/10"}}
	}
	recs := []review.Record{
		{Key: review.AniDBKey(10), Title: "x", Parts: review.Parts{DateTVDB: part(map[string]string{"date-mismatch:2": digest})}},
		{Key: review.AniDBKey(19), Title: "x", Parts: review.Parts{DateTVDB: part(map[string]string{"date-mismatch:1": digest})}},
		{Key: review.AniDBKey(12), Title: "x", Parts: review.Parts{DateTMDB: part(map[string]string{"date-mismatch:1": digest})}},
		{Key: review.AniDBKey(18), Title: "x", Parts: review.Parts{DateTMDB: part(map[string]string{"dead-movie:555": digest})}},
	}
	recs[3].Parts.DateTMDB.Verdict, recs[3].Parts.DateTMDB.Requires = review.Inexpressible, "a movie id TMDB serves"
	for i := range recs {
		b, err := recs[i].Encode()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(c.reviewed, review.File(recs[i].Key)), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doc = c.run(t)
	if m := subjectIn(doc, review.AniDBKey(10)).Sections.DateTVDB.Acceptance; m == nil || !m.Matches || m.File != "anidb-10.json" {
		t.Errorf("AniDB 10 acceptance = %+v, want its part to match", m)
	}
	s19 := subjectIn(doc, review.AniDBKey(19))
	if d := s19.Sections.DateTVDB; d == nil || !d.Evaluated || d.raised() || d.Acceptance == nil || !d.Acceptance.Matches {
		t.Errorf("AniDB 19 = %+v, want an evaluated, clear section whose part is stale", d)
	}
	if m := subjectIn(doc, review.AniDBKey(18)).Sections.DateTMDB.Acceptance; m == nil || m.Matches || !slices.Equal(m.Causes, []string{"finding-inputs:dead-movie:555"}) {
		t.Errorf("AniDB 18 acceptance = %+v, want a part whose digest moved, not matching", m)
	}
	s12 := subjectIn(doc, review.AniDBKey(12))
	if d := s12.Sections.DateTMDB; d == nil || d.Applicable || !d.Evaluated || d.Acceptance == nil {
		t.Errorf("AniDB 12 date-tmdb = %+v, want a part on a section that does not apply, stale", d)
	}
}

// Drift reads the TVDB store; on a fresh layout it sees exactly what a
// live fetch of the same show gives, on every season set an entry captures.
func TestDriftOnStoredLayoutsEqualsALiveFetch(t *testing.T) {
	body, err := os.ReadFile("../../testdata/skyhook-5000.json")
	if err != nil {
		t.Fatal(err)
	}
	show, err := skyhook.Fetch(t.Context(), getterFunc(func() ([]byte, error) { return body, nil }), 5000)
	if err != nil {
		t.Fatal(err)
	}
	r := &reviewRun{tvdb: datecheck.NewStore(), tvdbStale: map[int]string{}}
	r.tvdb.Series[5000] = datecheck.LayoutOf(show, time.Now())
	for _, seasons := range [][]int{nil, {1}, {0, 1}, {2}, {0}} {
		live := skyhook.Layout(show, seasons)
		e := &overlay.Entry{AniDBID: 10, Captured: overlay.Captured{TVDB: &overlay.CapturedTVDB{Series: 5000, Seasons: seasons, Episodes: live, SHA256: skyhook.Hash(live)}}}
		obs := r.observe(nil, e.Captured.TVDB)
		if !obs.LayoutRead || !slices.Equal(obs.Layout, live) {
			t.Errorf("seasons %v: stored layout %v, live %v", seasons, obs.Layout, live)
		}
		liveObs := drift.Observation{Layout: live, LayoutRead: true}
		got, _ := drift.Decide(drift.OfEntry(e), &obs)
		want, _ := drift.Decide(drift.OfEntry(e), &liveObs)
		if len(got) != len(want) {
			t.Errorf("seasons %v: drift on the store %v, on a live fetch %v", seasons, got, want)
		}
	}
	r.tvdbStale[5000] = "SkyHook fetch budget spent"
	if obs := r.observe(nil, &overlay.CapturedTVDB{Series: 5000}); obs.LayoutRead || obs.LayoutAbsent {
		t.Errorf("a stale series was read: %+v", obs)
	}
	r.tvdb.Series[5000] = datecheck.Layout{Absent: true}
	delete(r.tvdbStale, 5000)
	if obs := r.observe(nil, &overlay.CapturedTVDB{Series: 5000}); !obs.LayoutAbsent {
		t.Errorf("a dead series = %+v, want it absent", obs)
	}
}

func TestFetchAllStopsOnARefusedToken(t *testing.T) {
	h := &sourceHealth{Evaluated: true}
	calls := 0
	j := &refreshJob{
		name: "TMDB", into: datecheck.Layouts{}, due: []int{1, 2, 3}, stale: map[int]string{}, budget: time.Minute, health: h, count: &atomic.Int64{},
		fetch: func(_ context.Context, id int) (datecheck.Layout, error) {
			calls++
			switch id {
			case 1:
				return datecheck.Layout{}, source.ErrNotFound
			case 2:
				return datecheck.Layout{}, source.ErrRefused
			}
			return datecheck.Layout{}, nil
		},
	}
	if !fetchAll(t.Context(), j, slog.New(slog.DiscardHandler)) {
		t.Fatal("fetchAll did not report the refusal")
	}
	if calls != 2 || !h.Refused || h.Evaluated || !j.into[1].Absent || j.stale[2] == "" || j.stale[3] == "" {
		t.Errorf("after a refusal: calls %d, health %+v, layouts %v, stale %v", calls, h, j.into, j.stale)
	}
}

// SkyHook takes no token, so a 401 or 403 from it, which source.Client
// returns as the plain httpx.AuthError, is one unreadable show and the
// refresh goes on.
func TestFetchAllCountsARefusalWithoutABearerAsOneFailure(t *testing.T) {
	h := &sourceHealth{Evaluated: true}
	var fetched []int
	j := &refreshJob{
		name: "SkyHook", into: datecheck.Layouts{}, due: []int{1, 2}, stale: map[int]string{}, budget: time.Minute, health: h, count: &atomic.Int64{},
		fetch: func(_ context.Context, id int) (datecheck.Layout, error) {
			fetched = append(fetched, id)
			if id == 1 {
				return datecheck.Layout{}, &httpx.AuthError{Msg: "access denied (403)"}
			}
			return datecheck.Layout{Slug: "s"}, nil
		},
	}
	if fetchAll(t.Context(), j, slog.New(slog.DiscardHandler)) {
		t.Fatal("fetchAll reported a refused token")
	}
	if !slices.Equal(fetched, []int{1, 2}) || h.Failed != 1 || h.Refused || !h.Evaluated || j.into[2].Slug != "s" {
		t.Errorf("fetched %v, health %+v; want both read, one failed, the source evaluated", fetched, h)
	}
}

func TestFetchAllSpendsItsBudgetAndCountsFailures(t *testing.T) {
	h := &sourceHealth{Evaluated: true}
	j := &refreshJob{
		name: "SkyHook", into: datecheck.Layouts{}, due: []int{1, 2}, stale: map[int]string{}, budget: time.Hour, health: h, count: &atomic.Int64{},
		fetch: func(_ context.Context, id int) (datecheck.Layout, error) {
			if id == 1 {
				return datecheck.Layout{}, errors.New("HTTP 503")
			}
			return datecheck.Layout{Slug: "s"}, nil
		},
	}
	fetchAll(t.Context(), j, slog.New(slog.DiscardHandler))
	if h.Failed != 1 || h.Stale != 1 || j.stale[1] == "" || j.into[2].Slug != "s" || !h.Evaluated {
		t.Errorf("health %+v, stale %v, layouts %v; want 1 failed and stale, 2 stored", h, j.stale, j.into)
	}
}

// One dead show raises every node mapped to it, and each issue names the
// others.
func TestReviewNamesTheSiblingsOfADeadShow(t *testing.T) {
	dead := func() *dateSection {
		return &dateSection{Applicable: true, Evaluated: true, Series: 5001, Dead: true}
	}
	subjects := map[string]*subject{}
	for _, aid := range []int{12, 14, 15} {
		subjects[review.AniDBKey(aid)] = &subject{Key: review.AniDBKey(aid), Sections: sections{DateTVDB: dead()}}
	}
	subjects[review.AniDBKey(15)].Sections.DateTVDB.Series = 5002
	subjects[review.AniDBKey(14)].Sections.DateTMDB = dead()
	linkDeadSiblings(subjects)
	for aid, want := range map[int][]int{12: {14}, 14: {12}, 15: nil} {
		if got := subjects[review.AniDBKey(aid)].Sections.DateTVDB.Siblings; !slices.Equal(got, want) {
			t.Errorf("AniDB %d TVDB siblings = %v, want %v", aid, got, want)
		}
	}
	if got := subjects[review.AniDBKey(14)].Sections.DateTMDB.Siblings; len(got) != 0 {
		t.Errorf("AniDB 14 TMDB siblings = %v, want none: no other node is dead on that TMDB show", got)
	}
	s12 := subjects[review.AniDBKey(12)]
	if text := dateText(s12, review.DateTVDB, s12.Sections.DateTVDB); !strings.Contains(text, "AniDB 14 maps to this show too and is raised as well.") {
		t.Errorf("dead section text lacks its sibling:\n%s", text)
	}
	t.Run("through a review", func(t *testing.T) {
		t.Setenv("TMDB_TOKEN", "")
		c := newReviewCase(t)
		b, err := os.ReadFile(listFixture)
		if err != nil {
			t.Fatal(err)
		}
		list := filepath.Join(c.dir, "list.xml")
		moved := strings.Replace(string(b), `anidbid="19" tvdbid="5004" defaulttvdbseason="a"`, `anidbid="19" tvdbid="5001" defaulttvdbseason="1"`, 1)
		if moved == string(b) {
			t.Fatal("the fixture list no longer has node 19 on 5004")
		}
		if err := os.WriteFile(list, []byte(moved), 0o600); err != nil {
			t.Fatal(err)
		}
		doc := c.run(t, "-list", list)
		for aid, want := range map[int][]int{12: {19}, 19: {12}} {
			if s := subjectIn(doc, review.AniDBKey(aid)); s == nil || s.Sections.DateTVDB == nil || !slices.Equal(s.Sections.DateTVDB.Siblings, want) {
				t.Errorf("AniDB %d = %+v, want dead on 5001 beside %v", aid, s, want)
			}
		}
	})
}

// A token refused on the shows leaves the due movies unread too, and the
// source's health counts both.
func TestRefreshTMDBCountsTheMoviesARefusalLeaves(t *testing.T) {
	h := &sourceHealth{Evaluated: true}
	r := &reviewRun{
		tmdb: datecheck.NewStore(), tmdbShows: []int{9000, 9001}, tmdbMovies: []int{77, 78, 79},
		tmdbStale: map[int]string{}, movieStale: map[int]string{}, health: map[string]*sourceHealth{review.DateTMDB: h},
		tmdbClient: getterCount{g: getterFunc(func() ([]byte, error) { return nil, source.ErrRefused }), n: &atomic.Int64{}},
	}
	r.refreshTMDB(t.Context(), time.Minute, time.Now(), slog.New(slog.DiscardHandler))
	if h.Due != 5 || h.Stale != 5 || !h.Refused || h.Requests != 1 {
		t.Errorf("health %+v, want 5 due and 5 stale (2 shows, 3 movies) after one refused request", h)
	}
	if len(r.movieStale) != 3 || len(r.tmdbStale) != 2 {
		t.Errorf("stale shows %v, movies %v; want every one", r.tmdbStale, r.movieStale)
	}
}
