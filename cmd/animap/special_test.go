package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

func TestRecaptureProvesAgainstTheMirrorsSpecials(t *testing.T) {
	stale := []skyhook.Episode{{Season: 0, Number: 4, AirDate: "2005-04-20"}}
	bridge := func() *overlay.Bridge {
		return &overlay.Bridge{
			Kind: overlay.KindSpecialOfParent, AniListID: 376, ParentAniDBID: 1983, Specials: []int{1},
			Title: "OVA", Justification: "AniDB 1983 S1 2005-04-21.",
			Evidence: map[string]string{"anidb": "https://anidb.net/anime/1983"},
			Captured: overlay.BridgeCaptured{
				At: "2026-01-01", AnimeListsCommit: strings.Repeat("b", 40), NodeSHA256: strings.Repeat("0", 64),
				TVDB: &overlay.CapturedTVDB{Series: 75941, Seasons: []int{0}, Episodes: stale, SHA256: skyhook.Hash(stale)},
			},
		}
	}
	parent := &animelists.Node{
		AniDBID: 1983, Attrs: map[string]string{"tvdbid": "75941", "defaulttvdbseason": "1"},
		Rows: []animelists.Row{{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-4;"}},
	}
	eps := []skyhook.Episode{{Season: 0, Number: 4, AirDate: "2005-04-21"}}
	tv := &overlay.CapturedTVDB{Series: 75941, Seasons: []int{0}, Episodes: eps, SHA256: skyhook.Hash(eps)}
	commit := strings.Repeat("c", 40)

	out, err := recapture(bridge(), parent, []anidb.Episode{{Number: 1, AirDate: "2005-04-21"}}, commit, tv)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	var got overlay.Bridge
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode the rewritten bridge: %v\n%s", err, out)
	}
	if got.Captured.AnimeListsCommit != commit || got.Captured.NodeSHA256 != parent.Hash() || got.Captured.TVDB.SHA256 != tv.SHA256 {
		t.Errorf("rewritten captured = %+v, want commit %s, node %s, TVDB %s", got.Captured, commit, parent.Hash(), tv.SHA256)
	}
	if _, err := recapture(bridge(), parent, []anidb.Episode{{Number: 1, AirDate: "2005-06-01"}}, commit, tv); err == nil {
		t.Error("recapture accepted a layout the mirror's S1 date does not match")
	}
}

func TestCaptureSpecialRefusesHandCopiedAniDBFacts(t *testing.T) {
	dir := t.TempDir()
	base := map[string]any{
		"kind": overlay.KindSpecialOfParent, "anilist_id": 376, "parent_anidb_id": 999, "specials": []int{1},
		"title": "OVA", "justification": "AniDB 999 S1.", "evidence": map[string]string{"anidb": "https://anidb.net/anime/999"},
	}
	for field, value := range map[string]any{
		"parent_anidb_specials": [][]any{{2, 1, "2005-04-21"}},
		"captured":              map[string]any{"anidb": map[string]any{"episodes": [][]any{{2, 1, "2005-04-21"}}}},
	} {
		b := map[string]any{field: value}
		maps.Copy(b, base)
		path := writeFixture(t, dir, "376.json", b)
		args := []string{
			"overlay", "capture-special", "-bridge", path, "-list", listFixture,
			"-commit", strings.Repeat("a", 40), "-overlay", filepath.Join(dir, "none"), "-mirror", mirrorFixture,
		}
		want := `unknown field "` + field + `"`
		if field == "captured" {
			want = `unknown field "anidb"`
		}
		if err := run(t.Context(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("capture-special on a bridge with %s = %v, want %s refused", field, err, want)
		}
	}
}

// bridgeOnTVDB0x2 writes a bridge of AniList 101 to parent AniDB 10's S1,
// captured on TVDB 0x2 dated 2020-06-01.
func bridgeOnTVDB0x2(t *testing.T, ov string) {
	t.Helper()
	eps := []skyhook.Episode{{Season: 0, Number: 2, AirDate: "2020-06-01"}}
	bridges := filepath.Join(ov, overlay.BridgeDir)
	if err := os.Mkdir(bridges, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, bridges, "101.json", map[string]any{
		"kind": overlay.KindSpecialOfParent, "anilist_id": 101, "parent_anidb_id": 10, "specials": []int{1},
		"title": "Special", "justification": "test", "evidence": map[string]string{"anidb": "https://anidb.net/anime/10"},
		"captured": map[string]any{
			"at": "2026-10-05", "anime_lists_commit": strings.Repeat("a", 40), "node_sha256": strings.Repeat("b", 64),
			"tvdb": map[string]any{"series": 5000, "seasons": []int{0}, "episodes": eps, "sha256": skyhook.Hash(eps)},
		},
	})
}

// snapshotWithParentS1 writes a snapshot listing AniDB 10 with S1 on airDate.
func snapshotWithParentS1(t *testing.T, dir, airDate string) string {
	t.Helper()
	a := fin(12)
	a.Specials = []anidb.Episode{{Number: 1, AirDate: airDate}}
	return snapshotWith(t, dir, map[int]anidb.Anime{10: a})
}

// overlay check proves each bridge against the specials the AniDB mirror
// lists for its parent.
func TestOverlayCheckProvesBridgesAgainstTheMirror(t *testing.T) {
	dir, ov := t.TempDir(), t.TempDir()
	bridgeOnTVDB0x2(t, ov)
	check := func(airDate string) error {
		snap := snapshotWithParentS1(t, dir, airDate)
		return run(t.Context(), []string{"overlay", "check", "-overlay", ov, "-list", listFixture, "-aod", aodFixture, "-mirror", snap}, &bytes.Buffer{})
	}
	if err := check("2020-06-01"); err != nil {
		t.Errorf("overlay check with the mirror's S1 on TVDB 0x2's date = %v, want accepted", err)
	}
	if err := check("2020-06-05"); err == nil || !strings.Contains(err.Error(), "S1 aired") {
		t.Errorf("overlay check with the mirror's S1 four days from TVDB 0x2 = %v, want the bridge refused", err)
	}
}

// The build proves every bridge too, so a mirror bump that moves a parent's
// special stops the release.
func TestBuildProvesBridgesAgainstTheMirror(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	bridgeOnTVDB0x2(t, ov)
	if _, err := build(t, dir, ov, "", "-mirror", snapshotWithParentS1(t, dir, "2020-06-01")); err != nil {
		t.Errorf("build with the mirror's S1 on TVDB 0x2's date = %v, want accepted", err)
	}
	_, err := build(t, dir, ov, "", "-mirror", snapshotWithParentS1(t, dir, "2020-06-05"))
	if err == nil || !strings.Contains(err.Error(), "S1 aired") {
		t.Errorf("build with the mirror's S1 four days from TVDB 0x2 = %v, want the bridge refused", err)
	}
}
