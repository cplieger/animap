package main

import (
	"bytes"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/watch"
)

func evaluatedSources() map[string]*sourceHealth {
	out := map[string]*sourceHealth{}
	for _, name := range review.Sections {
		out[name] = &sourceHealth{Evaluated: true}
	}
	return out
}

func raisedDate(series int, kind datecheck.Kind) *dateSection {
	f := datecheck.Finding{Kind: kind, Episode: datecheck.Episode{Number: 2, AirDate: "2020-01-13"}, Target: datecheck.Target{Season: 1, Number: 3, AirDate: "2020-01-20"}, Inputs: strings.Repeat("a", 64)}
	return &dateSection{
		Applicable: true, Evaluated: true, Series: series, Slug: "series-5000", Node: "anime tvdbid=5000",
		Findings: []datecheck.Finding{f}, Digests: map[string]string{f.Key(): f.Inputs},
	}
}

func raisedDrift() *driftSection {
	return &driftSection{Applicable: true, Evaluated: true, Findings: []drift.Finding{{Cause: drift.NodeChanged, AniDB: 10, Path: "overlay/10.json"}}}
}

func raisedWatch(al int) *watchSection {
	return &watchSection{
		Applicable: true, Evaluated: true, EntryURL: "https://seadex.moe/{id}",
		Findings: []watchFinding{{AniListID: al, Type: "TV", AniDBID: 10, RecordSHA256: strings.Repeat("c", 64), Gaps: []watch.Gap{"no_tvdb_season"}}},
	}
}

func fullSubject() *subject {
	return &subject{Key: review.AniDBKey(10), Title: "Series with season 1", Sections: sections{
		DateTVDB: raisedDate(5000, datecheck.DateMismatch), DateTMDB: raisedDate(9000, datecheck.MissingTarget), Drift: raisedDrift(), Watch: raisedWatch(5),
	}}
}

func planDoc(t *testing.T, doc *reviewDoc, existing []issues.Issue, extra ...string) []issues.Action {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "actions.json")
	args := append([]string{"issues", "-review", writeFixture(t, dir, "review.json", doc), "-existing", writeFixture(t, dir, "existing.json", existing), "-out", out}, extra...)
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	return planned(t, out)
}

func actionFor(actions []issues.Action, key string) *issues.Action {
	for i := range actions {
		if actions[i].Key == key {
			return &actions[i]
		}
	}
	return nil
}

func TestIssuesOneIssuePerAnime(t *testing.T) {
	actions := planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{fullSubject()}}, nil)
	var subjects []issues.Action
	for _, a := range actions {
		if strings.HasPrefix(a.Key, review.Prefix) {
			subjects = append(subjects, a)
		}
	}
	if len(subjects) != 1 || subjects[0].Kind != issues.Create {
		t.Fatalf("actions = %+v, want one create for the anime", actions)
	}
	a := subjects[0]
	if want := "anime: AniDB 10 Series with season 1, TVDB and TMDB dates, overlay drift, SeaDex watch gap"; a.Title != want {
		t.Errorf("title = %q, want %q", a.Title, want)
	}
	for _, name := range review.Sections {
		if strings.Count(a.Body, sectionStart(name)) != 1 {
			t.Errorf("body holds section %s %d times, want once", name, strings.Count(a.Body, sectionStart(name)))
		}
	}
	if !slices.Equal(a.Labels, []string{"animap", "anime"}) || issues.KeyOf(a.Body) != review.AniDBKey(10) {
		t.Errorf("labels %v, key %q", a.Labels, issues.KeyOf(a.Body))
	}
	for _, want := range []string{
		"TVDB's official order disagrees", "https://thetvdb.com/series/series-5000/seasons/official/1", "https://www.themoviedb.org/tv/9000/season/1",
		"| 2 | 2020-01-13 | S1E3 | 2020-01-20 | not on TMDB | none |", "`overlay/10.json`: the Anime-Lists node changed", "AniList 5 (TV): `no_tvdb_season`", "https://seadex.moe/5",
		"-key anime:anidb:10 -part date-tvdb", "The node as checked:",
	} {
		if !strings.Contains(a.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, a.Body)
		}
	}
	if s := actionFor(actions, summaryKey); s == nil || !s.Pin || s.Kind != issues.Create {
		t.Errorf("summary = %+v, want the pinned summary created", s)
	}
}

// openIssue is the issue a full run created for AniDB 10, open.
func openIssue(t *testing.T) issues.Issue {
	t.Helper()
	a := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{fullSubject()}}, nil), review.AniDBKey(10))
	return issues.Issue{Number: 7, State: "OPEN", Title: a.Title, Body: a.Body}
}

// With one source unable to judge, its section is carried verbatim, every
// evaluated-clear section drops out, and the issue stays open.
func TestIssuesKeepAnUnevaluatedSectionVerbatim(t *testing.T) {
	is := openIssue(t)
	carried := sectionsOf(is.Body)
	if len(carried) != 4 {
		t.Fatalf("the open issue has sections %v, want all four", carried)
	}
	for _, name := range review.Sections {
		for _, globally := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s globally=%t", name, globally), func(t *testing.T) {
				doc := &reviewDoc{Sources: evaluatedSources()}
				if globally {
					doc.Sources[name] = &sourceHealth{UnevaluatedReason: "down"}
				} else {
					s := &subject{Key: review.AniDBKey(10)}
					h := review.Section{Applicable: true, Reason: "fetch failed"}
					switch name {
					case review.DateTVDB, review.DateTMDB:
						s.Sections.setDate(name, &dateSection{Section: h})
					case review.Drift:
						s.Sections.Drift = &driftSection{Section: h}
					case review.Watch:
						s.Sections.Watch = &watchSection{Section: h}
					}
					doc.Subjects = []*subject{s}
				}
				a := actionFor(planDoc(t, doc, []issues.Issue{is}), review.AniDBKey(10))
				if a == nil || a.Kind != issues.Edit {
					t.Fatalf("action = %+v, want an edit", a)
				}
				got := sectionsOf(a.Body)
				if len(got) != 1 || got[name] != carried[name] {
					t.Errorf("edited sections %v, want only %s, verbatim", slices.Sorted(maps.Keys(got)), name)
				}
			})
		}
	}
}

func TestIssuesCloseOnlyWhenEverySectionIsClear(t *testing.T) {
	is := openIssue(t)
	t.Run("everything evaluated and clear", func(t *testing.T) {
		a := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources()}, []issues.Issue{is}), review.AniDBKey(10))
		if a == nil || a.Kind != issues.Close || a.Number != 7 || !strings.Contains(a.Comment, "TVDB dates cleared on this run") {
			t.Errorf("action = %+v, want a close naming why", a)
		}
	})
	t.Run("one section covered by an acceptance", func(t *testing.T) {
		s := &subject{Key: review.AniDBKey(10), Sections: sections{DateTVDB: raisedDate(5000, datecheck.DateMismatch)}}
		s.Sections.DateTVDB.Acceptance = &review.Match{File: "anidb-10.json", Part: review.DateTVDB, Matches: true}
		a := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{s}}, []issues.Issue{is}), review.AniDBKey(10))
		if a == nil || a.Kind != issues.Close || !strings.Contains(a.Comment, "TVDB dates is covered by `reviewed/anidb-10.json` `date-tvdb`") {
			t.Errorf("action = %+v, want a close naming the record", a)
		}
	})
	t.Run("a source not checked and the section in the body", func(t *testing.T) {
		doc := &reviewDoc{Sources: evaluatedSources()}
		doc.Sources[review.DateTMDB] = &sourceHealth{UnevaluatedReason: "TMDB_TOKEN is not set"}
		if a := actionFor(planDoc(t, doc, []issues.Issue{is}), review.AniDBKey(10)); a == nil || a.Kind != issues.Edit {
			t.Errorf("action = %+v, want the issue kept open on its TMDB section", a)
		}
	})
	t.Run("a source not checked and the section not in the body", func(t *testing.T) {
		only := is
		only.Body = strings.Replace(is.Body, sectionsOf(is.Body)[review.DateTMDB], "", 1)
		doc := &reviewDoc{Sources: evaluatedSources()}
		doc.Sources[review.DateTMDB] = &sourceHealth{UnevaluatedReason: "TMDB_TOKEN is not set"}
		if a := actionFor(planDoc(t, doc, []issues.Issue{only}), review.AniDBKey(10)); a == nil || a.Kind != issues.Close {
			t.Errorf("action = %+v, want a close: every section in the body is clear", a)
		}
	})
}

func TestIssuesAnAcceptanceSilencesOnlyItsPart(t *testing.T) {
	s := fullSubject()
	s.Sections.Drift, s.Sections.Watch = nil, nil
	s.Sections.DateTVDB.Acceptance = &review.Match{File: "anidb-10.json", Part: review.DateTVDB, Matches: true}
	a := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{s}}, nil), review.AniDBKey(10))
	if a == nil || strings.Contains(a.Body, sectionStart(review.DateTVDB)) || !strings.Contains(a.Body, sectionStart(review.DateTMDB)) || !strings.HasSuffix(a.Title, ", TMDB dates") {
		t.Errorf("action = %+v, want the TMDB section alone", a)
	}
	s.Sections.DateTVDB.Acceptance = &review.Match{File: "anidb-10.json", Part: review.DateTVDB, Causes: []string{"finding-inputs:date-mismatch:2", "finding:out-of-order:3"}}
	a = actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{s}}, nil), review.AniDBKey(10))
	if a == nil || !strings.Contains(a.Body, "raised again because what `date-mismatch:2` rests on changed and `out-of-order:3` is new") {
		t.Errorf("body = %q, want the re-raise causes", a.Body)
	}
}

func TestIssuesNameASourceTheyCouldNotCheck(t *testing.T) {
	s := &subject{Key: review.AniDBKey(10), Sections: sections{
		DateTVDB: raisedDate(5000, datecheck.DateMismatch),
		DateTMDB: &dateSection{Applicable: true, Reason: "TMDB_TOKEN is not set"},
	}}
	a := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{s}}, nil), review.AniDBKey(10))
	if a == nil || !strings.Contains(a.Body, "TMDB dates: not checked this run (TMDB_TOKEN is not set).") || strings.Contains(a.Title, "TMDB") {
		t.Errorf("action = %+v, want the TMDB line and a title naming TVDB only", a)
	}
	only := &subject{Key: review.AniDBKey(11), Sections: sections{DateTMDB: &dateSection{Applicable: true, Reason: "x"}}}
	if a := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{only}}, nil), review.AniDBKey(11)); a != nil {
		t.Errorf("a subject with nothing but an unchecked source planned %+v", a)
	}
}

// A "not checked" line the planner wrote is carried on the next run
// without naming its source in the title or editing the issue, and it
// keeps the issue open once everything else clears.
func TestIssuesCarryTheirNotCheckedLineQuietly(t *testing.T) {
	s := &subject{Key: review.AniDBKey(10), Title: "Show", Sections: sections{
		DateTVDB: raisedDate(5000, datecheck.DateMismatch),
		DateTMDB: &dateSection{Applicable: true, Reason: "TMDB_TOKEN is not set"},
	}}
	doc := &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{s}}
	first := actionFor(planDoc(t, doc, nil), review.AniDBKey(10))
	if first == nil || first.Kind != issues.Create || first.Title != "anime: AniDB 10 Show, TVDB dates" {
		t.Fatalf("first run = %+v, want a create titled for TVDB only", first)
	}
	open := issues.Issue{Number: 7, State: "OPEN", Title: first.Title, Body: first.Body}
	if a := actionFor(planDoc(t, doc, []issues.Issue{open}), review.AniDBKey(10)); a != nil {
		t.Errorf("second run planned %+v, want nothing: the issue already says this", a)
	}
	s.Sections.DateTVDB = nil
	a := actionFor(planDoc(t, doc, []issues.Issue{open}), review.AniDBKey(10))
	if a == nil || a.Kind != issues.Edit || a.Title != "anime: AniDB 10 Show, TMDB dates not checked" || sectionsOf(a.Body)[review.DateTMDB] != sectionsOf(open.Body)[review.DateTMDB] {
		t.Errorf("with TVDB clear = %+v, want the issue kept open on its carried TMDB line", a)
	}
}

func TestIssuesSpendCreatesInOrder(t *testing.T) {
	sub := func(aid int, ss sections) *subject { return &subject{Key: review.AniDBKey(aid), Sections: ss} }
	dead := raisedDate(5001, datecheck.DateMismatch)
	dead.Findings, dead.Dead, dead.Digests = nil, true, map[string]string{"dead-series": strings.Repeat("a", 64)}
	doc := &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{
		sub(1, sections{DateTMDB: raisedDate(9000, datecheck.MissingTarget)}),
		sub(2, sections{Watch: raisedWatch(2)}),
		sub(3, sections{DateTVDB: raisedDate(5000, datecheck.DateMismatch)}),
		sub(4, sections{DateTVDB: raisedDate(5000, datecheck.MissingTarget)}),
		sub(5, sections{DateTVDB: dead}),
		sub(6, sections{Drift: raisedDrift()}),
	}}
	runs := []workflowRun{{Status: "completed", Conclusion: "failure", CreatedAt: time.Now().Add(-30 * time.Hour), URL: "u"}}
	dir := t.TempDir()
	actions := planDoc(t, doc, nil, "-limit", "4", "-runs", writeFixture(t, dir, "runs.json", runs))
	var created []string
	for _, a := range actions {
		if a.Kind == issues.Create && a.Key != summaryKey {
			created = append(created, a.Key)
		}
	}
	if want := []string{failingKey, review.AniDBKey(6), review.AniDBKey(5), review.AniDBKey(4)}; !slices.Equal(created, want) {
		t.Errorf("created %v, want %v", created, want)
	}
	summary := actionFor(actions, summaryKey)
	for _, waiting := range []string{"anime: AniDB 3, TVDB dates", "anime: AniDB 2, SeaDex watch gap", "anime: AniDB 1, TMDB dates"} {
		if !strings.Contains(summary.Body, "waiting for an issue (over this run's limit): "+waiting) {
			t.Errorf("summary lacks %q:\n%s", waiting, summary.Body)
		}
	}
}

func TestIssuesSpreadEditsAndClosesOverRuns(t *testing.T) {
	var existing []issues.Issue
	for i := range 120 {
		existing = append(existing, issues.Issue{Number: i + 1, State: "OPEN", Title: "x", Body: wrapSection(review.DateTVDB, "old\n") + issues.Marker(review.AniDBKey(i+1))})
	}
	var subjects []*subject
	for i := range 60 {
		subjects = append(subjects, &subject{Key: review.AniDBKey(i + 1), Sections: sections{DateTVDB: raisedDate(5000, datecheck.DateMismatch)}})
	}
	actions := planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: subjects}, existing)
	n := map[issues.Kind]int{}
	for _, a := range actions {
		if a.Key != summaryKey {
			n[a.Kind]++
		}
	}
	if n[issues.Edit] != 50 || n[issues.Close] != 50 {
		t.Errorf("planned %v, want 50 edits and 50 closes", n)
	}
}

func TestIssuesSummary(t *testing.T) {
	doc := &reviewDoc{Sources: evaluatedSources()}
	doc.Sources[review.DateTMDB] = &sourceHealth{UnevaluatedReason: "TMDB_TOKEN is not set"}
	for i := range 3000 {
		d := &dateSection{Applicable: true, Evaluated: true, Acceptance: &review.Match{File: fmt.Sprintf("anidb-%d.json", i+1), Part: review.DateTVDB, Matches: true}}
		doc.Subjects = append(doc.Subjects, &subject{Key: review.AniDBKey(i + 1), Sections: sections{DateTVDB: d}})
	}
	s := actionFor(planDoc(t, doc, nil), summaryKey)
	body := issues.Render(&issues.Want{Key: summaryKey, Body: s.Body})
	if strings.Contains(body, "(truncated)") || !strings.Contains(body, "more, see the run's review.json") || len([]rune(body)) > issues.MaxBodyRunes {
		t.Errorf("summary of 3,000 stale parts: %d runes, want it bounded with a 'more' line", len([]rune(body)))
	}
	for _, want := range []string{"> TMDB dates are not checked: TMDB_TOKEN is not set.", "- [ ] `reviewed/anidb-1.json` `date-tvdb`: its section is clear without it, delete the part", "| TMDB dates | no |"} {
		if !strings.Contains(body, want) {
			t.Errorf("summary lacks %q", want)
		}
	}
	accepted := &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{
		{Key: review.AniDBKey(1), Sections: sections{DateTVDB: raisedDate(5000, datecheck.DateMismatch)}},
		{Key: review.AniDBKey(2), Sections: sections{Watch: raisedWatch(7)}},
	}}
	accepted.Subjects[0].Sections.DateTVDB.Acceptance = &review.Match{File: "anidb-1.json", Part: review.DateTVDB, Verdict: review.Fine, Reason: "episode 2 is \"Title\"", Matches: true}
	accepted.Subjects[1].Sections.Watch.Findings[0].Acceptance = &review.Match{File: "anidb-2.json", Part: "watch:7", Verdict: review.Unmappable, Reason: "no source carries it", Matches: true}
	body = actionFor(planDoc(t, accepted, nil), summaryKey).Body
	if strings.Contains(body, "Title") {
		t.Errorf("summary carries a date part's reason: %s", body)
	}
	for _, want := range []string{"- date findings accepted as fine: 1 anime sections", "- watch gaps accepted as unmappable (1):", "`reviewed/anidb-2.json` `watch:7` (no_tvdb_season): `no source carries it`"} {
		if !strings.Contains(body, want) {
			t.Errorf("summary lacks %q", want)
		}
	}
}

func TestIssueTextOnlyCarriesPublishedTypesAndNoMarkers(t *testing.T) {
	w := raisedWatch(9)
	w.Findings[0].Type = "TV`\n@everyone"
	s := &subject{Key: review.AniListKey(9), Title: "<!-- /animap:section -->\n@everyone", Sections: sections{Watch: w}}
	d := raisedDrift()
	d.Findings[0].Title = "x <!-- /animap:section --> @y"
	s.Sections.Drift = d
	a := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{s}}, nil), review.AniListKey(9))
	if a == nil || strings.Contains(a.Title+a.Body, "@everyone") || !strings.Contains(a.Body, "(unrecognised type)") {
		t.Fatalf("action = %+v, want the type rendered as unrecognised", a)
	}
	if got := sectionsOf(issues.Render(&issues.Want{Key: a.Key, Body: a.Body})); len(got) != 2 || !strings.Contains(got[review.Drift], "Entry: ") {
		t.Errorf("sections = %v, want both whole: upstream text cannot end one", got)
	}
	if strings.Contains(a.Title, "\n") {
		t.Errorf("title %q holds a newline", a.Title)
	}
}

func TestDriftTextBodies(t *testing.T) {
	landed := driftText([]drift.Finding{{Cause: drift.Landed, AniDB: 544, Title: "@x", Path: "overlay/544.json"}})
	if !strings.Contains(landed, "git rm overlay/544.json") || strings.Contains(landed, " @x") {
		t.Errorf("landed = %q", landed)
	}
	gone := driftText([]drift.Finding{{Cause: drift.TVDBLayout, AniDB: 1, Before: []skyhook.Episode{{Season: 1, Number: 1}}, Gone: true}})
	if !strings.Contains(gone, "no longer serves") || !strings.Contains(gone, "S1E1 A0 D") || strings.Contains(gone, "After (now)") {
		t.Errorf("series gone = %q", gone)
	}
	changed := driftText([]drift.Finding{{Cause: drift.AniDBChanged, AniDB: 4169, AniDBIDs: []int{4169, 4168}, AniList: 2269, Path: "overlay/links/2269.json", Mirror: strings.Repeat("e", 40)}})
	if !strings.Contains(changed, "The AniDB mirror at "+strings.Repeat("e", 40)+" changed") || !strings.Contains(changed, "https://anidb.net/anime/4168\n") {
		t.Errorf("link anidb-changed = %q", changed)
	}
	linkLanded := driftText([]drift.Finding{{Cause: drift.Landed, AniDB: 1983, AniDBIDs: []int{1983}, AniList: 376, Linked: 4000, Path: "overlay/links/376.json"}})
	if !strings.Contains(linkLanded, "anime-offline-database now links AniList 376 to AniDB 4000, so `overlay/links/376.json` is no longer needed.") {
		t.Errorf("link landed = %q", linkLanded)
	}
}
