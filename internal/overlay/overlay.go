// Package overlay holds animap's own corrections to Anime-Lists: one JSON
// file per patched node, in the list's own attribute vocabulary, with the
// fingerprints the drift check compares.
package overlay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/strictjson"
)

// Bounds on the overlay directory.
const (
	MaxEntries   = 1000
	MaxFileBytes = 64 << 10
)

const (
	attrTVDBID        = "tvdbid"
	attrDefaultSeason = "defaulttvdbseason"
)

// UpstreamPending is the upstream value of an entry not yet filed.
const UpstreamPending = "TODO-PR"

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("overlay: invalid entry")

// Entry is one overlay file.
type Entry struct {
	Evidence      map[string]string `json:"evidence"`
	Set           Set               `json:"set"`
	Title         string            `json:"title"`
	Name          string            `json:"name,omitempty"`
	Justification string            `json:"justification"`
	Upstream      string            `json:"upstream"`
	Captured      Captured          `json:"captured"`
	AniListIDs    []int             `json:"anilist_ids,omitempty"`
	AniDBID       int               `json:"anidb_id"`
	Create        bool              `json:"create,omitempty"`
}

// Set is the patch. A nil field leaves upstream as it is; a MappingList of
// length zero deletes the node's mapping-list.
type Set struct {
	TVDBID            *string `json:"tvdbid,omitempty"`
	DefaultTVDBSeason *string `json:"defaulttvdbseason,omitempty"`
	EpisodeOffset     *string `json:"episodeoffset,omitempty"`
	TMDBTV            *string `json:"tmdbtv,omitempty"`
	TMDBSeason        *string `json:"tmdbseason,omitempty"`
	TMDBOffset        *string `json:"tmdboffset,omitempty"`
	TMDBID            *string `json:"tmdbid,omitempty"`
	IMDbID            *string `json:"imdbid,omitempty"`
	MappingList       *[]Row  `json:"mapping_list,omitempty"`
}

// Row is a mapping_list row in the published shape, except that a present
// Offset, 0 included, is written to the patched node, so Landed can match a
// row written with offset="0".
//
//nolint:govet // fieldalignment: member order is the file's JSON order
type Row struct {
	AniDBSeason int     `json:"anidb_season"`
	TVDBSeason  *int    `json:"tvdb_season,omitempty"`
	TMDBSeason  *int    `json:"tmdb_season,omitempty"`
	Start       int     `json:"start,omitempty"`
	End         int     `json:"end,omitempty"`
	Offset      *int    `json:"offset,omitempty"`
	Episodes    [][]int `json:"episodes,omitempty"`
}

func (r *Row) published() schema.Row {
	s := schema.Row{AniDBSeason: r.AniDBSeason, TVDBSeason: r.TVDBSeason, TMDBSeason: r.TMDBSeason, Start: r.Start, End: r.End, Episodes: r.Episodes}
	if r.Offset != nil {
		s.Offset = *r.Offset
	}
	return s
}

func (r *Row) listRow() animelists.Row {
	lr := animelists.RowFromSchema(r.published())
	if r.Offset != nil {
		lr.Attrs["offset"] = strconv.Itoa(*r.Offset)
	}
	return lr
}

// Attrs returns the set attributes by their list names.
func (s *Set) Attrs() map[string]string {
	out := map[string]string{}
	for name, p := range map[string]*string{
		attrTVDBID: s.TVDBID, attrDefaultSeason: s.DefaultTVDBSeason, "episodeoffset": s.EpisodeOffset,
		"tmdbtv": s.TMDBTV, "tmdbseason": s.TMDBSeason, "tmdboffset": s.TMDBOffset,
		"tmdbid": s.TMDBID, "imdbid": s.IMDbID,
	} {
		if p != nil {
			out[name] = *p
		}
	}
	return out
}

// Captured is the state the entry was written against. TVDB is nil when
// the patched node has no TVDB series (a film routed by TMDB alone).
type Captured struct {
	TVDB             *CapturedTVDB `json:"tvdb,omitempty"`
	At               string        `json:"at"`
	AnimeListsCommit string        `json:"anime_lists_commit"`
	NodeSHA256       string        `json:"node_sha256"`
}

// CapturedTVDB is the official-order layout of the seasons the entry
// touches. Seasons nil means every season from 1 up.
type CapturedTVDB struct {
	SHA256   string            `json:"sha256"`
	Seasons  []int             `json:"seasons"`
	Episodes []skyhook.Episode `json:"episodes"`
	Series   int               `json:"series"`
}

var (
	hexRE      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitRE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	upstreamRE = regexp.MustCompile(`^https://github\.com/Anime-Lists/anime-lists/(pull|issues)/[0-9]+$`)
	imdbRE     = regexp.MustCompile(`^tt\d{7,10}$`)
	tvdbMarker = []string{"movie", "OVA", "hentai", "tv special", "web", "other", "music video", "unknown"}
	evidenceOK = []string{"anidb.net", "thetvdb.com", "www.themoviedb.org", "anilist.co", "releases.moe"}
)

// Path is the entry's file, relative to the repository root.
func (e *Entry) Path() string { return "overlay/" + strconv.Itoa(e.AniDBID) + ".json" }

// LoadDir reads and validates every overlay/*.json file.
func LoadDir(dir string) ([]Entry, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) > MaxEntries {
		return nil, fmt.Errorf("%w: %d files, at most %d", ErrInvalid, len(paths), MaxEntries)
	}
	slices.Sort(paths)
	var out []Entry
	seen := map[int]bool{}
	for _, p := range paths {
		e, err := LoadFile(p)
		if err != nil {
			return nil, err
		}
		if seen[e.AniDBID] {
			return nil, fmt.Errorf("%w: %s: AniDB %d appears twice", ErrInvalid, p, e.AniDBID)
		}
		seen[e.AniDBID] = true
		out = append(out, e)
	}
	return out, nil
}

// LoadFile reads one entry with ReadEntry and validates it.
func LoadFile(path string) (Entry, error) {
	e, err := ReadEntry(path)
	if err != nil {
		return Entry{}, err
	}
	if err := e.Validate(); err != nil {
		return Entry{}, fmt.Errorf("%s: %w", path, err)
	}
	return e, nil
}

// ReadEntry reads one entry strictly and checks that the file is named for
// its AniDB id. It does not validate, so capture can read an entry whose
// captured fingerprints it is about to write.
func ReadEntry(path string) (Entry, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Entry{}, err
	}
	if st.Size() > MaxFileBytes {
		return Entry{}, fmt.Errorf("%w: %s is %d bytes", ErrInvalid, path, st.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	e, err := decode(body)
	if err != nil {
		return Entry{}, fmt.Errorf("%s: %w", path, err)
	}
	if want := strconv.Itoa(e.AniDBID) + ".json"; filepath.Base(path) != want {
		return Entry{}, fmt.Errorf("%w: %s must be named %s", ErrInvalid, path, want)
	}
	return e, nil
}

func decode(body []byte) (Entry, error) {
	var e Entry
	if err := strictjson.Decode(body, &e); err != nil {
		return Entry{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return e, nil
}

// Validate checks every field against the list's syntax and the overlay's
// own rules.
func (e *Entry) Validate() error {
	if e.AniDBID <= 0 {
		return fmt.Errorf("%w: anidb_id must be positive", ErrInvalid)
	}
	for _, check := range []func() error{e.validateText, e.validateSet, e.validateCaptured} {
		if err := check(); err != nil {
			return fmt.Errorf("%w: AniDB %d: %w", ErrInvalid, e.AniDBID, err)
		}
	}
	return nil
}

func (e *Entry) validateText() error {
	if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Justification) == "" {
		return errors.New("title and justification are required")
	}
	if e.Create && strings.TrimSpace(e.Name) == "" {
		return errors.New("create needs a name")
	}
	if e.Upstream != UpstreamPending && !upstreamRE.MatchString(e.Upstream) {
		return fmt.Errorf("upstream %q is neither %s nor an Anime-Lists PR or issue URL", e.Upstream, UpstreamPending)
	}
	return validateEvidence(e.Evidence)
}

func validateEvidence(ev map[string]string) error {
	if len(ev) == 0 {
		return errors.New("evidence is required")
	}
	for k, v := range ev {
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "https" || !slices.Contains(evidenceOK, u.Host) {
			return fmt.Errorf("evidence %s %q is not an https link to an allowed host", k, v)
		}
	}
	return nil
}

func (e *Entry) validateSet() error {
	attrs := e.Set.Attrs()
	if len(attrs) == 0 && e.Set.MappingList == nil {
		return errors.New("set is empty")
	}
	for name, v := range attrs {
		if !validAttr(name, v) && !filmSeason(attrs, name, v) {
			return fmt.Errorf("set.%s %q does not parse", name, v)
		}
	}
	if e.Set.MappingList == nil {
		return nil
	}
	for i := range *e.Set.MappingList {
		r := &(*e.Set.MappingList)[i]
		if r.TVDBSeason == nil && r.TMDBSeason == nil {
			return fmt.Errorf("mapping_list[%d] names neither a TVDB nor a TMDB season", i)
		}
		back, err := r.listRow().Schema()
		if err != nil {
			return fmt.Errorf("mapping_list[%d]: %w", i, err)
		}
		if !reflect.DeepEqual(back, r.published()) {
			return fmt.Errorf("mapping_list[%d] does not survive the list syntax (a 0 target, an empty pair or a negative bound)", i)
		}
	}
	return nil
}

func (e *Entry) validateCaptured() error {
	c := e.Captured
	switch {
	case !commitRE.MatchString(c.AnimeListsCommit):
		return errors.New("captured.anime_lists_commit is not a 40-hex commit")
	case !hexRE.MatchString(c.NodeSHA256) && c.NodeSHA256 != animelists.HashAbsent:
		return fmt.Errorf("captured.node_sha256 is neither 64 hex nor %q", animelists.HashAbsent)
	case e.Create != (c.NodeSHA256 == animelists.HashAbsent):
		return fmt.Errorf("create and an %q node hash go together", animelists.HashAbsent)
	case c.TVDB != nil && (!hexRE.MatchString(c.TVDB.SHA256) || c.TVDB.Series <= 0):
		return errors.New("captured.tvdb needs a series id and a 64-hex sha256")
	case c.TVDB != nil && skyhook.Hash(c.TVDB.Episodes) != c.TVDB.SHA256:
		return errors.New("captured.tvdb.sha256 does not match its episodes")
	}
	return nil
}

func filmSeason(attrs map[string]string, name, v string) bool {
	return name == attrDefaultSeason && v == "" && attrs[attrTVDBID] == "movie"
}

func validAttr(name, v string) bool {
	if v == "" {
		return name != attrTVDBID && name != attrDefaultSeason
	}
	switch name {
	case attrTVDBID:
		return isInt(v, 1) || slices.Contains(tvdbMarker, v)
	case attrDefaultSeason:
		return v == "a" || isInt(v, 0)
	case "tmdbtv":
		return isInt(v, 1)
	case "tmdbseason":
		return isInt(v, 0)
	case "episodeoffset", "tmdboffset":
		_, err := strconv.Atoi(v)
		return err == nil
	case "tmdbid":
		return allParts(v, func(p string) bool { return isInt(p, 1) })
	case "imdbid":
		return allParts(v, imdbRE.MatchString)
	}
	return false
}

func isInt(s string, minimum int) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= minimum
}

func allParts(s string, ok func(string) bool) bool {
	for p := range strings.SplitSeq(s, ",") {
		if !ok(p) {
			return false
		}
	}
	return true
}

// Apply returns nodes with every entry patched in. The input map is not
// modified; patched nodes are copies.
func Apply(nodes map[int]*animelists.Node, entries []Entry) map[int]*animelists.Node {
	out := make(map[int]*animelists.Node, len(nodes)+len(entries))
	maps.Copy(out, nodes)
	for i := range entries {
		out[entries[i].AniDBID] = Patch(nodes[entries[i].AniDBID], &entries[i])
	}
	return out
}

// Patch returns a copy of n with e applied; a nil n is created.
func Patch(n *animelists.Node, e *Entry) *animelists.Node {
	var c *animelists.Node
	if n == nil {
		c = &animelists.Node{AniDBID: e.AniDBID, Name: e.Name, Attrs: map[string]string{}}
	} else {
		c = n.Clone()
	}
	maps.Copy(c.Attrs, e.Set.Attrs())
	if e.Set.MappingList != nil {
		c.Rows = nil
		for i := range *e.Set.MappingList {
			c.Rows = append(c.Rows, (*e.Set.MappingList)[i].listRow())
		}
	}
	return c
}

// Landed reports whether upstream node u already carries every value the
// entry sets, so the entry has nothing left to patch.
func Landed(u *animelists.Node, e *Entry) bool {
	if u == nil {
		return false
	}
	for k, v := range e.Set.Attrs() {
		if u.Attr(k) != strings.TrimSpace(v) {
			return false
		}
	}
	if e.Set.MappingList != nil {
		want := Patch(&animelists.Node{AniDBID: u.AniDBID, Attrs: map[string]string{}}, &Entry{Set: Set{MappingList: e.Set.MappingList}})
		got := &animelists.Node{AniDBID: u.AniDBID, Attrs: map[string]string{}, Rows: u.Rows}
		if want.Canonical() != got.Canonical() {
			return false
		}
	}
	return true
}

// TouchedSeasons are the TVDB seasons a patched node maps onto, sorted,
// with season 0 when AniDB lists specials for it; nil for an absolute node,
// which touches every season from 1 up.
func TouchedSeasons(patched *animelists.Node, hasSpecials bool) []int {
	if patched.Attr(attrDefaultSeason) == "a" {
		return nil
	}
	set := map[int]bool{}
	if s, err := strconv.Atoi(patched.Attr(attrDefaultSeason)); err == nil && s >= 0 {
		set[s] = true
	}
	for _, r := range patched.Rows {
		if s, err := strconv.Atoi(strings.TrimSpace(r.Attrs["tvdbseason"])); err == nil && s >= 0 {
			set[s] = true
		}
	}
	if hasSpecials {
		set[0] = true
	}
	out := make([]int, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// SetHash is the digest of the overlay set as published in sources: the
// entries, then the bridges.
func SetHash(entries []Entry, bridges []Bridge) (string, error) {
	h := sha256.New()
	for i := range entries {
		if err := hashLine(h, entries[i]); err != nil {
			return "", err
		}
	}
	for i := range bridges {
		if err := hashLine(h, bridges[i]); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
