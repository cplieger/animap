// Package tmdb reads TMDB's episode order and whether a movie exists,
// through the TMDB API v3 (https://developer.themoviedb.org/docs/getting-started).
// Only season and episode numbers, air dates and titles leave Fetch, and
// callers keep no title but its digest.
package tmdb

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Host is the API's host, the only one a TMDB token is sent to.
const Host = "api.themoviedb.org"

const baseURL = "https://" + Host + "/3/"

// maxBodyBytes bounds one response: a show with twenty seasons appended,
// each with its crew and guest stars, runs to a few MB.
const maxBodyBytes = 32 << 20

// maxAppended is the most append_to_response objects one call may name;
// TMDB answers a 21st with HTTP 400
// (https://developer.themoviedb.org/docs/append-to-response).
const maxAppended = 20

// Episode is one TMDB episode. AirDate is "" when TMDB has none.
type Episode struct {
	AirDate string
	Title   string
	Season  int
	Number  int
}

// Show is a TV show's episodes on every season, sorted by season and number.
type Show struct {
	Episodes []Episode
}

// Getter is the fetch Fetch and Movie need; source.Client satisfies it.
type Getter interface {
	Get(ctx context.Context, url string, maxBytes int64) ([]byte, error)
}

type seasonJSON struct {
	Episodes []struct {
		AirDate string `json:"air_date"`
		Name    string `json:"name"`
		Season  int    `json:"season_number"`
		Number  int    `json:"episode_number"`
	} `json:"episodes"`
}

// Fetch reads show id with every season. The first call appends seasons 0
// to 19, which TMDB answers for the seasons that exist and ignores past
// them, so a show with no season numbered 20 or higher costs one request.
// A 404 surfaces as the getter's not-found error.
func Fetch(ctx context.Context, g Getter, id int) (*Show, error) {
	first := make([]int, maxAppended)
	for i := range first {
		first[i] = i
	}
	listed, got, err := fetchSeasons(ctx, g, id, first)
	if err != nil {
		return nil, err
	}
	var rest []int
	for _, n := range listed {
		if _, ok := got[n]; !ok && !slices.Contains(first, n) {
			rest = append(rest, n)
		}
	}
	for chunk := range slices.Chunk(rest, maxAppended) {
		_, more, err := fetchSeasons(ctx, g, id, chunk)
		if err != nil {
			return nil, err
		}
		maps.Copy(got, more)
	}
	return assemble(id, listed, got)
}

// assemble is the show from every season it lists, sorted; a listed season
// TMDB did not return makes the read incomplete.
func assemble(id int, listed []int, got map[int]seasonJSON) (*Show, error) {
	s := &Show{}
	for _, n := range listed {
		season, ok := got[n]
		if !ok {
			return nil, fmt.Errorf("tmdb: tv %d lists season %d and did not return it", id, n)
		}
		for _, e := range season.Episodes {
			s.Episodes = append(s.Episodes, Episode{Season: n, Number: e.Number, AirDate: e.AirDate, Title: e.Name})
		}
	}
	slices.SortFunc(s.Episodes, func(a, b Episode) int {
		return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Number, b.Number))
	})
	return s, nil
}

// fetchSeasons reads show id with seasons appended and returns the season
// numbers the show lists and the appended seasons it returned.
func fetchSeasons(ctx context.Context, g Getter, id int, seasons []int) (listed []int, got map[int]seasonJSON, err error) {
	parts := make([]string, 0, len(seasons))
	for _, n := range seasons {
		parts = append(parts, "season/"+strconv.Itoa(n))
	}
	body, err := g.Get(ctx, fmt.Sprintf("%stv/%d?append_to_response=%s", baseURL, id, strings.Join(parts, ",")), maxBodyBytes)
	if err != nil {
		return nil, nil, err
	}
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(body, &raw); err != nil {
		return nil, nil, fmt.Errorf("tmdb: decode tv %d: %w", id, err)
	}
	var list []struct {
		Number int `json:"season_number"`
	}
	if err = json.Unmarshal(raw["seasons"], &list); err != nil {
		return nil, nil, fmt.Errorf("tmdb: decode tv %d seasons: %w", id, err)
	}
	listed = make([]int, 0, len(list))
	for _, s := range list {
		listed = append(listed, s.Number)
	}
	got = map[int]seasonJSON{}
	for _, n := range seasons {
		r, ok := raw["season/"+strconv.Itoa(n)]
		if !ok {
			continue
		}
		var s seasonJSON
		if err = json.Unmarshal(r, &s); err != nil {
			return nil, nil, fmt.Errorf("tmdb: decode tv %d season %d: %w", id, n, err)
		}
		got[n] = s
	}
	return listed, got, nil
}

// Movie reports whether movie id exists: nil when it does, the getter's
// not-found error on a 404, any other error when TMDB could not be read.
func Movie(ctx context.Context, g Getter, id int) error {
	_, err := g.Get(ctx, fmt.Sprintf("%smovie/%d", baseURL, id), maxBodyBytes)
	return err
}
