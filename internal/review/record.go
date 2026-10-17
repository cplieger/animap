package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/strictjson"
)

// Dir is the records' directory, relative to the repository root.
const Dir = "reviewed"

// MaxFileBytes is the largest record file LoadDir reads and Encode writes.
// A part keeps a digest per finding, and a TVDB gap of 1,140 episodes
// (Chibi Maruko-chan, AniDB 1842) takes 112 KB.
const MaxFileBytes = 256 << 10

const maxRecords = 5000

// The verdicts a part holds. Fine: the mapping is right and the check
// misfired. Unmappable: no source carries the work. Inexpressible: the
// truth is known and the published format cannot say it; Requires names
// the format change it needs.
const (
	Fine          = "fine"
	Unmappable    = "unmappable"
	Inexpressible = "inexpressible"
)

var (
	errInvalid = errors.New("review: invalid record")
	hexRE      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	gapRE      = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
)

// Record is one reviewed/<file> acceptance record: a person's rulings on
// one subject, one part per section ruled on.
type Record struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Parts Parts  `json:"parts"`
}

// Parts are a record's rulings. There is no drift part: overlay capture
// acknowledges drift.
type Parts struct {
	DateTVDB *DatePart   `json:"date-tvdb,omitempty"`
	DateTMDB *DatePart   `json:"date-tmdb,omitempty"`
	Watch    []WatchPart `json:"watch,omitempty"`
}

// Ruling is what every part says: the verdict, the day it was made, why,
// and the pages that prove it.
type Ruling struct {
	Verdict  string   `json:"verdict"`
	Date     string   `json:"date"`
	Reason   string   `json:"reason"`
	Requires string   `json:"requires,omitempty"`
	Evidence []string `json:"evidence"`
}

// DatePart accepts a date check's findings on one side. Findings maps
// each accepted finding key to the digest of its inputs then.
type DatePart struct {
	Findings map[string]string `json:"findings"`
	Ruling
}

// WatchPart accepts one watched AniList record's gaps, while the published
// record's watched fields hash to RecordSHA256.
type WatchPart struct {
	RecordSHA256 string   `json:"record_sha256"`
	Gaps         []string `json:"gaps"`
	Ruling
	AniListID int `json:"anilist_id"`
}

// Date returns the record's part for a date section, nil for none.
func (r *Record) Date(section string) *DatePart {
	switch section {
	case DateTVDB:
		return r.Parts.DateTVDB
	case DateTMDB:
		return r.Parts.DateTMDB
	}
	return nil
}

// SetDate replaces the record's part for date section, which must be
// DateTVDB or DateTMDB.
func (r *Record) SetDate(section string, p *DatePart) {
	if section == DateTVDB {
		r.Parts.DateTVDB = p
	} else {
		r.Parts.DateTMDB = p
	}
}

// WatchFor returns the watch part for AniList id al, nil for none.
func (r *Record) WatchFor(al int) *WatchPart {
	i := slices.IndexFunc(r.Parts.Watch, func(w WatchPart) bool { return w.AniListID == al })
	if i < 0 {
		return nil
	}
	return &r.Parts.Watch[i]
}

// SetWatch adds or replaces the watch part for w.AniListID, keeping the
// list sorted.
func (r *Record) SetWatch(w *WatchPart) {
	if cur := r.WatchFor(w.AniListID); cur != nil {
		*cur = *w
		return
	}
	r.Parts.Watch = append(r.Parts.Watch, *w)
	slices.SortFunc(r.Parts.Watch, func(a, b WatchPart) int { return a.AniListID - b.AniListID })
}

// Changes lists why p no longer covers a run's findings, nil when it
// does: "finding-inputs:<key>" for a listed finding whose digest moved,
// "finding:<key>" for one p does not list. A finding that cleared changes
// nothing.
func (p *DatePart) Changes(now map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(now)) {
		was, ok := p.Findings[k]
		switch {
		case !ok:
			out = append(out, "finding:"+k)
		case was != now[k]:
			out = append(out, "finding-inputs:"+k)
		}
	}
	return out
}

// Changes lists why p no longer covers a record with watched-field digest
// sha and gaps, nil when it does: "record" when the digest moved,
// "gap:<gap>" for each gap p does not list.
func (p *WatchPart) Changes(sha string, gaps []string) []string {
	var out []string
	if sha != p.RecordSHA256 {
		out = append(out, "record")
	}
	for _, g := range gaps {
		if !slices.Contains(p.Gaps, g) {
			out = append(out, "gap:"+g)
		}
	}
	return out
}

// LoadDir reads and validates every <dir>/*.json record, by key. A missing
// directory holds no record.
func LoadDir(dir string) (map[string]Record, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) > maxRecords {
		return nil, fmt.Errorf("%w: %d files, at most %d", errInvalid, len(paths), maxRecords)
	}
	out := make(map[string]Record, len(paths))
	for _, p := range paths {
		r, err := Read(p)
		if err != nil {
			return nil, err
		}
		out[r.Key] = r
	}
	return out, nil
}

// Read decodes one record strictly, checks that the file is named for its
// key, and validates it. The name check makes each key appear once.
func Read(path string) (Record, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Record{}, err
	}
	if st.Size() > MaxFileBytes {
		return Record{}, fmt.Errorf("%w: %s is %d bytes", errInvalid, path, st.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := strictjson.Decode(body, MaxFileBytes, &r); err != nil {
		return Record{}, fmt.Errorf("%w: %s: %w", errInvalid, path, err)
	}
	if want := File(r.Key); want == "" || filepath.Base(path) != want {
		return Record{}, fmt.Errorf("%w: %s holds key %q and must be named for it", errInvalid, path, r.Key)
	}
	if err := r.Validate(); err != nil {
		return Record{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// Encode is r as its record file holds it. It refuses a record Validate
// refuses or LoadDir could not read back.
func (r *Record) Encode() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if len(b) > MaxFileBytes {
		return nil, fmt.Errorf("%w: %s: the record is %d bytes, at most %d", errInvalid, r.Key, len(b), MaxFileBytes)
	}
	return b, nil
}

// Validate checks every field and every verdict and part combination.
func (r *Record) Validate() error {
	s, ok := ParseKey(r.Key)
	if !ok {
		return fmt.Errorf("%w: key %q is not a subject key", errInvalid, r.Key)
	}
	if err := r.validate(s); err != nil {
		return fmt.Errorf("%w: %s: %w", errInvalid, r.Key, err)
	}
	return nil
}

func (r *Record) validate(s Subject) error {
	p := r.Parts
	switch {
	case strings.TrimSpace(r.Title) == "":
		return errors.New("title is required")
	case p.DateTVDB == nil && p.DateTMDB == nil && len(p.Watch) == 0:
		return errors.New("parts is empty")
	case !s.AniDB() && (p.DateTVDB != nil || p.DateTMDB != nil):
		return errors.New("an AniList subject has no date checks")
	}
	for _, name := range []string{DateTVDB, DateTMDB} {
		if d := r.Date(name); d != nil {
			if err := d.validate(); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return validateWatch(p.Watch, s)
}

func validateWatch(parts []WatchPart, s Subject) error {
	for i := range parts {
		w := &parts[i]
		if err := w.validate(s); err != nil {
			return fmt.Errorf("watch AniList %d: %w", w.AniListID, err)
		}
		if i > 0 && w.AniListID <= parts[i-1].AniListID {
			return errors.New("watch parts are not sorted by AniList id, each once")
		}
	}
	return nil
}

func (p *DatePart) validate() error {
	if p.Verdict != Fine && p.Verdict != Inexpressible {
		return fmt.Errorf("verdict %q is neither %q nor %q", p.Verdict, Fine, Inexpressible)
	}
	if err := p.Ruling.validate(); err != nil {
		return err
	}
	if len(p.Findings) == 0 {
		return errors.New("findings is empty")
	}
	for k, h := range p.Findings {
		switch {
		case !datecheck.ValidFindingKey(k):
			return fmt.Errorf("finding %q is not a date-check finding key", k)
		case !hexRE.MatchString(h):
			return fmt.Errorf("finding %q has digest %q, not 64 lower-case hex", k, h)
		case k == string(datecheck.DeadSeries):
			return fmt.Errorf("%s cannot be accepted: the provider no longer serves the series, so the node moves", k)
		case p.Verdict == Fine && strings.HasPrefix(k, string(datecheck.DeadMovie)+":"):
			return fmt.Errorf("%s cannot be fine: the provider no longer serves it", k)
		}
	}
	return nil
}

func (p *WatchPart) validate(s Subject) error {
	switch {
	case p.AniListID <= 0:
		return errors.New("anilist_id must be positive")
	case !s.AniDB() && p.AniListID != s.ID:
		return fmt.Errorf("an AniList subject holds only its own record, not AniList %d", p.AniListID)
	case p.Verdict != Unmappable && p.Verdict != Inexpressible:
		return fmt.Errorf("verdict %q is neither %q nor %q", p.Verdict, Unmappable, Inexpressible)
	case !hexRE.MatchString(p.RecordSHA256):
		return errors.New("record_sha256 is not 64 lower-case hex")
	case len(p.Gaps) == 0:
		return errors.New("gaps is empty")
	}
	for i, g := range p.Gaps {
		if !gapRE.MatchString(g) || (i > 0 && g <= p.Gaps[i-1]) {
			return errors.New("gaps are not sorted gap names, each once")
		}
	}
	return p.Ruling.validate()
}

func (r *Ruling) validate() error {
	if _, err := time.Parse(time.DateOnly, r.Date); err != nil {
		return fmt.Errorf("date %q is not YYYY-MM-DD", r.Date)
	}
	switch {
	case strings.TrimSpace(r.Reason) == "":
		return errors.New("reason is required")
	case r.Verdict == Inexpressible && strings.TrimSpace(r.Requires) == "":
		return errors.New("an inexpressible verdict names the format change it requires")
	case r.Verdict != Inexpressible && r.Requires != "":
		return errors.New("requires belongs to an inexpressible verdict only")
	case len(r.Evidence) == 0:
		return errors.New("evidence is required")
	}
	for _, link := range r.Evidence {
		if !overlay.EvidenceLink(link) {
			return fmt.Errorf("evidence %q is not an https link to an allowed host", link)
		}
	}
	return nil
}
