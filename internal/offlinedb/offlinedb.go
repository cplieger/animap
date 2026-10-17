// Package offlinedb stream-decodes anime-offline-database's .jsonl release
// asset under fixed bounds, keeping only the fields animap joins on.
package offlinedb

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cplieger/animap/internal/strictjson"
	"github.com/cplieger/animap/mapread"
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

// Errors Load wraps.
var (
	errTooLarge = errors.New("offlinedb: bound exceeded")
	errLicense  = errors.New("offlinedb: metadata does not declare the ODbL")
	errFormat   = errors.New("offlinedb: malformed database")
)

// Entry is one database entry, reduced to its join fields: its type,
// episode count, AniList and AniDB ids in file order, and every service id
// its sources name, IDs.AniDB included. Unknown are the hosts of sources on
// no service animap publishes.
type Entry struct {
	Type     string
	AniList  []int
	AniDB    []int
	Unknown  []string
	IDs      mapread.IDs
	Episodes int
}

// Load checks the file size, then streams it through read.
func Load(path string, lim Limits) ([]Entry, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > lim.MaxFileBytes {
		return nil, fmt.Errorf("%w: file is %d bytes", errTooLarge, st.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return read(io.LimitReader(f, lim.MaxFileBytes), lim)
}

type metaLine struct {
	License struct {
		Name string `json:"name"`
	} `json:"license"`
}

type entryLine struct {
	Type     string   `json:"type"`
	Sources  []string `json:"sources"`
	Episodes int      `json:"episodes"`
}

// read checks the metadata line declares the ODbL, then decodes one entry
// per line. A malformed line fails the read: the file is machine-generated,
// so one bad line means a broken file.
func read(r io.Reader, lim Limits) ([]Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, min(64<<10, lim.MaxLineBytes)), lim.MaxLineBytes)
	if err := readMeta(sc, lim.MaxLineBytes); err != nil {
		return nil, err
	}
	var out []Entry
	for line := 2; sc.Scan(); line++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		if len(out) >= lim.MaxEntries {
			return nil, fmt.Errorf("%w: more than %d entries", errTooLarge, lim.MaxEntries)
		}
		e, err := parseEntry(sc.Bytes(), lim)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, scanError(err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no entries", errFormat)
	}
	return out, nil
}

func readMeta(sc *bufio.Scanner, maxBytes int) error {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return scanError(err)
		}
		return fmt.Errorf("%w: empty file", errFormat)
	}
	var ml metaLine
	if err := strictjson.Unmarshal(sc.Bytes(), maxBytes, &ml); err != nil {
		return fmt.Errorf("%w: metadata line: %w", errFormat, err)
	}
	if !strings.Contains(ml.License.Name, "ODbL") {
		return fmt.Errorf("%w: %q", errLicense, ml.License.Name)
	}
	return nil
}

func parseEntry(b []byte, lim Limits) (Entry, error) {
	var el entryLine
	if err := strictjson.Unmarshal(b, lim.MaxLineBytes, &el); err != nil {
		return Entry{}, fmt.Errorf("%w: %w", errFormat, err)
	}
	if len(el.Sources) > lim.MaxSources {
		return Entry{}, fmt.Errorf("%w: %d sources", errTooLarge, len(el.Sources))
	}
	if !mapread.ValidType(el.Type) {
		return Entry{}, fmt.Errorf("%w: type %q is not one animap publishes", errFormat, el.Type)
	}
	e := Entry{Type: el.Type, Episodes: max(el.Episodes, 0)}
	for _, s := range el.Sources {
		if !e.addSource(s) {
			e.addUnknown(s)
		}
	}
	for _, ids := range []*[]int{&e.IDs.AnimeCountdown, &e.IDs.AniSearch, &e.IDs.ANN, &e.IDs.Kitsu, &e.IDs.LiveChart, &e.IDs.MAL, &e.IDs.Simkl} {
		slices.Sort(*ids)
	}
	e.IDs.AniDB = slices.Sorted(slices.Values(e.AniDB))
	slices.Sort(e.IDs.AnimePlanet)
	slices.Sort(e.IDs.NotifyMoe)
	return e, nil
}

// addSource files s under its service, false when no known prefix matches.
func (e *Entry) addSource(s string) bool {
	for _, p := range []struct {
		dst    *[]int
		prefix string
	}{
		{&e.AniList, "https://anilist.co/anime/"},
		{&e.AniDB, "https://anidb.net/anime/"},
		{&e.IDs.MAL, "https://myanimelist.net/anime/"},
		{&e.IDs.Kitsu, "https://kitsu.app/anime/"},
		{&e.IDs.AniSearch, "https://anisearch.com/anime/"},
		{&e.IDs.AnimeCountdown, "https://animecountdown.com/"},
		{&e.IDs.Simkl, "https://simkl.com/anime/"},
		{&e.IDs.ANN, "https://animenewsnetwork.com/encyclopedia/anime.php?id="},
		{&e.IDs.LiveChart, "https://livechart.me/anime/"},
	} {
		if appendID(p.dst, s, p.prefix) {
			return true
		}
	}
	return appendKey(&e.IDs.AnimePlanet, s, "https://anime-planet.com/anime/") ||
		appendKey(&e.IDs.NotifyMoe, s, "https://notify.moe/anime/")
}

func (e *Entry) addUnknown(s string) {
	host := s
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		host = u.Host
	}
	if !slices.Contains(e.Unknown, host) {
		e.Unknown = append(e.Unknown, host)
	}
}

// appendKey is appendID for a service whose item key is a path segment of
// 1 to 256 characters, as the JSON Schema's maxLength counts them; any
// other suffix is ignored.
func appendKey(dst *[]string, s, prefix string) bool {
	key, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return false
	}
	if key != "" && utf8.RuneCountInString(key) <= 256 && !strings.ContainsAny(key, "/?#") && !slices.Contains(*dst, key) {
		*dst = append(*dst, key)
	}
	return true
}

func scanError(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("%w: %w", errTooLarge, err)
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
