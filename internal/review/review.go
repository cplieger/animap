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

	"github.com/cplieger/animap/internal/schema"
)

// The sections of a subject, one per source of trouble.
const (
	DateTVDB = "date-tvdb"
	DateTMDB = "date-tmdb"
	Drift    = "drift"
	Watch    = "watch"
)

// Sections lists every section in the order an issue renders them.
var Sections = []string{DateTVDB, DateTMDB, Drift, Watch}

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

// RecordDigest is the hex SHA-256 of the fields of a published record the
// watch reads, "absent" hashed for no record, so a watch ruling lapses
// when any of them moves.
func RecordDigest(rec *schema.Record) string {
	if rec == nil {
		sum := sha256.Sum256([]byte("absent"))
		return hex.EncodeToString(sum[:])
	}
	b, err := json.Marshal(struct {
		AniDBParent   *schema.ParentSpecials `json:"anidb_parent"`
		TVDBSeason    *int                   `json:"tvdb_season"`
		Type          string                 `json:"type"`
		TMDBMovieIDs  []int                  `json:"tmdb_movie_ids"`
		IMDbIDs       []string               `json:"imdb_ids"`
		TVDBPlacement []schema.Segment       `json:"tvdb_placement"`
		AniDBID       int                    `json:"anidb_id"`
		TVDBID        int                    `json:"tvdb_id"`
		Episodes      int                    `json:"episodes"`
		TVDBAbsolute  bool                   `json:"tvdb_absolute"`
	}{
		rec.AniDBParent, rec.TVDBSeason, rec.Type, rec.TMDBMovieIDs, rec.IMDbIDs, rec.TVDBPlacement,
		rec.AniDBID, rec.TVDBID, rec.Episodes, rec.TVDBAbsolute,
	})
	if err != nil {
		panic(fmt.Sprintf("review: a record does not marshal: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
