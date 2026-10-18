// Package review holds what a daily run says about each anime and what a
// person ruled on it: the subject keys an issue and an acceptance record
// are filed under, a section's evaluation on one run, and the acceptance
// records in reviewed/, each part fingerprinted on the inputs it judged so
// it covers its findings only while they stand.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cplieger/animap/mapread"
)

// The sections of a subject, one per source of trouble.
const (
	DateTVDB  = "date-tvdb"
	DateTMDB  = "date-tmdb"
	Leftovers = "leftovers"
	Drift     = "drift"
	Watch     = "watch"
)

// Sections lists every section in the order an issue renders them.
var Sections = []string{DateTVDB, DateTMDB, Leftovers, Drift, Watch}

// Prefix starts every subject key, and no other issue key.
const Prefix = "anime:"

const (
	kindAniDB   = "anidb"
	kindAniList = "anilist"
)

// AniDBKey is the subject of AniDB node aid: its date checks, its overlay
// entry's drift, and the watch gap of the record carrying it.
func AniDBKey(aid int) string { return Prefix + kindAniDB + ":" + strconv.Itoa(aid) }

// AniListKey is the subject of AniList id al: its overlay link's drift and
// the watch gap of a record with no AniDB id or one a link names.
func AniListKey(al int) string { return Prefix + kindAniList + ":" + strconv.Itoa(al) }

// Subject is a parsed subject key.
type Subject struct {
	Kind string
	ID   int
}

// ParseKey reads a subject key, false for any other string.
func ParseKey(key string) (Subject, bool) {
	rest, ok := strings.CutPrefix(key, Prefix)
	if !ok {
		return Subject{}, false
	}
	kind, num, ok := strings.Cut(rest, ":")
	id, err := strconv.Atoi(num)
	if !ok || err != nil || id <= 0 || strconv.Itoa(id) != num || (kind != kindAniDB && kind != kindAniList) {
		return Subject{}, false
	}
	return Subject{Kind: kind, ID: id}, true
}

// AniDB reports whether s is an AniDB node's subject.
func (s Subject) AniDB() bool { return s.Kind == kindAniDB }

// Label is how a title names s: "AniDB 12" or "AniList 34".
func (s Subject) Label() string {
	if s.AniDB() {
		return "AniDB " + strconv.Itoa(s.ID)
	}
	return "AniList " + strconv.Itoa(s.ID)
}

// File is the acceptance record's file name for key, "" for a key that
// does not parse.
func File(key string) string {
	s, ok := ParseKey(key)
	if !ok {
		return ""
	}
	return s.Kind + "-" + strconv.Itoa(s.ID) + ".json"
}

// Section is one section's evaluation on one run. Evaluated says its
// source judged this subject; Reason says why not. A section may vanish
// from an issue, or be silenced by an acceptance, only once evaluated.
type Section struct {
	Acceptance *Match `json:"acceptance,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Applicable bool   `json:"applicable"`
	Evaluated  bool   `json:"evaluated"`
}

// Match is how an acceptance part met this run's findings: Matches when it
// covers every one, else Causes say why not, as Changes writes them.
// Verdict and Reason are the part's own.
type Match struct {
	File    string   `json:"file"`
	Part    string   `json:"part"`
	Verdict string   `json:"verdict"`
	Reason  string   `json:"reason"`
	Causes  []string `json:"causes,omitempty"`
	Matches bool     `json:"matches"`
}

// RecordDigest is the hex SHA-256 of what the watch reads of entry e:
// its type, its routes and, per entry episode in order, the list of its
// AniDB episodes with their TVDB statements, "absent" hashed for no
// entry. A watch ruling lapses when any of them moves, and only then.
func RecordDigest(x *mapread.Index, e *mapread.Entry) string {
	if e == nil {
		sum := sha256.Sum256([]byte("absent"))
		return hex.EncodeToString(sum[:])
	}
	r := x.Routes(*e)
	episodes := [][][]any{}
	for _, ep := range x.Episodes(*e) {
		refs := [][]any{}
		for _, ref := range ep.Refs {
			refs = append(refs, []any{ref.AniDB, ref.Kind, ref.Number, digestStatement(x, ref.Ref)})
		}
		episodes = append(episodes, refs)
	}
	b, err := json.Marshal(map[string]any{
		"type": e.Type,
		"routes": map[string]any{
			"tvdb": orEmpty(r.TVDB), "tmdb_movie_ids": orEmpty(r.TMDBMovies), "imdb_ids": orEmpty(r.IMDb),
		},
		"episodes": episodes,
	})
	if err != nil {
		panic(fmt.Sprintf("review: an entry does not marshal: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// digestStatement is ref's TVDB statement: null with no TVDB series,
// {"stated":false} for nothing stated, else {"stated":true} with its
// targets, if any.
func digestStatement(x *mapread.Index, ref mapread.Ref) any {
	ep, _ := x.Episode(ref)
	if ep.TVDB == nil {
		return nil
	}
	out := map[string]any{"stated": ep.TVDB.State != mapread.Unstated}
	if len(ep.TVDB.Targets) > 0 {
		out["targets"] = ep.TVDB.Targets
	}
	return out
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
