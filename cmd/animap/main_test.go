package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
)

const (
	aodFixture    = "../../testdata/aod-mini.jsonl"
	listFixture   = "../../testdata/anime-list-mini.xml"
	mirrorFixture = "../../testdata/mirror-mini.json"
)

var mirrorCommit = strings.Repeat("d", 40)

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
		"-out", filepath.Join(dir, "animap.json"), "-previous", prev, "-mirror", mirrorFixture, "-mirror-commit", mirrorCommit,
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
		"-mirror", mirrorFixture, "-mirror-commit", mirrorCommit,
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
		"upstream": "TODO-PR",
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
	_, err := build(t, dir, ov, "", "-mirror", snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1, 2), 13: fin(1)}))
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
	entry := map[string]any{
		"anidb_id": 20, "title": "Filed under specials", "justification": "test",
		"set":      map[string]any{"tvdbid": "5006", "defaulttvdbseason": "2"},
		"evidence": map[string]string{"anidb": "https://anidb.net/anime/20"},
		"upstream": "TODO-PR",
		"captured": map[string]any{
			"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40),
			"node_sha256": strings.Repeat("b", 64),
			"tvdb":        map[string]any{"series": 5006, "seasons": []int{2}, "episodes": eps, "sha256": skyhook.Hash(eps)},
		},
	}
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", entry)
	_, err := build(t, dir, ov, "", "-mirror", snapshotWith(t, dir, map[int]anidb.Anime{20: fin(3)}))
	if err == nil || !strings.Contains(err.Error(), "no regular episode count") || !strings.Contains(err.Error(), "AniDB 30") {
		t.Errorf("build with AniDB 30 uncounted = %v, want a refusal naming AniDB 30", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "animap.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a refused build still wrote animap.json")
	}
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWith(t, dir, map[int]anidb.Anime{20: fin(3), 30: fin(1)})); err != nil {
		t.Errorf("build with the mirror counting AniDB 30 = %v", err)
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
			"-out", filepath.Join(dir, "animap.json"), "-baseline", base, "-mirror", mirrorFixture, "-mirror-commit", mirrorCommit,
		}, extra...)
	}
	if err := run(t.Context(), args(), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "outside the baseline") {
		t.Fatalf("build with an unbaselined collision = %v, want a refusal", err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"baseline", "init", "-baseline", base, "-list", list, "-aod", aodFixture, "-mirror", mirrorFixture, "-commit", "c", "-overlay", t.TempDir()}, &out); err != nil {
		t.Fatalf("baseline init: %v", err)
	}
	if !strings.Contains(out.String(), "1 collision(s)") {
		t.Errorf("baseline init printed %q, want 1 collision", out.String())
	}
	if err := run(t.Context(), args(), &bytes.Buffer{}); err != nil {
		t.Errorf("build with the collisions baselined = %v", err)
	}
	if err := run(t.Context(), []string{"baseline", "init", "-baseline", base, "-list", list, "-aod", aodFixture, "-mirror", mirrorFixture, "-commit", "c"}, &out); err == nil {
		t.Error("a second baseline init overwrote the baseline")
	}
}

func TestCollisionsNamesAShowNoTVDBSeriesReaches(t *testing.T) {
	rep := guard.CollisionReport{
		Blocking:  []guard.Collision{{Target: "TMDB 99 S1E1", Claims: []string{"1 ep1", "3 ep1"}, Nodes: []int{1, 3}}},
		Uncounted: []guard.Uncounted{{AniDB: 4}},
	}
	absent := filepath.Join(t.TempDir(), "baseline.json")
	if _, err := collisions(absent, &rep, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "AniDB 4 on no TVDB series") {
		t.Errorf("collisions(uncounted AniDB 4 at series 0) = %v, want it named on no TVDB series", err)
	}
	rep.Uncounted = nil
	if _, err := collisions(absent, &rep, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "TMDB 99 S1E1 on no TVDB series") {
		t.Errorf("collisions(TMDB 99 S1E1 at series 0) = %v, want it named on no TVDB series", err)
	}
}

func TestCollisionsNamesTheSideAnUnresolvedNodeIsOn(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "baseline.json")
	for _, tc := range []struct {
		u    guard.Unresolved
		want string
	}{
		{guard.Unresolved{Episodes: []int{1}, TMDB: 99, AniDB: 5}, "AniDB 5, season 0 episodes [1] of TMDB 99"},
		{guard.Unresolved{Episodes: []int{2}, Series: 10, AniDB: 5}, "AniDB 5, season 0 episodes [2] of TVDB 10"},
	} {
		rep := guard.CollisionReport{Unresolved: []guard.Unresolved{tc.u}}
		if _, err := collisions(absent, &rep, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("collisions(unresolved %+v) = %v, want it to name %q", tc.u, err, tc.want)
		}
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
		"-out", filepath.Join(dir, "animap.json"), "-baseline", base, "-mirror", mirrorFixture, "-mirror-commit", mirrorCommit,
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no regular episode count") || !strings.Contains(err.Error(), "AniDB 30") {
		t.Fatalf("build with AniDB 30 uncounted on an untouched series = %v, want a refusal naming AniDB 30", err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"baseline", "init", "-baseline", base, "-list", list, "-aod", aodFixture, "-mirror", mirrorFixture, "-commit", "c", "-overlay", t.TempDir()}, &out); err != nil {
		t.Fatalf("baseline init: %v", err)
	}
	if !strings.Contains(out.String(), "1 uncounted node(s)") {
		t.Errorf("baseline init printed %q, want 1 uncounted node", out.String())
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err != nil {
		t.Errorf("build with AniDB 30 baselined = %v", err)
	}
}

// A sibling the mirror lists no episodes for blocks the build while
// another node claims a season-0 episode its specials could default onto.
func TestBuildBlocksOnAnUnresolvedSibling(t *testing.T) {
	noFloors(t)
	eps := []skyhook.Episode{{Season: 0, Number: 3, AirDate: "2020-06-01"}}
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", map[string]any{
		"anidb_id": 20, "title": "Filed under specials", "justification": "test",
		"set":      map[string]any{"tvdbid": "5000"},
		"evidence": map[string]string{"anidb": "https://anidb.net/anime/20"},
		"upstream": "TODO-PR",
		"captured": map[string]any{
			"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40),
			"node_sha256": strings.Repeat("b", 64),
			"tvdb":        map[string]any{"series": 5000, "seasons": []int{0}, "episodes": eps, "sha256": skyhook.Hash(eps)},
		},
	})
	unlisted := snapshotWith(t, dir, map[int]anidb.Anime{20: fin(3)})
	_, err := build(t, dir, ov, "", "-mirror", unlisted)
	if err == nil || !strings.Contains(err.Error(), "no AniDB episode list in the mirror") || !strings.Contains(err.Error(), "AniDB 10") {
		t.Errorf("build with AniDB 10 absent from the mirror = %v, want a refusal naming AniDB 10", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "animap.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a refused build still wrote animap.json")
	}
	check := func(snap string) error {
		return run(t.Context(), []string{"overlay", "check", "-overlay", ov, "-list", listFixture, "-aod", aodFixture, "-mirror", snap}, &bytes.Buffer{})
	}
	if err := check(unlisted); err == nil {
		t.Error("overlay check passed an unresolved sibling")
	}
	listed := writeFixture(t, t.TempDir(), "snapshot.json", &anidb.Snapshot{Commit: mirrorCommit, Anime: map[int]anidb.Anime{10: fin(12, 1, 3), 20: fin(3)}})
	if _, err := build(t, dir, ov, "", "-mirror", listed); err != nil {
		t.Errorf("build with the mirror listing AniDB 10's specials = %v", err)
	}
	if err := check(listed); err != nil {
		t.Errorf("overlay check with the mirror listing AniDB 10's specials = %v", err)
	}
}

func TestBaselineCheckShrink(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, cols ...guard.Collision) string {
		return writeFixture(t, dir, name, guard.NewBaseline(cols, nil, "c", anidb.CountBasis))
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
	older := writeFixture(t, dir, "older.json", guard.NewBaseline([]guard.Collision{c1}, nil, "c", ""))
	if err := run(t.Context(), []string{"baseline", "check-shrink", "-base", older, "-baseline", write("rederived.json", c1, c2, c3)}, &bytes.Buffer{}); err != nil {
		t.Errorf("entries added under a new basis = %v, want accepted as a re-derivation", err)
	}
	stale := writeFixture(t, dir, "stale.json", guard.NewBaseline([]guard.Collision{c1, c2, c3}, nil, "c", "another-basis/v0"))
	if err := run(t.Context(), []string{"baseline", "check-shrink", "-base", base, "-baseline", stale}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "rebase") {
		t.Errorf("a head baseline under a basis this code does not count by = %v, want a refusal naming rebase", err)
	}
}

// A baseline measured under another basis stops the build until it is
// re-derived; rebase is the only mode that may re-derive, and only once.
func TestBaselineRebase(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	list, base := collidingList(t, dir), filepath.Join(dir, "baseline.json")
	writeFixture(t, dir, "baseline.json", guard.NewBaseline(nil, nil, "c", ""))
	buildArgs := []string{
		"build", "-aod", aodFixture, "-aod-release", "2026-40", "-aod-sha256", fixtureSHA(t),
		"-list", list, "-list-commit", strings.Repeat("a", 40), "-overlay", t.TempDir(),
		"-out", filepath.Join(dir, "animap.json"), "-baseline", base, "-mirror", mirrorFixture, "-mirror-commit", mirrorCommit,
	}
	if err := run(t.Context(), buildArgs, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "baseline rebase") {
		t.Fatalf("build over a baseline from an older basis = %v, want a refusal naming baseline rebase", err)
	}
	mode := func(m string) (string, error) {
		var out bytes.Buffer
		err := run(t.Context(), []string{"baseline", m, "-baseline", base, "-list", list, "-aod", aodFixture, "-mirror", mirrorFixture, "-commit", "c", "-overlay", t.TempDir()}, &out)
		return out.String(), err
	}
	if _, err := mode("prune"); err == nil || !strings.Contains(err.Error(), "rebase") {
		t.Errorf("prune of a baseline from an older basis = %v, want a refusal naming rebase", err)
	}
	if out, err := mode("rebase"); err != nil || !strings.Contains(out, "1 collision(s)") {
		t.Fatalf("baseline rebase = %q, %v; want the one collision re-derived", out, err)
	}
	if b, err := guard.LoadBaseline(base); err != nil || b.Basis != anidb.CountBasis {
		t.Errorf("rebased baseline = %+v, %v; want basis %q", b, err, anidb.CountBasis)
	}
	if err := run(t.Context(), buildArgs, &bytes.Buffer{}); err != nil {
		t.Errorf("build over the rebased baseline = %v", err)
	}
	if _, err := mode("rebase"); err == nil || !strings.Contains(err.Error(), "only shrinks") {
		t.Errorf("a second rebase under an unchanged basis = %v, want a refusal", err)
	}
}
