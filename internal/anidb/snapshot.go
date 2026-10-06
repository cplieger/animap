package anidb

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	MaxArchiveBytes  = 1 << 30
	MaxUnpackedBytes = 4 << 30
	MaxFileBytes     = 8 << 20
	maxSnapshotBytes = 64 << 20
)

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

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
	return extract(r, commit, MaxArchiveBytes, MaxUnpackedBytes)
}

func extract(r io.Reader, commit string, maxArchive, maxUnpacked int64) (*Snapshot, []error, error) {
	if !commitRE.MatchString(commit) {
		return nil, nil, fmt.Errorf("%w: commit %q is not a 40-hex commit", ErrInvalid, commit)
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
		return nil, nil, fmt.Errorf("%w: archive over %d bytes, or over %d unpacked", ErrInvalid, maxArchive, maxUnpacked)
	case err != nil:
		return nil, nil, fmt.Errorf("anidb: archive: %w", err)
	case len(s.Anime) == 0:
		return nil, nil, fmt.Errorf("%w: archive holds no anime file for commit %s", ErrInvalid, commit)
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
		return fmt.Errorf("%w: %s is not named for an AniDB id", ErrInvalid, h.Name), nil
	}
	if h.Size > MaxFileBytes {
		s.Refused = append(s.Refused, aid)
		return fmt.Errorf("%w: AniDB %d: file is %d bytes", ErrInvalid, aid, h.Size), nil
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
	AirDate *string `json:"air_date"`
	Number  int     `json:"number"`
}

// parse returns AniDB id aid's episode list, and listed=false for a file
// with no episodes object. It refuses a file for another anime, a regular
// list that is not exactly the episodes 1 to N (the collision check places
// a count as those episodes), a special numbered twice or below 1, and a
// date that is not YYYY-MM-DD.
func parse(aid int, body []byte) (a Anime, listed bool, err error) {
	var f animeFile
	if err := json.Unmarshal(body, &f); err != nil {
		return Anime{}, false, fmt.Errorf("%w: AniDB %d: %w", ErrInvalid, aid, err)
	}
	if f.AnimeID != aid {
		return Anime{}, false, fmt.Errorf("%w: AniDB %d: file is for anime_id %d", ErrInvalid, aid, f.AnimeID)
	}
	if f.Episodes == nil {
		return Anime{}, false, nil
	}
	a.Finished = f.EndDate != nil && *f.EndDate != ""
	regular := sortedEpisodes(f.Episodes["REGULAR"])
	a.Specials = sortedEpisodes(f.Episodes["SPECIAL"])
	for i, e := range regular {
		if e.Number != i+1 {
			return Anime{}, false, fmt.Errorf("%w: AniDB %d: regular episodes are not 1 to %d", ErrInvalid, aid, len(regular))
		}
		a.Regular = append(a.Regular, e.AirDate)
	}
	if err := checkSpecials(a.Specials); err != nil {
		return Anime{}, false, fmt.Errorf("%w: AniDB %d: %w", ErrInvalid, aid, err)
	}
	if err := checkDates(a.Regular); err != nil {
		return Anime{}, false, fmt.Errorf("%w: AniDB %d: %w", ErrInvalid, aid, err)
	}
	return a, true, nil
}

func sortedEpisodes(in []rawEpisode) []Episode {
	out := make([]Episode, 0, len(in))
	for _, e := range in {
		ep := Episode{Number: e.Number}
		if e.AirDate != nil {
			ep.AirDate = *e.AirDate
		}
		out = append(out, ep)
	}
	slices.SortStableFunc(out, func(x, y Episode) int { return cmp.Compare(x.Number, y.Number) })
	return out
}

func checkSpecials(sp []Episode) error {
	dates := make([]string, 0, len(sp))
	for i, e := range sp {
		if e.Number < 1 || (i > 0 && e.Number == sp[i-1].Number) {
			return fmt.Errorf("special numbered %d is below 1 or listed twice", e.Number)
		}
		dates = append(dates, e.AirDate)
	}
	return checkDates(dates)
}

func checkDates(dates []string) error {
	for _, d := range dates {
		if _, err := time.Parse(time.DateOnly, d); d != "" && err != nil {
			return fmt.Errorf("air date %q is not YYYY-MM-DD", d)
		}
	}
	return nil
}

func checkListed(id int, a Anime) error {
	if id <= 0 {
		return errors.New("not a positive id")
	}
	for i := 1; i < len(a.Specials); i++ {
		if a.Specials[i].Number <= a.Specials[i-1].Number {
			return errors.New("specials are not sorted by number, each once")
		}
	}
	if err := checkSpecials(a.Specials); err != nil {
		return err
	}
	return checkDates(a.Regular)
}

// LoadSnapshot reads and validates a snapshot file that Extract's result
// was written to.
func LoadSnapshot(path string) (*Snapshot, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxSnapshotBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrInvalid, path, st.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := DecodeSnapshot(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// DecodeSnapshot parses a snapshot file and holds it to the rules Extract
// applies to each anime file.
func DecodeSnapshot(body []byte) (*Snapshot, error) {
	var s Snapshot
	if err := strictjson.Decode(body, &s); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !commitRE.MatchString(s.Commit) {
		return nil, fmt.Errorf("%w: commit %q is not a 40-hex commit", ErrInvalid, s.Commit)
	}
	for id, a := range s.Anime {
		if err := checkListed(id, a); err != nil {
			return nil, fmt.Errorf("%w: AniDB %d: %w", ErrInvalid, id, err)
		}
	}
	for i, id := range s.Refused {
		_, listed := s.Anime[id]
		if id <= 0 || listed || (i > 0 && id <= s.Refused[i-1]) {
			return nil, fmt.Errorf("%w: refused ids are positive, sorted, each once and not listed; AniDB %d is not", ErrInvalid, id)
		}
	}
	if s.Anime == nil {
		s.Anime = map[int]Anime{}
	}
	return &s, nil
}
