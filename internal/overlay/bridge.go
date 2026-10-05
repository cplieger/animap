package overlay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

// KindSpecialOfParent is the one bridge kind.
const KindSpecialOfParent = "special-of-parent"

// BridgeDir is where bridges live, below the overlay directory.
const BridgeDir = KindSpecialOfParent

// Bridge maps an AniList entry that AniDB files as specials of another
// anime onto those specials: AniList episode i is the parent's special
// Specials[i-1]. The offline database has no AniDB id for such an entry,
// so no Anime-Lists node reaches it without a bridge.
type Bridge struct {
	Evidence      map[string]string `json:"evidence"`
	Kind          string            `json:"kind"`
	Title         string            `json:"title"`
	Justification string            `json:"justification"`
	Captured      BridgeCaptured    `json:"captured"`
	Specials      []int             `json:"specials"`
	// ParentAniDBSpecials is the parent's specials as the author read them
	// from AniDB's episode list. Prove re-checks the TVDB layout against
	// their air dates.
	ParentAniDBSpecials []AniDBEpisode `json:"parent_anidb_specials"`
	AniListID           int            `json:"anilist_id"`
	ParentAniDBID       int            `json:"parent_anidb_id"`
}

// BridgeCaptured is the state a bridge was proven against, which
// capture-special writes: the parent's Anime-Lists node and the TVDB
// seasons its specials land on.
type BridgeCaptured struct {
	TVDB             *CapturedTVDB `json:"tvdb"`
	At               string        `json:"at"`
	AnimeListsCommit string        `json:"anime_lists_commit"`
	NodeSHA256       string        `json:"node_sha256"`
}

// AniDBSpecial is AniDB's epno type for a special, the one kind a bridge's
// ParentAniDBSpecials holds.
const AniDBSpecial = 2

// AniDBEpisode is one AniDB episode. Its JSON form is the compact
// [kind, number, air date] array; AirDate is "" when AniDB has none.
type AniDBEpisode struct {
	AirDate string
	Kind    int
	Number  int
}

// MarshalJSON writes the compact array form.
func (e AniDBEpisode) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{e.Kind, e.Number, e.AirDate})
}

// UnmarshalJSON reads the compact array form.
func (e *AniDBEpisode) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw) != 3 {
		return fmt.Errorf("overlay: AniDB episode has %d elements, want 3", len(raw))
	}
	if err := json.Unmarshal(raw[0], &e.Kind); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &e.Number); err != nil {
		return err
	}
	return json.Unmarshal(raw[2], &e.AirDate)
}

// Path is the bridge's file, relative to the repository root.
func (b *Bridge) Path() string {
	return "overlay/" + BridgeDir + "/" + strconv.Itoa(b.AniListID) + ".json"
}

// LoadBridges reads and validates every <dir>/special-of-parent/*.json; a
// missing directory is no bridges.
func LoadBridges(dir string) ([]Bridge, error) {
	paths, err := filepath.Glob(filepath.Join(dir, BridgeDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) > MaxEntries {
		return nil, fmt.Errorf("%w: %d bridges, at most %d", ErrInvalid, len(paths), MaxEntries)
	}
	slices.Sort(paths)
	out := make([]Bridge, 0, len(paths))
	for _, p := range paths {
		b, err := loadBridge(p)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func loadBridge(path string) (Bridge, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Bridge{}, err
	}
	if st.Size() > MaxFileBytes {
		return Bridge{}, fmt.Errorf("%w: %s is %d bytes", ErrInvalid, path, st.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Bridge{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var b Bridge
	if err := dec.Decode(&b); err != nil {
		return Bridge{}, fmt.Errorf("%w: %s: %w", ErrInvalid, path, err)
	}
	if dec.More() {
		return Bridge{}, fmt.Errorf("%w: %s: trailing data", ErrInvalid, path)
	}
	if want := strconv.Itoa(b.AniListID) + ".json"; filepath.Base(path) != want {
		return Bridge{}, fmt.Errorf("%w: %s must be named %s", ErrInvalid, path, want)
	}
	if err := b.Validate(); err != nil {
		return Bridge{}, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// Validate checks the bridge's own rules: consecutive specials that
// ParentAniDBSpecials carries, and a TVDB fingerprint that matches its list.
func (b *Bridge) Validate() error {
	if err := b.validate(); err != nil {
		return fmt.Errorf("%w: AniList %d: %w", ErrInvalid, b.AniListID, err)
	}
	return nil
}

func (b *Bridge) validate() error {
	switch {
	case b.Kind != KindSpecialOfParent:
		return fmt.Errorf("kind %q, want %q", b.Kind, KindSpecialOfParent)
	case b.AniListID <= 0 || b.ParentAniDBID <= 0:
		return errors.New("anilist_id and parent_anidb_id must be positive")
	case len(b.Specials) == 0:
		return errors.New("specials is empty")
	}
	for i, k := range b.Specials {
		if k <= 0 || (i > 0 && k != b.Specials[i-1]+1) {
			return errors.New("specials must be positive and consecutive")
		}
	}
	if strings.TrimSpace(b.Title) == "" || strings.TrimSpace(b.Justification) == "" {
		return errors.New("title and justification are required")
	}
	if err := validateEvidence(b.Evidence); err != nil {
		return err
	}
	if err := b.validateParentSpecials(); err != nil {
		return err
	}
	return b.validateCaptured()
}

func (b *Bridge) validateParentSpecials() error {
	for _, e := range b.ParentAniDBSpecials {
		if e.Kind != AniDBSpecial {
			return fmt.Errorf("parent_anidb_specials holds a kind %d episode, want only specials", e.Kind)
		}
	}
	for _, k := range b.Specials {
		if !slices.ContainsFunc(b.ParentAniDBSpecials, func(e AniDBEpisode) bool { return e.Number == k }) {
			return fmt.Errorf("parent_anidb_specials has no special S%d", k)
		}
	}
	return nil
}

func (b *Bridge) validateCaptured() error {
	c := b.Captured
	switch {
	case !commitRE.MatchString(c.AnimeListsCommit):
		return errors.New("captured.anime_lists_commit is not a 40-hex commit")
	case !hexRE.MatchString(c.NodeSHA256):
		return errors.New("captured.node_sha256 is not 64 hex")
	case c.TVDB == nil || c.TVDB.Series <= 0 || skyhook.Hash(c.TVDB.Episodes) != c.TVDB.SHA256:
		return errors.New("captured.tvdb needs a series id and a sha256 that matches its episodes")
	}
	return nil
}

// Prove re-checks the dated half of the accuracy bar against the patched
// parent node: each special lands on a TVDB episode the captured layout
// holds, on the parent's series, aired within a day of AniDB's date.
func (b *Bridge) Prove(parent *animelists.Node) error {
	if parent == nil {
		return fmt.Errorf("AniList %d: AniDB %d has no Anime-Lists node", b.AniListID, b.ParentAniDBID)
	}
	if s, err := strconv.Atoi(parent.Attr(attrTVDBID)); err != nil || s != b.Captured.TVDB.Series {
		return fmt.Errorf("AniList %d: parent is on TVDB %q, the capture on %d", b.AniListID, parent.Attr(attrTVDBID), b.Captured.TVDB.Series)
	}
	for _, k := range b.Specials {
		season, ep, ok := parent.SpecialTVDB(k)
		if !ok {
			return fmt.Errorf("AniList %d: special S%d maps to no single TVDB episode", b.AniListID, k)
		}
		i := slices.IndexFunc(b.Captured.TVDB.Episodes, func(e skyhook.Episode) bool { return e.Season == season && e.Number == ep })
		j := slices.IndexFunc(b.ParentAniDBSpecials, func(e AniDBEpisode) bool { return e.Number == k })
		if i < 0 || j < 0 {
			return fmt.Errorf("AniList %d: S%d lands on TVDB %dx%d, which the capture does not hold", b.AniListID, k, season, ep)
		}
		if !withinADay(b.Captured.TVDB.Episodes[i].AirDate, b.ParentAniDBSpecials[j].AirDate) {
			return fmt.Errorf("AniList %d: S%d aired %q on AniDB, TVDB %dx%d %q", b.AniListID, k,
				b.ParentAniDBSpecials[j].AirDate, season, ep, b.Captured.TVDB.Episodes[i].AirDate)
		}
	}
	return nil
}

func withinADay(a, b string) bool {
	ta, errA := time.Parse(time.DateOnly, a)
	tb, errB := time.Parse(time.DateOnly, b)
	if errA != nil || errB != nil {
		return false
	}
	d := ta.Sub(tb)
	return d <= 24*time.Hour && d >= -24*time.Hour
}
