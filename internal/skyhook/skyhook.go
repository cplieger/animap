// Package skyhook reads TVDB's official episode order through Sonarr's
// public metadata proxy and fingerprints the layout of chosen seasons.
package skyhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// baseURL is SkyHook's show endpoint; the TVDB series id is appended.
const baseURL = "https://skyhook.sonarr.tv/v1/tvdb/shows/en/"

const maxShowBytes = 16 << 20

// Episode is one TVDB episode in official order. Its JSON form is the
// compact [season, episode, absolute, air date] array overlay files carry;
// Absolute is 0 and AirDate "" when TVDB has none.
type Episode struct {
	AirDate  string
	Season   int
	Number   int
	Absolute int
}

// MarshalJSON writes the compact array form.
func (e Episode) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{e.Season, e.Number, e.Absolute, e.AirDate})
}

// UnmarshalJSON reads the compact array form.
func (e *Episode) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw) != 4 {
		return fmt.Errorf("skyhook: episode has %d elements, want 4", len(raw))
	}
	for i, dst := range []*int{&e.Season, &e.Number, &e.Absolute} {
		if err := json.Unmarshal(raw[i], dst); err != nil {
			return err
		}
	}
	return json.Unmarshal(raw[3], &e.AirDate)
}

// Line is the episode's fingerprint line.
func (e Episode) Line() string {
	return fmt.Sprintf("S%dE%d A%d D%s", e.Season, e.Number, e.Absolute, e.AirDate)
}

// Show is the part of a SkyHook show animap reads.
type Show struct {
	Episodes []Episode
}

type showJSON struct {
	Episodes []struct {
		AirDate  string `json:"airDate"`
		Season   int    `json:"seasonNumber"`
		Number   int    `json:"episodeNumber"`
		Absolute int    `json:"absoluteEpisodeNumber"`
	} `json:"episodes"`
}

func parseShow(body []byte) (*Show, error) {
	var sj showJSON
	if err := json.Unmarshal(body, &sj); err != nil {
		return nil, fmt.Errorf("skyhook: decode show: %w", err)
	}
	s := &Show{}
	for _, e := range sj.Episodes {
		s.Episodes = append(s.Episodes, Episode{Season: e.Season, Number: e.Number, Absolute: e.Absolute, AirDate: e.AirDate})
	}
	return s, nil
}

// Getter is the fetch a Client needs; source.Client satisfies it.
type Getter interface {
	Get(ctx context.Context, url string, maxBytes int64) ([]byte, error)
}

// Fetch reads one show. A 404 surfaces as the getter's not-found error.
func Fetch(ctx context.Context, g Getter, tvdbID int) (*Show, error) {
	body, err := g.Get(ctx, fmt.Sprintf("%s%d", baseURL, tvdbID), maxShowBytes)
	if err != nil {
		return nil, err
	}
	return parseShow(body)
}

// Layout returns the show's episodes on the given seasons, sorted. A nil
// seasons slice selects every season from 1 up (an absolute-numbered node).
func Layout(s *Show, seasons []int) []Episode {
	var out []Episode
	for _, e := range s.Episodes {
		if (seasons == nil && e.Season >= 1) || slices.Contains(seasons, e.Season) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Episode) int {
		if a.Season != b.Season {
			return a.Season - b.Season
		}
		return a.Number - b.Number
	})
	return out
}

// Hash fingerprints a layout: its lines sorted and joined.
func Hash(eps []Episode) string {
	lines := make([]string, 0, len(eps))
	for _, e := range eps {
		lines = append(lines, e.Line())
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
