// Package watch checks a list of AniList ids, a watch set, for mapping
// completeness against a published document: whether seadex-scout can
// route each record to Sonarr or Radarr and scope its episodes there.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/strictjson"
)

// Gap is one way a watched record falls short of fully mapped.
type Gap string

// The values are stored in acceptance records' watch parts, so a rename
// changes their file format.
const (
	noRecord               Gap = "no_record"
	noAniDB                Gap = "no_anidb"
	unknownType            Gap = "unknown_type"
	noTVDB                 Gap = "no_tvdb"
	noTVDBSeason           Gap = "no_tvdb_season"
	season0Unresolved      Gap = "season0_unresolved"
	episodeCountUnknown    Gap = "episode_count_unknown"
	movieNoRoute           Gap = "movie_no_route"
	movieSpecialUnresolved Gap = "movie_special_unresolved"
	landsNowhere           Gap = "lands_nowhere"
)

// gaps returns what rec lacks, nil when it is fully mapped, as
// seadex-scout reads a record (internal/mapping RoutedIDs, internal/match
// FindByID, internal/align RecordSeason and scope): route to an arr, then
// scope the episodes there. A nil rec is a watched id with no record.
func gaps(rec *schema.Record) []Gap {
	if rec == nil {
		return []Gap{noRecord}
	}
	movie := rec.Type == "MOVIE"
	// FindByID tries a MOVIE's TMDB and IMDb ids before its TVDB id, and
	// any other type's TMDB movie ids only when it has no TVDB id.
	if movie && (len(rec.TMDBMovieIDs) > 0 || len(rec.IMDbIDs) > 0) {
		return nil
	}
	if rec.TVDBID > 0 {
		return sonarrScope(rec, movie)
	}
	switch {
	case !movie && len(rec.TMDBMovieIDs) > 0:
		return nil
	// A record with no AniDB id carries a TVDB id only from an overlay link.
	case rec.AniDBID == 0 && rec.AniDBParent == nil:
		return []Gap{noAniDB}
	case movie:
		return []Gap{movieNoRoute}
	case rec.Type == "" || rec.Type == "UNKNOWN":
		return []Gap{unknownType}
	}
	return []Gap{noTVDB}
}

// sonarrScope judges a record Sonarr matches by TVDB id: a positive season
// or absolute numbering scopes it, unless the published placement lands
// every episode nowhere, when scope falls back to the whole season; a
// season 0 record needs a placement that lands somewhere and only on
// specials.
func sonarrScope(rec *schema.Record, movie bool) []Gap {
	switch {
	case rec.TVDBSeason != nil && *rec.TVDBSeason == 0:
		return season0Gaps(rec, movie)
	case rec.TVDBAbsolute || rec.TVDBSeason != nil:
		if len(rec.TVDBPlacement) > 0 && !slices.ContainsFunc(rec.TVDBPlacement, lands) {
			return []Gap{landsNowhere}
		}
		return nil
	}
	return []Gap{noTVDBSeason}
}

func season0Gaps(rec *schema.Record, movie bool) []Gap {
	switch {
	case len(rec.TVDBPlacement) > 0 && slices.ContainsFunc(rec.TVDBPlacement, onRealSeason):
	case len(rec.TVDBPlacement) > 0 && !slices.ContainsFunc(rec.TVDBPlacement, lands):
		return []Gap{landsNowhere}
	case len(rec.TVDBPlacement) > 0:
		return nil
	case rec.Episodes == 0:
		return []Gap{episodeCountUnknown}
	}
	if movie {
		return []Gap{movieSpecialUnresolved}
	}
	return []Gap{season0Unresolved}
}

func lands(s schema.Segment) bool { return s.Season != nil }

func onRealSeason(s schema.Segment) bool { return s.Season != nil && *s.Season != 0 }

// Entry is one watched id with its gaps. RecordSHA256 is the published
// record's review.RecordDigest, the fingerprint a watch ruling keeps.
type Entry struct {
	Type         string `json:"type,omitempty"`
	RecordSHA256 string `json:"record_sha256"`
	Gaps         []Gap  `json:"gaps"`
	AniListID    int    `json:"anilist_id"`
	AniDBID      int    `json:"anidb_id,omitempty"`
	TVDBID       int    `json:"tvdb_id,omitempty"`
}

// GapNames are e's gaps as strings, as an acceptance part lists them.
func (e *Entry) GapNames() []string {
	out := make([]string, 0, len(e.Gaps))
	for _, g := range e.Gaps {
		out = append(out, string(g))
	}
	return out
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
		if g := gaps(rec); len(g) > 0 {
			e := Entry{AniListID: id, Gaps: g, RecordSHA256: review.RecordDigest(rec)}
			if rec != nil {
				e.Type, e.AniDBID, e.TVDBID = rec.Type, rec.AniDBID, rec.TVDBID
			}
			out = append(out, e)
		}
	}
	return out
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
	var c Config
	if err = strictjson.Decode(body, &c); err != nil {
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

// errIncomplete reports a listing that cannot be trusted as the whole set.
var errIncomplete = errors.New("watch: listing is incomplete")

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
			return nil, fmt.Errorf("%w: totalItems moved from %d to %d", errIncomplete, total, p.TotalItems)
		}
		total = p.TotalItems
		for _, id := range p.ids {
			if seen[id] {
				return nil, fmt.Errorf("%w: id %d repeats", errIncomplete, id)
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if len(p.ids) == 0 || page >= p.TotalPages {
			break
		}
	}
	if len(ids) != total {
		return nil, fmt.Errorf("%w: collected %d of %d", errIncomplete, len(ids), total)
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
		return nil, fmt.Errorf("%w: more than %d pages", errIncomplete, c.MaxPages)
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
		return nil, fmt.Errorf("%w: page %d: %w", errIncomplete, page, err)
	}
	p := raw.listPage
	for _, it := range raw.Items {
		var id int
		if err := json.Unmarshal(it[c.IDField], &id); err != nil || id <= 0 {
			return nil, fmt.Errorf("%w: page %d has a non-positive or missing %s", errIncomplete, page, c.IDField)
		}
		p.ids = append(p.ids, id)
	}
	return &p, nil
}
