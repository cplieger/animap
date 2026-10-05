package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/counts"
	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/watch"
)

const (
	aodFixture  = "../../testdata/aod-mini.jsonl"
	listFixture = "../../testdata/anime-list-mini.xml"
)

func fixtureSHA(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(aodFixture)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func noFloors(t *testing.T) {
	t.Helper()
	saved := guard.Floors
	guard.Floors = schema.Populations{}
	t.Cleanup(func() { guard.Floors = saved })
}

func build(t *testing.T, dir, overlayDir, prev string, extra ...string) (string, error) {
	t.Helper()
	args := []string{
		"build", "-aod", aodFixture, "-aod-release", "2026-40", "-aod-sha256", fixtureSHA(t),
		"-list", listFixture, "-list-commit", strings.Repeat("a", 40), "-overlay", overlayDir,
		"-out", filepath.Join(dir, "animap.json"), "-previous", prev,
	}
	var out bytes.Buffer
	err := run(t.Context(), append(args, extra...), &out)
	return out.String(), err
}

func TestBuildPublishesOnlyOnChange(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	out, err := build(t, dir, t.TempDir(), filepath.Join(dir, "absent.json"))
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if !strings.Contains(out, "changed=true") || !strings.Contains(out, "previous_hash=\n") {
		t.Errorf("first build stdout = %q", out)
	}
	first, err := os.ReadFile(filepath.Join(dir, "animap.json"))
	if err != nil {
		t.Fatal(err)
	}
	prev := filepath.Join(dir, "prev.json")
	if err := os.WriteFile(prev, first, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = build(t, dir, t.TempDir(), prev)
	if err != nil || !strings.Contains(out, "changed=false") {
		t.Errorf("rebuild over its own output = %q, %v; want changed=false", out, err)
	}
	doc, err := schema.Decode(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Sources.AnimeOfflineDatabase.SHA256 != fixtureSHA(t) || doc.Attribution.License != "ODbL-1.0" || len(doc.Records) != 15 {
		t.Errorf("document sources %+v, attribution %+v, %d records", doc.Sources, doc.Attribution, len(doc.Records))
	}
}

func TestBuildRefuses(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	bigger := schema.Document{Version: schema.Version, Attribution: schema.DefaultAttribution}
	for i := range 40 {
		bigger.Records = append(bigger.Records, schema.Record{AniListID: i + 1, AniDBID: i + 1, TVDBID: 1, TMDBTVID: 1, MappingList: []schema.Row{{}}})
	}
	b, _ := schema.Encode(&bigger)
	prev := filepath.Join(dir, "prev.json")
	if err := os.WriteFile(prev, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := build(t, dir, t.TempDir(), prev); !errors.Is(err, guard.ErrCoverage) {
		t.Errorf("build below the previous release = %v, want ErrCoverage", err)
	}
	if _, err := build(t, dir, t.TempDir(), prev, "-accept-shrink"); err != nil {
		t.Errorf("accept-shrink build = %v", err)
	}

	args := []string{
		"build", "-aod", aodFixture, "-aod-release", "x", "-aod-sha256", strings.Repeat("0", 64),
		"-list", listFixture, "-list-commit", "c", "-out", filepath.Join(dir, "o.json"),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("wrong database digest = %v", err)
	}
	if err := run(t.Context(), []string{"build", "-aod", aodFixture}, &bytes.Buffer{}); !errors.Is(err, errUsage) {
		t.Errorf("missing flags = %v, want errUsage", err)
	}
}

func TestBuildBlocksOnCollision(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	// Entry 13 moves AniDB 13 onto TVDB 5000 and maps its episode 1 onto
	// 0x2, which AniDB 10's specials row already claims.
	eps := []skyhook.Episode{{Season: 0, Number: 1, AirDate: "2020-06-01"}}
	entry := map[string]any{
		"anidb_id": 13, "title": "Two AniList ids, one entry", "justification": "test",
		"set": map[string]any{"tvdbid": "5000", "mapping_list": []map[string]any{
			{"anidb_season": 1, "tvdb_season": 0, "episodes": [][]int{{1, 2}}},
		}},
		"evidence": map[string]string{"anidb": "https://anidb.net/anime/13"},
		"upstream": "TODO-PR", "episodes": map[string]any{"regular": 1, "specials": []int{}},
		"siblings": map[string]any{"10": map[string]any{"regular": 12, "specials": []int{1, 2}}},
		"captured": map[string]any{
			"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40),
			"node_sha256": strings.Repeat("b", 64),
			"tvdb":        map[string]any{"series": 5000, "seasons": []int{0}, "episodes": eps, "sha256": skyhook.Hash(eps)},
		},
	}
	b, _ := json.Marshal(entry)
	if err := os.WriteFile(filepath.Join(ov, "13.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := build(t, dir, ov, "")
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Errorf("colliding overlay = %v, want a collision refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "animap.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a refused build still wrote animap.json")
	}
}

func TestBuildBlocksOnAnUncountedSiblingOfATouchedSeries(t *testing.T) {
	noFloors(t)
	// AniDB 30 shares TVDB 5006 and has no anime-offline-database entry.
	eps := []skyhook.Episode{{Season: 2, Number: 1, AirDate: "2020-06-01"}}
	entry := func(siblings map[string]any) map[string]any {
		e := map[string]any{
			"anidb_id": 20, "title": "Filed under specials", "justification": "test",
			"set":      map[string]any{"tvdbid": "5006", "defaulttvdbseason": "2"},
			"evidence": map[string]string{"anidb": "https://anidb.net/anime/20"},
			"upstream": "TODO-PR", "episodes": map[string]any{"regular": 3, "specials": []int{}},
			"captured": map[string]any{
				"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40),
				"node_sha256": strings.Repeat("b", 64),
				"tvdb":        map[string]any{"series": 5006, "seasons": []int{2}, "episodes": eps, "sha256": skyhook.Hash(eps)},
			},
		}
		if siblings != nil {
			e["siblings"] = siblings
		}
		return e
	}
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", entry(nil))
	_, err := build(t, dir, ov, "")
	if err == nil || !strings.Contains(err.Error(), "no regular episode count") || !strings.Contains(err.Error(), "AniDB 30") {
		t.Errorf("build with AniDB 30 uncounted = %v, want a refusal naming AniDB 30", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "animap.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a refused build still wrote animap.json")
	}
	writeFixture(t, ov, "20.json", entry(map[string]any{"30": map[string]any{"regular": 1, "specials": []int{}}}))
	if _, err := build(t, dir, ov, ""); err != nil {
		t.Errorf("build with AniDB 30's count recorded = %v", err)
	}
}

func TestFailingWant(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	r := func(h int, c string) workflowRun {
		return workflowRun{Status: "completed", Conclusion: c, CreatedAt: now.Add(-time.Duration(h) * time.Hour), URL: "u"}
	}
	for _, tc := range []struct {
		name string
		runs []workflowRun
		want bool
	}{
		{"last run passed", []workflowRun{r(1, "success"), r(30, "failure")}, false},
		{"only run passed, long ago", []workflowRun{r(30, "success")}, false},
		{"failing for 6h", []workflowRun{r(1, "failure"), r(6, "success")}, false},
		{"failing for 30h", []workflowRun{r(1, "failure"), r(10, "failure"), r(30, "success")}, true},
		{"never succeeded in the window", []workflowRun{r(1, "failure"), r(26, "failure")}, true},
		{"in progress only", []workflowRun{{Status: "in_progress"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := failingWant(tc.runs, now); got != tc.want {
				t.Errorf("failingWant = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestDriftWantBodies(t *testing.T) {
	landed := driftWant(&drift.Finding{Key: "drift:landed:544", Cause: drift.Landed, AniDB: 544, Title: "@x", Path: "overlay/544.json"})
	if !strings.Contains(landed.Body, "git rm overlay/544.json") || !strings.Contains(landed.Title, "delete the entry") {
		t.Errorf("landed want = %+v", landed)
	}
	if strings.Contains(landed.Body, " @x") {
		t.Error("an entry title reached the body outside a code span")
	}
	gone := driftWant(&drift.Finding{Key: "drift:tvdb:1", Cause: drift.TVDBLayout, AniDB: 1, Before: []skyhook.Episode{{Season: 1, Number: 1}}})
	if !strings.Contains(gone.Body, "no longer serves") || !strings.Contains(gone.Body, "S1E1 A0 D") {
		t.Errorf("series-gone want = %q", gone.Body)
	}
}

func TestDriftWantCountBodies(t *testing.T) {
	f := drift.Finding{Key: "drift:counts:17281", Cause: drift.CountInDatabase, AniDB: 17281, Path: counts.Path, Counted: 2, DatabaseEpisodes: 2}
	agree := driftWant(&f)
	if agree.Title != "counts: AniDB 17281 now in anime-offline-database" || agree.Key != "drift:counts:17281" {
		t.Errorf("counts want title %q key %q", agree.Title, agree.Key)
	}
	if !strings.Contains(agree.Body, "counts 2 too, so the two agree") || !strings.Contains(agree.Body, "Remove the row from `checks/counts.json`") ||
		!strings.Contains(agree.Body, "https://anidb.net/anime/17281") || strings.Contains(agree.Body, "Entry:") {
		t.Errorf("agreeing counts body = %q", agree.Body)
	}
	f.DatabaseEpisodes = 3
	if differ := driftWant(&f); !strings.Contains(differ.Body, "counts 3, so the two disagree") || !strings.Contains(differ.Body, "Remove the row from") {
		t.Errorf("disagreeing counts body = %q", differ.Body)
	}
	f.DatabaseEpisodes = 0
	if uncounted := driftWant(&f); !strings.Contains(uncounted.Body, "no episode count yet, so keep the row") || strings.Contains(uncounted.Body, "Remove the row") ||
		!strings.Contains(uncounted.Body, "https://anidb.net/anime/17281") {
		t.Errorf("counts body for a carried id with no count = %q, want it to keep the row", uncounted.Body)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}, {"overlay"}, {"overlay", "x"}, {"build", "extra-arg"}, {"issues", "-existing", "missing.json", "extra-arg"}} {
		if err := run(t.Context(), args, &bytes.Buffer{}); !errors.Is(err, errUsage) {
			t.Errorf("run(%q) = %v, want errUsage", args, err)
		}
	}
}

func writeFixture(t *testing.T, dir, name string, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func planned(t *testing.T, path string) []issues.Action {
	t.Helper()
	var actions []issues.Action
	if err := readJSON(path, &actions); err != nil {
		t.Fatal(err)
	}
	return actions
}

func TestIssuesPublishAndDriftAreNotQueuedBehindTheWatchSet(t *testing.T) {
	dir := t.TempDir()
	var gaps []watch.Entry
	for i := range 30 {
		gaps = append(gaps, watch.Entry{AniListID: i + 1, Type: "SPECIAL", Gaps: []watch.Gap{watch.NoAniDB}})
	}
	wr := watchResult{
		Set: "seadex", EntryURL: "https://releases.moe/{id}", Complete: true, Watched: 30, Gaps: gaps,
		Classification: watch.Classification{New: gaps},
	}
	failed := time.Now().Add(-30 * time.Hour)
	args := []string{
		"issues", "-limit", "10", "-out", filepath.Join(dir, "actions.json"),
		"-existing", writeFixture(t, dir, "existing.json", []issues.Issue{}),
		"-watch", writeFixture(t, dir, "watch.json", wr),
		"-drift", writeFixture(t, dir, "drift.json", driftResult{Findings: []drift.Finding{{Key: "drift:node:544", Cause: drift.NodeChanged, AniDB: 544}}}),
		"-runs", writeFixture(t, dir, "runs.json", []workflowRun{{Status: "completed", Conclusion: "failure", CreatedAt: failed, URL: "u"}}),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	created := map[string]bool{}
	n := 0
	for _, a := range planned(t, filepath.Join(dir, "actions.json")) {
		if a.Kind == issues.Create && a.Key != dashboardKey {
			created[a.Key] = true
			n++
		}
	}
	if !created[failingKey] || !created["drift:node:544"] || n != 10 {
		t.Errorf("created %d issues %v, want publish:failing and drift:node:544 among the 10 the limit allows", n, created)
	}
}

// collidingList is the fixture list with AniDB 20 moved onto AniDB 10's
// TVDB series and season, so the two collide on a series no overlay entry
// touches.
func collidingList(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(listFixture)
	if err != nil {
		t.Fatal(err)
	}
	moved := regexp.MustCompile(`<anime anidbid="20" tvdbid="[^"]*" defaulttvdbseason="[^"]*"`).
		ReplaceAll(b, []byte(`<anime anidbid="20" tvdbid="5000" defaulttvdbseason="1"`))
	if bytes.Equal(moved, b) {
		t.Fatal("the fixture no longer has the AniDB 20 node this test moves")
	}
	p := filepath.Join(dir, "list.xml")
	if err := os.WriteFile(p, moved, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildBlocksOnACollisionOutsideTheBaseline(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	list, base := collidingList(t, dir), filepath.Join(dir, "baseline.json")
	args := func(extra ...string) []string {
		return append([]string{
			"build", "-aod", aodFixture, "-aod-release", "2026-40", "-aod-sha256", fixtureSHA(t),
			"-list", list, "-list-commit", strings.Repeat("a", 40), "-overlay", t.TempDir(),
			"-out", filepath.Join(dir, "animap.json"), "-baseline", base,
		}, extra...)
	}
	if err := run(t.Context(), args(), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "outside the baseline") {
		t.Fatalf("build with an unbaselined collision = %v, want a refusal", err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"baseline", "init", "-baseline", base, "-list", list, "-aod", aodFixture, "-commit", "c", "-overlay", t.TempDir()}, &out); err != nil {
		t.Fatalf("baseline init: %v", err)
	}
	if !strings.Contains(out.String(), "1 collision(s)") {
		t.Errorf("baseline init printed %q, want 1 collision", out.String())
	}
	if err := run(t.Context(), args(), &bytes.Buffer{}); err != nil {
		t.Errorf("build with the collisions baselined = %v", err)
	}
	if err := run(t.Context(), []string{"baseline", "init", "-baseline", base, "-list", list, "-aod", aodFixture, "-commit", "c"}, &out); err == nil {
		t.Error("a second baseline init overwrote the baseline")
	}
}

func TestBuildBlocksOnAnUncountedNodeOutsideTheBaseline(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	b, err := os.ReadFile(listFixture)
	if err != nil {
		t.Fatal(err)
	}
	// AniDB 30 has no database entry; moved beside AniDB 10, it shares a
	// series no overlay entry touches.
	moved := bytes.Replace(b, []byte(`<anime anidbid="30" tvdbid="5006"`), []byte(`<anime anidbid="30" tvdbid="5000"`), 1)
	if bytes.Equal(moved, b) {
		t.Fatal("the fixture no longer has the AniDB 30 node this test moves")
	}
	list, base := filepath.Join(dir, "list.xml"), filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(list, moved, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"build", "-aod", aodFixture, "-aod-release", "2026-40", "-aod-sha256", fixtureSHA(t),
		"-list", list, "-list-commit", strings.Repeat("a", 40), "-overlay", t.TempDir(),
		"-out", filepath.Join(dir, "animap.json"), "-baseline", base,
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no regular episode count") || !strings.Contains(err.Error(), "AniDB 30") {
		t.Fatalf("build with AniDB 30 uncounted on an untouched series = %v, want a refusal naming AniDB 30", err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"baseline", "init", "-baseline", base, "-list", list, "-aod", aodFixture, "-commit", "c", "-overlay", t.TempDir()}, &out); err != nil {
		t.Fatalf("baseline init: %v", err)
	}
	if !strings.Contains(out.String(), "1 uncounted node(s)") {
		t.Errorf("baseline init printed %q, want 1 uncounted node", out.String())
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Errorf("build with AniDB 30 baselined = %v", err)
	}
}

// A sibling whose AniDB specials were never read blocks the build while
// another node claims a season-0 episode its specials could default onto.
func TestBuildBlocksOnAnUnresolvedSibling(t *testing.T) {
	noFloors(t)
	eps := []skyhook.Episode{{Season: 0, Number: 3, AirDate: "2020-06-01"}}
	entry := func(specials any) map[string]any {
		return map[string]any{
			"anidb_id": 20, "title": "Filed under specials", "justification": "test",
			"set":      map[string]any{"tvdbid": "5000"},
			"evidence": map[string]string{"anidb": "https://anidb.net/anime/20"},
			"upstream": "TODO-PR", "episodes": map[string]any{"regular": 3, "specials": []int{}},
			"siblings": map[string]any{"10": map[string]any{"regular": 12, "specials": specials}},
			"captured": map[string]any{
				"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40),
				"node_sha256": strings.Repeat("b", 64),
				"tvdb":        map[string]any{"series": 5000, "seasons": []int{0}, "episodes": eps, "sha256": skyhook.Hash(eps)},
			},
		}
	}
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", entry(nil))
	_, err := build(t, dir, ov, "")
	if err == nil || !strings.Contains(err.Error(), "unread AniDB specials") || !strings.Contains(err.Error(), "AniDB 10") {
		t.Errorf("build with AniDB 10's specials unread = %v, want a refusal naming AniDB 10", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "animap.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a refused build still wrote animap.json")
	}
	writeFixture(t, ov, "20.json", entry([]int{1, 3}))
	if _, err := build(t, dir, ov, ""); err != nil {
		t.Errorf("build with AniDB 10's specials recorded = %v", err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"overlay", "check", "-overlay", ov, "-list", listFixture, "-aod", aodFixture}, &out); err != nil {
		t.Errorf("overlay check with AniDB 10's specials recorded = %v", err)
	}
	writeFixture(t, ov, "20.json", entry(nil))
	if err := run(t.Context(), []string{"overlay", "check", "-overlay", ov, "-list", listFixture, "-aod", aodFixture}, &out); err == nil {
		t.Error("overlay check passed an unresolved sibling")
	}
}

func TestBaselineCheckShrink(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, cols ...guard.Collision) string {
		return writeFixture(t, dir, name, guard.NewBaseline(cols, nil, "c"))
	}
	c1 := guard.Collision{Series: 1, Target: "TVDB 0x1", Nodes: []int{1, 2}}
	c2 := guard.Collision{Series: 2, Target: "TVDB 0x1", Nodes: []int{3, 4}}
	base := write("base.json", c1, c2)
	shrink := []string{"baseline", "check-shrink", "-base", base, "-baseline"}
	if err := run(t.Context(), append(shrink, write("smaller.json", c1)), &bytes.Buffer{}); err != nil {
		t.Errorf("a removed entry = %v, want accepted", err)
	}
	c3 := guard.Collision{Series: 3, Target: "TVDB 0x1", Nodes: []int{5, 6}}
	if err := run(t.Context(), append(shrink, write("grown.json", c1, c2, c3)), &bytes.Buffer{}); !errors.Is(err, guard.ErrBaselineGrew) {
		t.Errorf("an added entry = %v, want ErrBaselineGrew", err)
	}
	if err := run(t.Context(), []string{"baseline", "check-shrink", "-base", filepath.Join(dir, "absent.json"), "-baseline", base}, &bytes.Buffer{}); err != nil {
		t.Errorf("no baseline on the target branch yet = %v, want accepted", err)
	}
}

func TestUnmappableWant(t *testing.T) {
	c := &watch.Classification{
		Unmappable: []watch.Entry{{AniListID: 1, Type: "OVA", Reason: "unmappable", Detail: "no TVDB series @x"}},
		Cleared:    []watch.Entry{{AniListID: 2, Type: "SPECIAL"}},
	}
	w, ok := unmappableWant(c)
	if !ok || !w.Pin || w.Key != unmappableKey || w.Title != "Unmappable SeaDex entries" {
		t.Fatalf("unmappableWant = %+v, %t; want the pinned issue", w, ok)
	}
	if !strings.Contains(w.Body, "- [ ] AniList 1 (OVA): unmappable.") || !strings.Contains(w.Body, "- [x] AniList 2 (SPECIAL)") {
		t.Errorf("body = %q, want an open item for 1 and a ticked one for 2", w.Body)
	}
	if !strings.Contains(w.Body, "`no TVDB series @x`") {
		t.Errorf("body = %q, want the detail inside a code span", w.Body)
	}
	if _, ok := unmappableWant(&watch.Classification{Cleared: c.Cleared}); !ok {
		t.Error("a list with only cleared entries does not want the issue")
	}
	if _, ok := unmappableWant(&watch.Classification{}); ok {
		t.Error("an empty list still wants the issue")
	}
}

func TestIssuesCloseADeletedEntrysDriftIssues(t *testing.T) {
	dir := t.TempDir()
	open := func(n int, key string) issues.Issue {
		return issues.Issue{Number: n, State: "OPEN", Title: key, Body: "x\n\n" + issues.Marker(key) + "\n"}
	}
	existing := writeFixture(t, dir, "existing.json", []issues.Issue{
		open(1, "drift:landed:544"),
		open(2, "drift:special:376:tvdb"),
		open(3, "drift:tvdb:353"),
		open(4, "watch:9"),
	})
	plan := func(d driftResult) map[string]issues.Kind {
		out := filepath.Join(dir, "actions.json")
		args := []string{"issues", "-out", out, "-existing", existing, "-drift", writeFixture(t, dir, "drift.json", d)}
		if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		kinds := map[string]issues.Kind{}
		for _, a := range planned(t, out) {
			kinds[a.Key] = a.Kind
		}
		return kinds
	}
	// 544 and bridge 376 were deleted; 353 is still in the overlay but its
	// TVDB layout was not read this run.
	complete := driftResult{Complete: true, Evaluated: []string{"drift:node:353", "drift:landed:353"}, Owned: drift.KeysOf(353).All()}
	got := plan(complete)
	if got["drift:landed:544"] != issues.Close || got["drift:special:376:tvdb"] != issues.Close {
		t.Errorf("complete run over an overlay without 544 and 376 planned %v, want both their issues closed", got)
	}
	if _, ok := got["drift:tvdb:353"]; ok {
		t.Errorf("planned %v for 353's unevaluated TVDB issue, want it left alone", got["drift:tvdb:353"])
	}
	if _, ok := got["watch:9"]; ok {
		t.Error("a drift run planned an action on a watch issue")
	}
	complete.Complete = false
	if got := plan(complete); len(got) != 0 {
		t.Errorf("an incomplete drift result planned %v, want nothing closed", got)
	}
}

// End to end: a drift run over an overlay that no longer holds AniDB 544
// closes 544's issue, and 18's TVDB-layout issue closes too because 18 was
// re-captured onto a TMDB-only route and holds no TVDB fingerprint.
func TestDriftThenIssuesCloseADeletedEntry(t *testing.T) {
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "18.json", map[string]any{
		"anidb_id": 18, "title": "Film with TVDB movie marker", "justification": "test",
		"set":      map[string]any{"tmdbid": "556"},
		"evidence": map[string]string{"tmdb": "https://www.themoviedb.org/movie/556"},
		"upstream": "TODO-PR", "episodes": map[string]any{"regular": 1, "specials": []int{}},
		"captured": map[string]any{"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40), "node_sha256": strings.Repeat("b", 64)},
	})
	driftOut := filepath.Join(dir, "drift.json")
	if err := run(t.Context(), []string{"drift", "-list", listFixture, "-overlay", ov, "-out", driftOut}, &bytes.Buffer{}); err != nil {
		t.Fatalf("drift: %v", err)
	}
	var d driftResult
	if err := readJSON(driftOut, &d); err != nil {
		t.Fatal(err)
	}
	if !d.Complete || !slices.Contains(d.Owned, "drift:landed:18") {
		t.Fatalf("drift result complete=%t owned=%v, want complete and owning 18's keys", d.Complete, d.Owned)
	}
	existing := writeFixture(t, dir, "existing.json", []issues.Issue{
		{Number: 1, State: "OPEN", Body: issues.Marker("drift:landed:544")},
		{Number: 2, State: "OPEN", Body: issues.Marker("drift:tvdb:18")},
	})
	out := filepath.Join(dir, "actions.json")
	if err := run(t.Context(), []string{"issues", "-out", out, "-existing", existing, "-drift", driftOut}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var closed []int
	for _, a := range planned(t, out) {
		if a.Kind == issues.Close {
			closed = append(closed, a.Number)
		}
	}
	if !slices.Equal(closed, []int{1, 2}) {
		t.Errorf("closed issues %v, want #1 (deleted entry) and #2 (TVDB key of an entry with no TVDB fingerprint)", closed)
	}
}

func TestIssueTextOnlyCarriesPublishedTypes(t *testing.T) {
	bad := "TV`\n@everyone"
	w := watchWant(&watch.Entry{AniListID: 9, Type: bad, Gaps: []watch.Gap{watch.NoAniDB}}, "https://releases.moe/{id}")
	if strings.Contains(w.Title+w.Body, "@everyone") || !strings.Contains(w.Title, "(unrecognised type)") {
		t.Errorf("watch want = %q / %q, want the type rendered as unrecognised", w.Title, w.Body)
	}
	u, _ := unmappableWant(&watch.Classification{Unmappable: []watch.Entry{{AniListID: 1, Type: bad, Reason: "unmappable"}}})
	if strings.Contains(u.Body, "@everyone") {
		t.Errorf("unmappable body = %q, carries the raw type", u.Body)
	}
	d := dashboardWant(&watchResult{Set: "seadex", Gaps: []watch.Entry{{AniListID: 1, Type: bad, Gaps: []watch.Gap{watch.NoAniDB}}}}, nil)
	if strings.Contains(d.Body, "@everyone") || !strings.Contains(d.Body, "| unrecognised type / ") {
		t.Errorf("dashboard body = %q, want the type rendered as unrecognised", d.Body)
	}
	if got := typeLabel("MOVIE"); got != "MOVIE" {
		t.Errorf("typeLabel(MOVIE) = %q", got)
	}
}

func TestWatchWantLinksTheWatchSetEntry(t *testing.T) {
	w := watchWant(&watch.Entry{AniListID: 9, Type: "TV", Gaps: []watch.Gap{watch.NoAniDB}}, "https://releases.moe/{id}")
	if !strings.Contains(w.Body, "- SeaDex: https://releases.moe/9\n") {
		t.Errorf("watchWant(AniList 9) body = %q, want the entry_url template filled with the AniList id", w.Body)
	}
}

func TestUnmappableWantStaysUnderTheBodyCap(t *testing.T) {
	c := &watch.Classification{}
	for i := range 1000 {
		c.Unmappable = append(c.Unmappable, watch.Entry{AniListID: i + 10, Type: "OVA", Reason: "unmappable", Detail: strings.Repeat("d", 150)})
	}
	c.Cleared = []watch.Entry{{AniListID: 1, Type: "SPECIAL"}}
	w, _ := unmappableWant(c)
	body := issues.Render(&w)
	if strings.Contains(body, "(truncated)") || !strings.Contains(body, "more, see `checks/unmappable.json`") {
		t.Errorf("a 1,001-item list rendered %d runes, truncated=%t; want it to stop at the cap with a 'more' line", len([]rune(body)), strings.Contains(body, "(truncated)"))
	}
	if !strings.Contains(body, "- [x] AniList 1 (SPECIAL)") {
		t.Error("the cleared item was cut from a long list")
	}
}

func TestDriftWantBridgeLanded(t *testing.T) {
	w := driftWant(&drift.Finding{
		Key: "drift:special:376:landed", Cause: drift.Landed, AniDB: 1983, AniList: 376, Linked: 4000,
		Path: "overlay/special-of-parent/376.json",
	})
	if !strings.Contains(w.Body, "links AniList 376 to AniDB 4000") || !strings.Contains(w.Body, "git rm overlay/special-of-parent/376.json") ||
		!strings.Contains(w.Title, "AniList 376 (specials of AniDB 1983)") {
		t.Errorf("bridge landed want = %+v", w)
	}
}
