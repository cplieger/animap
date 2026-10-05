// Package watch checks a list of AniList ids, a watch set, for mapping
// completeness against a published document, and decides which gaps are
// new against a tracked backlog.
package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/schema"
)

// Gap is one way a watched record falls short of fully mapped.
type Gap string

// The values are stored in the tracked checks/unmappable.json and
// watch/backlog.json, so a rename changes their file format.
const (
	NoRecord               Gap = "no_record"
	NoAniDB                Gap = "no_anidb"
	UnknownType            Gap = "unknown_type"
	NoTVDB                 Gap = "no_tvdb"
	NoTVDBSeason           Gap = "no_tvdb_season"
	Season0Unresolved      Gap = "season0_unresolved"
	EpisodeCountUnknown    Gap = "episode_count_unknown"
	MovieNoRoute           Gap = "movie_no_route"
	MovieSpecialUnresolved Gap = "movie_special_unresolved"
)

// Gaps returns what rec lacks, nil when it is fully mapped. A nil rec is a
// watched id with no record at all.
func Gaps(rec *schema.Record) []Gap {
	switch {
	case rec == nil:
		return []Gap{NoRecord}
	case rec.AniDBID == 0 && rec.AniDBParent == nil:
		return []Gap{NoAniDB}
	case rec.Type == "MOVIE":
		return movieGaps(rec)
	case rec.TVDBID == 0 && (rec.Type == "" || rec.Type == "UNKNOWN"):
		return []Gap{UnknownType}
	case rec.TVDBID == 0:
		return []Gap{NoTVDB}
	case rec.TVDBSeason != nil && *rec.TVDBSeason == 0:
		return season0Gaps(rec, Season0Unresolved)
	case rec.TVDBAbsolute || (rec.TVDBSeason != nil && *rec.TVDBSeason >= 1):
		return nil
	default:
		return []Gap{NoTVDBSeason}
	}
}

func movieGaps(rec *schema.Record) []Gap {
	radarr := len(rec.TMDBMovieIDs) > 0 || len(rec.IMDbIDs) > 0
	var out []Gap
	sonarr := false
	if rec.TVDBID > 0 {
		switch {
		case rec.TVDBAbsolute || (rec.TVDBSeason != nil && *rec.TVDBSeason >= 1):
			sonarr = true
		case rec.TVDBSeason != nil && *rec.TVDBSeason == 0:
			out = season0Gaps(rec, MovieSpecialUnresolved)
			sonarr = len(out) == 0
		}
	}
	if !radarr && !sonarr {
		out = append(out, MovieNoRoute)
	}
	return out
}

// season0Gaps checks that every AniDB episode 1..episodes of a record filed
// under season 0 resolves to a TVDB season-0 episode: a row names it (a
// "-0" pair counts as resolved), or an explicit offset places the run. An
// absent offset is not "the numbering matches": it is ambiguous.
func season0Gaps(rec *schema.Record, unresolved Gap) []Gap {
	if rec.TVDBEpisodeOffset != nil {
		return nil
	}
	n := rec.Episodes
	if rec.Type == "MOVIE" && n == 0 {
		n = 1
	}
	if n == 0 {
		return []Gap{EpisodeCountUnknown}
	}
	for k := 1; k <= n; k++ {
		if !season0Resolved(rec.MappingList, k) {
			return []Gap{unresolved}
		}
	}
	return nil
}

func season0Resolved(rows []schema.Row, k int) bool {
	for _, r := range rows {
		if r.AniDBSeason != 1 || r.TVDBSeason == nil || *r.TVDBSeason != 0 {
			continue
		}
		for _, p := range r.Episodes {
			if len(p) > 0 && p[0] == k {
				return true
			}
		}
		if r.Start > 0 && r.Start <= k && (r.End == 0 || k <= r.End) {
			return true
		}
	}
	return false
}

// Entry is one watched id with its gaps.
type Entry struct {
	Type      string `json:"type,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Gaps      []Gap  `json:"gaps"`
	AniListID int    `json:"anilist_id"`
	AniDBID   int    `json:"anidb_id,omitempty"`
	TVDBID    int    `json:"tvdb_id,omitempty"`
}

// Evaluate returns every watched id that is not fully mapped, by id.
func Evaluate(ids []int, doc *schema.Document) []Entry {
	byAniList := make(map[int]*schema.Record, len(doc.Records))
	for i := range doc.Records {
		byAniList[doc.Records[i].AniListID] = &doc.Records[i]
	}
	var out []Entry
	for _, id := range slices.Sorted(slices.Values(ids)) {
		rec := byAniList[id]
		if g := Gaps(rec); len(g) > 0 {
			e := Entry{AniListID: id, Gaps: g}
			if rec != nil {
				e.Type, e.AniDBID, e.TVDBID = rec.Type, rec.AniDBID, rec.TVDBID
			}
			out = append(out, e)
		}
	}
	return out
}

// Backlog is a tracked list of gaps that get no per-entry issue: the
// backlog proper, or the unmappable list, whose entries carry a Reason.
type Backlog struct {
	WatchSet   string  `json:"watch_set"`
	CapturedAt string  `json:"captured_at"`
	Entries    []Entry `json:"entries"`
	Version    int     `json:"version"`
}

// Classification splits a run's gaps against the backlog and the
// unmappable list. Unmappable holds the listed entries still not fully
// mapped; Cleared holds the listed ones that now are.
type Classification struct {
	New        []Entry `json:"new"`
	Known      []Entry `json:"known"`
	Resolved   []Entry `json:"resolved"`
	Unmappable []Entry `json:"unmappable"`
	Cleared    []Entry `json:"cleared"`
}

// Classify splits gaps against the backlog and the unmappable list. An
// entry is new when neither lists its id or it now has a gap kind its list
// did not record. A listed entry with no gap this run is resolved
// (backlog) or cleared (unmappable).
func Classify(gaps []Entry, backlog, unmappable *Backlog) Classification {
	inBacklog, inUnmappable := byID(backlog), byID(unmappable)
	var c Classification
	now := map[int]bool{}
	for i := range gaps {
		e := &gaps[i]
		now[e.AniListID] = true
		switch {
		case covers(inUnmappable, e):
			c.Unmappable = append(c.Unmappable, *inUnmappable[e.AniListID])
		case covers(inBacklog, e):
			c.Known = append(c.Known, *e)
		default:
			c.New = append(c.New, *e)
		}
	}
	c.Resolved = gone(backlog, now)
	c.Cleared = gone(unmappable, now)
	return c
}

func byID(b *Backlog) map[int]*Entry {
	m := map[int]*Entry{}
	if b != nil {
		for i := range b.Entries {
			m[b.Entries[i].AniListID] = &b.Entries[i]
		}
	}
	return m
}

func gone(b *Backlog, now map[int]bool) []Entry {
	if b == nil {
		return nil
	}
	var out []Entry
	for i := range b.Entries {
		if !now[b.Entries[i].AniListID] {
			out = append(out, b.Entries[i])
		}
	}
	return out
}

func covers(list map[int]*Entry, e *Entry) bool {
	known, ok := list[e.AniListID]
	return ok && subset(e.Gaps, known.Gaps)
}

func subset(a, b []Gap) bool {
	for _, g := range a {
		if !slices.Contains(b, g) {
			return false
		}
	}
	return true
}

// LoadBacklog reads a backlog file strictly.
func LoadBacklog(path string) (*Backlog, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var b Backlog
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("watch: backlog %s: %w", path, err)
	}
	if b.Version != 1 {
		return nil, fmt.Errorf("watch: backlog %s: version %d, want 1", path, b.Version)
	}
	return &b, nil
}

// Config is one watch set's source.
type Config struct {
	Name     string `json:"name"`
	ListURL  string `json:"list_url"`
	IDField  string `json:"id_field"`
	EntryURL string `json:"entry_url"`
	PerPage  int    `json:"per_page"`
	MaxPages int    `json:"max_pages"`
}

// LoadConfig reads and checks a watch-set config.
func LoadConfig(path string) (*Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var c Config
	if err = dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("watch: config %s: %w", path, err)
	}
	u, err := url.Parse(c.ListURL)
	if err != nil || u.Scheme != "https" || c.IDField == "" || c.PerPage < 1 || c.PerPage > 500 || c.MaxPages < 1 || c.MaxPages > 100 {
		return nil, fmt.Errorf("watch: config %s is incomplete or out of range", path)
	}
	return &c, nil
}

// EntryLink is the watch set's page for one id, from a Config.EntryURL
// template.
func EntryLink(template string, id int) string {
	return strings.ReplaceAll(template, "{id}", strconv.Itoa(id))
}

// Getter is the fetch the pager needs; source.Client satisfies it.
type Getter interface {
	Get(ctx context.Context, url string, maxBytes int64) ([]byte, error)
}

// ErrIncomplete reports a listing that cannot be trusted as the whole set.
var ErrIncomplete = errors.New("watch: listing is incomplete")

const maxPageBytes = 4 << 20

// FetchIDs pages through a PocketBase-style listing. It succeeds only when
// the ids it collected equal totalItems, none repeats and all are positive;
// anything else means the run evaluates nothing.
func FetchIDs(ctx context.Context, g Getter, c *Config) ([]int, error) {
	seen := map[int]bool{}
	var ids []int
	total := -1
	for page := 1; ; page++ {
		p, err := fetchPage(ctx, g, c, page)
		if err != nil {
			return nil, err
		}
		if total >= 0 && p.TotalItems != total {
			return nil, fmt.Errorf("%w: totalItems moved from %d to %d", ErrIncomplete, total, p.TotalItems)
		}
		total = p.TotalItems
		for _, id := range p.ids {
			if seen[id] {
				return nil, fmt.Errorf("%w: id %d repeats", ErrIncomplete, id)
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if len(p.ids) == 0 || page >= p.TotalPages {
			break
		}
	}
	if len(ids) != total {
		return nil, fmt.Errorf("%w: collected %d of %d", ErrIncomplete, len(ids), total)
	}
	return ids, nil
}

type listPage struct {
	ids        []int
	TotalItems int `json:"totalItems"`
	TotalPages int `json:"totalPages"`
}

func fetchPage(ctx context.Context, g Getter, c *Config, page int) (*listPage, error) {
	if page > c.MaxPages {
		return nil, fmt.Errorf("%w: more than %d pages", ErrIncomplete, c.MaxPages)
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "perPage": {strconv.Itoa(c.PerPage)}, "fields": {c.IDField}}
	body, err := g.Get(ctx, c.ListURL+"?"+q.Encode(), maxPageBytes)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
		listPage
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: page %d: %w", ErrIncomplete, page, err)
	}
	p := raw.listPage
	for _, it := range raw.Items {
		var id int
		if err := json.Unmarshal(it[c.IDField], &id); err != nil || id <= 0 {
			return nil, fmt.Errorf("%w: page %d has a non-positive or missing %s", ErrIncomplete, page, c.IDField)
		}
		p.ids = append(p.ids, id)
	}
	return &p, nil
}
