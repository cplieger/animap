package mapread

import "slices"

// Version is the major version of the format this package reads and writes.
// It is raised only for a breaking change; a member added within a version
// is additive and Decode ignores members it does not know. A document in
// the earlier shape also says version 2; Decode refuses it by its sources.
const Version = 2

// Document is a whole animap.json.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Document struct {
	Version     int         `json:"version"`
	GeneratedAt string      `json:"generated_at"`
	Sources     Sources     `json:"sources"`
	Attribution Attribution `json:"attribution"`
	Anime       []Anime     `json:"anime"`
	Entries     []Entry     `json:"entries"`
}

// Sources names what the document was built from.
type Sources struct {
	Mapping MappingSource `json:"mapping"`
	AniDB   AniDBSource   `json:"anidb"`
}

// MappingSource is the mapping state read: Revision is an opaque,
// non-empty identifier of at most 100 bytes.
type MappingSource struct {
	Revision string `json:"revision"`
}

// AniDBSource is the AniDB title dump the anime list reflects: TitlesDump
// is its source date, yyyy-mm-dd.
type AniDBSource struct {
	TitlesDump string `json:"titles_dump"`
}

// Attribution is the licence notice the document carries.
type Attribution struct {
	License            string `json:"license"`
	LicenseURL         string `json:"license_url"`
	ContentsLicense    string `json:"contents_license"`
	ContentsLicenseURL string `json:"contents_license_url"`
	Notice             string `json:"notice"`
}

// Anime is one AniDB anime: its ids on other services, the highest
// episode number of each kind the document places, and its episodes: the
// ones AniDB lists, with their AniDB episode ids, and where they land.
// EpisodesListed says AniDB's episode list was read, so a number no run
// lists with an id is one AniDB does not list. Read an episode through
// Index.Episode, which applies the reading rule.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Anime struct {
	AniDBID        int      `json:"anidb_id"`
	Type           string   `json:"type,omitempty"`
	TVDBID         int      `json:"tvdb_id,omitempty"`
	MALIDs         []int    `json:"mal_ids,omitempty"`
	IMDbIDs        []string `json:"imdb_ids,omitempty"`
	Last           Last     `json:"last,omitzero"`
	EpisodesListed bool     `json:"episodes_listed,omitempty"`
	Episodes       []Run    `json:"episodes,omitempty"`
}

// TVDBSeries is every TVDB series a's episodes may land on: TVDBID first
// when a has one, then each other series a target names, in run order,
// each once. It is empty for an anime on no TVDB series.
func (a *Anime) TVDBSeries() []int {
	out := appendNew(nil, a.TVDBID)
	for i := range a.Episodes {
		if ts := a.Episodes[i].TVDB; ts != nil {
			for _, t := range *ts {
				out = appendNew(out, t.Series)
			}
		}
	}
	return out
}

// Last is the highest episode number of each kind the document places; 0
// means none. AniDB can list regular episodes past Regular, which runs
// name with their ids and state no landing for.
type Last struct {
	Regular   int `json:"regular,omitempty"`
	Special   int `json:"special,omitempty"`
	Credit    int `json:"credit,omitempty"`
	Promotion int `json:"promotion,omitempty"`
	Parody    int `json:"parody,omitempty"`
	Other     int `json:"other,omitempty"`
}

// Of is the highest number of kind k, 0 for none or an unknown kind.
func (l Last) Of(k Kind) int {
	if p := l.field(k); p != nil {
		return *p
	}
	return 0
}

// Set returns l with kind k's highest number n; an unknown kind is ignored.
func (l Last) Set(k Kind, n int) Last {
	if p := l.field(k); p != nil {
		*p = n
	}
	return l
}

func (l *Last) field(k Kind) *int {
	switch k {
	case Regular:
		return &l.Regular
	case Special:
		return &l.Special
	case Credit:
		return &l.Credit
	case Promotion:
		return &l.Promotion
	case Parody:
		return &l.Parody
	case Other:
		return &l.Other
	}
	return nil
}

// Run is consecutive AniDB episodes Start to End of one kind: episode
// Start+i lands on (Series, Season, Episode+i) of every TVDB target. A nil
// TVDB states nothing there; an empty list lands nowhere. TMDBMovies are
// the TMDB movies each episode of the run is, aliases of one work, nil for
// none stated. AniDBEpisodeIDs[i] is episode Start+i's AniDB episode id
// when AniDB lists the run's episodes, and nil when it lists none of them.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Run struct {
	Kind            Kind      `json:"kind"`
	Start           int       `json:"start"`
	End             int       `json:"end"`
	AniDBEpisodeIDs []int     `json:"anidb_episode_ids,omitempty"`
	TVDB            *[]Target `json:"tvdb,omitempty"`
	TMDBMovies      []int     `json:"tmdb_movies,omitempty"`
}

// Target is a TVDB episode in official order as a row writes it; an Index
// answers with a Placement instead. Series is the TVDB series it is on
// when that is not the row's TVDBID, 0 for the row's. EpisodeIDs[i] is the
// TVDB episode id of Episode+i, where the run's episode Start+i lands, nil
// when the document holds none. Default marks a target that rests only on
// a default, not on a stated placement.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Target struct {
	Series     int   `json:"series,omitempty"`
	Season     int   `json:"season"`
	Episode    int   `json:"episode"`
	EpisodeIDs []int `json:"episode_ids,omitempty"`
	Default    bool  `json:"default,omitempty"`
}

// on is the series t is on, for a target of a row on series row.
func (t Target) on(row int) int {
	if t.Series > 0 {
		return t.Series
	}
	return row
}

// ExtendedBy reports whether next continues r, so the two must be written
// as one run: the next number of r's kind, both listed by AniDB or
// neither, TVDB statements of the same form whose targets keep their
// series, season and Default flag, are each one episode on and hold TVDB
// episode ids in both or neither, and equal TMDBMovies. Decode refuses a
// document holding such a pair; a producer merges it.
func (r *Run) ExtendedBy(next *Run) bool {
	if r.Kind != next.Kind || r.End != next.Start-1 || (r.AniDBEpisodeIDs == nil) != (next.AniDBEpisodeIDs == nil) {
		return false
	}
	return advances(r.TVDB, next.TVDB, next.Start-r.Start) && slices.Equal(r.TMDBMovies, next.TMDBMovies)
}

func advances(run, next *[]Target, step int) bool {
	if run == nil || next == nil {
		return run == nil && next == nil
	}
	a, b := *run, *next
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Series != b[i].Series || a[i].Season != b[i].Season || a[i].Default != b[i].Default || a[i].Episode+step != b[i].Episode ||
			(a[i].EpisodeIDs == nil) != (b[i].EpisodeIDs == nil) {
			return false
		}
	}
	return true
}

// Entry is one AniList entry: its type, its ids and the AniDB episodes it
// is (Parts), in entry episode order.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Entry struct {
	AniListID int    `json:"anilist_id"`
	Type      string `json:"type,omitempty"`
	IDs       *IDs   `json:"ids,omitempty"`
	Parts     []Part `json:"parts,omitempty"`
}

// IDs are an entry's ids, each list ascending and unique: AniDB the AniDB
// anime it may be, two or more being ambiguous, and MAL its MyAnimeList
// ids.
type IDs struct {
	AniDB []int `json:"anidb,omitempty"`
	MAL   []int `json:"mal,omitempty"`
}

// Part is AniDB episodes of one anime and kind. With no range it names
// episodes 1 to the anime's Last of that kind. Each named episode is one
// entry episode, except that a Combined part, every regular episode of the
// anime, is one.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Part struct {
	AniDBID  int  `json:"anidb_id"`
	Kind     Kind `json:"kind"`
	Start    int  `json:"start,omitempty"`
	End      int  `json:"end,omitempty"`
	Combined bool `json:"combined,omitempty"`
}

// Identity reports whether p is the anime itself, its regular episodes
// with no range and not combined: the part an identity gives an entry.
func (p Part) Identity() bool {
	return p.Kind == Regular && p.Start == 0 && p.End == 0 && !p.Combined
}

// Kind is an AniDB episode type.
type Kind string

// The kinds, in the order the document lists them.
const (
	Regular   Kind = "regular"
	Special   Kind = "special"
	Credit    Kind = "credit"
	Promotion Kind = "promotion"
	Parody    Kind = "parody"
	Other     Kind = "other"
)

// kinds is every kind in document order, which Decode checks runs by.
var kinds = [...]Kind{Regular, Special, Credit, Promotion, Parody, Other}

// Kinds returns every kind in document order, a new slice on each call.
func Kinds() []Kind { return slices.Clone(kinds[:]) }

// order is k's position in kinds, -1 for an unknown kind.
func (k Kind) order() int { return slices.Index(kinds[:], k) }

var types = []string{"TV", "MOVIE", "OVA", "ONA", "SPECIAL", "UNKNOWN"}

// ValidType reports whether t is a published type, or "" for none.
func ValidType(t string) bool { return t == "" || slices.Contains(types, t) }
