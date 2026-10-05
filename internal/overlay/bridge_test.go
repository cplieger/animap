package overlay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

func validBridge() Bridge {
	tv := []skyhook.Episode{{Season: 0, Number: 4, AirDate: "2005-04-21"}, {Season: 0, Number: 5, AirDate: "2005-06-01"}, {Season: 0, Number: 6, AirDate: "2005-04-23"}}
	sp := []AniDBEpisode{{Kind: AniDBSpecial, Number: 1, AirDate: "2005-04-21"}, {Kind: AniDBSpecial, Number: 2, AirDate: "2005-06-02"}}
	return Bridge{
		Kind: KindSpecialOfParent, AniListID: 376, ParentAniDBID: 1983, Specials: []int{1, 2}, ParentAniDBSpecials: sp,
		Title: "Elfen Lied OVA", Justification: "AniDB 1983 S1 2005-04-21, same title.",
		Evidence: map[string]string{"anilist": "https://anilist.co/anime/376", "anidb": "https://anidb.net/anime/1983"},
		Captured: BridgeCaptured{
			At: "2026-10-05", AnimeListsCommit: strings.Repeat("a", 40), NodeSHA256: strings.Repeat("b", 64),
			TVDB: &CapturedTVDB{Series: 75941, Seasons: []int{0}, Episodes: tv, SHA256: skyhook.Hash(tv)},
		},
	}
}

// captureS3 adds S3 to the parent's specials, so only the specials rule can
// refuse a bridge naming it.
func captureS3(b *Bridge) {
	b.ParentAniDBSpecials = append(b.ParentAniDBSpecials, AniDBEpisode{Kind: AniDBSpecial, Number: 3, AirDate: "2005-07-01"})
}

func TestBridgeValidate(t *testing.T) {
	if b := validBridge(); b.Validate() != nil {
		t.Fatalf("valid bridge: %v", b.Validate())
	}
	ok := validBridge()
	ok.Specials = []int{2, 3}
	captureS3(&ok)
	if err := ok.Validate(); err != nil {
		t.Errorf("consecutive specials S2-S3, both captured: Validate = %v, want nil", err)
	}
	for _, tc := range []struct {
		name string
		mut  func(*Bridge)
	}{
		{"wrong kind", func(b *Bridge) { b.Kind = "other" }},
		{"no parent", func(b *Bridge) { b.ParentAniDBID = 0 }},
		{"no specials", func(b *Bridge) { b.Specials = nil }},
		{"specials not consecutive", func(b *Bridge) { b.Specials = []int{1, 3}; captureS3(b) }},
		{"specials out of order", func(b *Bridge) { b.Specials = []int{2, 1} }},
		{"a special the parent specials lack", func(b *Bridge) { b.Specials = []int{2, 3} }},
		{"no justification", func(b *Bridge) { b.Justification = "" }},
		{"evidence on another host", func(b *Bridge) { b.Evidence["x"] = "https://example.com" }},
		{"no parent specials", func(b *Bridge) { b.ParentAniDBSpecials = nil }},
		{"a regular episode among the parent specials", func(b *Bridge) {
			b.ParentAniDBSpecials = append(b.ParentAniDBSpecials, AniDBEpisode{Kind: 1, Number: 1, AirDate: "2005-01-01"})
		}},
		{"no TVDB capture", func(b *Bridge) { b.Captured.TVDB = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := validBridge()
			tc.mut(&b)
			if err := b.Validate(); !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate(%s) = %v, want ErrInvalid", tc.name, err)
			}
		})
	}
}

func TestLoadBridges(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadBridges(dir); err != nil || len(got) != 0 {
		t.Fatalf("LoadBridges(no directory) = %v, %v; want none", got, err)
	}
	if err := os.Mkdir(filepath.Join(dir, BridgeDir), 0o750); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(validBridge())
	if err := os.WriteFile(filepath.Join(dir, BridgeDir, "376.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadBridges(dir); err != nil || len(got) != 1 || got[0].Path() != "overlay/special-of-parent/376.json" {
		t.Errorf("LoadBridges = %+v, %v; want the one bridge", got, err)
	}
	if err := os.Rename(filepath.Join(dir, BridgeDir, "376.json"), filepath.Join(dir, BridgeDir, "377.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBridges(dir); !errors.Is(err, ErrInvalid) {
		t.Errorf("a bridge named for another AniList id = %v, want ErrInvalid", err)
	}
}

func TestLoadBridgesRefusesSpecialsUnderCaptured(t *testing.T) {
	valid, err := json.Marshal(validBridge())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		topLevel bool
	}{
		{"captured only", false},
		{"captured and top level", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(valid, &m); err != nil {
				t.Fatal(err)
			}
			captured, ok := m["captured"].(map[string]any)
			if !ok {
				t.Fatalf("captured is %T", m["captured"])
			}
			captured["anidb"] = map[string]any{"episodes": m["parent_anidb_specials"]}
			if !tc.topLevel {
				delete(m, "parent_anidb_specials")
			}
			body, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, BridgeDir), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, BridgeDir, "376.json"), body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadBridges(dir); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `unknown field "anidb"`) {
				t.Errorf("LoadBridges(specials under captured.anidb, %s) = %v, want ErrInvalid naming the unknown field", tc.name, err)
			}
		})
	}
}

func TestBridgeProve(t *testing.T) {
	parent := func(tvdb, text string) *animelists.Node {
		return &animelists.Node{
			AniDBID: 1983, Attrs: map[string]string{"tvdbid": tvdb, "defaulttvdbseason": "1"},
			Rows: []animelists.Row{{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: text}},
		}
	}
	b := validBridge()
	if err := b.Prove(parent("75941", ";1-4;2-5;")); err != nil {
		t.Errorf("Prove(matching dates, one a day apart) = %v", err)
	}
	for name, n := range map[string]*animelists.Node{
		"no parent node":              nil,
		"another series":              parent("1", ";1-4;2-5;"),
		"a target the layout lacks":   parent("75941", ";1-4;2-6;"),
		"a date two days apart":       parent("75941", ";1-6;2-5;"),
		"a special mapped to nothing": parent("75941", ";1-4;2-0;"),
	} {
		if err := b.Prove(n); err == nil {
			t.Errorf("Prove(%s) = nil, want a refusal", name)
		}
	}
	late := validBridge()
	late.ParentAniDBSpecials[0].AirDate = "2005-04-25"
	if err := late.Prove(parent("75941", ";1-4;2-5;")); err == nil {
		t.Errorf("Prove(parent_anidb_specials S1 aired 2005-04-25, TVDB 0x4 2005-04-21) = nil, want a refusal")
	}
}
