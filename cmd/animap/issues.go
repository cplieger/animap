package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/watch"
	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/runesafe/v3"
)

const (
	maxInputBytes  = 64 << 20
	maxListedLines = 200
	labelAnimap    = "animap"
	labelAnime     = "anime"
	failingKey     = "publish:failing"
	summaryKey     = "summary:review"
	failingAfter   = 24 * time.Hour
	// summaryReserve is room for the "more" line and the marker Render adds.
	summaryReserve = 200
)

type workflowRun struct {
	CreatedAt  time.Time `json:"createdAt"`
	Conclusion string    `json:"conclusion"`
	Status     string    `json:"status"`
	URL        string    `json:"url"`
}

func runIssues(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("issues", flag.ContinueOnError)
	reviewPath := fs.String("review", "", "review.json from animap review")
	runsPath := fs.String("runs", "", "gh run list --json conclusion,status,createdAt,url for publish.yaml")
	existingPath := fs.String("existing", "", "gh issue list --json number,state,title,body")
	limit := fs.Int("limit", 50, "most creates and reopens, edits, and closes per run")
	out := fs.String("out", "actions.json", "output path")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "existing"); err != nil {
		return err
	}
	var existing []issues.Issue
	if err := readJSON(*existingPath, &existing); err != nil {
		return err
	}
	doc, err := readReview(*reviewPath)
	if err != nil {
		return err
	}
	p := planner{doc: doc, open: openBodies(existing), evaluated: map[string]bool{}, closings: map[string]string{}}
	if err := p.addRuns(*runsPath); err != nil {
		return err
	}
	p.plan()
	slices.SortStableFunc(p.wants, func(a, b ranked) int { return cmp.Compare(a.rank, b.rank) })
	wants := make([]issues.Want, 0, len(p.wants))
	for i := range p.wants {
		wants = append(wants, p.wants[i].Want)
	}
	actions, deferred := issues.Plan(wants, p.evaluated, existing, *limit)
	for i := range actions {
		if c, ok := p.closings[actions[i].Key]; ok && actions[i].Kind == issues.Close {
			actions[i].Comment = c
		}
	}
	actions, held := capUpdates(actions, *limit)
	if len(deferred)+held > 0 {
		log.Info("issues: changes over this run's limit wait for a later run", "deferred", len(deferred), "held", held)
	}
	if doc != nil {
		summary, _ := issues.Plan([]issues.Want{summaryWant(doc, &p, deferred)}, nil, existing, 1)
		actions = append(actions, summary...)
	}
	return writeJSON(ctx, *out, actions)
}

// readReview reads review.json; no path is no review.
func readReview(path string) (*reviewDoc, error) {
	if path == "" {
		return nil, nil
	}
	doc := &reviewDoc{}
	if err := readJSON(path, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// capUpdates keeps at most limit edits and limit closes of a plan and
// returns how many it held back. A held edit still differs, and a held
// close is still due, on the next run, so a change that touches every
// issue spreads over several runs instead of one burst against GitHub's
// limits on content-creating requests:
// https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
func capUpdates(actions []issues.Action, limit int) (kept []issues.Action, held int) {
	left := map[issues.Kind]int{issues.Edit: limit, issues.Close: limit}
	for _, a := range actions {
		if n, capped := left[a.Kind]; capped {
			if n <= 0 {
				held++
				continue
			}
			left[a.Kind] = n - 1
		}
		kept = append(kept, a)
	}
	return kept, held
}

// clearedNow is why a section the run judged clear left its issue.
const clearedNow = "cleared on this run"

// The order creates are spent in: facts about publishing and animap's own
// data first, then facts about TVDB, then readings of two calendars, the
// watch, season 0 leftovers on TVDB, and last what only TMDB disagrees
// with.
const (
	rankPublish = iota
	rankDrift
	rankDead
	rankMissing
	rankDates
	rankWatch
	rankLeftovers
	rankTMDBOnly
)

type ranked struct {
	issues.Want
	rank int
}

// planner turns review.json into one want per subject with something to
// say. closings holds the close comment of each subject issue that clears.
type planner struct {
	doc       *reviewDoc
	open      map[string]string
	evaluated map[string]bool
	closings  map[string]string
	wants     []ranked
	// counts and stale feed the summary.
	counts map[string]int
	stale  []string
}

// addRuns plans the publish-health issue from the publish runs at path.
func (p *planner) addRuns(path string) error {
	if path == "" {
		return nil
	}
	var runs []workflowRun
	if err := readJSON(path, &runs); err != nil {
		return err
	}
	p.evaluated[failingKey] = true
	if w, ok := failingWant(runs, time.Now()); ok {
		p.wants = append(p.wants, ranked{Want: w, rank: rankPublish})
	}
	return nil
}

// openBodies indexes the open issues' bodies by key, the newest per key,
// as issues.Plan reads them.
func openBodies(existing []issues.Issue) map[string]string {
	out, num := map[string]string{}, map[string]int{}
	for _, is := range existing {
		k := issues.KeyOf(is.Body)
		if k == "" || !strings.EqualFold(is.State, "open") || is.Number < num[k] {
			continue
		}
		out[k], num[k] = is.Body, is.Number
	}
	return out
}

func (p *planner) plan() {
	if p.doc == nil {
		return
	}
	p.counts = map[string]int{}
	listed := map[string]bool{}
	for _, s := range p.doc.Subjects {
		listed[s.Key] = true
		p.subject(s)
	}
	for _, k := range slices.Sorted(maps.Keys(p.open)) {
		if _, ok := review.ParseKey(k); ok && !listed[k] {
			p.subject(&subject{Key: k})
		}
	}
}

// rendered is one section of a subject's body this run: its text, whether
// it raises something, and why it is clear when it is. A placeholder is a
// carried "not checked" line: it keeps the issue open but disagrees with
// nothing, so the title never names it.
type rendered struct {
	text        string
	clear       string
	rank        int
	raises      bool
	carried     bool
	placeholder bool
}

// subject plans one subject: a want when any section raises something or
// is carried over from the open issue, else a close when the open issue
// has one, with every section in its body evaluated and clear.
func (p *planner) subject(s *subject) {
	body := p.open[s.Key]
	carried := sectionsOf(body)
	parts := map[string]rendered{}
	for _, name := range review.Sections {
		if r, ok := p.section(s, name, carried[name]); ok {
			parts[name] = r
		}
	}
	p.noteStale(s)
	var raises, unchecked, cleared []string
	rank := rankTMDBOnly
	for _, name := range review.Sections {
		r, ok := parts[name]
		switch {
		case !ok:
		case r.placeholder:
			unchecked = append(unchecked, name)
			rank = min(rank, r.rank)
		case r.raises || r.carried:
			raises = append(raises, name)
			rank = min(rank, r.rank)
		case r.clear != "" && carried[name] != "":
			cleared = append(cleared, sourceName(name)+" "+r.clear)
		}
	}
	if len(raises)+len(unchecked) == 0 {
		if body != "" {
			p.evaluated[s.Key] = true
			p.closings[s.Key] = closeComment(cleared)
		}
		return
	}
	for _, name := range raises {
		if parts[name].raises {
			p.counts[name]++
		}
	}
	p.wants = append(p.wants, ranked{Want: subjectWant(s, parts, raises, unchecked), rank: rank})
}

// carry is a section copied from the open issue's body.
func carry(name, text string) rendered {
	return rendered{text: text, carried: true, rank: carriedRank(name), placeholder: strings.HasPrefix(text, placeholderPrefix(name))}
}

// placeholderPrefix opens the line section writes for a source that could
// not judge a subject the open issue does not hold yet.
func placeholderPrefix(name string) string {
	return sectionStart(name) + "\n" + sourceName(name) + ": not checked this run ("
}

// section renders one section of s, false when it has nothing to show and
// nothing to clear: not applicable, and not in the open issue's body.
func (p *planner) section(s *subject, name, carried string) (rendered, bool) {
	h := s.Sections.head(name)
	src := p.doc.Sources[name]
	switch {
	case h == nil && src != nil && src.Evaluated:
		return rendered{clear: clearedNow}, carried != ""
	case h == nil:
		if carried != "" {
			return carry(name, carried), true
		}
		return rendered{}, false
	case !h.Evaluated && carried != "":
		return carry(name, carried), true
	case !h.Evaluated:
		reason := h.Reason
		if reason == "" && src != nil {
			reason = src.UnevaluatedReason
		}
		return rendered{text: wrapSection(name, fmt.Sprintf("%s: not checked this run (%s).\n", sourceName(name), reason))}, h.Applicable
	}
	text, rank, why := renderEvaluated(s, name)
	if text == "" {
		return rendered{clear: why}, true
	}
	return rendered{text: wrapSection(name, text), raises: true, rank: rank}, true
}

// renderEvaluated is an evaluated section's text, "" when it is clear,
// with why it is clear.
func renderEvaluated(s *subject, name string) (text string, rank int, why string) {
	switch name {
	case review.DateTVDB, review.DateTMDB:
		d := s.Sections.date(name)
		if !d.raised() {
			return "", 0, clearedNow
		}
		if m := d.Acceptance; m != nil && m.Matches {
			return "", 0, "is covered by " + partRef(m)
		}
		return dateText(s, name, d), dateRank(name, d), ""
	case review.Leftovers:
		return renderLeftovers(s)
	case review.Drift:
		if len(s.Sections.Drift.Findings) == 0 {
			return "", 0, clearedNow
		}
		return driftText(s.Sections.Drift.Findings), rankDrift, ""
	}
	return renderWatch(s)
}

func renderLeftovers(s *subject) (text string, rank int, why string) {
	l := s.Sections.Leftovers
	if !l.raised() {
		return "", 0, clearedNow
	}
	if m := l.Acceptance; m != nil && m.Matches {
		return "", 0, "is covered by " + partRef(m)
	}
	return leftoversText(s, l), leftoversRank(l), ""
}

func renderWatch(s *subject) (text string, rank int, why string) {
	w := s.Sections.Watch
	var open []watchFinding
	var covered []string
	for i := range w.Findings {
		if m := w.Findings[i].Acceptance; m != nil && m.Matches {
			covered = append(covered, partRef(m))
			continue
		}
		open = append(open, w.Findings[i])
	}
	if len(open) == 0 {
		if len(covered) > 0 {
			return "", 0, "is covered by " + strings.Join(covered, ", ")
		}
		return "", 0, clearedNow
	}
	return watchText(s.Key, open, w.EntryURL), rankWatch, ""
}

func dateRank(name string, d *dateSection) int {
	switch {
	case name == review.DateTMDB:
		return rankTMDBOnly
	case d.Dead:
		return rankDead
	case slices.ContainsFunc(d.Findings, func(f datecheck.Finding) bool { return f.Kind == datecheck.MissingTarget }):
		return rankMissing
	}
	return rankDates
}

func carriedRank(name string) int {
	switch name {
	case review.Drift:
		return rankDrift
	case review.Watch:
		return rankWatch
	case review.Leftovers:
		return rankLeftovers
	case review.DateTMDB:
		return rankTMDBOnly
	}
	return rankDates
}

// noteStale lists, for the summary, every acceptance part on s whose
// section is evaluated and would be clear without it.
func (p *planner) noteStale(s *subject) {
	for _, name := range []string{review.DateTVDB, review.DateTMDB} {
		if d := s.Sections.date(name); d != nil && d.Acceptance != nil && d.Evaluated && !d.raised() {
			p.stale = append(p.stale, partRef(d.Acceptance))
		}
	}
	if l := s.Sections.Leftovers; l != nil && l.Acceptance != nil && l.Evaluated && !l.raised() {
		p.stale = append(p.stale, partRef(l.Acceptance))
	}
	if w := s.Sections.Watch; w != nil && w.Evaluated {
		for i := range w.Unclaimed {
			p.stale = append(p.stale, partRef(&w.Unclaimed[i]))
		}
	}
}

// partRef names an acceptance part as a person finds it: its file and part.
func partRef(m *review.Match) string { return fmt.Sprintf("%#q %#q", "reviewed/"+m.File, m.Part) }

func sourceName(section string) string {
	switch section {
	case review.DateTVDB:
		return "TVDB dates"
	case review.DateTMDB:
		return "TMDB dates"
	case review.Leftovers:
		return "Season 0 leftovers"
	case review.Drift:
		return "Overlay drift"
	}
	return "SeaDex watch"
}

const sectionEnd = "<!-- /animap:section -->"

func sectionStart(name string) string { return "<!-- animap:section=" + name + " -->" }

func wrapSection(name, text string) string {
	return sectionStart(name) + "\n" + text + sectionEnd + "\n"
}

var sectionRE = regexp.MustCompile(`(?s)<!-- animap:section=([a-z-]+) -->\n.*?<!-- /animap:section -->\n`)

// sectionsOf is each section of an issue body, verbatim with its markers.
func sectionsOf(body string) map[string]string {
	out := map[string]string{}
	for _, m := range sectionRE.FindAllStringSubmatch(body, -1) {
		if slices.Contains(review.Sections, m[1]) {
			out[m[1]] = m[0]
		}
	}
	return out
}

func closeComment(cleared []string) string {
	if len(cleared) == 0 {
		return "Every section this issue raised is clear on this run, so it closes. It opens again when a source raises something new."
	}
	return "Every section is clear: " + strings.Join(cleared, "; ") + ". So this closes. It opens again when a source raises something new."
}

// subjectWant is the one issue of a subject: its sections in a fixed
// order, the title naming the sources that disagree, or the ones that
// could not check it when none does.
func subjectWant(s *subject, parts map[string]rendered, raises, unchecked []string) issues.Want {
	sub, _ := review.ParseKey(s.Key)
	var b strings.Builder
	fmt.Fprintf(&b, "The daily review found something to check on %s. Each section below names its source; the issue closes on its own once every section is clear.\n\n", sub.Label())
	if sub.AniDB() {
		fmt.Fprintf(&b, "- AniDB: https://anidb.net/anime/%d\n\n", sub.ID)
	} else {
		fmt.Fprintf(&b, "- AniList: https://anilist.co/anime/%d\n\n", sub.ID)
	}
	var node, overlay string
	for _, name := range review.Sections {
		r, ok := parts[name]
		if !ok || r.text == "" {
			continue
		}
		b.WriteString(r.text)
		b.WriteByte('\n')
		switch {
		case !r.raises || node != "":
		case name == review.DateTVDB || name == review.DateTMDB:
			node, overlay = s.Sections.date(name).Node, s.Sections.date(name).Overlay
		case name == review.Leftovers:
			node, overlay = s.Sections.Leftovers.Node, s.Sections.Leftovers.Overlay
		}
	}
	if node != "" {
		writeNode(&b, node, overlay)
	}
	b.WriteString("docs/review.md explains each section, how to accept a finding that is right, and why closing this by hand does not stick.\n")
	title := subjectTitle(sub, s.Title, raises, "")
	if len(raises) == 0 {
		title = subjectTitle(sub, s.Title, unchecked, " not checked")
	}
	return issues.Want{Key: s.Key, Title: title, Body: b.String(), Labels: []string{labelAnimap, labelAnime}}
}

// subjectTitle names the subject and its sections' sources, each followed
// by suffix.
func subjectTitle(sub review.Subject, name string, sections []string, suffix string) string {
	var dates []string
	var rest []string
	for _, n := range sections {
		switch n {
		case review.DateTVDB:
			dates = append(dates, "TVDB")
		case review.DateTMDB:
			dates = append(dates, tmdbName)
		case review.Leftovers:
			rest = append(rest, "season 0 leftovers")
		case review.Drift:
			rest = append(rest, "overlay drift")
		case review.Watch:
			rest = append(rest, "SeaDex watch gap")
		}
	}
	var sources []string
	if len(dates) > 0 {
		sources = append(sources, strings.Join(dates, " and ")+" dates"+suffix)
	}
	for _, r := range rest {
		sources = append(sources, r+suffix)
	}
	t := "anime: " + sub.Label()
	// An @ is rewritten so no upstream name reads as a mention.
	name = strings.ReplaceAll(runesafe.SanitizeSingleLineBounded(name, 80), "@", "\uff20")
	if strings.TrimSpace(name) != "" {
		t += " " + strings.TrimSpace(name)
	}
	return t + ", " + strings.Join(sources, ", ")
}

// untrusted is issues.Untrusted, with a section marker in the text broken
// so it cannot end a section early.
func untrusted(s string) string {
	return issues.Untrusted(strings.ReplaceAll(s, "<!--", "<! --"))
}

func driftText(fs []drift.Finding) string {
	var b strings.Builder
	b.WriteString("### Overlay drift\n\n")
	for i := range fs {
		writeDrift(&b, &fs[i])
	}
	return b.String()
}

var driftHeading = map[string]string{
	"landed": "fix landed upstream, delete it", "node-changed": "the Anime-Lists node changed",
	"tvdb-layout": "the TVDB episode layout changed", "anidb-changed": "an AniDB episode it links changed",
}

func writeDrift(b *strings.Builder, f *drift.Finding) {
	file := f.Path
	fmt.Fprintf(b, "#### `%s`: %s\n\n", file, driftHeading[string(f.Cause)])
	switch f.Cause {
	case drift.Landed:
		if f.AniList > 0 {
			fmt.Fprintf(b, "anime-offline-database now links AniList %d to AniDB %d, so `%s` is no longer needed.\n\n", f.AniList, f.Linked, file)
		} else {
			fmt.Fprintf(b, "Anime-Lists now carries every value `%s` sets, so the entry has nothing left to patch.\n\n", file)
		}
		fmt.Fprintf(b, "Delete it with:\n\n```sh\ngit rm %s\n```\n\n", file)
	case drift.NodeChanged:
		fmt.Fprintf(b, "The Anime-Lists node that `%s` patches changed since the entry was captured. The entry still applies.\n\n", file)
		b.WriteString("Review it against the new node. Then either delete the entry, fix it, or re-capture it with `animap overlay capture`.\n\n")
	case drift.TVDBLayout:
		writeLayoutChange(b, f)
	case drift.AniDBChanged:
		fmt.Fprintf(b, "The AniDB mirror at %s changed the number or titles of an episode `%s` links. ", f.Mirror, file)
		b.WriteString("Re-prove the link by title, then re-capture it with `animap overlay capture-link`.\n\n")
	}
	if f.Title != "" {
		fmt.Fprintf(b, "Entry: %s\n", untrusted(f.Title))
	}
	for _, id := range f.AniDBIDs {
		fmt.Fprintf(b, "AniDB: https://anidb.net/anime/%d\n", id)
	}
	b.WriteByte('\n')
}

func writeLayoutChange(b *strings.Builder, f *drift.Finding) {
	fmt.Fprintf(b, "TVDB's official order changed on the seasons `%s` maps onto. The entry still applies.\n\n", f.Path)
	if f.Gone {
		b.WriteString("SkyHook no longer serves this series.\n\n")
	}
	writeEpisodes(b, "Before (captured)", f.Before)
	if !f.Gone {
		writeEpisodes(b, "After (now)", f.After)
	}
}

func writeEpisodes(b *strings.Builder, heading string, eps []skyhook.Episode) {
	fmt.Fprintf(b, "%s:\n\n```text\n", heading)
	for i, e := range eps {
		if i == maxListedLines {
			fmt.Fprintf(b, "... %d more\n", len(eps)-i)
			break
		}
		b.WriteString(e.Line())
		b.WriteByte('\n')
	}
	b.WriteString("```\n\n")
}

func watchText(key string, fs []watchFinding, entryURL string) string {
	var b strings.Builder
	b.WriteString("### SeaDex watch\n\n")
	b.WriteString("These watched records are not fully mapped: seadex-scout cannot route them to Sonarr or Radarr, or cannot scope their episodes there. docs/watch.md defines each gap. Fix one with an overlay entry or link that meets the accuracy bar, and send the same change to Anime-Lists.\n\n")
	for i := range fs {
		e := &fs[i]
		fmt.Fprintf(&b, "- AniList %d (%s): `%s`. https://anilist.co/anime/%d, SeaDex %s", e.AniListID, typeLabel(e.Type),
			strings.Join(e.GapNames(), "`, `"), e.AniListID, watch.EntryLink(entryURL, e.AniListID))
		if e.TVDBID > 0 {
			b.WriteString(", TVDB " + tvdbSeriesPage(e.TVDBID))
		}
		b.WriteString("\n")
		if m := e.Acceptance; m != nil {
			fmt.Fprintf(&b, "  - %s accepted this record's gaps, and they are raised again because %s.\n", partRef(m), causesText(m.Causes))
		}
	}
	b.WriteString("\nIf no source carries the work, or the published format cannot say it, record that from the run's `review.json` (docs/review.md):\n\n```sh\n")
	fmt.Fprintf(&b, "go run ./cmd/animap accept -from review.json -key %s -part watch -anilist %d -verdict unmappable -reason \"<why no source carries it>\" -evidence https://anilist.co/anime/%d\n", key, fs[0].AniListID, fs[0].AniListID)
	b.WriteString("```\n\n")
	return b.String()
}

// causesText reads acceptance causes as a clause.
func causesText(causes []string) string {
	parts := make([]string, 0, len(causes))
	for _, c := range causes {
		kind, k, _ := strings.Cut(c, ":")
		switch kind {
		case "finding-inputs":
			parts = append(parts, fmt.Sprintf("what %#q rests on changed", k))
		case "finding":
			parts = append(parts, fmt.Sprintf("%#q is new", k))
		case "record":
			parts = append(parts, "the published record changed")
		case "gap":
			parts = append(parts, fmt.Sprintf("gap %#q is new", k))
		}
	}
	if len(parts) == 0 {
		return "the run could not match it"
	}
	if len(parts) > maxListedLines {
		parts = append(parts[:maxListedLines], fmt.Sprintf("%d more", len(parts)-maxListedLines))
	}
	return joinAnd(parts)
}

// typeLabel lets only the published vocabulary into an issue, whatever
// wrote the review it came from.
func typeLabel(s string) string {
	switch {
	case s == "":
		return "no type"
	case mapread.ValidType(s):
		return s
	}
	return "unrecognised type"
}

// summaryWant is the pinned issue over every subject: each source's
// health, what is raised, what waits for an issue, the stale acceptance
// parts to delete, and the accepted parts.
func summaryWant(doc *reviewDoc, p *planner, deferred []issues.Want) issues.Want {
	var b strings.Builder
	b.WriteString("The daily review checks every anime against TVDB and TMDB dates, season 0 leftovers, overlay drift and the SeaDex watch, and keeps one issue per anime. docs/review.md explains each part.\n\n")
	if t := doc.Sources[review.DateTMDB]; t != nil && !t.Evaluated {
		fmt.Fprintf(&b, "> TMDB dates are not checked: %s.\n\n", t.UnevaluatedReason)
	}
	b.WriteString("| Source | Checked | Failed | Stale | Note |\n| --- | --- | --- | --- | --- |\n")
	for _, name := range review.Sections {
		h := doc.Sources[name]
		if h == nil {
			continue
		}
		checked := "yes"
		if !h.Evaluated {
			checked = "no"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s |\n", sourceName(name), checked, h.Failed, h.Stale, dash(h.UnevaluatedReason))
	}
	b.WriteString("\n| Raised | Anime |\n| --- | --- |\n")
	for _, name := range review.Sections {
		fmt.Fprintf(&b, "| %s | %d |\n", sourceName(name), p.counts[name])
	}
	b.WriteString("\n")
	var items []string
	for _, st := range p.stale {
		items = append(items, "- [ ] "+st+": its section is clear without it, delete the part")
	}
	for i, w := range deferred {
		if i == maxListedLines {
			items = append(items, fmt.Sprintf("- ... %d more waiting", len(deferred)-i))
			break
		}
		items = append(items, "- waiting for an issue (over this run's limit): "+w.Title)
	}
	items = append(items, acceptedItems(doc)...)
	budget := issues.MaxBodyRunes - utf8.RuneCountInString(b.String()) - summaryReserve
	for i, it := range items {
		if budget -= utf8.RuneCountInString(it) + 1; budget < 0 {
			fmt.Fprintf(&b, "- ... %d more, see the run's review.json\n", len(items)-i)
			break
		}
		b.WriteString(it)
		b.WriteByte('\n')
	}
	return issues.Want{Key: summaryKey, Title: "Daily review: summary", Body: b.String(), Labels: []string{labelAnimap}, Pin: true}
}

// acceptedItems counts the date and leftovers parts that covered a
// finding this run by verdict, and lists the watch parts, the old
// unmappable list, each with its reason. A date or leftovers part's reason
// names episode titles, which issues never carry, so it stays in its file.
func acceptedItems(doc *reviewDoc) []string {
	dates, pairs := map[string]int{}, map[string]int{}
	byVerdict := map[string][]string{}
	for _, s := range doc.Subjects {
		for _, name := range []string{review.DateTVDB, review.DateTMDB} {
			if d := s.Sections.date(name); d != nil && d.raised() && d.Acceptance != nil && d.Acceptance.Matches {
				dates[d.Acceptance.Verdict]++
			}
		}
		if l := s.Sections.Leftovers; l != nil && l.raised() && l.Acceptance != nil && l.Acceptance.Matches {
			pairs[l.Acceptance.Verdict]++
		}
		watchAccepted(s.Sections.Watch, byVerdict)
	}
	var out []string
	for _, v := range slices.Sorted(maps.Keys(dates)) {
		out = append(out, fmt.Sprintf("- date findings accepted as %s: %d anime sections", v, dates[v]))
	}
	for _, v := range slices.Sorted(maps.Keys(pairs)) {
		out = append(out, fmt.Sprintf("- season 0 leftovers accepted as %s: %d anime", v, pairs[v]))
	}
	for _, v := range slices.Sorted(maps.Keys(byVerdict)) {
		out = append(out, fmt.Sprintf("- watch gaps accepted as %s (%d):", v, len(byVerdict[v])))
		out = append(out, byVerdict[v]...)
	}
	return out
}

func watchAccepted(w *watchSection, byVerdict map[string][]string) {
	if w == nil {
		return
	}
	for i := range w.Findings {
		if m := w.Findings[i].Acceptance; m != nil && m.Matches {
			byVerdict[m.Verdict] = append(byVerdict[m.Verdict], fmt.Sprintf("  - %s (%s): %s", partRef(m), strings.Join(w.Findings[i].GapNames(), ", "), untrusted(m.Reason)))
		}
	}
}

// failingWant wants an issue when the newest finished publish failed and no
// publish has succeeded for failingAfter.
func failingWant(runs []workflowRun, now time.Time) (issues.Want, bool) {
	var done []workflowRun
	for _, r := range runs {
		if r.Status == "completed" {
			done = append(done, r)
		}
	}
	if len(done) == 0 {
		return issues.Want{}, false
	}
	slices.SortFunc(done, func(a, b workflowRun) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if done[0].Conclusion != "failure" {
		return issues.Want{}, false
	}
	since := done[len(done)-1].CreatedAt
	for _, r := range done {
		if r.Conclusion == "success" {
			since = r.CreatedAt
			break
		}
	}
	if now.Sub(since) < failingAfter {
		return issues.Want{}, false
	}
	body := fmt.Sprintf("Publishing has not succeeded since %s, so the latest release is going stale. The previous release stays the latest.\n\nNewest failed run: %s\n",
		since.UTC().Format(time.RFC3339), done[0].URL)
	return issues.Want{Key: failingKey, Title: "publish: animap.json has not published for over a day", Body: body, Labels: []string{labelAnimap, "publish"}}, true
}
