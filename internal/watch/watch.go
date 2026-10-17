// Package watch checks a list of AniList ids, a watch set, for mapping
// completeness against a published document: whether seadex-scout can
// route each entry to Sonarr or Radarr and scope its episodes there.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/strictjson"
	"github.com/cplieger/animap/mapread"
)

// Gap is one way a watched entry falls short of fully mapped.
type Gap string

// The values are stored in acceptance records' watch parts, so a rename
// changes their file format.
const (
	noRecord               Gap = "no_record"
	noAniDB                Gap = "no_anidb"
	unknownType            Gap = "unknown_type"
	noTVDB                 Gap = "no_tvdb"
	season0Unresolved      Gap = "season0_unresolved"
	episodeCountUnknown    Gap = "episode_count_unknown"
	movieNoRoute           Gap = "movie_no_route"
	movieSpecialUnresolved Gap = "movie_special_unresolved"
	landsNowhere           Gap = "lands_nowhere"
	severalTVDBSeries      Gap = "several_tvdb_series"
)

// gaps returns what e lacks, nil when it is fully mapped, as seadex-scout
// reads an entry (internal/mapping RoutedIDs, internal/match FindByID,
// internal/align RecordSeason and scope): route to an arr, then scope the
// episodes there. A nil e is a watched id with no entry.
func gaps(x *mapread.Index, e *mapread.Entry) []Gap {
	if e == nil {
		return []Gap{noRecord}
	}
	movie := e.Type == "MOVIE"
	r := x.Routes(*e)
	// FindByID tries a MOVIE's TMDB and IMDb ids before its TVDB id, and
	// any other type's TMDB movie ids only when it has no TVDB id. Its
	// record carries a TVDB id only for an entry on exactly one series.
	if movie && (len(r.TMDBMovies) > 0 || len(r.IMDb) > 0) {
		return nil
	}
	if len(r.TVDB) == 1 {
		return sonarrScope(x, e, movie)
	}
	switch {
	case !movie && len(r.TMDBMovies) > 0:
		return nil
	case len(r.TVDB) > 1:
		return []Gap{severalTVDBSeries}
	case len(e.Parts) == 0:
		return []Gap{noAniDB}
	case movie:
		return []Gap{movieNoRoute}
	case e.Type == "" || e.Type == "UNKNOWN":
		return []Gap{unknownType}
	}
	return []Gap{noTVDB}
}

// sonarrScope judges an entry Sonarr matches by TVDB id. A landing on a
// positive season scopes it, and so does an episode with nothing stated,
// which seadex-scout compares on the whole series; an entry landing only
// on season 0 needs every episode stated there.
func sonarrScope(x *mapread.Index, e *mapread.Entry, movie bool) []Gap {
	t := tallyTVDB(x, e)
	switch {
	case t.all == 0:
		return []Gap{episodeCountUnknown}
	case t.nowhere == t.all:
		return []Gap{landsNowhere}
	case t.positive || !t.season0:
		return nil
	case !t.defaults && t.stated == t.all:
		return nil
	case movie:
		return []Gap{movieSpecialUnresolved}
	}
	return []Gap{season0Unresolved}
}

// tvdbTally counts an entry's episodes by their mapread.Index.OnTVDB
// reading, each entry episode once: it lands when it lands on any season;
// it has nothing stated when it lands on none and is unstated on a series
// or has an AniDB episode on none; else it lands nowhere. stated counts
// the episodes that land or land nowhere, and defaults whether any season
// basis rests on a default.
type tvdbTally struct {
	all, stated, nowhere        int
	positive, season0, defaults bool
}

func tallyTVDB(x *mapread.Index, e *mapread.Entry) tvdbTally {
	var t tvdbTally
	for _, ep := range x.Episodes(*e) {
		t.add(x.OnTVDB(ep))
	}
	return t
}

// add counts one entry episode from its TVDB reading.
func (t *tvdbTally) add(r mapread.TVDBReading) {
	t.all++
	lands, unstated := false, r.Off
	for _, sr := range r.Series {
		unstated = unstated || sr.Unstated
		for _, b := range sr.Seasons {
			lands = true
			t.positive = t.positive || b.Season > 0
			t.season0 = t.season0 || b.Season == 0
			t.defaults = t.defaults || b.Default
		}
	}
	switch {
	case !lands && unstated:
		return
	case !lands:
		t.nowhere++
	}
	t.stated++
}

// Entry is one watched id with its gaps. RecordSHA256 is the published
// entry's review.RecordDigest, the fingerprint a watch ruling keeps;
// AniDBID is the anime the entry is, TVDBID the one series it reaches.
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
func Evaluate(ids []int, x *mapread.Index) []Entry {
	var out []Entry
	for _, id := range slices.Sorted(slices.Values(ids)) {
		var e *mapread.Entry
		if found, ok := x.Entry(id); ok {
			e = &found
		}
		g := gaps(x, e)
		if len(g) == 0 {
			continue
		}
		w := Entry{AniListID: id, Gaps: g, RecordSHA256: review.RecordDigest(x, e)}
		if e != nil {
			w.Type = e.Type
			if p := e.Parts; len(p) == 1 && p[0].Identity() {
				w.AniDBID = p[0].AniDBID
			}
			if r := x.Routes(*e); len(r.TVDB) == 1 {
				w.TVDBID = r.TVDB[0]
			}
		}
		out = append(out, w)
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

// maxConfigBytes bounds a watch-set config, a few hundred bytes.
const maxConfigBytes = 64 << 10

// LoadConfig reads and checks a watch-set config.
func LoadConfig(path string) (*Config, error) {
	body, err := strictjson.ReadFile(path, maxConfigBytes)
	if err != nil {
		return nil, err
	}
	var c Config
	if err = strictjson.Decode(body, maxConfigBytes, &c); err != nil {
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
	if err := strictjson.Unmarshal(body, maxPageBytes, &raw); err != nil {
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
