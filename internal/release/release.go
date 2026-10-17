// Package release is what a published animap.json carries beyond the
// format mapread owns: the size bound the build encodes under, the
// attribution notice, the content hash that decides whether a build
// publishes, and the populations the coverage guard compares.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cplieger/animap/mapread"
)

// maxDocumentBytes is mapread's default cap, so every reader on the
// defaults can read what the build writes.
var maxDocumentBytes = mapread.DefaultLimits().MaxDocumentBytes

// errTooLarge reports a document over maxDocumentBytes.
var errTooLarge = errors.New("release: document exceeds the size bound")

// DefaultAttribution is the notice every release ships.
var DefaultAttribution = mapread.Attribution{
	License:            "ODbL-1.0",
	LicenseURL:         "https://opendatacommons.org/licenses/odbl/1-0/",
	ContentsLicense:    "DbCL-1.0",
	ContentsLicenseURL: "https://opendatacommons.org/licenses/dbcl/1-0/",
	Notice: "animap is made available under the Open Database License (ODbL) 1.0; its contents under the Database Contents License (DbCL) 1.0. " +
		"It contains information from anime-offline-database (https://github.com/cedya77/anime-offline-database), made available under the ODbL 1.0 and DbCL 1.0, " +
		"from Anime-Lists (https://github.com/Anime-Lists/anime-lists), " +
		"and from AniDB (https://anidb.net) which episodes exist, with their kind, number and AniDB episode id, " +
		"read through AnimeAggregations (https://github.com/notseteve/AnimeAggregations); AniDB's data is licensed under CC BY-NC-SA 4.0.",
}

// Encode is mapread.Encode, refusing a document over the size bound.
func Encode(doc *mapread.Document) ([]byte, error) {
	b, err := mapread.Encode(doc)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxDocumentBytes {
		return nil, fmt.Errorf("%w: %d bytes, at most %d", errTooLarge, len(b), maxDocumentBytes)
	}
	return b, nil
}

// ContentHash is the SHA-256 of what a release publishes as data. It
// excludes generated_at and sources, so an upstream commit that changes no
// row does not publish.
func ContentHash(doc *mapread.Document) (string, error) {
	b, err := json.Marshal(struct {
		Anime       []mapread.Anime     `json:"anime"`
		Attribution mapread.Attribution `json:"attribution"`
		Entries     []mapread.Entry     `json:"entries"`
		Version     int                 `json:"version"`
	}{doc.Anime, doc.Attribution, doc.Entries, doc.Version})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Populations are the counts the coverage guard compares across releases.
type Populations struct {
	Entries          int `json:"entries"`
	EntriesWithParts int `json:"entries_with_parts"`
	AnimeWithTVDB    int `json:"anime_with_tvdb"`
	AnimeWithTMDB    int `json:"anime_with_tmdb"`
	EpisodesOnTVDB   int `json:"episodes_on_tvdb"`
	EpisodesOnTMDB   int `json:"episodes_on_tmdb"`
	AniDBEpisodeIDs  int `json:"anidb_episode_ids"`
}

// Census counts the populations: entries with an AniList id, those with
// parts, anime with a TVDB series or a TMDB show or movie, the AniDB
// episodes with a target on each site, and the AniDB episode ids listed.
func Census(doc *mapread.Document) Populations {
	var p Populations
	for i := range doc.Entries {
		if e := &doc.Entries[i]; e.AniListID > 0 {
			p.Entries++
			if len(e.Parts) > 0 {
				p.EntriesWithParts++
			}
		}
	}
	for i := range doc.Anime {
		a := &doc.Anime[i]
		if a.TVDBID > 0 {
			p.AnimeWithTVDB++
		}
		if a.TMDBTVID > 0 || len(a.TMDBMovieIDs) > 0 {
			p.AnimeWithTMDB++
		}
		for _, r := range a.Episodes {
			p.EpisodesOnTVDB += targeted(&r, r.TVDB)
			p.EpisodesOnTMDB += targeted(&r, r.TMDB)
			p.AniDBEpisodeIDs += len(r.AniDBEpisodeIDs)
		}
	}
	return p
}

// targeted is how many of r's episodes site lands somewhere.
func targeted(r *mapread.Run, site *[]mapread.Target) int {
	if site == nil || len(*site) == 0 {
		return 0
	}
	return r.End - r.Start + 1
}
