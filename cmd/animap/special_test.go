package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

func TestRecaptureKeepsTheParentsAniDBSpecials(t *testing.T) {
	sp := []overlay.AniDBEpisode{{Kind: overlay.AniDBSpecial, Number: 1, AirDate: "2005-04-21"}}
	stale := []skyhook.Episode{{Season: 0, Number: 4, AirDate: "2005-04-20"}}
	b := &overlay.Bridge{
		Kind: overlay.KindSpecialOfParent, AniListID: 376, ParentAniDBID: 1983, Specials: []int{1}, ParentAniDBSpecials: slices.Clone(sp),
		Title: "OVA", Justification: "AniDB 1983 S1 2005-04-21.",
		Evidence: map[string]string{"anidb": "https://anidb.net/anime/1983"},
		Captured: overlay.BridgeCaptured{
			At: "2026-01-01", AnimeListsCommit: strings.Repeat("b", 40), NodeSHA256: strings.Repeat("0", 64),
			TVDB: &overlay.CapturedTVDB{Series: 75941, Seasons: []int{0}, Episodes: stale, SHA256: skyhook.Hash(stale)},
		},
	}
	parent := &animelists.Node{
		AniDBID: 1983, Attrs: map[string]string{"tvdbid": "75941", "defaulttvdbseason": "1"},
		Rows: []animelists.Row{{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-4;"}},
	}
	eps := []skyhook.Episode{{Season: 0, Number: 4, AirDate: "2005-04-21"}}
	tv := &overlay.CapturedTVDB{Series: 75941, Seasons: []int{0}, Episodes: eps, SHA256: skyhook.Hash(eps)}
	commit := strings.Repeat("c", 40)

	out, err := recapture(b, parent, commit, tv)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	var got overlay.Bridge
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode the rewritten bridge: %v\n%s", err, out)
	}
	if !slices.Equal(got.ParentAniDBSpecials, sp) {
		t.Errorf("rewritten parent_anidb_specials = %v, want the author's %v", got.ParentAniDBSpecials, sp)
	}
	if got.Captured.AnimeListsCommit != commit || got.Captured.NodeSHA256 != parent.Hash() || got.Captured.TVDB.SHA256 != tv.SHA256 {
		t.Errorf("rewritten captured = %+v, want commit %s, node %s, TVDB %s", got.Captured, commit, parent.Hash(), tv.SHA256)
	}
}

func TestCaptureSpecialRefusesSpecialsUnderCaptured(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "376.json", map[string]any{
		"kind": overlay.KindSpecialOfParent, "anilist_id": 376, "parent_anidb_id": 999, "specials": []int{1},
		"title": "OVA", "justification": "AniDB 999 S1.", "evidence": map[string]string{"anidb": "https://anidb.net/anime/999"},
		"captured": map[string]any{"anidb": map[string]any{"episodes": [][]any{{2, 1, "2005-04-21"}}}},
	})
	args := []string{
		"overlay", "capture-special", "-bridge", path, "-list", listFixture,
		"-commit", strings.Repeat("a", 40), "-overlay", filepath.Join(dir, "none"),
	}
	if err := run(t.Context(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), `unknown field "anidb"`) {
		t.Errorf("capture-special on specials under captured.anidb = %v, want the unknown field refused", err)
	}
}
