package main

import (
	"bytes"
	"encoding/json"
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
	"github.com/cplieger/animap/internal/join"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
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

// The command rewrites the file in place, from the mirror alone.
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

// built builds with mirror and returns the records by AniList id, and the
// AniDB-only ones by AniDB id.
func built(t *testing.T, dir, ov, mirror string) (al, ad map[int]schema.Record) {
	t.Helper()
	if _, err := build(t, dir, ov, "", "-mirror", mirror, "-stats", filepath.Join(dir, "stats.json")); err != nil {
		t.Fatalf("build: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "animap.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc schema.Document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	al, ad = map[int]schema.Record{}, map[int]schema.Record{}
	for _, r := range doc.Records {
		if r.AniListID > 0 {
			al[r.AniListID] = r
		} else {
			ad[r.AniDBID] = r
		}
	}
	return al, ad
}

func TestBuildAppliesASpecialsLink(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeLinks(t, ov, specialLink())
	al, _ := built(t, dir, ov, snapshotWithParentSpecial(t, dir, 1))
	r := al[101]
	if p := r.AniDBParent; p == nil || p.AniDBID != 10 || !slices.Equal(p.Specials, []int{1}) || r.TVDBID != 5000 || r.TVDBSeason == nil || *r.TVDBSeason != 0 ||
		len(r.MappingList) != 1 || !slices.EqualFunc(r.MappingList[0].Episodes, [][]int{{1, 2}}, slices.Equal) || len(r.TVDBPlacement) != 1 {
		t.Errorf("AniList 101 = %+v, want anidb_parent AniDB 10 S1 on TVDB 5000 0x2", r)
	}
	var st buildStats
	if err := readJSON(filepath.Join(dir, "stats.json"), &st); err != nil {
		t.Fatal(err)
	}
	if st.Links.Applied != 1 || len(st.Links.Skipped) != 0 {
		t.Errorf("stats links = %+v, want one applied", st.Links)
	}
}

// The build publishes a regular-episode link with no anidb_parent, and
// refuses one whose episode the mirror does not list or the collision
// check would not place.
func TestBuildAppliesARegularEpisodeLink(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	// The fixture list's 13-24 row puts AniDB 10's ep13 on TVDB 1x1.
	writeLinks(t, ov, linkFile(101, overlay.Part{AniDBID: 10, Episodes: []int{13}}))
	al, _ := built(t, dir, ov, snapshotWith(t, dir, map[int]anidb.Anime{10: fin(24)}))
	if r := al[101]; r.AniDBParent != nil || r.TVDBID != 5000 || r.TVDBSeason == nil || *r.TVDBSeason != 1 || len(r.MappingList) != 1 ||
		!slices.EqualFunc(r.MappingList[0].Episodes, [][]int{{1, 1}}, slices.Equal) || r.TMDBTVID != 0 {
		t.Errorf("AniList 101 = %+v, want TVDB 5000 1x1, no anidb_parent and no TMDB", r)
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
// entry's captured TVDB order numbers it; with no entry the link is refused.
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
	al, _ := built(t, dir, ov, mirror)
	if r := al[101]; r.TVDBID != 5004 || r.TVDBSeason == nil || *r.TVDBSeason != 3 || len(r.MappingList) != 1 || !slices.EqualFunc(r.MappingList[0].Episodes, [][]int{{1, 1}}, slices.Equal) {
		t.Errorf("AniList 101 = %+v, want TVDB 5004 3x1 from absolute 2", r)
	}
}

// A link names a promotion or other episode by its Anime-Lists season 0
// number, and lands where the parent's row puts it, here a regular season.
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
	parent.Extras = []anidb.Episode{{Number: 201}, {Number: 401}, {Number: 402}}
	al, _ := built(t, dir, ov, snapshotWith(t, dir, map[int]anidb.Anime{10: parent}))
	if r := al[997]; r.AniDBParent == nil || !slices.Equal(r.AniDBParent.Specials, []int{401, 402}) || r.TVDBID != 5000 || r.TVDBSeason == nil || *r.TVDBSeason != 3 ||
		len(r.MappingList) != 1 || !slices.EqualFunc(r.MappingList[0].Episodes, [][]int{{1, 1}, {2, 2}}, slices.Equal) {
		t.Errorf("AniList 997 = %+v, want anidb_parent AniDB 10 401 and 402 on TVDB 5000 3x1 and 3x2", r)
	}
	if r := al[998]; r.AniDBParent == nil || r.AniDBParent.AniDBID != 10 || !slices.Equal(r.AniDBParent.Specials, []int{201}) || r.TVDBSeason == nil || *r.TVDBSeason != 0 ||
		len(r.MappingList) != 1 || !slices.EqualFunc(r.MappingList[0].Episodes, [][]int{{1, 3}}, slices.Equal) {
		t.Errorf("AniList 998 = %+v, want anidb_parent AniDB 10 201 on TVDB 5000 0x3", r)
	}
	parent.Extras = parent.Extras[:2]
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWith(t, dir, map[int]anidb.Anime{10: parent})); err == nil || !strings.Contains(err.Error(), "AniDB lists no S402 for AniDB 10") {
		t.Errorf("build with the mirror lacking other episode 2 = %v, want the link refused", err)
	}
}

// An identity link gives the AniList record the AniDB id and the node's
// facts, and the AniDB-only record of that id goes; a link to an id
// anime-offline-database already gives another AniList id is refused, and
// a link to an AniList id with no record creates one.
func TestBuildIdentityAndCreatedLinks(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeLinks(t, ov, linkFile(101, overlay.Part{AniDBID: 12, Episodes: []int{1, 2}}), linkFile(999, overlay.Part{AniDBID: 10, Specials: []int{1}}))
	mirror := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(12, 1), 12: fin(2)})
	plainAL, plainAD := built(t, dir, t.TempDir(), mirror)
	al, ad := built(t, dir, ov, mirror)
	if r := al[101]; r.AniDBID != 12 || r.Type != "SPECIAL" || r.MALID != 1001 || r.TVDBID != plainAD[12].TVDBID || r.Episodes != 2 {
		t.Errorf("AniList 101 = %+v, want AniDB 12 with its node facts %+v and its own type and MAL", r, plainAD[12])
	}
	if _, ok := ad[12]; ok {
		t.Error("the AniDB-only record of AniDB 12 is still published")
	}
	if r := al[999]; r.Episodes != 1 || r.Type != "" || r.TVDBID != 5000 || r.AniDBParent == nil {
		t.Errorf("created AniList 999 = %+v, want one episode on TVDB 5000 and no type", r)
	}
	if len(al)+len(ad) != len(plainAL)+len(plainAD) {
		t.Errorf("records = %d, want %d: one created, one AniDB-only record folded in", len(al)+len(ad), len(plainAL)+len(plainAD))
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
// the same AniDB id is skipped softly and changes no record; one whose AniDB
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
		plainAL, plainAD := built(t, dir, t.TempDir(), mirror)
		al, ad := built(t, dir, ov, mirror)
		if !reflect.DeepEqual(al, plainAL) || !reflect.DeepEqual(ad, plainAD) {
			t.Errorf("AniList 100 = %+v, want every record as the build without the link made it (%+v)", al[100], plainAL[100])
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
