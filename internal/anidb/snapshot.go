package anidb

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/strictjson"
)

// Repository is the mirror's home, as named in a release's sources.
const Repository = "https://github.com/notseteve/AnimeAggregations"

// Bounds on what Extract and LoadSnapshot read. The 2026-10-01 archive is
// 103 MB, 874 MB unpacked; its largest anime file is 2.1 MB.
const (
	maxArchiveBytes  = 1 << 30
	maxUnpackedBytes = 4 << 30
	maxFileBytes     = 8 << 20
	maxSnapshotBytes = 64 << 20
)

var (
	commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Snapshot is every episode list the mirror holds at Commit, by AniDB id,
// and the ids whose file Extract refused.
type Snapshot struct {
	Anime   map[int]Anime `json:"anime"`
	Commit  string        `json:"commit"`
	Refused []int         `json:"refused"`
}

// Extract reads the mirror's archive at commit and keeps each anime file's
// episode list. A file that fails the content checks is refused, reported
// and skipped: at a pinned commit it is the same file every run. A file
// with no episodes object gives no list. A damaged or truncated archive, or
// one with no anime file under the commit's directory, is an error.
func Extract(r io.Reader, commit string) (*Snapshot, []error, error) {
	return extract(r, commit, maxArchiveBytes, maxUnpackedBytes)
}

func extract(r io.Reader, commit string, maxArchive, maxUnpacked int64) (*Snapshot, []error, error) {
	if !commitRE.MatchString(commit) {
		return nil, nil, fmt.Errorf("%w: commit %q is not a 40-hex commit", errInvalid, commit)
	}
	limited := &io.LimitedReader{R: r, N: maxArchive + 1}
	zr, err := gzip.NewReader(limited)
	if err != nil {
		return nil, nil, fmt.Errorf("anidb: archive: %w", err)
	}
	unpacked := &io.LimitedReader{R: zr, N: maxUnpacked + 1}
	s := &Snapshot{Commit: commit, Anime: map[int]Anime{}, Refused: []int{}}
	refusals, err := s.readArchive(tar.NewReader(unpacked), "AnimeAggregations-"+commit+"/anime/")
	if err == nil {
		// tar stops at its end-of-archive blocks; the gzip trailer that
		// proves the stream whole comes after them.
		_, err = io.CopyN(io.Discard, unpacked, unpacked.N)
		if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	switch {
	case limited.N <= 0 || unpacked.N <= 0:
		return nil, nil, fmt.Errorf("%w: archive over %d bytes, or over %d unpacked", errInvalid, maxArchive, maxUnpacked)
	case err != nil:
		return nil, nil, fmt.Errorf("anidb: archive: %w", err)
	case len(s.Anime) == 0:
		return nil, nil, fmt.Errorf("%w: archive holds no anime file for commit %s", errInvalid, commit)
	}
	slices.Sort(s.Refused)
	return s, refusals, nil
}

func (s *Snapshot) readArchive(tr *tar.Reader, dir string) ([]error, error) {
	var refusals []error
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return refusals, nil
		}
		if err != nil {
			return nil, err
		}
		name, inDir := strings.CutPrefix(h.Name, dir)
		stem, isJSON := strings.CutSuffix(name, ".json")
		if h.Typeflag != tar.TypeReg || !inDir || !isJSON || strings.Contains(stem, "/") {
			continue
		}
		refusal, err := s.readFile(tr, h, stem)
		if err != nil {
			return nil, err
		}
		if refusal != nil {
			refusals = append(refusals, refusal)
		}
	}
}

// readFile keeps one anime file's episode list, and returns why it refused
// the file, or a failed read of the archive itself.
func (s *Snapshot) readFile(tr *tar.Reader, h *tar.Header, stem string) (refusal, err error) {
	aid, err := strconv.Atoi(stem)
	if err != nil || aid <= 0 {
		return fmt.Errorf("%w: %s is not named for an AniDB id", errInvalid, h.Name), nil
	}
	if h.Size > maxFileBytes {
		s.Refused = append(s.Refused, aid)
		return fmt.Errorf("%w: AniDB %d: file is %d bytes", errInvalid, aid, h.Size), nil
	}
	body, err := io.ReadAll(tr)
	if err != nil {
		return nil, err
	}
	a, listed, err := parse(aid, body)
	switch {
	case err != nil:
		s.Refused = append(s.Refused, aid)
		return err, nil
	case listed:
		s.Anime[aid] = a
	}
	return nil, nil
}

type animeFile struct {
	EndDate  *string                 `json:"end_date"`
	Episodes map[string][]rawEpisode `json:"episodes"`
	AnimeID  int                     `json:"anime_id"`
}

type rawEpisode struct {
	AirDate   *string    `json:"air_date"`
	Titles    []rawTitle `json:"titles"`
	Number    int        `json:"number"`
	EpisodeID int        `json:"episode_id"`
}

type rawTitle struct {
	Language string `json:"language"`
	Title    string `json:"title"`
}

// titleDigest is the hex SHA-256 of every title of the regular episodes and
// specials, by type and number, independent of the order the file lists
// them in.
func titleDigest(episodes map[string][]rawEpisode) string {
	var lines []string
	for _, kind := range []string{"REGULAR", "SPECIAL"} {
		for i := range episodes[kind] {
			lines = append(lines, titleLine(kind, &episodes[kind][i]))
		}
	}
	return digestLines(lines)
}

// titleLine is one episode's type, number and titles, its titles sorted.
func titleLine(kind string, e *rawEpisode) string {
	titles := make([]string, 0, len(e.Titles))
	for _, t := range e.Titles {
		titles = append(titles, strconv.Quote(t.Language)+"="+strconv.Quote(strings.TrimSpace(t.Title)))
	}
	slices.Sort(titles)
	return kind + " " + strconv.Itoa(e.Number) + " " + strings.Join(titles, " ")
}

func digestLines(lines []string) string {
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// parse returns AniDB id aid's episode list, and listed=false for a file
// with no episodes object. It refuses a file strictjson refuses, one for
// another anime, one that lists an episode kind outside the six, numbers an
// episode of any kind twice or below 1, lists an episode with no positive
// episode_id, or gives a regular episode or special a date that is not
// YYYY-MM-DD.
func parse(aid int, body []byte) (a Anime, listed bool, err error) {
	a, listed, err = parseFile(aid, body)
	if err != nil {
		return Anime{}, false, fmt.Errorf("%w: AniDB %d: %w", errInvalid, aid, err)
	}
	return a, listed, nil
}

func parseFile(aid int, body []byte) (Anime, bool, error) {
	var f animeFile
	if err := strictjson.Unmarshal(body, maxFileBytes, &f); err != nil {
		return Anime{}, false, err
	}
	if f.AnimeID != aid {
		return Anime{}, false, fmt.Errorf("file is for anime_id %d", f.AnimeID)
	}
	if f.Episodes == nil {
		return Anime{}, false, nil
	}
	if err := checkKinds(f.Episodes); err != nil {
		return Anime{}, false, err
	}
	if err := checkEpisodeIDs(f.Episodes); err != nil {
		return Anime{}, false, err
	}
	var a Anime
	a.Finished = f.EndDate != nil && *f.EndDate != ""
	a.Titles = titleDigest(f.Episodes)
	a.Regular = sortedEpisodes("REGULAR", f.Episodes["REGULAR"])
	a.Specials = sortedEpisodes("SPECIAL", f.Episodes["SPECIAL"])
	if err := checkEpisodes("regular episode", a.Regular); err != nil {
		return Anime{}, false, err
	}
	if err := checkEpisodes("special", a.Specials); err != nil {
		return Anime{}, false, err
	}
	x, err := extras(f.Episodes, a.Specials)
	if err != nil {
		return Anime{}, false, err
	}
	a.Extras = x
	return a, true, nil
}

// checkKinds refuses an episodes key outside the mirror's six kinds: an
// episode filed under it would be dropped from a list read as whole.
func checkKinds(episodes map[string][]rawEpisode) error {
	for _, key := range slices.Sorted(maps.Keys(episodes)) {
		if key != "REGULAR" && key != "SPECIAL" && !slices.ContainsFunc(extraKinds, func(k extraKind) bool { return k.mirror == key }) {
			return fmt.Errorf("episode kind %q is not one of the mirror's six", key)
		}
	}
	return nil
}

// The AniDB episode kinds, as Anime.Episodes names them; Extra.Kind and
// Anime.Last take those after Regular.
const (
	Regular   = "regular"
	Special   = "special"
	Credit    = "credit"
	Promotion = "promotion"
	Parody    = "parody"
	Other     = "other"
)

// extraKinds are the mirror's other episode kinds and the number before
// each one's first in Anime-Lists' season 0: credit 1 is 101, promotion 1
// 201, parody 1 301, other 1 401. The README states the promotion (201)
// and other (401) blocks
// (https://github.com/Anime-Lists/anime-lists#mapping-list); the list's
// rows use all four. Other episodes are the last block, so they have no
// upper bound.
var extraKinds = []extraKind{{"CREDITS", Credit, 100}, {"PROMOTION", Promotion, 200}, {"PARODY", Parody, 300}, {"OTHER", Other, 400}}

type extraKind struct {
	mirror, kind string
	base         int
}

// seasonZeroKind decodes Anime-Lists season 0 number k by its blocks, for
// a number no listed episode answers: 1 to 100 is special k, 101 to 200
// credit k-100, then promotion, parody, and other from 401 with no upper
// bound. ok is false below 1.
func seasonZeroKind(k int) (kind string, number int, ok bool) {
	if k < 1 {
		return "", 0, false
	}
	for _, x := range slices.Backward(extraKinds) {
		if k > x.base {
			return x.kind, k - x.base, true
		}
	}
	return Special, k, true
}

// seasonZeroNumber is the Anime-Lists season 0 number of AniDB episode
// number of kind, seasonZeroKind's inverse; ok is false for a number below
// 1, past its block's 100th, or of an unknown kind.
func seasonZeroNumber(kind string, number int) (k int, ok bool) {
	if number < 1 {
		return 0, false
	}
	if kind == Special {
		return number, number <= extraKinds[0].base
	}
	for i, x := range extraKinds {
		if x.kind == kind {
			return x.base + number, i == len(extraKinds)-1 || number <= extraKinds[i+1].base-x.base
		}
	}
	return 0, false
}

// extras keeps every extra, numbered in season 0 as numberExtra says, and
// refuses one numbered twice or below 1 within its kind.
func extras(episodes map[string][]rawEpisode, specials []Episode) ([]Extra, error) {
	out := []Extra{}
	for _, k := range extraKinds {
		eps := sortedEpisodes(k.mirror, episodes[k.mirror])
		if err := checkNumbers(k.kind, eps); err != nil {
			return nil, err
		}
		for _, e := range eps {
			out = append(out, numberExtra(Extra{Kind: k.kind, Number: e.Number, Titles: e.Titles, ID: e.ID}, specials))
		}
	}
	return out, nil
}

// numberExtra sets x's SeasonZero: seasonZeroNumber's, or 0 when that is
// none or a listed special's number, which Anime-Lists cannot tell apart.
func numberExtra(x Extra, specials []Episode) Extra {
	k, ok := seasonZeroNumber(x.Kind, x.Number)
	if !ok || slices.ContainsFunc(specials, func(s Episode) bool { return s.Number == k }) {
		k = 0
	}
	x.SeasonZero = k
	return x
}

func sortedEpisodes(kind string, in []rawEpisode) []Episode {
	out := make([]Episode, 0, len(in))
	for i := range in {
		e := &in[i]
		ep := Episode{Number: e.Number, ID: e.EpisodeID, Titles: digestLines([]string{titleLine(kind, e)})}
		if e.AirDate != nil {
			ep.AirDate = *e.AirDate
		}
		out = append(out, ep)
	}
	slices.SortStableFunc(out, func(x, y Episode) int { return cmp.Compare(x.Number, y.Number) })
	return out
}

// checkEpisodeIDs refuses an episode of any kind with no positive
// episode_id, which every episode of the mirror carries.
func checkEpisodeIDs(episodes map[string][]rawEpisode) error {
	for _, kind := range slices.Sorted(maps.Keys(episodes)) {
		for _, e := range episodes[kind] {
			if e.EpisodeID <= 0 {
				return fmt.Errorf("%s episode %d has no positive episode_id", kind, e.Number)
			}
		}
	}
	return nil
}

// checkEpisodes is checkNumbers, and refuses a date that is not
// YYYY-MM-DD.
func checkEpisodes(what string, eps []Episode) error {
	if err := checkNumbers(what, eps); err != nil {
		return err
	}
	for _, e := range eps {
		if _, err := time.Parse(time.DateOnly, e.AirDate); e.AirDate != "" && err != nil {
			return fmt.Errorf("air date %q is not YYYY-MM-DD", e.AirDate)
		}
	}
	return nil
}

// checkNumbers refuses an episode of eps numbered below 1 or not after the
// one before it, so a sorted list numbered twice.
func checkNumbers(what string, eps []Episode) error {
	for i, e := range eps {
		if e.Number < 1 || (i > 0 && e.Number <= eps[i-1].Number) {
			return fmt.Errorf("%s numbered %d is below 1, listed twice or out of order", what, e.Number)
		}
	}
	return nil
}

func checkListed(id int, a *Anime) error {
	if id <= 0 {
		return errors.New("not a positive id")
	}
	if !digestRE.MatchString(a.Titles) || slices.ContainsFunc(a.Regular, func(e Episode) bool { return notDigest(e.Titles) }) ||
		slices.ContainsFunc(a.Specials, func(e Episode) bool { return notDigest(e.Titles) }) {
		return errors.New("titles is not a 64-hex digest for the anime and each episode; re-extract the snapshot with this animap")
	}
	if a.Extras == nil || slices.ContainsFunc(a.Extras, func(x Extra) bool { return notDigest(x.Titles) }) {
		return errors.New("extras are missing or lack a 64-hex title digest; re-extract the snapshot with this animap")
	}
	if !identified(a) {
		return errors.New("an episode has no positive AniDB episode id; re-extract the snapshot with this animap")
	}
	if err := checkExtras(a); err != nil {
		return err
	}
	if err := checkEpisodes("regular episode", a.Regular); err != nil {
		return err
	}
	return checkEpisodes("special", a.Specials)
}

// identified reports whether every episode of a has a positive AniDB
// episode id.
func identified(a *Anime) bool {
	none := func(e Episode) bool { return e.ID <= 0 }
	return !slices.ContainsFunc(a.Regular, none) && !slices.ContainsFunc(a.Specials, none) &&
		!slices.ContainsFunc(a.Extras, func(x Extra) bool { return x.ID <= 0 })
}

// checkExtras holds a snapshot's extras to the order and numbering extras
// gives them.
func checkExtras(a *Anime) error {
	for i, x := range a.Extras {
		o := extraOrder(x.Kind)
		if o < 0 || x.Number < 1 {
			return fmt.Errorf("extra %s %d is not a known kind numbered from 1", x.Kind, x.Number)
		}
		if i > 0 {
			p := a.Extras[i-1]
			if po := extraOrder(p.Kind); po > o || po == o && p.Number >= x.Number {
				return errors.New("extras are not sorted by kind, then number, each once")
			}
		}
		if numberExtra(x, a.Specials).SeasonZero != x.SeasonZero {
			return fmt.Errorf("extra %s %d has season 0 number %d, not the one its numbering gives", x.Kind, x.Number, x.SeasonZero)
		}
	}
	return nil
}

func extraOrder(kind string) int {
	for i, k := range extraKinds {
		if k.kind == kind {
			return i
		}
	}
	return -1
}

func notDigest(s string) bool { return !digestRE.MatchString(s) }

// LoadSnapshot reads and validates a snapshot file that Extract's result
// was written to.
func LoadSnapshot(path string) (*Snapshot, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxSnapshotBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes", errInvalid, path, st.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := decodeSnapshot(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// decodeSnapshot parses a snapshot file and holds it to the rules Extract
// applies to each anime file.
func decodeSnapshot(body []byte) (*Snapshot, error) {
	var s Snapshot
	if err := strictjson.Decode(body, maxSnapshotBytes, &s); err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalid, err)
	}
	if !commitRE.MatchString(s.Commit) {
		return nil, fmt.Errorf("%w: commit %q is not a 40-hex commit", errInvalid, s.Commit)
	}
	for id := range s.Anime {
		a := s.Anime[id]
		if err := checkListed(id, &a); err != nil {
			return nil, fmt.Errorf("%w: AniDB %d: %w", errInvalid, id, err)
		}
	}
	for i, id := range s.Refused {
		_, listed := s.Anime[id]
		if id <= 0 || listed || (i > 0 && id <= s.Refused[i-1]) {
			return nil, fmt.Errorf("%w: refused ids are positive, sorted, each once and not listed; AniDB %d is not", errInvalid, id)
		}
	}
	if s.Anime == nil {
		s.Anime = map[int]Anime{}
	}
	return &s, nil
}
