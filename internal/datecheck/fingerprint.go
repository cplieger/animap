package datecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

// inputsOf digests what a finding on ep rests on: the show it is judged
// on, the AniDB episode with its title digest, every target the node
// gives it, whether the default placed it, the link naming it, its
// carriers and the show's presence. A node edit that moves ep moves a
// target or show line; any other change shows as a finding the digest
// does not cover.
func (s *show) inputsOf(ep Episode, titles string, ts []Target, byDefault bool, link int) string {
	kind := "regular"
	switch {
	case ep.Extra:
		kind = "extra"
	case ep.Special:
		kind = "special"
	}
	lines := []string{showLine(s.side, s.series), fmt.Sprintf("anidb %s %d D%s T%s", kind, ep.Number, ep.AirDate, titles)}
	for _, t := range ts {
		lines = append(lines, "target "+s.targetLine(t))
	}
	lines = append(lines, fmt.Sprintf("default=%t", byDefault), fmt.Sprintf("link=%d", link))
	for _, c := range s.carriers(ep) {
		lines = append(lines, "carrier "+s.episodeLine(c))
	}
	return digest(append(lines, "absent=false"))
}

func (s *show) targetLine(t Target) string {
	if t.Absolute > 0 && t.Number == 0 {
		return fmt.Sprintf("abs%d missing", t.Absolute)
	}
	if _, ok := s.byEpisode[[2]int{t.Season, t.Number}]; !ok {
		return fmt.Sprintf("S%dE%d missing", t.Season, t.Number)
	}
	return s.episodeLine(skyhook.Episode{Season: t.Season, Number: t.Number})
}

func (s *show) episodeLine(e skyhook.Episode) string {
	a := s.byEpisode[[2]int{e.Season, e.Number}]
	return fmt.Sprintf("S%dE%d A%d D%s T%s", a.Season, a.Number, a.Absolute, a.AirDate, a.Title)
}

// showLine names the provider show a finding was judged on, so a ruling
// on one show never covers the same coordinates on another.
func showLine(side animelists.Side, series int) string {
	provider := "tvdb"
	if side == animelists.TMDB {
		provider = "tmdb"
	}
	return fmt.Sprintf("show %s %d", provider, series)
}

// digest hashes lines in order, one per line.
func digest(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// deadKey names a dead show's one finding.
const deadKey = string(DeadSeries)

var findingKeyRE = regexp.MustCompile(`^((` + string(MissingTarget) + `|` + string(DateMismatch) + `|` + string(OutOfOrder) + `):S?[1-9]\d*|` +
	deadKey + `|` + string(DeadMovie) + `:[1-9]\d*)$`)

// Key names f by kind and AniDB episode, S before a season 0 number, so
// two targets of one episode failing alike share a key; a dead movie by
// its id.
func (f *Finding) Key() string {
	if f.Kind == DeadMovie {
		return string(DeadMovie) + ":" + strconv.Itoa(f.Movie)
	}
	ep := strconv.Itoa(f.Episode.Number)
	if f.Episode.Special {
		ep = "S" + ep
	}
	return string(f.Kind) + ":" + ep
}

// Digests maps each of the lead's finding keys to its inputs digest: one
// key for a dead show, one per kind and episode, one per dead movie.
func (l *Lead) Digests() map[string]string {
	out := make(map[string]string, len(l.Findings)+1)
	if l.Dead {
		out[deadKey] = digest([]string{showLine(l.Side, l.Series), "absent=true"})
	}
	for i := range l.Findings {
		if k := l.Findings[i].Key(); out[k] == "" {
			out[k] = l.Findings[i].Inputs
		}
	}
	return out
}

// ValidFindingKey reports whether k has the shape Digests writes.
func ValidFindingKey(k string) bool { return findingKeyRE.MatchString(k) }
