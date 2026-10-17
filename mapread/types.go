package mapread

import "slices"

// Version is the major version of the format this package reads and writes.
// It is raised only for a breaking change; a member added within a version
// is additive and Decode ignores members it does not know.
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

// Sources names the inputs the document was built from.
type Sources struct {
	AnimeOfflineDatabase OfflineDatabaseSource `json:"anime_offline_database"`
	AnimeLists           AnimeListsSource      `json:"anime_lists"`
	AniDBMirror          AniDBMirrorSource     `json:"anidb_mirror"`
	Overlay              OverlaySource         `json:"overlay"`
}

// OfflineDatabaseSource is the anime-offline-database release asset read.
type OfflineDatabaseSource struct {
	Repository string `json:"repository"`
	Release    string `json:"release"`
	Asset      string `json:"asset"`
	SHA256     string `json:"sha256"`
}

// AnimeListsSource is the Anime-Lists commit read.
type AnimeListsSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	File       string `json:"file"`
}

// AniDBMirrorSource is the AniDB mirror commit read.
type AniDBMirrorSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

// OverlaySource is the overlay applied on top of Anime-Lists: Entries
// patched nodes and Links overlay links applied.
type OverlaySource struct {
	SHA256  string `json:"sha256"`
	Entries int    `json:"entries"`
	Links   int    `json:"links,omitempty"`
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
	TMDBTVID       int      `json:"tmdb_tv_id,omitempty"`
	TMDBMovieIDs   []int    `json:"tmdb_movie_ids,omitempty"`
	IMDbIDs        []string `json:"imdb_ids,omitempty"`
	Last           Last     `json:"last,omitzero"`
	EpisodesListed bool     `json:"episodes_listed,omitempty"`
	Episodes       []Run    `json:"episodes,omitempty"`
}

// Last is the highest episode number of each kind the document places; 0
// means none. AniDB can list regular episodes past Regular, which runs
// name with their ids and state no site for.
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
// Start+i lands on (Season, Episode+i) of every target. A nil site means
// nothing stated there; an empty list lands nowhere. AniDBEpisodeIDs[i] is
// episode Start+i's AniDB episode id when AniDB lists the run's episodes,
// and nil when it lists none of them.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Run struct {
	Kind            Kind      `json:"kind"`
	Start           int       `json:"start"`
	End             int       `json:"end"`
	AniDBEpisodeIDs []int     `json:"anidb_episode_ids,omitempty"`
	TVDB            *[]Target `json:"tvdb,omitempty"`
	TMDB            *[]Target `json:"tmdb,omitempty"`
}

// Target is a site episode in official order. Default marks a target that
// rests only on a node default, not on a written mapping.
type Target struct {
	Season  int  `json:"season"`
	Episode int  `json:"episode"`
	Default bool `json:"default,omitempty"`
}

// ExtendedBy reports whether next continues r, so the two must be written
// as one run: the next number of r's kind, both listed by AniDB or
// neither, and on each site a statement of the same form whose targets
// keep their season and Default flag and are each one episode on. Decode
// refuses a document holding such a pair; a producer merges it.
func (r *Run) ExtendedBy(next *Run) bool {
	if r.Kind != next.Kind || r.End != next.Start-1 || (r.AniDBEpisodeIDs == nil) != (next.AniDBEpisodeIDs == nil) {
		return false
	}
	step := next.Start - r.Start
	return advances(r.TVDB, next.TVDB, step) && advances(r.TMDB, next.TMDB, step)
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
		if a[i].Season != b[i].Season || a[i].Default != b[i].Default || a[i].Episode+step != b[i].Episode {
			return false
		}
	}
	return true
}

// Entry is one published identity row: keyed by its AniList id when it has
// one, merging every anime-offline-database entry that names that id, and
// otherwise one database entry. It holds its ids on every service the
// database lists, its episode count, and the AniDB episodes it is (Parts),
// in entry episode order.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Entry struct {
	AniListID int    `json:"anilist_id,omitempty"`
	Type      string `json:"type,omitempty"`
	Episodes  int    `json:"episodes,omitempty"`
	IDs       *IDs   `json:"ids,omitempty"`
	Parts     []Part `json:"parts,omitempty"`
}

// IDs are an entry's ids per service, each list ascending and unique. The
// members are declared alphabetically, so the encoded member is the sort
// key of entries with no AniList id.
type IDs struct {
	AniDB          []int    `json:"anidb,omitempty"`
	AnimeCountdown []int    `json:"animecountdown,omitempty"`
	AnimePlanet    []string `json:"animeplanet,omitempty"`
	AniSearch      []int    `json:"anisearch,omitempty"`
	ANN            []int    `json:"ann,omitempty"`
	Kitsu          []int    `json:"kitsu,omitempty"`
	LiveChart      []int    `json:"livechart,omitempty"`
	MAL            []int    `json:"mal,omitempty"`
	NotifyMoe      []string `json:"notifymoe,omitempty"`
	Simkl          []int    `json:"simkl,omitempty"`
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
