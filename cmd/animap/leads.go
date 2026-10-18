package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/runesafe/v3"
)

const (
	maxNodeLine  = 2000
	maxNodeLines = 100
	leadFix      = "Prove the right target against AniDB, TVDB's official order and TMDB, episode by episode. " +
		"If the node is wrong, add an overlay entry (docs/overlay.md) and send the same rows to Anime-Lists in a pull request.\n\n"
)

// slugRE admits a SkyHook slug into a TVDB URL; any other slug falls back
// to the id link.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// dateText is one side's date-check section. It carries AniDB episode
// numbers and air dates, never AniDB text, and provider episode numbers
// and dates, never provider titles.
func dateText(s *subject, name string, d *dateSection) string {
	var b strings.Builder
	sub, _ := review.ParseKey(s.Key)
	provider, order := "TVDB", "TVDB's official order"
	if name == review.DateTMDB {
		provider, order = tmdbName, "TMDB's episode order"
	}
	fmt.Fprintf(&b, "### %s dates\n\n", provider)
	links := providerLinks(name, d)
	switch {
	case d.Dead:
		fmt.Fprintf(&b, "Anime-Lists maps AniDB %d to %s show %d, and %s answers 404 for it, so the show was removed or merged. Find the show that carries these episodes now.\n\n", sub.ID, provider, d.Series, provider)
		b.WriteString(siblingsText(d.Siblings))
	case d.Series > 0 && len(showFindings(d)) > 0:
		fmt.Fprintf(&b, "Anime-Lists maps AniDB %d to %s show %d, and %s disagrees with AniDB's air dates. This is a lead, not a verdict.\n\n", sub.ID, provider, d.Series, order)
	}
	for _, m := range deadMovies(d) {
		fmt.Fprintf(&b, "TMDB answers 404 for movie %d, which the node names in `tmdbid`. Find the movie's id now, or drop it.\n\n", m)
	}
	if m := d.Acceptance; m != nil && !m.Matches && len(m.Causes) > 0 {
		fmt.Fprintf(&b, "`reviewed/%s` `%s` accepted these findings, and they are raised again because %s.\n\n", m.File, m.Part, causesText(m.Causes))
	}
	for _, u := range links {
		fmt.Fprintf(&b, "- %s: %s\n", provider, u)
	}
	b.WriteString("\n")
	if fs := showFindings(d); len(fs) > 0 {
		writeFindings(&b, provider, fs)
	}
	b.WriteString(leadFix)
	if !d.Dead && len(deadMovies(d)) == 0 {
		writeAccept(&b, s.Key, name, sub.ID, links)
	}
	return b.String()
}

// siblingsText names the other nodes a dead show raises, "" for none.
func siblingsText(siblings []int) string {
	if len(siblings) == 0 {
		return ""
	}
	ids := make([]string, len(siblings))
	for i, id := range siblings {
		ids[i] = strconv.Itoa(id)
	}
	if len(ids) == 1 {
		return fmt.Sprintf("AniDB %s maps to this show too and is raised as well.\n\n", ids[0])
	}
	return fmt.Sprintf("AniDB %s map to this show too and are raised as well.\n\n", joinAnd(ids))
}

func showFindings(d *dateSection) []datecheck.Finding {
	var out []datecheck.Finding
	for i := range d.Findings {
		if d.Findings[i].Kind != datecheck.DeadMovie {
			out = append(out, d.Findings[i])
		}
	}
	return out
}

func deadMovies(d *dateSection) []int {
	var out []int
	for i := range d.Findings {
		if d.Findings[i].Kind == datecheck.DeadMovie {
			out = append(out, d.Findings[i].Movie)
		}
	}
	return out
}

// providerLinks are the pages of the seasons the findings target: TVDB's
// official-order pages, or the series page when there is no usable slug or
// season; TMDB's season pages after the show page, and each dead movie.
func providerLinks(name string, d *dateSection) []string {
	seasons := findingSeasons(d)
	var out []string
	if name == review.DateTMDB {
		if d.Series > 0 {
			out = append(out, fmt.Sprintf("https://www.themoviedb.org/tv/%d", d.Series))
		}
		for _, s := range seasons {
			out = append(out, seasonPage(animelists.TMDB, d.Series, "", s))
		}
		for _, m := range deadMovies(d) {
			out = append(out, fmt.Sprintf("https://www.themoviedb.org/movie/%d", m))
		}
		return out
	}
	if !slugRE.MatchString(d.Slug) || len(seasons) == 0 {
		return []string{tvdbSeriesPage(d.Series)}
	}
	for _, s := range seasons {
		out = append(out, seasonPage(animelists.TVDB, d.Series, d.Slug, s))
	}
	return out
}

// seasonPage is season of show series on side: TMDB's season page, TVDB's
// official-order page, or the TVDB series page when slug is unusable.
func seasonPage(side animelists.Side, series int, slug string, season int) string {
	switch {
	case side == animelists.TMDB:
		return fmt.Sprintf("https://www.themoviedb.org/tv/%d/season/%d", series, season)
	case !slugRE.MatchString(slug):
		return tvdbSeriesPage(series)
	}
	return fmt.Sprintf("https://thetvdb.com/series/%s/seasons/official/%d", slug, season)
}

func tvdbSeriesPage(series int) string {
	return fmt.Sprintf("https://thetvdb.com/dereferrer/series/%d", series)
}

// findingSeasons are the seasons a live show's findings target or carry.
func findingSeasons(d *dateSection) []int {
	set := map[int]bool{}
	fs := showFindings(d)
	for i := range fs {
		f := &fs[i]
		if f.Target.Absolute == 0 || f.Target.Number > 0 {
			set[f.Target.Season] = true
		}
		for _, c := range f.Carriers {
			set[c.Season] = true
		}
	}
	if d.Dead {
		return nil
	}
	return slices.Sorted(maps.Keys(set))
}

func writeAccept(b *strings.Builder, key, part string, aid int, links []string) {
	b.WriteString("If the node is right and the dates misled the check, accept it from the run's `review.json` (docs/review.md) and commit the file it writes:\n\n```sh\n")
	fmt.Fprintf(b, "go run ./cmd/animap accept -from review.json -key %s -part %s -verdict fine -reason \"<the episodes and their titles that prove the mapping>\" -evidence https://anidb.net/anime/%d", key, part, aid)
	if len(links) > 0 {
		fmt.Fprintf(b, " -evidence %s", links[0])
	}
	b.WriteString("\n")
	b.WriteString("```\n\n")
}

func writeNode(b *strings.Builder, node, overlay string) {
	b.WriteString("The node as checked")
	if overlay != "" {
		fmt.Fprintf(b, ", with `%s` applied", overlay)
	}
	b.WriteString(":\n\n```text\n")
	for i, line := range strings.Split(node, "\n") {
		if i == maxNodeLines {
			b.WriteString("...\n")
			break
		}
		line = strings.ReplaceAll(runesafe.SanitizeSingleLineBounded(line, maxNodeLine), "`", "'")
		b.WriteString(strings.ReplaceAll(line, "<!--", "<! --"))
		b.WriteByte('\n')
	}
	b.WriteString("```\n\n")
}

func writeFindings(b *strings.Builder, provider string, fs []datecheck.Finding) {
	fmt.Fprintf(b, "| AniDB episode | AniDB date | %[1]s episode | %[1]s date | Finding | %[1]s episodes on the AniDB date |\n", provider)
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for i := range fs {
		if i == maxListedLines {
			fmt.Fprintf(b, "\n... %d more\n", len(fs)-i)
			break
		}
		f := &fs[i]
		label := findingLabel(f.Kind, provider)
		if f.Link > 0 {
			label += fmt.Sprintf(" (AniList %d links it)", f.Link)
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n", anidbLabel(f.Episode), dash(f.Episode.AirDate),
			targetLabel(f.Target), dash(f.Target.AirDate), label, carriersLabel(f.Carriers))
	}
	b.WriteString("\n")
}

func anidbLabel(e datecheck.Episode) string {
	switch {
	case e.Extra:
		return fmt.Sprintf("S%d (extra)", e.Number)
	case e.Special:
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

func findingLabel(k datecheck.Kind, provider string) string {
	switch k {
	case datecheck.MissingTarget:
		return "not on " + provider
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

// joinAnd reads items as a list: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func dash(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
