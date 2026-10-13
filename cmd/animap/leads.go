package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/reviewed"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/runesafe/v3"
)

const (
	labelLead    = "lead:date-mismatch"
	maxNodeLine  = 2000
	maxNodeLines = 100
	leadFix      = "Prove the right target against AniDB, TVDB's official order and TMDB, episode by episode. " +
		"If the node is wrong, add an overlay entry (docs/overlay.md) and send the same rows to Anime-Lists in a pull request. " +
		"This issue closes on its own once the node passes, whether Anime-Lists or an overlay entry fixes it.\n\n"
	leadSticks = "Closing this issue by hand does not stick, because the next run opens it again while the node fails and no reviewed record covers it. " +
		"docs/date-check.md explains each finding and what the check skips.\n"
)

// slugRE admits a SkyHook slug into a TVDB URL; any other slug falls back
// to the id link.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// leadWant is the issue for one date-check lead, with causes naming why its
// record rec no longer covers it. Its body carries AniDB episode numbers
// and air dates, never AniDB text, and TVDB episode numbers and dates,
// never TVDB titles.
func leadWant(l *dateLead, rec *reviewed.Record, causes []reviewed.Cause) issues.Want {
	var b strings.Builder
	var title string
	if l.Dead {
		title = fmt.Sprintf("lead: AniDB %d maps to TVDB %d, which SkyHook no longer serves", l.AniDB, l.Series)
		fmt.Fprintf(&b, "Anime-Lists maps AniDB %d to TVDB series %d, and SkyHook answers 404 for it, so TVDB removed or merged the series. Find the series that carries these episodes now.\n\n", l.AniDB, l.Series)
	} else {
		title = fmt.Sprintf("lead: AniDB %d, %d mapped episode(s) fail the date check on TVDB %d", l.AniDB, len(l.Findings), l.Series)
		fmt.Fprintf(&b, "Anime-Lists maps AniDB %d to TVDB series %d, and TVDB's official order disagrees with AniDB's air dates. This is a lead, not a verdict.\n\n", l.AniDB, l.Series)
	}
	if rec != nil && len(causes) > 0 {
		b.WriteString(reraised(rec, causes))
	}
	fmt.Fprintf(&b, "- AniDB: https://anidb.net/anime/%d\n", l.AniDB)
	links := tvdbLinks(l)
	for _, u := range links {
		fmt.Fprintf(&b, "- TVDB: %s\n", u)
	}
	b.WriteString("\n")
	writeNode(&b, l)
	if !l.Dead {
		writeFindings(&b, l.Findings)
	}
	b.WriteString(leadFix)
	if !l.Dead {
		writeAccept(&b, l.AniDB, links[0])
	}
	b.WriteString(leadSticks)
	return issues.Want{Key: datecheck.Key(l.AniDB), Title: title, Body: b.String(), Labels: []string{labelAnimap, labelLead}}
}

// tvdbLinks are the official-order pages of the seasons the findings
// target, or the series page when there is no usable slug or season.
func tvdbLinks(l *dateLead) []string {
	fallback := []string{fmt.Sprintf("https://thetvdb.com/dereferrer/series/%d", l.Series)}
	if l.Dead || !slugRE.MatchString(l.Slug) {
		return fallback
	}
	seasons := map[int]bool{}
	for _, f := range l.Findings {
		if f.Target.Absolute == 0 || f.Target.Number > 0 {
			seasons[f.Target.Season] = true
		}
		for _, c := range f.Carriers {
			seasons[c.Season] = true
		}
	}
	if len(seasons) == 0 {
		return fallback
	}
	var out []string
	for _, s := range slices.Sorted(maps.Keys(seasons)) {
		out = append(out, fmt.Sprintf("https://thetvdb.com/series/%s/seasons/official/%d", l.Slug, s))
	}
	return out
}

func writeAccept(b *strings.Builder, aid int, tvdbLink string) {
	b.WriteString("If the node is right and the dates misled the check, record it from a local run (docs/date-check.md) and commit the file it writes:\n\n```sh\n")
	fmt.Fprintf(b, "go run ./cmd/animap datecheck accept -datecheck datecheck.json -id %d -reason \"<the episodes and their titles that prove the mapping>\" -evidence https://anidb.net/anime/%d -evidence %s\n", aid, aid, tvdbLink)
	b.WriteString("```\n\n")
}

func writeNode(b *strings.Builder, l *dateLead) {
	b.WriteString("The node as checked")
	if l.Overlay != "" {
		fmt.Fprintf(b, ", with `%s` applied", l.Overlay)
	}
	b.WriteString(":\n\n```text\n")
	for i, line := range strings.Split(l.Node, "\n") {
		if i == maxNodeLines {
			b.WriteString("...\n")
			break
		}
		b.WriteString(strings.ReplaceAll(runesafe.SanitizeSingleLineBounded(line, maxNodeLine), "`", "'"))
		b.WriteByte('\n')
	}
	b.WriteString("```\n\n")
}

func writeFindings(b *strings.Builder, fs []datecheck.Finding) {
	b.WriteString("| AniDB episode | AniDB date | TVDB episode | TVDB date | Finding | TVDB episodes on the AniDB date |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for i := range fs {
		if i == maxListedLines {
			fmt.Fprintf(b, "\n... %d more\n", len(fs)-i)
			break
		}
		f := &fs[i]
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n", anidbLabel(f.Episode), dash(f.Episode.AirDate),
			targetLabel(f.Target), dash(f.Target.AirDate), findingLabel(f.Kind), carriersLabel(f.Carriers))
	}
	b.WriteString("\n")
}

func anidbLabel(e datecheck.Episode) string {
	if e.Special {
		return fmt.Sprintf("S%d", e.Number)
	}
	return strconv.Itoa(e.Number)
}

func targetLabel(t datecheck.Target) string {
	switch {
	case t.Absolute > 0 && t.Number > 0:
		return fmt.Sprintf("absolute %d (S%dE%d)", t.Absolute, t.Season, t.Number)
	case t.Absolute > 0:
		return fmt.Sprintf("absolute %d", t.Absolute)
	}
	return fmt.Sprintf("S%dE%d", t.Season, t.Number)
}

func findingLabel(k datecheck.Kind) string {
	switch k {
	case datecheck.MissingTarget:
		return "not on TVDB"
	case datecheck.OutOfOrder:
		return "out of order"
	}
	return "dates differ"
}

func carriersLabel(cs []skyhook.Episode) string {
	if len(cs) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("S%dE%d", c.Season, c.Number))
	}
	return strings.Join(parts, ", ")
}

func dash(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
