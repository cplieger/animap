package mapread

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cplieger/jsoncap/v2"
)

// Limits bound one Decode. A zero field takes DefaultLimits' value.
type Limits struct {
	// MaxDocumentBytes caps the whole document.
	MaxDocumentBytes int64
	// MaxRows caps each table's rows.
	MaxRows int
	// MaxRowBytes caps one encoded row, and the sources and attribution
	// members.
	MaxRowBytes int
	// MaxEpisodes caps the AniDB episodes all entries' parts name, summed:
	// what Index.Episodes expands over every entry.
	MaxEpisodes int
}

// The default limits are about 2.5 times the 2026-10 document (13.4 MB),
// 3.4 times its larger table (38,323 entries), 10 times its largest row
// and 8 times the episodes its entries name.
const (
	defaultDocumentBytes = 32 << 20
	defaultRows          = 1 << 17
	defaultRowBytes      = 256 << 10
	defaultEpisodes      = 1 << 21
)

// DefaultLimits are the limits a zero Limits takes.
func DefaultLimits() Limits {
	return Limits{MaxDocumentBytes: defaultDocumentBytes, MaxRows: defaultRows, MaxRowBytes: defaultRowBytes, MaxEpisodes: defaultEpisodes}
}

// maxNumber bounds every episode number, count and season, so the
// arithmetic within one run or part (a target plus an offset into the run,
// End-Start+1) stays far inside a 32-bit int; addEpisodes guards the sum
// over parts.
const maxNumber = 1 << 20

func (l Limits) orDefault() Limits {
	if l.MaxDocumentBytes <= 0 {
		l.MaxDocumentBytes = defaultDocumentBytes
	}
	if l.MaxRows <= 0 {
		l.MaxRows = defaultRows
	}
	if l.MaxRowBytes <= 0 {
		l.MaxRowBytes = defaultRowBytes
	}
	if l.MaxEpisodes <= 0 {
		l.MaxEpisodes = defaultEpisodes
	}
	return l
}

// Errors Decode, Source.Latest and Release.Fetch return, matched with
// errors.Is. An empty body, a truncated one and a failed read are plain
// wrapped errors, so a caller can treat them as transient; these are not.
var (
	ErrVersion     = errors.New("mapread: unsupported version")
	ErrNotDocument = errors.New("mapread: not an animap document")
	ErrTooLarge    = errors.New("mapread: over a decode limit")
	ErrInvalid     = errors.New("mapread: invalid document")
	ErrChecksum    = errors.New("mapread: checksum mismatch")
	ErrRelease     = errors.New("mapread: unexpected release answer")
	ErrRedirect    = errors.New("mapread: redirect refused")
)

// VersionError is a document of another version; it matches ErrVersion.
type VersionError struct{ Found int }

func (e *VersionError) Error() string {
	return fmt.Sprintf("mapread: document is version %d, this reader reads version %d", e.Found, Version)
}

// Unwrap makes errors.Is(err, ErrVersion) hold.
func (*VersionError) Unwrap() error { return ErrVersion }

// What capReader returns past its cap, and on bytes that are not UTF-8.
var (
	errOverCap = errors.New("mapread: document over the byte cap")
	errBadUTF8 = errors.New("mapread: document is not UTF-8")
)

// capReader reads at most left bytes and fails, rather than truncating,
// when r holds more, and fails on bytes that are not UTF-8. It records how
// the underlying stream ended, which is what tells a truncated or failed
// read from a malformed document.
type capReader struct {
	r       io.Reader
	readErr error
	left    int64
	nheld   int
	held    [utf8.UTFMax]byte
	over    bool
	eof     bool
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var probe [1]byte
		n, err := c.r.Read(probe[:])
		if n > 0 {
			c.over = true
			return 0, errOverCap
		}
		return 0, c.note(err)
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if !c.validUTF8(p[:n]) {
		return 0, errBadUTF8
	}
	return n, c.note(err)
}

// validUTF8 checks b, which continues the bytes read before it, holding a
// rune cut at its end until the next read completes it; capReader leaves a
// stream ending on one to encoding/json, which reports a cut string as
// truncated. encoding/json would otherwise accept an invalid byte in a
// string as U+FFFD.
func (c *capReader) validUTF8(b []byte) bool {
	b, ok := c.finishHeld(b)
	if !ok || c.nheld > 0 {
		return ok
	}
	cut := runeCut(b)
	c.nheld = copy(c.held[:], b[cut:])
	return utf8.Valid(b[:cut])
}

// finishHeld completes the held rune from b's first bytes and returns the
// rest of b.
func (c *capReader) finishHeld(b []byte) ([]byte, bool) {
	for c.nheld > 0 && len(b) > 0 {
		c.held[c.nheld] = b[0]
		c.nheld++
		b = b[1:]
		if utf8.FullRune(c.held[:c.nheld]) {
			if r, size := utf8.DecodeRune(c.held[:c.nheld]); r == utf8.RuneError && size == 1 {
				return b, false
			}
			c.nheld = 0
		}
	}
	return b, true
}

// runeCut is where a rune cut by b's end starts, or len(b).
func runeCut(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return i
			}
			break
		}
	}
	return len(b)
}

func (c *capReader) note(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		c.eof = true
	case err != nil:
		c.readErr = err
	}
	return err
}

// Decode reads one document from r, streaming it, under lim. It refuses a
// document whose first member is not version 2 before reading further, and
// one not UTF-8 or with an invalid row: it checks the types, the caps and
// docs/schema.md's "Order and checks" but the order of entries with no
// AniList id (entryOrder), and not an entry's parts against its ids.anidb.
// Members it does not know are ignored at every level, a key matching one
// only case-insensitively among them; a key repeated in one object,
// case-insensitively, is invalid.
func Decode(r io.Reader, lim Limits) (*Document, error) {
	lim = lim.orDefault()
	cr := &capReader{r: r, left: lim.MaxDocumentBytes}
	d := &decoder{dec: jsoncap.NewDecoder(cr, 0), cr: cr, lim: lim}
	doc, err := d.document()
	if err != nil {
		return nil, err
	}
	if err := validate(doc, lim); err != nil {
		return nil, err
	}
	return doc, nil
}

type decoder struct {
	dec *jsoncap.Decoder
	cr  *capReader
	lim Limits
}

// stream classifies a failure of the document stream: past the byte cap;
// a failed read, or a stream that ended mid-document, which stays plain;
// else malformed JSON or not UTF-8. encoding/json reports a stream ending
// mid-value as io.ErrUnexpectedEOF from Decode and as a SyntaxError from
// Token.
func (d *decoder) stream(what string, err error) error {
	var syntax *json.SyntaxError
	truncated := d.cr.eof && (errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.As(err, &syntax) && strings.Contains(syntax.Error(), "unexpected end of JSON input"))
	switch {
	case d.cr.over || errors.Is(err, errOverCap):
		return fmt.Errorf("%w: document over %d bytes", ErrTooLarge, d.lim.MaxDocumentBytes)
	case d.cr.readErr != nil:
		return fmt.Errorf("mapread: %s: %w", what, err)
	case truncated:
		return fmt.Errorf("mapread: %s: document truncated: %w", what, err)
	}
	return fmt.Errorf("%w: %s: %w", ErrNotDocument, what, err)
}

// documentMembers are the known top-level members after version, in the
// order a document carries them.
var documentMembers = []string{"generated_at", "sources", "attribution", "anime", "entries"}

func (d *decoder) document() (*Document, error) {
	ok, err := d.dec.Open('{')
	switch {
	case errors.Is(err, io.EOF) && !d.cr.over:
		return nil, fmt.Errorf("mapread: body is empty: %w", err)
	case err != nil:
		return nil, d.stream("document", err)
	case !ok:
		return nil, fmt.Errorf("%w: document is null", ErrNotDocument)
	}
	doc := &Document{}
	if err := d.version(doc); err != nil {
		return nil, err
	}
	if err := d.members(doc); err != nil {
		return nil, err
	}
	if err := d.dec.Close(); err != nil {
		return nil, d.stream("document end", err)
	}
	if err := d.dec.End(); err != nil {
		return nil, d.stream("after the document", err)
	}
	return doc, nil
}

// members reads the members after version: each known one once, in
// order, every other skipped; a key repeating another case-insensitively,
// version included, is refused.
func (d *decoder) members(doc *Document) error {
	seen, got := -1, 0
	keys := map[string]bool{foldKey("version"): true}
	for d.dec.More() {
		key, err := d.dec.Key()
		if err != nil {
			return d.stream("document key", err)
		}
		f := foldKey(key)
		if keys[f] {
			return fmt.Errorf("%w: %.64q is repeated", ErrNotDocument, key)
		}
		keys[f] = true
		i := indexOf(documentMembers, key)
		if i < 0 {
			if err := d.dec.Skip(); err != nil {
				return d.stream(key, err)
			}
			continue
		}
		if i <= seen {
			return fmt.Errorf("%w: %s is out of order", ErrNotDocument, key)
		}
		seen, got = i, got+1
		if err := d.member(doc, key); err != nil {
			return err
		}
	}
	if got < len(documentMembers) {
		return fmt.Errorf("%w: a member of %v is missing", ErrNotDocument, documentMembers)
	}
	return nil
}

// foldKey maps each rune to the smallest of its simple case folds, so two
// keys fold equal exactly when strings.EqualFold holds, as encoding/json
// matches a member.
func foldKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		c := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			c = min(c, f)
		}
		b.WriteRune(c)
	}
	return b.String()
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// version reads the first member, which must be version.
func (d *decoder) version(doc *Document) error {
	if !d.dec.More() {
		return fmt.Errorf("%w: document is empty", ErrNotDocument)
	}
	key, err := d.dec.Key()
	if err != nil {
		return d.stream("document key", err)
	}
	if key != "version" {
		return fmt.Errorf("%w: the first member is %q, not version", ErrNotDocument, key)
	}
	var raw json.RawMessage
	if err = d.dec.Decode(&raw); err != nil {
		return d.stream("version", err)
	}
	v, null, err := intOf(raw)
	switch {
	case err != nil:
		return fmt.Errorf("%w: version: %w", ErrNotDocument, err)
	case null:
		return fmt.Errorf("%w: version is null", ErrNotDocument)
	case v != Version:
		return &VersionError{Found: v}
	}
	doc.Version = v
	return nil
}

func (d *decoder) member(doc *Document, key string) error {
	switch key {
	case "generated_at":
		raw, err := d.raw(key)
		if err != nil {
			return err
		}
		return decodeRow(key, raw, func(w *walker) error { return w.instant("generated_at", &doc.GeneratedAt) })
	case "sources":
		raw, err := d.raw(key)
		if err != nil {
			return err
		}
		return decodeRow(key, raw, func(w *walker) error { return w.sources(&doc.Sources) })
	case "attribution":
		raw, err := d.raw(key)
		if err != nil {
			return err
		}
		return decodeRow(key, raw, func(w *walker) error { return w.attribution(&doc.Attribution) })
	case "anime":
		rows, err := table(d, key, (*walker).anime)
		doc.Anime = rows
		return err
	}
	rows, err := table(d, key, (*walker).entry)
	doc.Entries = rows
	return err
}

// raw reads the next value whole, under MaxRowBytes, and refuses a key
// repeated in any of its objects.
func (d *decoder) raw(what string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := d.dec.Decode(&raw); err != nil {
		return nil, d.stream(what, err)
	}
	if len(raw) > d.lim.MaxRowBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes, over %d", ErrTooLarge, what, len(raw), d.lim.MaxRowBytes)
	}
	if err := jsoncap.Preflight(bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, what, err)
	}
	return raw, nil
}

func table[T any](d *decoder, name string, row func(*walker, *T) error) ([]T, error) {
	ok, err := d.dec.Open('[')
	if err != nil {
		return nil, d.stream(name, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s is null", ErrNotDocument, name)
	}
	out := []T{}
	for i := 0; d.dec.More(); i++ {
		if i == d.lim.MaxRows {
			return nil, fmt.Errorf("%w: %s has more than %d rows", ErrTooLarge, name, d.lim.MaxRows)
		}
		where := fmt.Sprintf("%s[%d]", name, i)
		raw, err := d.raw(where)
		if err != nil {
			return nil, err
		}
		var v T
		if err := decodeRow(where, raw, func(w *walker) error { return row(w, &v) }); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := d.dec.Close(); err != nil {
		return nil, d.stream(name, err)
	}
	return out, nil
}

// decodeRow walks one complete row; any failure is ErrInvalid at where.
func decodeRow(where string, raw json.RawMessage, fn func(*walker) error) error {
	w := &walker{dec: jsoncap.NewDecoder(bytes.NewReader(raw), 0)}
	err := fn(w)
	if err == nil {
		err = w.dec.End()
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, where, err)
	}
	return nil
}

// walker reads one row with exact key matching and strict types: a null,
// or a value of the wrong JSON type, is refused for every known member.
type walker struct{ dec *jsoncap.Decoder }

// object walks an object; field reports whether it consumed key's value,
// and any key it does not is skipped. n is the number of members.
func (w *walker) object(what string, field func(key string) (bool, error)) (n int, err error) {
	ok, err := w.dec.Open('{')
	if err != nil {
		return 0, fmt.Errorf("%s: %w", what, err)
	}
	if !ok {
		return 0, fmt.Errorf("%s is null", what)
	}
	for ; w.dec.More(); n++ {
		key, err := w.dec.Key()
		if err != nil {
			return 0, err
		}
		known, err := field(key)
		if err != nil {
			return 0, err
		}
		if !known {
			if err := w.dec.Skip(); err != nil {
				return 0, err
			}
		}
	}
	return n, w.dec.Close()
}

func (w *walker) array(what string, elem func(i int) error) (n int, err error) {
	ok, err := w.dec.Open('[')
	if err != nil {
		return 0, fmt.Errorf("%s: %w", what, err)
	}
	if !ok {
		return 0, fmt.Errorf("%s is null", what)
	}
	for ; w.dec.More(); n++ {
		if err := elem(n); err != nil {
			return 0, err
		}
	}
	return n, w.dec.Close()
}

// intOf reads one JSON value as an integer written with digits alone, as
// Encode writes one: JSON Schema's integer type also admits 2.0 and 2e0.
// null reports true with no error.
func intOf(raw json.RawMessage) (v int, null bool, err error) {
	s := string(raw)
	switch {
	case s == "null":
		return 0, true, nil
	case s == "" || s[0] != '-' && (s[0] < '0' || s[0] > '9'):
		return 0, false, fmt.Errorf("%.32s is not an integer", s)
	case strings.ContainsAny(s, ".eE"):
		return 0, false, fmt.Errorf("%.32s is not written as an integer", s)
	}
	n, err := strconv.ParseInt(s, 10, strconv.IntSize)
	if err != nil {
		return 0, false, fmt.Errorf("%.32s is outside this platform's int", s)
	}
	return int(n), false, nil
}

// integer reads an integer of at least minimum.
func (w *walker) integer(what string, dst *int, minimum int) error {
	var raw json.RawMessage
	if err := w.dec.Decode(&raw); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	v, null, err := intOf(raw)
	switch {
	case err != nil:
		return fmt.Errorf("%s: %w", what, err)
	case null:
		return fmt.Errorf("%s is null", what)
	case v < minimum:
		return fmt.Errorf("%s is %d, below %d", what, v, minimum)
	}
	*dst = v
	return nil
}

// number reads an episode number, count or season: an integer of at least
// minimum and at most maxNumber.
func (w *walker) number(what string, dst *int, minimum int) error {
	if err := w.integer(what, dst, minimum); err != nil {
		return err
	}
	if *dst > maxNumber {
		return fmt.Errorf("%s is %d, above %d", what, *dst, maxNumber)
	}
	return nil
}

func (w *walker) str(what string, dst *string) error {
	var v *string
	if err := w.dec.Decode(&v); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if v == nil {
		return fmt.Errorf("%s is null", what)
	}
	*dst = *v
	return nil
}

// instantRE is RFC 3339's date-time (§5.6) in UTC: Z, or the offset
// +00:00, but not -00:00, which says the local offset is unknown (§4.3).
var instantRE = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:[Zz]|\+00:00)$`)

// instant reads an RFC 3339 time in UTC.
func (w *walker) instant(what string, dst *string) error {
	if err := w.str(what, dst); err != nil {
		return err
	}
	if !utcInstant(*dst) {
		return fmt.Errorf("%s %.64q is not an RFC 3339 time in UTC", what, *dst)
	}
	return nil
}

// utcInstant reports whether s is an RFC 3339 date-time in UTC. A leap
// second is 23:59:60 UTC (§5.7); time.Parse refuses every second 60.
func utcInstant(s string) bool {
	m := instantRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	if _, err := time.Parse(time.DateOnly, m[1]); err != nil {
		return false
	}
	h, _ := strconv.Atoi(m[2])
	mi, _ := strconv.Atoi(m[3])
	sec, _ := strconv.Atoi(m[4])
	return h <= 23 && mi <= 59 && (sec <= 59 || sec == 60 && h == 23 && mi == 59)
}

// isTrue reads a member whose only value is true.
func (w *walker) isTrue(what string, dst *bool) error {
	var v *bool
	if err := w.dec.Decode(&v); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if v == nil || !*v {
		return fmt.Errorf("%s is present and not true", what)
	}
	*dst = true
	return nil
}

func (w *walker) kind(what string, dst *Kind) error {
	var s string
	if err := w.str(what, &s); err != nil {
		return err
	}
	if Kind(s).order() < 0 {
		return fmt.Errorf("%s %q is not a kind", what, s)
	}
	*dst = Kind(s)
	return nil
}

func (w *walker) typ(what string, dst *string) error {
	if err := w.str(what, dst); err != nil {
		return err
	}
	if *dst == "" || !ValidType(*dst) {
		return fmt.Errorf("%s %q is not a type", what, *dst)
	}
	return nil
}

// ids reads a non-empty, strictly ascending list of ids.
func (w *walker) ids(what string, dst *[]int) error {
	var out []int
	n, err := w.array(what, func(i int) error {
		var v int
		if err := w.integer(fmt.Sprintf("%s[%d]", what, i), &v, 1); err != nil {
			return err
		}
		if i > 0 && v <= out[i-1] {
			return fmt.Errorf("%s is not ascending and unique at %d", what, i)
		}
		out = append(out, v)
		return nil
	})
	if err == nil && n == 0 {
		err = fmt.Errorf("%s is empty", what)
	}
	*dst = out
	return err
}

// keys reads a non-empty, bytewise ascending and unique list of service
// keys of 1 to 256 characters, as the JSON Schema's maxLength counts them.
func (w *walker) keys(what string, dst *[]string) error {
	var out []string
	n, err := w.array(what, func(i int) error {
		var v string
		if err := w.str(fmt.Sprintf("%s[%d]", what, i), &v); err != nil {
			return err
		}
		switch {
		case v == "" || utf8.RuneCountInString(v) > 256:
			return fmt.Errorf("%s[%d] is empty or over 256 characters", what, i)
		case i > 0 && v <= out[i-1]:
			return fmt.Errorf("%s is not ascending and unique at %d", what, i)
		}
		out = append(out, v)
		return nil
	})
	if err == nil && n == 0 {
		err = fmt.Errorf("%s is empty", what)
	}
	*dst = out
	return err
}

var (
	imdbRE   = regexp.MustCompile(`^tt\d{7,10}$`)
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func (w *walker) anime(a *Anime) error {
	n, err := w.object("anime", func(key string) (bool, error) {
		switch key {
		case "anidb_id":
			return true, w.integer(key, &a.AniDBID, 1)
		case "type":
			return true, w.typ(key, &a.Type)
		case "tvdb_id":
			return true, w.integer(key, &a.TVDBID, 1)
		case "tmdb_tv_id":
			return true, w.integer(key, &a.TMDBTVID, 1)
		case "tmdb_movie_ids":
			return true, w.uniqueInts(key, &a.TMDBMovieIDs)
		case "imdb_ids":
			return true, w.imdb(key, &a.IMDbIDs)
		case "last":
			return true, w.last(&a.Last)
		case "episodes_listed":
			return true, w.isTrue(key, &a.EpisodesListed)
		case "episodes":
			return true, w.runs(&a.Episodes)
		}
		return false, nil
	})
	switch {
	case err != nil:
		return err
	case a.AniDBID == 0:
		return errors.New("anidb_id is missing")
	case n < 2:
		return errors.New("the row carries nothing besides anidb_id")
	}
	return nil
}

// uniqueInts reads a non-empty list of unique ids in any order.
func (w *walker) uniqueInts(what string, dst *[]int) error {
	seen := map[int]bool{}
	n, err := w.array(what, func(i int) error {
		var v int
		if err := w.integer(fmt.Sprintf("%s[%d]", what, i), &v, 1); err != nil {
			return err
		}
		if seen[v] {
			return fmt.Errorf("%s repeats %d", what, v)
		}
		seen[v] = true
		*dst = append(*dst, v)
		return nil
	})
	if err == nil && n == 0 {
		err = fmt.Errorf("%s is empty", what)
	}
	return err
}

func (w *walker) imdb(what string, dst *[]string) error {
	seen := map[string]bool{}
	n, err := w.array(what, func(i int) error {
		var v string
		if err := w.str(fmt.Sprintf("%s[%d]", what, i), &v); err != nil {
			return err
		}
		if !imdbRE.MatchString(v) || seen[v] {
			return fmt.Errorf("%s[%d] %q is not a unique IMDb id", what, i, v)
		}
		seen[v] = true
		*dst = append(*dst, v)
		return nil
	})
	if err == nil && n == 0 {
		err = fmt.Errorf("%s is empty", what)
	}
	return err
}

func (w *walker) last(l *Last) error {
	n, err := w.object("last", func(key string) (bool, error) {
		p := l.field(Kind(key))
		if p == nil {
			return false, nil
		}
		return true, w.number("last."+key, p, 1)
	})
	if err == nil && n == 0 {
		err = errors.New("last is empty")
	}
	return err
}

func (w *walker) runs(dst *[]Run) error {
	n, err := w.array("episodes", func(i int) error {
		var r Run
		if err := w.run(fmt.Sprintf("episodes[%d]", i), &r); err != nil {
			return err
		}
		*dst = append(*dst, r)
		return nil
	})
	if err == nil && n == 0 {
		err = errors.New("episodes is empty")
	}
	return err
}

func (w *walker) run(what string, r *Run) error {
	_, err := w.object(what, func(key string) (bool, error) {
		switch key {
		case "kind":
			return true, w.kind(what+".kind", &r.Kind)
		case "start":
			return true, w.number(what+".start", &r.Start, 1)
		case "end":
			return true, w.number(what+".end", &r.End, 1)
		case "anidb_episode_ids":
			return true, w.episodeIDs(what+".anidb_episode_ids", &r.AniDBEpisodeIDs)
		case "tvdb":
			r.TVDB = &[]Target{}
			return true, w.targets(what+".tvdb", r.TVDB)
		case "tmdb":
			r.TMDB = &[]Target{}
			return true, w.targets(what+".tmdb", r.TMDB)
		}
		return false, nil
	})
	switch {
	case err != nil:
		return err
	case r.Kind == "" || r.Start == 0 || r.End == 0:
		return fmt.Errorf("%s needs kind, start and end", what)
	case r.Start > r.End:
		return fmt.Errorf("%s starts at %d after its end %d", what, r.Start, r.End)
	case r.AniDBEpisodeIDs != nil && len(r.AniDBEpisodeIDs) != r.End-r.Start+1:
		return fmt.Errorf("%s has %d AniDB episode ids for %d episodes", what, len(r.AniDBEpisodeIDs), r.End-r.Start+1)
	}
	return lastTargets(what, r)
}

// lastTargets bounds the target episode r's last episode lands on, which
// the lookups compute: each target's episode plus the run's span.
func lastTargets(what string, r *Run) error {
	for _, s := range []struct {
		ts   *[]Target
		site string
	}{{r.TVDB, "tvdb"}, {r.TMDB, "tmdb"}} {
		if s.ts == nil {
			continue
		}
		for _, t := range *s.ts {
			if last := t.Episode + r.End - r.Start; last > maxNumber {
				return fmt.Errorf("%s.%s lands episode %d on target episode %d, above %d", what, s.site, r.End, last, maxNumber)
			}
		}
	}
	return nil
}

// episodeIDs reads a list of AniDB episode ids, one per episode in number
// order, so not sorted; run checks its length.
func (w *walker) episodeIDs(what string, dst *[]int) error {
	out := []int{}
	_, err := w.array(what, func(i int) error {
		var v int
		if err := w.integer(fmt.Sprintf("%s[%d]", what, i), &v, 1); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	*dst = out
	return err
}

func (w *walker) targets(what string, dst *[]Target) error {
	_, err := w.array(what, func(i int) error {
		at := fmt.Sprintf("%s[%d]", what, i)
		t := Target{Season: -1}
		_, err := w.object(at, func(key string) (bool, error) {
			switch key {
			case "season":
				return true, w.number(at+".season", &t.Season, 0)
			case "episode":
				return true, w.number(at+".episode", &t.Episode, 1)
			case "default":
				return true, w.isTrue(at+".default", &t.Default)
			}
			return false, nil
		})
		switch {
		case err != nil:
			return err
		case t.Season < 0 || t.Episode == 0:
			return fmt.Errorf("%s needs season and episode", at)
		case i > 0 && !targetBefore((*dst)[i-1], t):
			return fmt.Errorf("%s is not after the target before it", at)
		}
		*dst = append(*dst, t)
		return nil
	})
	return err
}

// targetBefore orders targets by season, then episode, each pair once.
func targetBefore(a, b Target) bool {
	return a.Season < b.Season || a.Season == b.Season && a.Episode < b.Episode
}

func (w *walker) entry(e *Entry) error {
	_, err := w.object("entry", func(key string) (bool, error) {
		switch key {
		case "anilist_id":
			return true, w.integer(key, &e.AniListID, 1)
		case "type":
			return true, w.typ(key, &e.Type)
		case "episodes":
			return true, w.number(key, &e.Episodes, 1)
		case "ids":
			e.IDs = &IDs{}
			return true, w.serviceIDs(e.IDs)
		case "parts":
			return true, w.parts(&e.Parts)
		}
		return false, nil
	})
	if err == nil && e.AniListID == 0 && e.IDs == nil {
		err = errors.New("an entry needs anilist_id or ids")
	}
	return err
}

func (w *walker) serviceIDs(ids *IDs) error {
	ints := map[string]*[]int{
		"anidb": &ids.AniDB, "animecountdown": &ids.AnimeCountdown, "anisearch": &ids.AniSearch, "ann": &ids.ANN,
		"kitsu": &ids.Kitsu, "livechart": &ids.LiveChart, "mal": &ids.MAL, "simkl": &ids.Simkl,
	}
	keys := map[string]*[]string{"animeplanet": &ids.AnimePlanet, "notifymoe": &ids.NotifyMoe}
	n, err := w.object("ids", func(key string) (bool, error) {
		if p, ok := ints[key]; ok {
			return true, w.ids("ids."+key, p)
		}
		if p, ok := keys[key]; ok {
			return true, w.keys("ids."+key, p)
		}
		return false, nil
	})
	if err == nil && n == 0 {
		err = errors.New("ids is empty")
	}
	return err
}

func (w *walker) parts(dst *[]Part) error {
	n, err := w.array("parts", func(i int) error {
		at := fmt.Sprintf("parts[%d]", i)
		var p Part
		_, err := w.object(at, func(key string) (bool, error) {
			switch key {
			case "anidb_id":
				return true, w.integer(at+".anidb_id", &p.AniDBID, 1)
			case "kind":
				return true, w.kind(at+".kind", &p.Kind)
			case "start":
				return true, w.number(at+".start", &p.Start, 1)
			case "end":
				return true, w.number(at+".end", &p.End, 1)
			case "combined":
				return true, w.isTrue(at+".combined", &p.Combined)
			}
			return false, nil
		})
		if err == nil {
			err = checkPart(at, &p)
		}
		if err != nil {
			return err
		}
		*dst = append(*dst, p)
		return nil
	})
	if err == nil && n == 0 {
		err = errors.New("parts is empty")
	}
	return err
}

// checkPart holds one part to its own rules.
func checkPart(at string, p *Part) error {
	switch {
	case p.AniDBID == 0 || p.Kind == "":
		return fmt.Errorf("%s needs anidb_id and kind", at)
	case (p.Start == 0) != (p.End == 0):
		return fmt.Errorf("%s has one of start and end", at)
	case p.Start > p.End:
		return fmt.Errorf("%s starts at %d after its end %d", at, p.Start, p.End)
	case p.Combined && (p.Kind != Regular || p.Start != 0):
		return fmt.Errorf("%s is combined, so it is regular with no range", at)
	}
	return nil
}

// fields reads an object of required string members, each checked by its
// pattern when it has one, and returns the members it saw.
func (w *walker) fields(what string, strs map[string]*string, patterns map[string]*regexp.Regexp, extra func(key string) (bool, error)) (map[string]bool, error) {
	seen := map[string]bool{}
	_, err := w.object(what, func(key string) (bool, error) {
		p, ok := strs[key]
		if !ok {
			if extra != nil {
				known, err := extra(key)
				seen[key] = known
				return known, err
			}
			return false, nil
		}
		seen[key] = true
		if err := w.str(what+"."+key, p); err != nil {
			return true, err
		}
		if re := patterns[key]; re != nil && !re.MatchString(*p) {
			return true, fmt.Errorf("%s.%s %q is malformed", what, key, *p)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	for k := range strs {
		if !seen[k] {
			return nil, fmt.Errorf("%s.%s is missing", what, k)
		}
	}
	return seen, nil
}

// The members sources repeats.
const (
	keyRepository = "repository"
	keyCommit     = "commit"
	keySHA256     = "sha256"
)

func (w *walker) sources(s *Sources) error {
	seen := map[string]bool{}
	_, err := w.object("sources", func(key string) (bool, error) {
		var err error
		switch key {
		case "anime_offline_database":
			o := &s.AnimeOfflineDatabase
			_, err = w.fields("sources."+key, map[string]*string{keyRepository: &o.Repository, "release": &o.Release, "asset": &o.Asset, keySHA256: &o.SHA256},
				map[string]*regexp.Regexp{keySHA256: sha256RE}, nil)
		case "anime_lists":
			o := &s.AnimeLists
			_, err = w.fields("sources."+key, map[string]*string{keyRepository: &o.Repository, keyCommit: &o.Commit, "file": &o.File},
				map[string]*regexp.Regexp{keyCommit: commitRE}, nil)
		case "anidb_mirror":
			o := &s.AniDBMirror
			_, err = w.fields("sources."+key, map[string]*string{keyRepository: &o.Repository, keyCommit: &o.Commit},
				map[string]*regexp.Regexp{keyCommit: commitRE}, nil)
		case "overlay":
			err = w.overlay(&s.Overlay)
		default:
			return false, nil
		}
		seen[key] = true
		return true, err
	})
	if err != nil {
		return err
	}
	for _, k := range []string{"anime_offline_database", "anime_lists", "anidb_mirror", "overlay"} {
		if !seen[k] {
			return fmt.Errorf("sources.%s is missing", k)
		}
	}
	return nil
}

func (w *walker) overlay(o *OverlaySource) error {
	seen, err := w.fields("sources.overlay", map[string]*string{keySHA256: &o.SHA256}, map[string]*regexp.Regexp{keySHA256: sha256RE},
		func(key string) (bool, error) {
			switch key {
			case "entries":
				return true, w.integer("sources.overlay.entries", &o.Entries, 0)
			case "links":
				return true, w.integer("sources.overlay.links", &o.Links, 1)
			}
			return false, nil
		})
	if err == nil && !seen["entries"] {
		err = errors.New("sources.overlay.entries is missing")
	}
	return err
}

func (w *walker) attribution(a *Attribution) error {
	_, err := w.fields("attribution", map[string]*string{
		"license": &a.License, "license_url": &a.LicenseURL, "contents_license": &a.ContentsLicense,
		"contents_license_url": &a.ContentsLicenseURL, "notice": &a.Notice,
	}, nil, nil)
	switch {
	case err != nil:
		return err
	case a.License != "ODbL-1.0" || a.ContentsLicense != "DbCL-1.0":
		return fmt.Errorf("attribution licenses are %q and %q, want ODbL-1.0 and DbCL-1.0", a.License, a.ContentsLicense)
	}
	return nil
}
