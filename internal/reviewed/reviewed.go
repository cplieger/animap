// Package reviewed holds the date-check leads a person proved false: one
// JSON file per node, with the findings it was proven against and the
// fingerprints of what the check read, so the lead stays closed until one
// of them changes.
package reviewed

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/strictjson"
)

// Dir is the records' directory, relative to the repository root.
const Dir = "reviewed"

// Verdict is the only verdict a record holds: the mapping is right and
// the check misfired.
const Verdict = "fine"

// MaxFileBytes is the largest record file LoadDir reads and Encode writes.
const MaxFileBytes = 64 << 10

const maxRecords = 5000

var errInvalid = errors.New("reviewed: invalid record")

var hexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Record is one reviewed/<anidb_id>.json file. Findings are the lead's
// finding keys as datecheck.FindingKeys writes them.
type Record struct {
	Captured Captured `json:"captured"`
	Title    string   `json:"title"`
	Verdict  string   `json:"verdict"`
	Reason   string   `json:"reason"`
	Findings []string `json:"findings"`
	Evidence []string `json:"evidence"`
	AniDBID  int      `json:"anidb_id"`
}

// Captured is the date of the review and the lead's fingerprints then.
type Captured struct {
	Date string `json:"date"`
	datecheck.Fingerprints
}

// Cause is one reason a record no longer covers its lead.
type Cause string

// The causes, in the order Changes reports them.
const (
	Node       Cause = "node"
	TVDB       Cause = "tvdb"
	AniDB      Cause = "anidb"
	NewFinding Cause = "finding"
)

// Path is the record file of AniDB id aid, relative to the repository root.
func Path(aid int) string { return Dir + "/" + strconv.Itoa(aid) + ".json" }

// LoadDir reads and validates every <dir>/*.json record, by AniDB id. A
// missing directory holds no record.
func LoadDir(dir string) (map[int]Record, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) > maxRecords {
		return nil, fmt.Errorf("%w: %d files, at most %d", errInvalid, len(paths), maxRecords)
	}
	out := make(map[int]Record, len(paths))
	for _, p := range paths {
		r, err := read(p)
		if err != nil {
			return nil, err
		}
		out[r.AniDBID] = r
	}
	return out, nil
}

// read decodes one record strictly, checks that the file is named for its
// AniDB id, and validates it. The name check makes each id appear once.
func read(path string) (Record, error) {
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
	if err := strictjson.Decode(body, &r); err != nil {
		return Record{}, fmt.Errorf("%w: %s: %w", errInvalid, path, err)
	}
	if want := strconv.Itoa(r.AniDBID) + ".json"; filepath.Base(path) != want {
		return Record{}, fmt.Errorf("%w: %s must be named %s", errInvalid, path, want)
	}
	if err := r.validate(); err != nil {
		return Record{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// Encode is r as its record file holds it. It refuses a record Validate
// refuses or LoadDir could not read back.
func (r *Record) Encode() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if len(b) > MaxFileBytes {
		return nil, fmt.Errorf("%w: AniDB %d: the record is %d bytes, at most %d", errInvalid, r.AniDBID, len(b), MaxFileBytes)
	}
	return b, nil
}

// validate checks every field.
func (r *Record) validate() error {
	if r.AniDBID <= 0 {
		return fmt.Errorf("%w: anidb_id must be positive", errInvalid)
	}
	for _, check := range []func() error{r.validateText, r.validateFindings, r.validateCaptured} {
		if err := check(); err != nil {
			return fmt.Errorf("%w: AniDB %d: %w", errInvalid, r.AniDBID, err)
		}
	}
	return nil
}

func (r *Record) validateText() error {
	switch {
	case strings.TrimSpace(r.Title) == "":
		return errors.New("title is required")
	case r.Verdict != Verdict:
		return fmt.Errorf("verdict %q is not %q", r.Verdict, Verdict)
	case strings.TrimSpace(r.Reason) == "":
		return errors.New("reason is required")
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

func (r *Record) validateFindings() error {
	if len(r.Findings) == 0 {
		return errors.New("findings is empty")
	}
	for i, k := range r.Findings {
		if !datecheck.ValidFindingKey(k) {
			return fmt.Errorf("finding %q is not a date-check finding key", k)
		}
		if i > 0 && k <= r.Findings[i-1] {
			return errors.New("findings are not sorted, each once")
		}
	}
	return nil
}

func (r *Record) validateCaptured() error {
	c := r.Captured
	if _, err := time.Parse(time.DateOnly, c.Date); err != nil {
		return fmt.Errorf("captured.date %q is not YYYY-MM-DD", c.Date)
	}
	for name, h := range map[string]string{"node_sha256": c.Node, "tvdb_sha256": c.TVDB, "anidb_sha256": c.AniDB} {
		if !hexRE.MatchString(h) {
			return fmt.Errorf("captured.%s is not 64 lower-case hex", name)
		}
	}
	return nil
}

// Changes lists why r no longer covers a lead with fingerprints fp and
// finding keys findings, nil when it still does: a fingerprint moved, or a
// finding r does not list appeared. A finding that cleared changes nothing.
func (r *Record) Changes(fp datecheck.Fingerprints, findings []string) []Cause {
	var out []Cause
	for _, c := range []struct {
		cause    Cause
		was, now string
	}{{Node, r.Captured.Node, fp.Node}, {TVDB, r.Captured.TVDB, fp.TVDB}, {AniDB, r.Captured.AniDB, fp.AniDB}} {
		if c.was != c.now {
			out = append(out, c.cause)
		}
	}
	if slices.ContainsFunc(findings, func(k string) bool { return !slices.Contains(r.Findings, k) }) {
		out = append(out, NewFinding)
	}
	return out
}
