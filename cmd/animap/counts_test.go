package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/skyhook"
)

func countRow(aid, regular int) anidb.Row {
	return anidb.Row{
		AniDBID: aid, RegularEpisodes: regular, Evidence: "https://anidb.net/anime/" + strconv.Itoa(aid),
		Date: "2026-10-04", Justification: "AniDB lists the regular episodes",
	}
}

// offsetEntry moves AniDB 20 onto TVDB 5006 season 1 from episode 2 and
// drops its season-0 rows, so a count of 2 or more for AniDB 30, the
// series' node with no database entry, collides on 1x2 and a count of 1
// does not.
func offsetEntry() map[string]any {
	eps := []skyhook.Episode{{Season: 1, Number: 2, AirDate: "2020-06-01"}}
	e := map[string]any{
		"anidb_id": 20, "title": "Filed under specials", "justification": "test",
		"set":      map[string]any{"tvdbid": "5006", "defaulttvdbseason": "1", "episodeoffset": "1", "mapping_list": []any{}},
		"evidence": map[string]string{"anidb": "https://anidb.net/anime/20"},
		"upstream": "TODO-PR",
		"captured": map[string]any{
			"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40),
			"node_sha256": strings.Repeat("b", 64),
			"tvdb":        map[string]any{"series": 5006, "seasons": []int{1}, "episodes": eps, "sha256": skyhook.Hash(eps)},
		},
	}
	return e
}

func TestBuildPlacesANodeThroughItsCount(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", offsetEntry())
	withCounts := func(rows ...anidb.Row) string {
		return writeFixture(t, dir, "counts.json", append([]anidb.Row{}, rows...))
	}
	if _, err := build(t, dir, ov, "", "-counts", withCounts()); err == nil || !strings.Contains(err.Error(), "AniDB 30") {
		t.Errorf("build with no count for AniDB 30 = %v, want a refusal naming AniDB 30", err)
	}
	if _, err := build(t, dir, ov, "", "-counts", withCounts(countRow(30, 1))); err != nil {
		t.Errorf("build with AniDB 30 counted at 1 = %v, want it placed on 1x1 and accepted", err)
	}
	_, err := build(t, dir, ov, "", "-counts", withCounts(countRow(30, 2)))
	if err == nil || !strings.Contains(err.Error(), "TVDB 1x2") || !strings.Contains(err.Error(), "30 ep2") {
		t.Errorf("build with AniDB 30 counted at 2 = %v, want its episode 2 colliding on TVDB 1x2", err)
	}
	check := func(rows ...anidb.Row) error {
		args := []string{"overlay", "check", "-overlay", ov, "-list", listFixture, "-aod", aodFixture, "-mirror", mirrorFixture, "-counts", withCounts(rows...)}
		return run(t.Context(), args, &bytes.Buffer{})
	}
	if err := check(countRow(30, 1)); err != nil {
		t.Errorf("overlay check with AniDB 30 counted at 1 = %v, want accepted", err)
	}
	if err := check(countRow(30, 2)); err == nil {
		t.Error("overlay check passed the collision the count reveals")
	}
}

// A counts row is the correction no source can carry, so it overrides the
// mirror's count.
func TestBuildPrefersACountsRowToTheMirror(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", offsetEntry())
	snap := snapshotWith(t, dir, map[int]anidb.Anime{20: fin(3), 30: fin(2)})
	if _, err := build(t, dir, ov, "", "-mirror", snap); err == nil || !strings.Contains(err.Error(), "30 ep2") {
		t.Fatalf("build with the mirror counting AniDB 30 at 2 = %v, want its episode 2 colliding", err)
	}
	rows := writeFixture(t, dir, "counts.json", []anidb.Row{countRow(30, 1)})
	if _, err := build(t, dir, ov, "", "-mirror", snap, "-counts", rows); err != nil {
		t.Errorf("build with the mirror at 2 and counts.json at 1 = %v, want the row's 1 used", err)
	}
}

func TestBuildRefusesAnInvalidCountsFile(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	bad := countRow(30, 1)
	bad.Evidence = "https://anidb.net/anime/31"
	if _, err := build(t, dir, t.TempDir(), "", "-counts", writeFixture(t, dir, "counts.json", []anidb.Row{bad})); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Errorf("build with a row whose evidence names another anime = %v, want a refusal", err)
	}
}

// movedList is the fixture list with AniDB 30, which has no database
// entry, moved beside AniDB 10 on TVDB 5000, a series no entry touches,
// onto the given default season.
func movedList(t *testing.T, dir, season string) string {
	t.Helper()
	b, err := os.ReadFile(listFixture)
	if err != nil {
		t.Fatal(err)
	}
	moved := bytes.Replace(b, []byte(`<anime anidbid="30" tvdbid="5006" defaulttvdbseason="1"`),
		[]byte(`<anime anidbid="30" tvdbid="5000" defaulttvdbseason="`+season+`"`), 1)
	if bytes.Equal(moved, b) {
		t.Fatal("the fixture no longer has the AniDB 30 node this test moves")
	}
	p := filepath.Join(dir, "list-"+season+".xml")
	if err := os.WriteFile(p, moved, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type baselineCase struct {
	t                                        *testing.T
	list, base, rows, outAt, mirror, overlay string
}

func (c *baselineCase) build() error {
	return run(c.t.Context(), []string{
		"build", "-aod", aodFixture, "-aod-release", "2026-40", "-aod-sha256", fixtureSHA(c.t),
		"-list", c.list, "-list-commit", strings.Repeat("a", 40), "-overlay", c.t.TempDir(),
		"-out", c.outAt, "-baseline", c.base, "-counts", c.rows, "-mirror", c.mirror, "-mirror-commit", mirrorCommit,
	}, &bytes.Buffer{})
}

func (c *baselineCase) baseline(mode string) string {
	c.t.Helper()
	var out bytes.Buffer
	ov := c.overlay
	if ov == "" {
		ov = c.t.TempDir()
	}
	args := []string{"baseline", mode, "-baseline", c.base, "-list", c.list, "-aod", aodFixture, "-commit", "c", "-overlay", ov, "-counts", c.rows, "-mirror", c.mirror}
	if err := run(c.t.Context(), args, &out); err != nil {
		c.t.Fatalf("baseline %s: %v", mode, err)
	}
	return out.String()
}

func newBaselineCase(t *testing.T, season string) *baselineCase {
	dir := t.TempDir()
	return &baselineCase{
		t: t, list: movedList(t, dir, season), base: filepath.Join(dir, "baseline.json"),
		rows: writeFixture(t, dir, "counts.json", []anidb.Row{}), outAt: filepath.Join(dir, "animap.json"),
		mirror: mirrorFixture,
	}
}

func TestCountingABaselinedNodeShrinksTheBaseline(t *testing.T) {
	noFloors(t)
	c := newBaselineCase(t, "3")
	if out := c.baseline("init"); !strings.Contains(out, "0 collision(s), 1 uncounted node(s)") {
		t.Fatalf("baseline init printed %q, want AniDB 30 as the one uncounted node", out)
	}
	before := c.base + ".before"
	b, err := os.ReadFile(c.base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(before, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c.rows = writeFixture(t, filepath.Dir(c.base), "counts.json", []anidb.Row{countRow(30, 2)})
	if err := c.build(); err != nil {
		t.Fatalf("build with AniDB 30 counted on a free season = %v", err)
	}
	if out := c.baseline("prune"); !strings.Contains(out, "0 collision(s), 0 uncounted node(s)") {
		t.Errorf("baseline prune printed %q, want AniDB 30 gone from the uncounted list", out)
	}
	if err := run(t.Context(), []string{"baseline", "check-shrink", "-base", before, "-baseline", c.base}, &bytes.Buffer{}); err != nil {
		t.Errorf("check-shrink of the pruned baseline = %v, want accepted", err)
	}
	if err := c.build(); err != nil {
		t.Errorf("build with the pruned baseline = %v", err)
	}
}

func TestACountThatRevealsACollisionBlocksAndIsNotBaselined(t *testing.T) {
	noFloors(t)
	c := newBaselineCase(t, "1")
	c.baseline("init")
	c.rows = writeFixture(t, filepath.Dir(c.base), "counts.json", []anidb.Row{countRow(30, 1)})
	err := c.build()
	if err == nil || !strings.Contains(err.Error(), "outside the baseline") || !strings.Contains(err.Error(), "TVDB 1x1") {
		t.Fatalf("build with AniDB 30 counted onto AniDB 10's 1x1 = %v, want a refusal for that collision", err)
	}
	if out := c.baseline("prune"); !strings.Contains(out, "0 collision(s), 0 uncounted node(s)") {
		t.Errorf("baseline prune printed %q, want the revealed collision kept out", out)
	}
	if err := c.build(); err == nil {
		t.Error("build after the prune passed the revealed collision")
	}
	b, err := guard.LoadBaseline(c.base)
	if err != nil || len(b.Collisions) != 0 {
		t.Errorf("pruned baseline = %+v, %v; want no collision recorded", b, err)
	}
}

func TestBaselinePruneDropsACollisionAnEntryMovesAClaimOnto(t *testing.T) {
	noFloors(t)
	c := newBaselineCase(t, "1")
	c.rows = writeFixture(t, filepath.Dir(c.base), "counts.json", []anidb.Row{countRow(30, 2)})
	if out := c.baseline("init"); !strings.Contains(out, "2 collision(s), 0 uncounted node(s)") {
		t.Fatalf("baseline init printed %q, want AniDB 30 on AniDB 10's 1x1 and 1x2", out)
	}
	eps := []skyhook.Episode{{Season: 1, Number: 2, AirDate: "2020-06-01"}}
	c.overlay = t.TempDir()
	writeFixture(t, c.overlay, "30.json", map[string]any{
		"anidb_id": 30, "title": "Node with no database entry", "justification": "test",
		"set":      map[string]any{"episodeoffset": "1"},
		"evidence": map[string]string{"anidb": "https://anidb.net/anime/30"},
		"upstream": "TODO-PR",
		"captured": map[string]any{
			"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40),
			"node_sha256": strings.Repeat("b", 64),
			"tvdb":        map[string]any{"series": 5000, "seasons": []int{1}, "episodes": eps, "sha256": skyhook.Hash(eps)},
		},
	})
	if out := c.baseline("prune"); !strings.Contains(out, "0 collision(s), 0 uncounted node(s)") {
		t.Errorf("baseline prune with AniDB 30 moved onto 1x2 and 1x3 printed %q, want both collisions out of the baseline", out)
	}
}
