// Package schema is the published animap.json document: its types, its
// deterministic encoding, its strict decoding and its content hash.
package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/cplieger/animap/internal/strictjson"
)

// Version is the schema version every document carries.
const Version = 1

// MaxDocumentBytes bounds a document on both encode and decode.
const MaxDocumentBytes = 12 << 20

// ErrTooLarge reports a document over MaxDocumentBytes.
var ErrTooLarge = errors.New("schema: document exceeds the size bound")

// ErrType reports a record type outside the published vocabulary.
var ErrType = errors.New("schema: type outside the published vocabulary")

var types = []string{"TV", "MOVIE", "OVA", "ONA", "SPECIAL", "UNKNOWN"}

// ValidType reports whether t is a published Record.Type: one of TV, MOVIE,
// OVA, ONA, SPECIAL and UNKNOWN, or "" for no type.
func ValidType(t string) bool { return t == "" || slices.Contains(types, t) }

// Document is the whole published file.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Document struct {
	Version     int         `json:"version"`
	GeneratedAt string      `json:"generated_at"`
	Sources     Sources     `json:"sources"`
	Attribution Attribution `json:"attribution"`
	Records     []Record    `json:"records"`
}

// Sources names exactly what the build read.
type Sources struct {
	AnimeOfflineDatabase OfflineDatabaseSource `json:"anime_offline_database"`
	AnimeLists           AnimeListsSource      `json:"anime_lists"`
	AniDBMirror          AniDBMirrorSource     `json:"anidb_mirror"`
	Overlay              OverlaySource         `json:"overlay"`
}

// AniDBMirrorSource is the AniDB mirror commit the episode counts and
// episode lists were read at.
type AniDBMirrorSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

// OfflineDatabaseSource is the pinned anime-offline-database release asset.
type OfflineDatabaseSource struct {
	Repository string `json:"repository"`
	Release    string `json:"release"`
	Asset      string `json:"asset"`
	SHA256     string `json:"sha256"`
}

// AnimeListsSource is the Anime-Lists commit the list was read at.
type AnimeListsSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	File       string `json:"file"`
}

// OverlaySource is the overlay set applied on top of Anime-Lists.
type OverlaySource struct {
	SHA256          string `json:"sha256"`
	Entries         int    `json:"entries"`
	SpecialOfParent int    `json:"special_of_parent,omitempty"`
}

// Attribution carries the licence notice the ODbL requires inside the file.
type Attribution struct {
	License            string `json:"license"`
	LicenseURL         string `json:"license_url"`
	ContentsLicense    string `json:"contents_license"`
	ContentsLicenseURL string `json:"contents_license_url"`
	Notice             string `json:"notice"`
}

// DefaultAttribution is the notice every release ships.
var DefaultAttribution = Attribution{
	License:            "ODbL-1.0",
	LicenseURL:         "https://opendatacommons.org/licenses/odbl/1-0/",
	ContentsLicense:    "DbCL-1.0",
	ContentsLicenseURL: "https://opendatacommons.org/licenses/dbcl/1-0/",
	Notice: "animap is made available under the Open Database License (ODbL) 1.0; its contents under the Database Contents License (DbCL) 1.0. " +
		"It contains information from anime-offline-database (https://github.com/cedya77/anime-offline-database), made available under the ODbL 1.0 and DbCL 1.0, " +
		"from Anime-Lists (https://github.com/Anime-Lists/anime-lists), " +
		"and episode counts from AniDB (https://anidb.net), read through AnimeAggregations (https://github.com/notseteve/AnimeAggregations).",
}

// Record is one AniList-keyed or AniDB-keyed mapping. The pointer-typed
// integers are present-or-absent: 0 is a meaningful value for each of them.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Record struct {
	AniListID         int             `json:"anilist_id,omitempty"`
	AniDBID           int             `json:"anidb_id,omitempty"`
	AniDBParent       *ParentSpecials `json:"anidb_parent,omitempty"`
	MALID             int             `json:"mal_id,omitempty"`
	Type              string          `json:"type,omitempty"`
	Episodes          int             `json:"episodes,omitempty"`
	TVDBID            int             `json:"tvdb_id,omitempty"`
	TVDBSeason        *int            `json:"tvdb_season,omitempty"`
	TVDBAbsolute      bool            `json:"tvdb_absolute,omitempty"`
	TVDBEpisodeOffset *int            `json:"tvdb_episode_offset,omitempty"`
	TMDBTVID          int             `json:"tmdb_tv_id,omitempty"`
	TMDBSeason        *int            `json:"tmdb_season,omitempty"`
	TMDBEpisodeOffset *int            `json:"tmdb_episode_offset,omitempty"`
	TMDBMovieIDs      []int           `json:"tmdb_movie_ids,omitempty"`
	IMDbIDs           []string        `json:"imdb_ids,omitempty"`
	MappingList       []Row           `json:"mapping_list,omitempty"`
}

// ParentSpecials is set on an AniList record that AniDB files as specials
// of another anime: AniList episode i is special Specials[i-1] of AniDBID.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type ParentSpecials struct {
	AniDBID  int   `json:"anidb_id"`
	Specials []int `json:"specials"`
}

// Row is one Anime-Lists mapping-list row. Each Episodes element is
// [anidb, target...]: one element means the AniDB episode has no
// counterpart, two or more name its target episodes.
//
//nolint:govet // fieldalignment: member order is the published JSON order
type Row struct {
	AniDBSeason int     `json:"anidb_season"`
	TVDBSeason  *int    `json:"tvdb_season,omitempty"`
	TMDBSeason  *int    `json:"tmdb_season,omitempty"`
	Start       int     `json:"start,omitempty"`
	End         int     `json:"end,omitempty"`
	Offset      int     `json:"offset,omitempty"`
	Episodes    [][]int `json:"episodes,omitempty"`
}

// Encode returns the minified document with no HTML escaping and no
// trailing newline, refusing one over MaxDocumentBytes.
func Encode(doc *Document) ([]byte, error) {
	b, err := marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxDocumentBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(b))
	}
	return b, nil
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Decode reads a document strictly: unknown members, trailing data, a wrong
// version, a type outside the vocabulary ([ErrType]) and a body over
// MaxDocumentBytes are all errors.
func Decode(r io.Reader) (*Document, error) {
	body, err := io.ReadAll(io.LimitReader(r, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxDocumentBytes {
		return nil, ErrTooLarge
	}
	var doc Document
	if err := strictjson.Decode(body, &doc); err != nil {
		return nil, fmt.Errorf("schema: decode: %w", err)
	}
	if doc.Version != Version {
		return nil, fmt.Errorf("schema: version %d, want %d", doc.Version, Version)
	}
	for i := range doc.Records {
		if t := doc.Records[i].Type; !ValidType(t) {
			return nil, fmt.Errorf("%w: record %d has type %q", ErrType, i, t)
		}
	}
	return &doc, nil
}

// ContentHash is the SHA-256 of what a release publishes as data. It
// excludes generated_at and sources, so an upstream commit that changes no
// record does not publish.
func ContentHash(doc *Document) (string, error) {
	b, err := marshal(struct {
		Attribution Attribution `json:"attribution"`
		Records     []Record    `json:"records"`
		Version     int         `json:"version"`
	}{doc.Attribution, doc.Records, doc.Version})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Populations are the counts the coverage guard compares across releases.
type Populations struct {
	Records          int `json:"records"`
	AniListWithAniDB int `json:"anilist_with_anidb"`
	AniDBWithTVDB    int `json:"anidb_with_tvdb"`
	WithTMDB         int `json:"with_tmdb"`
	WithMappingList  int `json:"with_mapping_list"`
}

// Census counts the populations in one pass. AniDBWithTVDB counts distinct
// AniDB ids, since several AniList records can share one.
func Census(records []Record) Populations {
	p := Populations{Records: len(records)}
	tvdb := make(map[int]struct{})
	for i := range records {
		r := &records[i]
		if r.AniListID > 0 && r.AniDBID > 0 {
			p.AniListWithAniDB++
		}
		if r.AniDBID > 0 && r.TVDBID > 0 {
			tvdb[r.AniDBID] = struct{}{}
		}
		if r.TMDBTVID > 0 || len(r.TMDBMovieIDs) > 0 {
			p.WithTMDB++
		}
		if len(r.MappingList) > 0 {
			p.WithMappingList++
		}
	}
	p.AniDBWithTVDB = len(tvdb)
	return p
}
