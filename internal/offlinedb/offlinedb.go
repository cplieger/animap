// Package offlinedb stream-decodes anime-offline-database's .jsonl release
// asset under fixed bounds, keeping only the fields animap joins on.
package offlinedb

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/schema"
)

// Limits bounds one read. Sized off the 2026-40 asset: 60 MB, 38,288
// entries, longest line well under 64 KiB.
type Limits struct {
	MaxFileBytes int64
	MaxLineBytes int
	MaxEntries   int
	MaxSources   int
}

// DefaultLimits are the publisher's bounds.
var DefaultLimits = Limits{
	MaxFileBytes: 256 << 20,
	MaxLineBytes: 1 << 20,
	MaxEntries:   1 << 17,
	MaxSources:   64,
}

// Errors a caller can branch on.
var (
	ErrTooLarge = errors.New("offlinedb: bound exceeded")
	ErrLicense  = errors.New("offlinedb: metadata does not declare the ODbL")
	ErrFormat   = errors.New("offlinedb: malformed database")
)

// Entry is one database entry, reduced to its join fields.
type Entry struct {
	Type     string
	AniList  []int
	AniDB    []int
	MAL      []int
	Episodes int
}

// Meta is the database's first line.
type Meta struct {
	LicenseName string
	LicenseURL  string
	LastUpdate  string
}

// Load checks the file size, then streams it through Read.
func Load(path string, lim Limits) (Meta, []Entry, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Meta{}, nil, err
	}
	if st.Size() > lim.MaxFileBytes {
		return Meta{}, nil, fmt.Errorf("%w: file is %d bytes", ErrTooLarge, st.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, nil, err
	}
	defer func() { _ = f.Close() }()
	return Read(io.LimitReader(f, lim.MaxFileBytes), lim)
}

type metaLine struct {
	License struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"license"`
	LastUpdate string `json:"lastUpdate"`
}

type entryLine struct {
	Type     string   `json:"type"`
	Sources  []string `json:"sources"`
	Episodes int      `json:"episodes"`
}

// Read decodes a metadata line then one entry per line. A malformed line
// fails the read: the file is machine-generated, so one bad line means a
// broken file.
func Read(r io.Reader, lim Limits) (Meta, []Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, min(64<<10, lim.MaxLineBytes)), lim.MaxLineBytes)
	meta, err := readMeta(sc)
	if err != nil {
		return Meta{}, nil, err
	}
	var out []Entry
	for line := 2; sc.Scan(); line++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		if len(out) >= lim.MaxEntries {
			return Meta{}, nil, fmt.Errorf("%w: more than %d entries", ErrTooLarge, lim.MaxEntries)
		}
		e, err := parseEntry(sc.Bytes(), lim)
		if err != nil {
			return Meta{}, nil, fmt.Errorf("line %d: %w", line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return Meta{}, nil, scanError(err)
	}
	if len(out) == 0 {
		return Meta{}, nil, fmt.Errorf("%w: no entries", ErrFormat)
	}
	return meta, out, nil
}

func readMeta(sc *bufio.Scanner) (Meta, error) {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Meta{}, scanError(err)
		}
		return Meta{}, fmt.Errorf("%w: empty file", ErrFormat)
	}
	var ml metaLine
	if err := json.Unmarshal(sc.Bytes(), &ml); err != nil {
		return Meta{}, fmt.Errorf("%w: metadata line: %w", ErrFormat, err)
	}
	if !strings.Contains(ml.License.Name, "ODbL") {
		return Meta{}, fmt.Errorf("%w: %q", ErrLicense, ml.License.Name)
	}
	return Meta{LicenseName: ml.License.Name, LicenseURL: ml.License.URL, LastUpdate: ml.LastUpdate}, nil
}

func parseEntry(b []byte, lim Limits) (Entry, error) {
	var el entryLine
	if err := json.Unmarshal(b, &el); err != nil {
		return Entry{}, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if len(el.Sources) > lim.MaxSources {
		return Entry{}, fmt.Errorf("%w: %d sources", ErrTooLarge, len(el.Sources))
	}
	if !schema.ValidType(el.Type) {
		return Entry{}, fmt.Errorf("%w: type %q is not one animap publishes", ErrFormat, el.Type)
	}
	e := Entry{Type: el.Type, Episodes: max(el.Episodes, 0)}
	for _, s := range el.Sources {
		switch {
		case appendID(&e.AniList, s, "https://anilist.co/anime/"):
		case appendID(&e.AniDB, s, "https://anidb.net/anime/"):
		case appendID(&e.MAL, s, "https://myanimelist.net/anime/"):
		}
	}
	return e, nil
}

func scanError(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("%w: %w", ErrTooLarge, err)
	}
	return err
}

// appendID reports whether s carries prefix; a positive integer suffix is
// appended, de-duplicated.
func appendID(dst *[]int, s, prefix string) bool {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return false
	}
	id, err := strconv.Atoi(rest)
	if err != nil || id <= 0 {
		return true
	}
	if !slices.Contains(*dst, id) {
		*dst = append(*dst, id)
	}
	return true
}
