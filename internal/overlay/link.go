package overlay

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/strictjson"
	"github.com/cplieger/animap/mapread"
)

// LinkDir is where links live, below the overlay directory.
const LinkDir = "links"

// Link says which AniDB episodes an AniList entry is, for an entry
// anime-offline-database gives no AniDB id: the parts concatenate in
// AniList episode order. It holds identities only; where the episodes land
// on TVDB is their nodes' mapping, read by Compile.
//
//nolint:govet // fieldalignment: member order is the file order
type Link struct {
	AniListID     int               `json:"anilist_id"`
	Title         string            `json:"title"`
	Parts         []Part            `json:"parts"`
	Justification string            `json:"justification"`
	Evidence      map[string]string `json:"evidence"`
	Captured      LinkCaptured      `json:"captured"`
}

// Part is one run of AniDB anime AniDBID. Exactly one form is set: each
// number of Specials or Episodes is one AniList episode, and Whole is one
// AniList episode made of every regular episode of the anime. Specials are
// numbers in Anime-Lists' season 0, as anidb.Anime.SeasonZero reads them.
//
//nolint:govet // fieldalignment: member order is the file order
type Part struct {
	AniDBID  int   `json:"anidb_id"`
	Specials []int `json:"specials,omitempty"`
	Episodes []int `json:"episodes,omitempty"`
	Whole    bool  `json:"whole,omitempty"`
}

// LinkCaptured is the AniDB state a link was proven against: AniDBSHA256 is
// AniDBDigest at the mirror commit.
type LinkCaptured struct {
	At                string `json:"at"`
	AniDBMirrorCommit string `json:"anidb_mirror_commit"`
	AniDBSHA256       string `json:"anidb_sha256"`
}

// Source is one AniDB episode a link names. AniDBSeason is 0 for a season
// 0 number, 1 for a regular episode.
type Source struct {
	AniDB, AniDBSeason, Number, AniListEpisode int
}

// Claim is a source and the TVDB [season, episode] it lands on.
type Claim struct {
	Target                      [2]int
	Parent, AniDBSeason, Number int
}

// Compiled is a link placed on TVDB: Targets[i] is the episode of season
// Season of series Series that AniList episode i+1 lands on.
type Compiled struct {
	Targets                 []int
	Claims                  []Claim
	AniList, Series, Season int
}

func (p *Part) season() int {
	if len(p.Specials) > 0 {
		return 0
	}
	return 1
}

func (p *Part) numbers() []int {
	if len(p.Specials) > 0 {
		return p.Specials
	}
	return p.Episodes
}

// Path is the link's file, relative to the repository root.
func (l *Link) Path() string {
	return "overlay/" + LinkDir + "/" + strconv.Itoa(l.AniListID) + ".json"
}

// episodes is the number of AniList episodes the link names.
func (l *Link) episodes() int {
	n := 0
	for i := range l.Parts {
		if l.Parts[i].Whole {
			n++
		}
		n += len(l.Parts[i].numbers())
	}
	return n
}

// AniDBIDs are the anime the parts name, in part order, each once.
func (l *Link) AniDBIDs() []int {
	var out []int
	for i := range l.Parts {
		if !slices.Contains(out, l.Parts[i].AniDBID) {
			out = append(out, l.Parts[i].AniDBID)
		}
	}
	return out
}

// LoadLinks reads and validates every <dir>/links/*.json; a missing
// directory is no links.
func LoadLinks(dir string) ([]Link, error) {
	paths, err := filepath.Glob(filepath.Join(dir, LinkDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) > maxEntries {
		return nil, fmt.Errorf("%w: %d links, at most %d", errInvalid, len(paths), maxEntries)
	}
	slices.Sort(paths)
	out := make([]Link, 0, len(paths))
	for _, p := range paths {
		l, err := ReadLink(p)
		if err != nil {
			return nil, err
		}
		if err := l.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, l)
	}
	return out, nil
}

// ReadLink reads one link strictly and checks that the file is named for
// its AniList id, which is what keeps one link per AniList id. It does not
// validate, so capture-link can read a link it is about to capture.
func ReadLink(path string) (Link, error) {
	body, err := strictjson.ReadFile(path, maxLinkBytes)
	if err != nil {
		return Link{}, err
	}
	var l Link
	if err := strictjson.Decode(body, maxLinkBytes, &l); err != nil {
		return Link{}, fmt.Errorf("%w: %s: %w", errInvalid, path, err)
	}
	if want := strconv.Itoa(l.AniListID) + ".json"; filepath.Base(path) != want {
		return Link{}, fmt.Errorf("%w: %s must be named %s", errInvalid, path, want)
	}
	return l, nil
}

// Validate checks the link's own rules, without the mirror.
func (l *Link) Validate() error {
	if err := l.validate(); err != nil {
		return fmt.Errorf("%w: AniList %d: %w", errInvalid, l.AniListID, err)
	}
	return nil
}

func (l *Link) validate() error {
	switch {
	case l.AniListID <= 0:
		return errors.New("anilist_id must be positive")
	case len(l.Parts) == 0:
		return errors.New("parts must name at least one part")
	case strings.TrimSpace(l.Title) == "" || strings.TrimSpace(l.Justification) == "":
		return errors.New("title and justification are required")
	}
	if err := l.validateParts(); err != nil {
		return err
	}
	if err := validateEvidence(l.Evidence); err != nil {
		return err
	}
	c := l.Captured
	if _, err := time.Parse(time.DateOnly, c.At); err != nil {
		return errors.New("captured.at is not a YYYY-MM-DD date")
	}
	switch {
	case !commitRE.MatchString(c.AniDBMirrorCommit):
		return errors.New("captured.anidb_mirror_commit is not a 40-hex commit")
	case !hexRE.MatchString(c.AniDBSHA256):
		return errors.New("captured.anidb_sha256 is not 64 hex")
	}
	return nil
}

// validateParts holds a whole part to stand for every regular episode of
// its anime, so no other part may name one of them again.
func (l *Link) validateParts() error {
	seen := map[[3]int]bool{}
	whole := map[int]bool{}
	for i := range l.Parts {
		if err := l.validatePart(i, seen, whole); err != nil {
			return fmt.Errorf("parts[%d]: %w", i, err)
		}
	}
	return nil
}

func (l *Link) validatePart(i int, seen map[[3]int]bool, whole map[int]bool) error {
	p := &l.Parts[i]
	forms := 0
	for _, set := range []bool{len(p.Specials) > 0, len(p.Episodes) > 0, p.Whole} {
		if set {
			forms++
		}
	}
	regularBefore := slices.ContainsFunc(l.Parts[:i], func(q Part) bool { return q.AniDBID == p.AniDBID && len(q.Episodes) > 0 })
	switch {
	case p.AniDBID <= 0:
		return errors.New("anidb_id must be positive")
	case forms != 1:
		return errors.New("exactly one of specials, episodes and whole must be set")
	case p.Whole && (whole[p.AniDBID] || regularBefore):
		return fmt.Errorf("AniDB %d's regular episodes are named twice", p.AniDBID)
	case p.Whole:
		whole[p.AniDBID] = true
		return nil
	}
	s := p.season()
	for _, k := range p.numbers() {
		key := [3]int{p.AniDBID, s, k}
		if k <= 0 || seen[key] || (s == 1 && whole[p.AniDBID]) {
			return fmt.Errorf("AniDB %d %s is not positive or is named twice", p.AniDBID, animelists.EpisodeLabel(s, k))
		}
		seen[key] = true
	}
	return nil
}

// Sources expands the parts into AniDB episodes in AniList order. A whole
// part is the regular episodes facts lists for its anime, none when it
// lists none.
func (l *Link) Sources(facts *anidb.Facts) []Source {
	var out []Source
	ep := 1
	for i := range l.Parts {
		p := &l.Parts[i]
		if p.Whole {
			a, _ := facts.Anime(p.AniDBID)
			for _, e := range a.Regular {
				out = append(out, Source{AniDB: p.AniDBID, AniDBSeason: 1, Number: e.Number, AniListEpisode: ep})
			}
			ep++
			continue
		}
		for _, k := range p.numbers() {
			out = append(out, Source{AniDB: p.AniDBID, AniDBSeason: p.season(), Number: k, AniListEpisode: ep})
			ep++
		}
	}
	return out
}

// Identity is the AniDB id the link makes the AniList entry: when Listed
// accepts it and Published is one regular part from 1 to identityEnd, so
// the identity names no episode the link does not.
func (l *Link) Identity(facts *anidb.Facts) (aniDBID int, ok bool) {
	ps := l.Published(facts)
	if len(ps) != 1 || l.Listed(facts) != nil {
		return 0, false
	}
	p := ps[0]
	if p.Kind != mapread.Regular || p.Start != 1 || p.End != identityEnd(facts, p.AniDBID) {
		return 0, false
	}
	return p.AniDBID, true
}

// identityEnd is the last regular episode an identity to aid names: its
// Count; while no source counts it, every regular episode the mirror
// lists, 1 to N under a listed link, whose rangeless part then names none
// until a count exists and the link is classed again.
func identityEnd(facts *anidb.Facts, aid int) int {
	if n := facts.Count(aid); n > 0 {
		return n
	}
	a, _ := facts.Anime(aid)
	return len(a.Regular)
}

// Published are the link's parts as the file gives them its entry, in
// AniDB's numbering: each run of consecutive numbers of one anime and kind
// one ranged part, and a whole part one combined part.
func (l *Link) Published(facts *anidb.Facts) []mapread.Part {
	var out []mapread.Part
	for i := range l.Parts {
		p := &l.Parts[i]
		if p.Whole {
			out = append(out, mapread.Part{AniDBID: p.AniDBID, Kind: mapread.Regular, Combined: true})
			continue
		}
		a, _ := facts.Anime(p.AniDBID)
		for _, k := range p.Episodes {
			out = extendPart(out, p.AniDBID, mapread.Regular, k)
		}
		for _, k := range p.Specials {
			if kind, n, ok := a.SeasonZeroOf(k); ok {
				out = extendPart(out, p.AniDBID, mapread.Kind(kind), n)
			}
		}
	}
	return out
}

func extendPart(parts []mapread.Part, aid int, kind mapread.Kind, n int) []mapread.Part {
	if m := len(parts); m > 0 {
		if last := &parts[m-1]; last.AniDBID == aid && last.Kind == kind && !last.Combined && last.End == n-1 {
			last.End = n
			return parts
		}
	}
	return append(parts, mapread.Part{AniDBID: aid, Kind: kind, Start: n, End: n})
}

// Listed refuses a source facts does not list with its kind, and a whole
// part unless facts lists exactly the anime's regular episodes 1 to its
// Count: the published combined part names 1 to Count, and Sources the
// listed ones.
func (l *Link) Listed(facts *anidb.Facts) error {
	for i := range l.Parts {
		p := &l.Parts[i]
		a, _ := facts.Anime(p.AniDBID)
		if p.Whole {
			if err := wholeListed(a.Regular, facts.Count(p.AniDBID)); err != nil {
				return fmt.Errorf("AniList %d: AniDB %d %w", l.AniListID, p.AniDBID, err)
			}
			continue
		}
		for _, k := range p.numbers() {
			_, ok := a.RegularEpisode(k)
			if p.season() == 0 {
				_, _, ok = a.SeasonZero(k)
			}
			if !ok {
				return fmt.Errorf("AniList %d: AniDB lists no %s for AniDB %d", l.AniListID, animelists.EpisodeLabel(p.season(), k), p.AniDBID)
			}
		}
	}
	return nil
}

// wholeListed is why regular, a sorted list, is not exactly episodes 1 to
// count, nil when it is.
func wholeListed(regular []anidb.Episode, count int) error {
	if len(regular) == 0 {
		return errors.New("lists no regular episode")
	}
	for i := range regular {
		if k := regular[i].Number; k != i+1 {
			return fmt.Errorf("lists regular episodes with a gap before ep%d, so a whole part cannot name them", k)
		}
	}
	if n := len(regular); n != count {
		return fmt.Errorf("lists %d regular episodes and counts %d, so a whole part cannot name them", n, count)
	}
	return nil
}

// AniDBDigest fingerprints the linked episodes at facts: each source's
// number and title digest, or that the mirror lacks it. A renumbered,
// retitled or removed linked episode moves it, an unrelated episode does not.
func (l *Link) AniDBDigest(facts *anidb.Facts) string {
	h := sha256.New()
	for _, s := range l.Sources(facts) {
		kind, digest := "R", "absent"
		a, _ := facts.Anime(s.AniDB)
		if s.AniDBSeason == 0 {
			kind = "S"
			if e, _, ok := a.SeasonZero(s.Number); ok {
				digest = e.Titles
			}
		} else if e, ok := a.RegularEpisode(s.Number); ok {
			digest = e.Titles
		}
		fmt.Fprintf(h, "AniDB %d %s%d %s\n", s.AniDB, kind, s.Number, digest)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Compile places every source through its node in patched with Place, and
// takes Place's single target, default included: a link is proven by
// title, which states what an absent episodeoffset leaves open. An
// absolute number resolves through the parent's overlay entry in entries,
// whose capture is TVDB's order (Entry.TVDBAt). It
// fails unless each AniList episode lands on one TVDB episode, all on one
// series and season, no two on one, and each regular source is within the
// parent's count, the episodes the collision check places.
func (l *Link) Compile(patched map[int]*animelists.Node, entries map[int]*Entry, facts *anidb.Facts) (Compiled, error) {
	c := Compiled{AniList: l.AniListID, Targets: make([]int, l.episodes())}
	byEpisode := make([][][2]int, l.episodes())
	for _, s := range l.Sources(facts) {
		t, series, err := l.place(s, patched[s.AniDB], entries[s.AniDB], facts)
		if err != nil {
			return Compiled{}, err
		}
		if c.Series != 0 && series != c.Series {
			return Compiled{}, fmt.Errorf("AniList %d: the linked episodes are on TVDB %d and %d", l.AniListID, c.Series, series)
		}
		c.Series = series
		c.Claims = append(c.Claims, Claim{Parent: s.AniDB, AniDBSeason: s.AniDBSeason, Number: s.Number, Target: t})
		if at := &byEpisode[s.AniListEpisode-1]; !slices.Contains(*at, t) {
			*at = append(*at, t)
		}
	}
	for i, ts := range byEpisode {
		switch {
		case len(ts) == 0:
			return Compiled{}, fmt.Errorf("AniList %d episode %d names no AniDB episode the mirror lists", l.AniListID, i+1)
		case len(ts) > 1:
			return Compiled{}, fmt.Errorf("AniList %d episode %d lands on TVDB %dx%d and TVDB %dx%d", l.AniListID, i+1, ts[0][0], ts[0][1], ts[1][0], ts[1][1])
		case i > 0 && ts[0][0] != c.Season:
			return Compiled{}, fmt.Errorf("AniList %d: the linked episodes are on TVDB seasons %d and %d", l.AniListID, c.Season, ts[0][0])
		}
		if j := slices.Index(c.Targets[:i], ts[0][1]); j >= 0 {
			return Compiled{}, fmt.Errorf("AniList %d episodes %d and %d both land on TVDB %dx%d", l.AniListID, j+1, i+1, ts[0][0], ts[0][1])
		}
		c.Season, c.Targets[i] = ts[0][0], ts[0][1]
	}
	return c, nil
}

// place is where source s lands on TVDB through its node n, and n's series.
func (l *Link) place(s Source, n *animelists.Node, e *Entry, facts *anidb.Facts) (at [2]int, series int, err error) {
	label := fmt.Sprintf("AniDB %d %s", s.AniDB, animelists.EpisodeLabel(s.AniDBSeason, s.Number))
	if n == nil {
		return at, 0, fmt.Errorf("AniList %d: AniDB %d has no Anime-Lists node", l.AniListID, s.AniDB)
	}
	if series, err = strconv.Atoi(n.Attr(attrTVDBID)); err != nil || series <= 0 {
		return at, 0, fmt.Errorf("AniList %d: AniDB %d is on no TVDB series (tvdbid %q)", l.AniListID, s.AniDB, n.Attr(attrTVDBID))
	}
	if s.AniDBSeason == 1 && s.Number > facts.Count(s.AniDB) {
		return at, 0, fmt.Errorf("AniList %d: AniDB %d counts %d regular episodes, so the collision check places no ep%d", l.AniListID, s.AniDB, facts.Count(s.AniDB), s.Number)
	}
	p := n.Place(animelists.TVDB, s.AniDBSeason, s.Number)
	switch {
	case len(p.Targets) == 1:
		return p.Targets[0], series, nil
	case p.Absolute > 0:
		if abs, ok := e.TVDBAt(series, p.Absolute); ok {
			return abs, series, nil
		}
		return at, 0, fmt.Errorf("AniList %d: %s lands on TVDB absolute episode %d; no overlay entry for AniDB %d captures one TVDB episode with that number", l.AniListID, label, p.Absolute, s.AniDB)
	}
	return at, 0, fmt.Errorf("AniList %d: %s maps to no single TVDB episode", l.AniListID, label)
}
