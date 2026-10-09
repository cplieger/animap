package overlay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

func validBridge() Bridge {
	tv := []skyhook.Episode{{Season: 0, Number: 4, AirDate: "2005-04-21"}, {Season: 0, Number: 5, AirDate: "2005-06-01"}, {Season: 0, Number: 6, AirDate: "2005-04-23"}}
	return Bridge{
		Kind: KindSpecialOfParent, AniListID: 376, ParentAniDBID: 1983, Specials: []int{1, 2},
		Title: "Elfen Lied OVA", Justification: "AniDB 1983 S1 2005-04-21, same title.",
		Evidence: map[string]string{"anilist": "https://anilist.co/anime/376", "anidb": "https://anidb.net/anime/1983"},
		Captured: BridgeCaptured{
			At: "2026-10-05", AnimeListsCommit: strings.Repeat("a", 40), NodeSHA256: strings.Repeat("b", 64),
			TVDB: &CapturedTVDB{Series: 75941, Seasons: []int{0}, Episodes: tv, SHA256: skyhook.Hash(tv)},
		},
	}
}

// parentSpecials is what the AniDB mirror lists for AniDB 1983.
func parentSpecials() []anidb.Episode {
	return []anidb.Episode{{Number: 1, AirDate: "2005-04-21"}, {Number: 2, AirDate: "2005-06-02"}, {Number: 3, AirDate: "2005-07-01"}}
}

func TestBridgeValidate(t *testing.T) {
	if b := validBridge(); b.Validate() != nil {
		t.Fatalf("valid bridge: %v", b.Validate())
	}
	ok := validBridge()
	ok.Specials = []int{2, 3}
	if err := ok.Validate(); err != nil {
		t.Errorf("consecutive specials S2-S3: Validate = %v, want nil", err)
	}
	for _, tc := range []struct {
		name string
		mut  func(*Bridge)
	}{
		{"wrong kind", func(b *Bridge) { b.Kind = "other" }},
		{"no parent", func(b *Bridge) { b.ParentAniDBID = 0 }},
		{"no specials", func(b *Bridge) { b.Specials = nil }},
		{"specials not consecutive", func(b *Bridge) { b.Specials = []int{1, 3} }},
		{"specials out of order", func(b *Bridge) { b.Specials = []int{2, 1} }},
		{"no justification", func(b *Bridge) { b.Justification = "" }},
		{"evidence on another host", func(b *Bridge) { b.Evidence["x"] = "https://example.com" }},
		{"no TVDB capture", func(b *Bridge) { b.Captured.TVDB = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := validBridge()
			tc.mut(&b)
			if err := b.Validate(); !errors.Is(err, errInvalid) {
				t.Errorf("Validate(%s) = %v, want errInvalid", tc.name, err)
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
	untitled := validBridge()
	untitled.Title = ""
	ub, _ := json.Marshal(untitled)
	if err := os.WriteFile(filepath.Join(dir, BridgeDir, "376.json"), ub, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBridges(dir); !errors.Is(err, errInvalid) || !strings.Contains(err.Error(), "title") {
		t.Errorf("a bridge with no title = %v, want errInvalid naming it", err)
	}
	if err := os.WriteFile(filepath.Join(dir, BridgeDir, "376.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, BridgeDir, "376.json"), filepath.Join(dir, BridgeDir, "377.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBridges(dir); !errors.Is(err, errInvalid) {
		t.Errorf("a bridge named for another AniList id = %v, want errInvalid", err)
	}
}

func TestLoadBridgesRefusesTrailingData(t *testing.T) {
	good, err := json.Marshal(validBridge())
	if err != nil {
		t.Fatal(err)
	}
	for name, trailer := range map[string]string{"a stray ]": "]", "a stray }": "}", "a second document": string(good)} {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, BridgeDir), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, BridgeDir, "376.json"), append(append(good, '\n'), trailer...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBridges(dir); !errors.Is(err, errInvalid) {
			t.Errorf("LoadBridges(a bridge followed by %s) = %v, want errInvalid", name, err)
		}
	}
}

// AniDB's episode lists come from the mirror at build time, so a bridge
// that still carries a hand copy of them is refused, never read.
func TestLoadBridgesRefusesHandCopiedAniDBFacts(t *testing.T) {
	valid, err := json.Marshal(validBridge())
	if err != nil {
		t.Fatal(err)
	}
	for field, value := range map[string]any{
		"parent_anidb_specials": [][]any{{2, 1, "2005-04-21"}},
		"siblings":              map[string]any{"1984": map[string]any{"regular": 13, "specials": []int{}}},
	} {
		t.Run(field, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(valid, &m); err != nil {
				t.Fatal(err)
			}
			m[field] = value
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
			if _, err := LoadBridges(dir); !errors.Is(err, errInvalid) || !strings.Contains(err.Error(), `unknown field "`+field+`"`) {
				t.Errorf("LoadBridges(a bridge with %s) = %v, want errInvalid naming the unknown field", field, err)
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
	if err := b.Prove(parent("75941", ";1-4;2-5;"), parentSpecials()); err != nil {
		t.Errorf("Prove(matching dates, one a day apart) = %v", err)
	}
	for name, n := range map[string]*animelists.Node{
		"no parent node":              nil,
		"another series":              parent("1", ";1-4;2-5;"),
		"a target the layout lacks":   parent("75941", ";1-4;2-6;"),
		"a date two days apart":       parent("75941", ";1-6;2-5;"),
		"a special mapped to nothing": parent("75941", ";1-4;2-0;"),
	} {
		if err := b.Prove(n, parentSpecials()); err == nil {
			t.Errorf("Prove(%s) = nil, want a refusal", name)
		}
	}
	late := parentSpecials()
	late[0].AirDate = "2005-04-25"
	if err := b.Prove(parent("75941", ";1-4;2-5;"), late); err == nil {
		t.Error("Prove(AniDB S1 aired 2005-04-25, TVDB 0x4 2005-04-21) = nil, want a refusal")
	}
	if err := b.Prove(parent("75941", ";1-4;2-5;"), parentSpecials()[:1]); err == nil || !strings.Contains(err.Error(), "no special S2") {
		t.Errorf("Prove(the mirror lists no S2) = %v, want a refusal naming S2", err)
	}
	if err := b.Prove(parent("75941", ";1-4;2-5;"), nil); err == nil {
		t.Error("Prove(the mirror lists no specials for the parent) = nil, want a refusal")
	}
}
