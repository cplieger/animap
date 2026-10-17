package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/join"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/watch"
	"github.com/cplieger/animap/mapread"
)

// linkFile is a link of AniList al to parts, with a placeholder capture.
func linkFile(al int, parts ...overlay.Part) overlay.Link {
	return overlay.Link{
		AniListID: al, Title: "Linked " + strconv.Itoa(al), Parts: parts, Justification: "proven by title",
		Evidence: map[string]string{"anidb": "https://anidb.net/anime/" + strconv.Itoa(parts[0].AniDBID)},
		Captured: overlay.LinkCaptured{At: "2026-10-05", AniDBMirrorCommit: strings.Repeat("a", 40), AniDBSHA256: strings.Repeat("b", 64)},
	}
}

func writeLinks(t *testing.T, ov string, links ...overlay.Link) {
	t.Helper()
	dir := filepath.Join(ov, overlay.LinkDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for i := range links {
		writeFixture(t, dir, strconv.Itoa(links[i].AniListID)+".json", &links[i])
	}
}

func TestCaptureLink(t *testing.T) {
	a := titled(fin(3, 1))
	a.Specials[0].Titles = strings.Repeat("9", 64)
	facts := anidb.NewFacts(&anidb.Snapshot{Commit: mirrorCommit, Anime: map[int]anidb.Anime{10: a}}, nil, nil)
	l := linkFile(376, overlay.Part{AniDBID: 10, Specials: []int{1}})
	before := l
	out, err := captureLink(&l, facts, time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("captureLink = %v", err)
	}
	got, err := overlay.ReadLink(writeFixtureBytes(t, "376.json", out))
	if err != nil {
		t.Fatal(err)
	}
	want := overlay.LinkCaptured{At: "2026-10-15", AniDBMirrorCommit: mirrorCommit, AniDBSHA256: before.AniDBDigest(facts)}
	if got.Captured != want {
		t.Errorf("captured = %+v, want %+v", got.Captured, want)
	}
	got.Captured = before.Captured
	if !slicesEqualJSON(t, got, before) {
		t.Errorf("rewritten link = %s, want every authored field as before", out)
	}
	if !bytes.HasSuffix(out, []byte("}\n")) || !bytes.Contains(out, []byte("\n  \"anilist_id\": 376,\n  \"title\"")) {
		t.Errorf("rewritten link = %s, want indented members in file order and a final newline", out)
	}
	unlisted := linkFile(376, overlay.Part{AniDBID: 10, Specials: []int{2}})
	if _, err := captureLink(&unlisted, facts, time.Now()); err == nil || !strings.Contains(err.Error(), "AniDB lists no S2 for AniDB 10") {
		t.Errorf("captureLink(an unlisted special) = %v, want a refusal", err)
	}
}

func writeFixtureBytes(t *testing.T, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func slicesEqualJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}

// The command rewrites the file in place.
func TestCaptureLinkCommand(t *testing.T) {
	dir, ov := t.TempDir(), t.TempDir()
	writeLinks(t, ov, linkFile(101, overlay.Part{AniDBID: 10, Specials: []int{1}}))
	path := filepath.Join(ov, overlay.LinkDir, "101.json")
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1)})
	if err := run(t.Context(), []string{"overlay", "capture-link", "-link", path, "-mirror", mirror}, &bytes.Buffer{}); err != nil {
		t.Fatalf("capture-link = %v", err)
	}
	links, err := overlay.LoadLinks(ov)
	if err != nil || len(links) != 1 || links[0].Captured.AniDBMirrorCommit != mirrorCommit {
		t.Errorf("LoadLinks after capture-link = %+v, %v; want the link captured at %s", links, err, mirrorCommit)
	}
}

func TestCaptureLinkRefusesHandCopiedAniDBFacts(t *testing.T) {
	dir := t.TempDir()
	base := map[string]any{
		"anilist_id": 376, "parts": []map[string]any{{"anidb_id": 999, "specials": []int{1}}},
		"title": "OVA", "justification": "AniDB 999 S1.", "evidence": map[string]string{"anidb": "https://anidb.net/anime/999"},
	}
	for field, value := range map[string]any{
		"parent_anidb_specials": [][]any{{2, 1, "2005-04-21"}},
		"captured":              map[string]any{"anidb": map[string]any{"episodes": [][]any{{2, 1, "2005-04-21"}}}},
	} {
		b := map[string]any{field: value}
		maps.Copy(b, base)
		path := writeFixture(t, dir, "376.json", b)
		want := `unknown field "` + field + `"`
		if field == "captured" {
			want = `unknown field "anidb"`
		}
		args := []string{"overlay", "capture-link", "-link", path, "-mirror", mirrorFixture}
		if err := run(t.Context(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("capture-link on a link with %s = %v, want %s refused", field, err, want)
		}
	}
}

// specialLink is a link of AniList 101 to AniDB 10's S1, which the fixture
// list's row puts on TVDB 5000 0x2.
func specialLink() overlay.Link { return linkFile(101, overlay.Part{AniDBID: 10, Specials: []int{1}}) }

// snapshotWithParentSpecial writes a snapshot listing AniDB 10 with one
// special, S<n>.
func snapshotWithParentSpecial(t *testing.T, dir string, n int) string {
	t.Helper()
	return snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, n)})
}

// overlay check and the build prove each link against the episodes the
// AniDB mirror lists, so a mirror bump that drops one stops the release.
func TestLinksAreProvenAgainstTheMirror(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeLinks(t, ov, specialLink())
	check := func(n int) error {
		snap := snapshotWithParentSpecial(t, dir, n)
		return run(t.Context(), []string{"overlay", "check", "-overlay", ov, "-list", listFixture, "-aod", aodFixture, "-mirror", snap}, &bytes.Buffer{})
	}
	if err := check(1); err != nil {
		t.Errorf("overlay check with the mirror listing S1 = %v, want accepted", err)
	}
	if err := check(2); err == nil || !strings.Contains(err.Error(), "AniDB lists no S1 for AniDB 10") {
		t.Errorf("overlay check with the mirror listing S2 alone = %v, want the link refused", err)
	}
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWithParentSpecial(t, dir, 1)); err != nil {
		t.Errorf("build with the mirror listing S1 = %v, want accepted", err)
	}
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWithParentSpecial(t, dir, 2)); err == nil || !strings.Contains(err.Error(), "AniDB lists no S1 for AniDB 10") {
		t.Errorf("build with the mirror listing S2 alone = %v, want the link refused", err)
	}
}

// built builds with mirror and extra flags and returns the decoded
// document and its index.
func built(t *testing.T, dir, ov, mirror string, extra ...string) (*mapread.Document, *mapread.Index) {
	t.Helper()
	if _, err := build(t, dir, ov, "", append([]string{"-mirror", mirror, "-stats", filepath.Join(dir, "stats.json")}, extra...)...); err != nil {
		t.Fatalf("build: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "animap.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := mapread.Decode(f, mapread.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return doc, mapread.NewIndex(doc)
}

// tvdbOf is AniDB episode ref's TVDB statement, failing when the build has
// none.
func tvdbOf(t *testing.T, x *mapread.Index, ref mapread.Ref) *mapread.Statement {
	t.Helper()
	ep, ok := x.Episode(ref)
	if !ok || ep.TVDB == nil {
		t.Fatalf("Episode(%+v) = %+v, %t; want a TVDB statement", ref, ep, ok)
	}
	return ep.TVDB
}

func stated(s *mapread.Statement, targets ...mapread.Target) bool {
	return s.State == mapread.Lands && slices.Equal(s.Targets, targets)
}

func TestBuildAppliesASpecialsLink(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeLinks(t, ov, specialLink())
	_, x := built(t, dir, ov, snapshotWithParentSpecial(t, dir, 1))
	e, _ := x.Entry(101)
	if want := []mapread.Part{{AniDBID: 10, Kind: mapread.Special, Start: 1, End: 1}}; !slices.Equal(e.Parts, want) {
		t.Errorf("AniList 101 parts = %+v, want %+v", e.Parts, want)
	}
	if s := tvdbOf(t, x, mapread.Ref{AniDB: 10, Kind: mapread.Special, Number: 1}); !stated(s, mapread.Target{Season: 0, Episode: 2}) {
		t.Errorf("AniDB 10 S1 on TVDB = %+v, want its row's 0x2, stated", s)
	}
	if r := x.Routes(e); !slices.Equal(r.TVDB, []int{5000}) || r.TMDBMovies != nil {
		t.Errorf("routes = %+v, want TVDB 5000 and no film", r)
	}
	var st buildStats
	if err := readJSON(filepath.Join(dir, "stats.json"), &st); err != nil {
		t.Fatal(err)
	}
	if st.Links.Applied != 1 || len(st.Links.Skipped) != 0 {
		t.Errorf("stats links = %+v, want one applied", st.Links)
	}
}

// The build publishes a regular-episode link as a part, and refuses one
// whose episode the mirror does not list or the collision check would not
// place.
func TestBuildAppliesARegularEpisodeLink(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	// The fixture list's 13-24 row puts AniDB 10's ep13 on TVDB 1x1.
	writeLinks(t, ov, linkFile(101, overlay.Part{AniDBID: 10, Episodes: []int{13}}))
	_, x := built(t, dir, ov, snapshotWith(t, dir, map[int]anidb.Anime{10: fin(24)}))
	e, _ := x.Entry(101)
	if want := []mapread.Part{{AniDBID: 10, Kind: mapread.Regular, Start: 13, End: 13}}; !slices.Equal(e.Parts, want) {
		t.Errorf("AniList 101 parts = %+v, want %+v", e.Parts, want)
	}
	if s := tvdbOf(t, x, mapread.Ref{AniDB: 10, Kind: mapread.Regular, Number: 13}); !stated(s, mapread.Target{Season: 1, Episode: 1}) {
		t.Errorf("AniDB 10 ep13 on TVDB = %+v, want 1x1", s)
	}
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12)})); err == nil || !strings.Contains(err.Error(), "AniDB lists no ep13 for AniDB 10") {
		t.Errorf("build with the mirror listing 12 episodes = %v, want ep13 refused", err)
	}
	airing := fin(24)
	airing.Finished = false
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWith(t, dir, map[int]anidb.Anime{10: airing})); err == nil || !strings.Contains(err.Error(), "AniDB 10 counts 12 regular episodes, so the collision check places no ep13") {
		t.Errorf("build with AniDB 10 counted at the database's 12 = %v, want ep13 refused", err)
	}
}

// On an absolute-numbered parent the linked episode lands where the parent
// entry's captured TVDB order numbers it, as does every episode the
// capture numbers; with no entry the link is refused.
func TestBuildLinksAnAbsoluteParent(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	// The fixture list's AniDB 19 is absolute on TVDB 5004.
	writeLinks(t, ov, linkFile(101, overlay.Part{AniDBID: 19, Episodes: []int{2}}))
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{19: fin(3)})
	if _, err := build(t, dir, ov, "", "-mirror", mirror); err == nil || !strings.Contains(err.Error(), "no overlay entry for AniDB 19 captures one TVDB episode with that number") {
		t.Errorf("build with no entry for AniDB 19 = %v, want the absolute number refused", err)
	}
	eps := []skyhook.Episode{{Season: 3, Number: 1, Absolute: 2}, {Season: 3, Number: 2, Absolute: 3}}
	writeFixture(t, ov, "19.json", map[string]any{
		"anidb_id": 19, "title": "Absolute run", "justification": "test", "upstream": "TODO-PR",
		"set":      map[string]any{"defaulttvdbseason": "a"},
		"evidence": map[string]string{"anidb": "https://anidb.net/anime/19"},
		"captured": map[string]any{
			"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40), "node_sha256": strings.Repeat("b", 64),
			"tvdb": map[string]any{"series": 5004, "seasons": nil, "episodes": eps, "sha256": skyhook.Hash(eps)},
		},
	})
	_, x := built(t, dir, ov, mirror)
	for k, want := range map[int]*mapread.Statement{
		1: {},
		2: {State: mapread.Lands, Targets: []mapread.Target{{Season: 3, Episode: 1}}},
		3: {State: mapread.Lands, Targets: []mapread.Target{{Season: 3, Episode: 2}}},
	} {
		if s := tvdbOf(t, x, mapread.Ref{AniDB: 19, Kind: mapread.Regular, Number: k}); s.State != want.State || !slices.Equal(s.Targets, want.Targets) {
			t.Errorf("AniDB 19 ep%d = %+v, want %+v", k, s, want)
		}
	}
}

// A link names a promotion or other episode by its Anime-Lists season 0
// number; the part names it in AniDB's own kind and number, and it lands
// where the parent's row puts it, here a regular season.
func TestBuildAppliesALinkToOtherEpisodes(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "10.json", map[string]any{
		"anidb_id": 10, "title": "Series with season 1", "justification": "test", "upstream": "TODO-PR",
		"set": map[string]any{"mapping_list": []any{
			map[string]any{"anidb_season": 0, "tvdb_season": 0, "episodes": [][]int{{1, 2}, {201, 3}}},
			map[string]any{"anidb_season": 0, "tvdb_season": 3, "tmdb_season": 3, "episodes": [][]int{{401, 1}, {402, 2}}},
		}},
		"evidence": map[string]string{"anidb": "https://anidb.net/anime/10"},
		"captured": map[string]any{"at": "2026-10-15", "anime_lists_commit": strings.Repeat("a", 40), "node_sha256": strings.Repeat("b", 64)},
	})
	writeLinks(t, ov, linkFile(997, overlay.Part{AniDBID: 10, Specials: []int{401, 402}}), linkFile(998, overlay.Part{AniDBID: 10, Specials: []int{201}}))
	parent := fin(12, 1)
	parent.Extras = []anidb.Extra{{Kind: anidb.Promotion, Number: 1, SeasonZero: 201}, {Kind: anidb.Other, Number: 1, SeasonZero: 401}, {Kind: anidb.Other, Number: 2, SeasonZero: 402}}
	_, x := built(t, dir, ov, snapshotWith(t, dir, map[int]anidb.Anime{10: parent}))
	for al, want := range map[int][]mapread.Part{
		997: {{AniDBID: 10, Kind: mapread.Other, Start: 1, End: 2}},
		998: {{AniDBID: 10, Kind: mapread.Promotion, Start: 1, End: 1}},
	} {
		if e, _ := x.Entry(al); !slices.Equal(e.Parts, want) {
			t.Errorf("AniList %d parts = %+v, want %+v", al, e.Parts, want)
		}
	}
	for _, tc := range []struct {
		ref  mapread.Ref
		want mapread.Target
	}{
		{mapread.Ref{AniDB: 10, Kind: mapread.Other, Number: 1}, mapread.Target{Season: 3, Episode: 1}},
		{mapread.Ref{AniDB: 10, Kind: mapread.Other, Number: 2}, mapread.Target{Season: 3, Episode: 2}},
		{mapread.Ref{AniDB: 10, Kind: mapread.Promotion, Number: 1}, mapread.Target{Season: 0, Episode: 3}},
	} {
		if s := tvdbOf(t, x, tc.ref); !stated(s, tc.want) {
			t.Errorf("%+v on TVDB = %+v, want %+v", tc.ref, s, tc.want)
		}
	}
	parent.Extras = parent.Extras[:2]
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWith(t, dir, map[int]anidb.Anime{10: parent})); err == nil || !strings.Contains(err.Error(), "AniDB lists no S402 for AniDB 10") {
		t.Errorf("build with the mirror lacking other episode 2 = %v, want the link refused", err)
	}
}

// A link naming every episode of a gapped list is no identity: the entry
// gets the listed episodes as ranged parts, never 1 to last. A whole part
// on that list is refused, since its combined part would name 1 to Count.
// The fixture's AniDB 30 has no database entry, so no AniList id carries it.
func TestBuildLinksAGappedList(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	gapped := titled(anidb.Anime{Regular: []anidb.Episode{{Number: 1}, {Number: 2}, {Number: 5}}, Specials: []anidb.Episode{}, Finished: true, Titles: fixtureOtherTitles})
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{30: gapped})
	counts := writeFixture(t, dir, "counts.json", []anidb.Row{countRow(30, 5)})
	writeLinks(t, ov, linkFile(999, overlay.Part{AniDBID: 30, Episodes: []int{1, 2, 5}}))
	_, x := built(t, dir, ov, mirror, "-counts", counts)
	e, _ := x.Entry(999)
	if want := []mapread.Part{{AniDBID: 30, Kind: mapread.Regular, Start: 1, End: 2}, {AniDBID: 30, Kind: mapread.Regular, Start: 5, End: 5}}; !slices.Equal(e.Parts, want) {
		t.Errorf("AniList 999 parts = %+v, want %+v", e.Parts, want)
	}
	if got := x.Episodes(e); len(got) != 3 || got[2].Refs[0].Number != 5 {
		t.Errorf("AniList 999 episodes = %+v, want three, the third AniDB ep5", got)
	}
	writeLinks(t, ov, linkFile(999, overlay.Part{AniDBID: 30, Whole: true}))
	if _, err := build(t, dir, ov, "", "-mirror", mirror, "-counts", counts); err == nil || !strings.Contains(err.Error(), "AniList 999: AniDB 30 lists regular episodes with a gap before ep5") {
		t.Errorf("build with a whole link on the gapped list = %v, want it refused", err)
	}
}

// A link publishes the episodes it names and no more: a list short of the
// anime's count is its ranged part, one past it is refused, and only the
// counted list 1 to Count is an identity. With no count yet, a link to the
// mirror's whole list is one, naming no episode until a count exists, and
// the watch reads its entry as of unknown count.
func TestBuildLinkIdentityNeedsTheCount(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	airing := func(n int) string {
		a := fin(n)
		a.Finished = false
		return snapshotWith(t, dir, map[int]anidb.Anime{30: a})
	}
	upTo := func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	counts := writeFixture(t, dir, "counts.json", []anidb.Row{countRow(30, 12)})
	link := func(n int) { writeLinks(t, ov, linkFile(999, overlay.Part{AniDBID: 30, Episodes: upTo(n)})) }
	for _, tc := range []struct {
		name       string
		listed, n  int
		parts      []mapread.Part
		episodes   int
		withCounts bool
	}{
		{"a list short of its count", 3, 3, []mapread.Part{{AniDBID: 30, Kind: mapread.Regular, Start: 1, End: 3}}, 3, true},
		{"the counted list", 12, 12, []mapread.Part{{AniDBID: 30, Kind: mapread.Regular}}, 12, true},
		{"an uncounted list", 3, 3, []mapread.Part{{AniDBID: 30, Kind: mapread.Regular}}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link(tc.n)
			var extra []string
			if tc.withCounts {
				extra = []string{"-counts", counts}
			}
			_, x := built(t, dir, ov, airing(tc.listed), extra...)
			e, _ := x.Entry(999)
			if !slices.Equal(e.Parts, tc.parts) || len(x.Episodes(e)) != tc.episodes {
				t.Errorf("AniList 999 = %+v with %d episodes, want parts %+v and %d episodes", e.Parts, len(x.Episodes(e)), tc.parts, tc.episodes)
			}
			if tc.withCounts {
				return
			}
			w := watch.Evaluate([]int{999}, x)
			if len(w) != 1 || w[0].AniDBID != 30 || w[0].TVDBID != 5006 || !slices.Equal(w[0].GapNames(), []string{"episode_count_unknown"}) {
				t.Errorf("watch = %+v, want AniList 999 as AniDB 30 on TVDB 5006 of unknown count", w)
			}
		})
	}
	link(14)
	if _, err := build(t, dir, ov, "", "-mirror", airing(14), "-counts", counts); err == nil || !strings.Contains(err.Error(), "AniDB 30 counts 12 regular episodes, so the collision check places no ep13") {
		t.Errorf("build with a link to 14 episodes of a 12-episode anime = %v, want ep13 refused", err)
	}
}

// The review classes a link with the counts the build reads, so a list
// short of its anime's count is a linked link there too, and the guard's
// claims cover the counted episodes.
func TestReviewClassesLinksWithTheBuildsCounts(t *testing.T) {
	dir, ov := t.TempDir(), t.TempDir()
	a := fin(3)
	a.Finished = false
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{30: a})
	counts := writeFixture(t, dir, "counts.json", []anidb.Row{countRow(30, 12)})
	writeLinks(t, ov, linkFile(999, overlay.Part{AniDBID: 30, Episodes: []int{1, 2, 3}}))
	c := &reviewConfig{listPath: listFixture, overlayDir: ov, mirrorPath: mirror, countsPath: counts, aodPath: aodFixture, reviewedDir: t.TempDir()}
	r, err := loadReview(c, "", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if ids, linked := r.applied.Identities(), r.applied.Linked(); len(ids) != 0 || len(linked) != 1 || linked[0].Link.AniListID != 999 {
		t.Errorf("applied identities %+v, linked %+v; want AniList 999 linked, as the build classes it", ids, linked)
	}
	claims := r.claimIndex([]int{30})[claimKey{side: animelists.TVDB, show: 5006}]
	for k := 1; k <= 12; k++ {
		if want := fmt.Sprintf("30 ep%d", k); !slices.Contains(claims[[2]int{1, k}], want) {
			t.Errorf("claims on TVDB 5006 1x%d = %v, want %q: Count is the counts row's 12", k, claims[[2]int{1, k}], want)
		}
	}
}

// capture-link reads counts as the build does, so a whole part on an
// airing anime is accepted at the count a counts row or the database gives
// and refused on the mirror alone, which counts it 0.
func TestCaptureLinkCountsAsTheBuildDoes(t *testing.T) {
	dir, ov := t.TempDir(), t.TempDir()
	thirty, twelve := fin(3), fin(2)
	thirty.Finished, twelve.Finished = false, false
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{30: thirty, 12: twelve})
	counts := writeFixture(t, dir, "counts.json", []anidb.Row{countRow(30, 3)})
	writeLinks(t, ov, linkFile(998, overlay.Part{AniDBID: 30, Whole: true}), linkFile(999, overlay.Part{AniDBID: 12, Whole: true}))
	capture := func(al int, extra ...string) error {
		args := []string{"overlay", "capture-link", "-link", filepath.Join(ov, overlay.LinkDir, strconv.Itoa(al)+".json"), "-mirror", mirror}
		return run(t.Context(), append(args, extra...), &bytes.Buffer{})
	}
	for _, tc := range []struct {
		name  string
		al    int
		extra []string
		says  string
	}{
		{"a counts row", 998, []string{"-counts", counts}, ""},
		{"the database", 999, []string{"-aod", aodFixture}, ""},
		{"the mirror alone", 999, nil, "AniDB 12 lists 2 regular episodes and counts 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := capture(tc.al, tc.extra...)
			if (tc.says == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.says) {
				t.Errorf("capture-link AniList %d %v = %v, want %q", tc.al, tc.extra, err, tc.says)
			}
		})
	}
}

// An identity link gives the AniList entry the anime as its part; a link
// to an id anime-offline-database already gives another AniList id is
// refused, and a link to an AniList id with no entry creates one.
func TestBuildIdentityAndCreatedLinks(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeLinks(t, ov, linkFile(101, overlay.Part{AniDBID: 12, Episodes: []int{1, 2}}), linkFile(999, overlay.Part{AniDBID: 10, Specials: []int{1}}))
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1), 12: fin(2)})
	plain, _ := built(t, dir, t.TempDir(), mirror)
	doc, x := built(t, dir, ov, mirror)
	if e, _ := x.Entry(101); !slices.Equal(e.Parts, []mapread.Part{{AniDBID: 12, Kind: mapread.Regular}}) || e.Type != "SPECIAL" || !slices.Equal(e.IDs.MAL, []int{1001}) {
		t.Errorf("AniList 101 = %+v, want AniDB 12 as its part with its own type and MAL id", e)
	}
	if e, ok := x.Entry(999); !ok || e.Type != "" || e.Episodes != 0 || !slices.Equal(e.Parts, []mapread.Part{{AniDBID: 10, Kind: mapread.Special, Start: 1, End: 1}}) {
		t.Errorf("created AniList 999 = %+v, %t; want AniDB 10's S1 as its part and no type", e, ok)
	}
	if len(doc.Entries) != len(plain.Entries)+1 || !reflect.DeepEqual(doc.Anime, plain.Anime) {
		t.Errorf("entries = %d, want %d: one created; the anime rows unchanged", len(doc.Entries), len(plain.Entries)+1)
	}
	every := make([]int, 12)
	for i := range every {
		every[i] = i + 1
	}
	writeLinks(t, ov, linkFile(101, overlay.Part{AniDBID: 10, Episodes: every}))
	if _, err := build(t, dir, ov, "", "-mirror", mirror); err == nil || !strings.Contains(err.Error(), "AniList 101: anime-offline-database already gives AniDB 10 to AniList 100") {
		t.Errorf("build with an identity link to AniDB 10 = %v, want it refused", err)
	}
}

// An identity link whose AniList id anime-offline-database has since given
// the same AniDB id is skipped softly and changes no row; one whose AniDB
// id another AniList id carries too is still refused, whichever is lower.
func TestBuildIdentityLinkLanded(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	every := make([]int, 12)
	for i := range every {
		every[i] = i + 1
	}
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1), 13: fin(1)})
	t.Run("same AniList id", func(t *testing.T) {
		ov := t.TempDir()
		writeLinks(t, ov, linkFile(100, overlay.Part{AniDBID: 10, Episodes: every}))
		plain, _ := built(t, dir, t.TempDir(), mirror)
		doc, _ := built(t, dir, ov, mirror)
		if !reflect.DeepEqual(doc.Entries, plain.Entries) || !reflect.DeepEqual(doc.Anime, plain.Anime) {
			t.Errorf("entries = %+v, want every row as the build without the link made it (%+v)", doc.Entries, plain.Entries)
		}
		var st buildStats
		if err := readJSON(filepath.Join(dir, "stats.json"), &st); err != nil {
			t.Fatal(err)
		}
		want := []join.Skipped{{AniList: 100, Reason: "anime-offline-database now links AniDB 10"}}
		if st.Links.Applied != 0 || !reflect.DeepEqual(st.Links.Skipped, want) {
			t.Errorf("stats links = %+v, want none applied and %+v skipped", st.Links, want)
		}
	})
	t.Run("another AniList id too", func(t *testing.T) {
		ov := t.TempDir()
		// The fixture database gives AniDB 13 to AniList 102 and 103.
		writeLinks(t, ov, linkFile(102, overlay.Part{AniDBID: 13, Episodes: []int{1}}))
		if _, err := build(t, dir, ov, "", "-mirror", mirror); err == nil || !strings.Contains(err.Error(), "AniList 102: anime-offline-database already gives AniDB 13 to AniList 103") {
			t.Errorf("build with an identity link 102 to AniDB 13 = %v, want it refused naming AniList 103", err)
		}
	})
}

// skippedLinks are a link to AniDB 10's S2 that the fixture database skips,
// once per reason. It gives AniList 100 AniDB 10, AniList 104 AniDB 14 and
// 15, and AniList 101 one episode and no AniDB id.
var skippedLinks = []struct {
	name   string
	link   overlay.Link
	reason string
}{
	{"one database AniDB id", linkFile(100, overlay.Part{AniDBID: 10, Specials: []int{2}}), "anime-offline-database now links AniDB 10"},
	{"two database AniDB ids", linkFile(104, overlay.Part{AniDBID: 10, Specials: []int{2}}), "anime-offline-database gives it 2 AniDB ids"},
	{"another episode count", linkFile(101, overlay.Part{AniDBID: 10, Specials: []int{2, 4}}), "the entry has 1 episodes, the link 2"},
}

// linkReaders is what each review reader of link claims sees of AniList
// al and AniDB 10: the date check's linked episodes, the watch's subject
// for al's gap, and explain's nodes, links and claims.
type linkReaders struct {
	Linked   map[int]map[[2]int]int
	Claims   map[claimKey]map[[2]int][]string
	WatchKey string
	Nodes    []int
	Links    []overlay.Link
}

func reviewReaders(t *testing.T, ov, mirror string, al int) linkReaders {
	t.Helper()
	c := &reviewConfig{listPath: listFixture, overlayDir: ov, mirrorPath: mirror, aodPath: aodFixture, reviewedDir: t.TempDir()}
	r, err := loadReview(c, "", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := review.ParseKey(review.AniListKey(al))
	return linkReaders{
		Linked: r.linked, Claims: r.claimIndex([]int{10}), WatchKey: r.watchKey(&watch.Entry{AniListID: al, AniDBID: 10}),
		Nodes: r.subjectIDs(sub), Links: r.detail(sub).Links,
	}
}

// A link the build skips claims nothing: the anime rows, the entries, the
// collision report and every review reader are as they are without it,
// and the stats say why it was skipped.
func TestBuildSkippedLinkClaimsNothing(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	// AniDB 10's S2 and S4 have no row, so they read by their defaults,
	// which a link's claim would state.
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1, 2, 4)})
	none := t.TempDir()
	plain, _ := built(t, dir, none, mirror)
	var plainStats buildStats
	if err := readJSON(filepath.Join(dir, "stats.json"), &plainStats); err != nil {
		t.Fatal(err)
	}
	if s := tvdbOf(t, mapread.NewIndex(plain), mapread.Ref{AniDB: 10, Kind: mapread.Special, Number: 2}); !stated(s, mapread.Target{Season: 0, Episode: 2, Default: true}) {
		t.Fatalf("AniDB 10 S2 with no link = %+v, want its default 0x2", s)
	}
	for _, tc := range skippedLinks {
		t.Run(tc.name, func(t *testing.T) {
			ov := t.TempDir()
			writeLinks(t, ov, tc.link)
			doc, _ := built(t, dir, ov, mirror)
			if !reflect.DeepEqual(doc.Anime, plain.Anime) {
				t.Errorf("anime rows differ from the build without the link: a skipped link's claims reached them")
			}
			if !reflect.DeepEqual(doc.Entries, plain.Entries) {
				t.Errorf("entries differ from the build without the link: a skipped link's parts reached them")
			}
			var st buildStats
			if err := readJSON(filepath.Join(dir, "stats.json"), &st); err != nil {
				t.Fatal(err)
			}
			want := []join.Skipped{{AniList: tc.link.AniListID, Reason: tc.reason}}
			if st.Links.Applied != 0 || !reflect.DeepEqual(st.Links.Skipped, want) {
				t.Errorf("stats links = %+v, want none applied and %+v skipped", st.Links, want)
			}
			if !reflect.DeepEqual(st.Collisions, plainStats.Collisions) {
				t.Errorf("collisions = %+v, want the build's without the link, %+v", st.Collisions, plainStats.Collisions)
			}
			if got, want := reviewReaders(t, ov, mirror, tc.link.AniListID), reviewReaders(t, none, mirror, tc.link.AniListID); !reflect.DeepEqual(got, want) {
				t.Errorf("review readers = %+v, want what they read without the link, %+v", got, want)
			}
		})
	}
}

// A skipped link's claim does not block an applied link's on the same
// AniDB episode and TVDB target: the build applies the one and skips the
// other.
func TestBuildSkippedLinkBlocksNoAppliedLink(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1, 2, 4)})
	applied := linkFile(999, overlay.Part{AniDBID: 10, Specials: []int{2}})
	for _, tc := range skippedLinks {
		t.Run(tc.name, func(t *testing.T) {
			ov := t.TempDir()
			writeLinks(t, ov, tc.link, applied)
			doc, x := built(t, dir, ov, mirror)
			if e, ok := x.Entry(999); !ok || !slices.Equal(e.Parts, []mapread.Part{{AniDBID: 10, Kind: mapread.Special, Start: 2, End: 2}}) {
				t.Errorf("AniList 999 = %+v, %t; want AniDB 10's S2 as its part", e, ok)
			}
			if s := tvdbOf(t, x, mapread.Ref{AniDB: 10, Kind: mapread.Special, Number: 2}); !stated(s, mapread.Target{Season: 0, Episode: 2}) {
				t.Errorf("AniDB 10 S2 = %+v, want the applied link's 0x2", s)
			}
			var st buildStats
			if err := readJSON(filepath.Join(dir, "stats.json"), &st); err != nil {
				t.Fatal(err)
			}
			want := []join.Skipped{{AniList: tc.link.AniListID, Reason: tc.reason}}
			if st.Links.Applied != 1 || !reflect.DeepEqual(st.Links.Skipped, want) || doc.Sources.Overlay.Links != 1 {
				t.Errorf("stats links = %+v, overlay links %d; want one applied and %+v skipped", st.Links, doc.Sources.Overlay.Links, want)
			}
		})
	}
}

// A review that cannot class the links as the build does, for want of the
// mirror or the database, leaves the watch unevaluated rather than file a
// linked entry's gap under the AniDB subject; with no link it watches.
func TestReviewWatchesNothingWithLinksItCannotClass(t *testing.T) {
	dir := t.TempDir()
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1, 2, 4)})
	rel := writeFixtureBytes(t, "release.json", previousDoc(t, mapread.Version, 1))
	linked := t.TempDir()
	writeLinks(t, linked, linkFile(999, overlay.Part{AniDBID: 10, Specials: []int{2}}))
	const noMirror = "the AniDB mirror was not read, so no overlay link is applied"
	const noAOD = "anime-offline-database was not read, so which overlay links the build applies is unknown"
	for _, tc := range []struct {
		name, ov, mirror, aod, reason string
	}{
		{"no mirror", linked, "", aodFixture, noMirror},
		{"no database", linked, mirror, "", noAOD},
		{"neither", linked, "", "", noMirror},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &reviewConfig{listPath: listFixture, overlayDir: tc.ov, mirrorPath: tc.mirror, aodPath: tc.aod, releasePath: rel, reviewedDir: t.TempDir()}
			r, err := loadReview(c, "", slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			r.readWatch(t.Context(), c, slog.New(slog.DiscardHandler))
			if h := r.health[review.Watch]; h.Evaluated || h.UnevaluatedReason != tc.reason {
				t.Errorf("watch = %+v, want unevaluated because %q", h, tc.reason)
			}
		})
	}
	for _, tc := range []struct{ name, ov, mirror, aod string }{
		{"no link", t.TempDir(), "", ""},
		{"both read", linked, mirror, aodFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &reviewConfig{listPath: listFixture, overlayDir: tc.ov, mirrorPath: tc.mirror, aodPath: tc.aod, releasePath: rel, reviewedDir: t.TempDir()}
			r, err := loadReview(c, "", slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			r.readWatch(t.Context(), c, slog.New(slog.DiscardHandler))
			// No watch config is given, so a run past the links stops at it.
			if h := r.health[review.Watch]; h.UnevaluatedReason != "the watch-set config is unreadable" {
				t.Errorf("watch = %+v, want the links not to stop it", h)
			}
		})
	}
}
