package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/watch"
)

const (
	maxInputBytes  = 64 << 20
	maxListedLines = 200
	dashboardKey   = "watch:dashboard"
	unmappableKey  = "watch:unmappable"
	labelAnimap    = "animap"
	labelWatch     = "watch"
	failingKey     = "publish:failing"
	failingAfter   = 24 * time.Hour
	// unmappableReserve is room for the "more" line and the marker Render adds.
	unmappableReserve = 200
)

type workflowRun struct {
	CreatedAt  time.Time `json:"createdAt"`
	Conclusion string    `json:"conclusion"`
	Status     string    `json:"status"`
	URL        string    `json:"url"`
}

type issueInputs struct {
	evaluated map[string]bool
	watch     *watchResult
	wants     []issues.Want
}

func runIssues(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("issues", flag.ContinueOnError)
	driftPath := fs.String("drift", "", "drift.json from animap drift")
	watchPath := fs.String("watch", "", "watch.json from animap watch")
	runsPath := fs.String("runs", "", "gh run list --json conclusion,status,createdAt,url for publish.yaml")
	existingPath := fs.String("existing", "", "gh issue list --json number,state,title,body")
	limit := fs.Int("limit", 10, "most creates and reopens per run")
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
	// The order is the order the create limit is spent in: publish health
	// and overlay drift are never queued behind the watch set's backlog.
	in := &issueInputs{evaluated: map[string]bool{}}
	if err := in.addRuns(*runsPath, time.Now()); err != nil {
		return err
	}
	if err := in.addDrift(*driftPath, existing); err != nil {
		return err
	}
	if err := in.addWatch(*watchPath, existing); err != nil {
		return err
	}
	actions, deferred := issues.Plan(in.wants, in.evaluated, existing, *limit)
	if in.watch != nil && in.watch.Complete {
		dash, _ := issues.Plan([]issues.Want{dashboardWant(in.watch, deferred)}, nil, existing, 1)
		actions = append(actions, dash...)
	}
	return writeJSON(ctx, *out, actions)
}

// addDrift also evaluates, after a complete run, every drift issue whose
// entry or row no longer exists, so deleting one closes its issues.
func (in *issueInputs) addDrift(path string, existing []issues.Issue) error {
	if path == "" {
		return nil
	}
	var d driftResult
	if err := readJSON(path, &d); err != nil {
		return err
	}
	for _, k := range d.Evaluated {
		in.evaluated[k] = true
	}
	if d.Complete {
		owned := make(map[string]bool, len(d.Owned))
		for _, k := range d.Owned {
			owned[k] = true
		}
		for _, is := range existing {
			if k := issues.KeyOf(is.Body); strings.HasPrefix(k, drift.Prefix) && !owned[k] {
				in.evaluated[k] = true
			}
		}
	}
	for i := range d.Findings {
		in.wants = append(in.wants, driftWant(&d.Findings[i]))
	}
	return nil
}

// addWatch evaluates every open watch issue only when the listing was
// complete: an incomplete run has no authority to close anything.
func (in *issueInputs) addWatch(path string, existing []issues.Issue) error {
	if path == "" {
		return nil
	}
	in.watch = &watchResult{}
	if err := readJSON(path, in.watch); err != nil {
		return err
	}
	if !in.watch.Complete {
		return nil
	}
	for _, is := range existing {
		if k := issues.KeyOf(is.Body); strings.HasPrefix(k, "watch:") && k != dashboardKey && k != unmappableKey {
			in.evaluated[k] = true
		}
	}
	in.evaluated[unmappableKey] = true
	if w, ok := unmappableWant(&in.watch.Classification); ok {
		in.wants = append(in.wants, w)
	}
	for i := range in.watch.Classification.New {
		in.wants = append(in.wants, watchWant(&in.watch.Classification.New[i], in.watch.EntryURL))
	}
	return nil
}

func (in *issueInputs) addRuns(path string, now time.Time) error {
	if path == "" {
		return nil
	}
	var runs []workflowRun
	if err := readJSON(path, &runs); err != nil {
		return err
	}
	in.evaluated[failingKey] = true
	if w, ok := failingWant(runs, now); ok {
		in.wants = append(in.wants, w)
	}
	return nil
}

func driftWant(f *drift.Finding) issues.Want {
	id := f.AniDB
	file := f.Path
	subject := fmt.Sprintf("AniDB %d", id)
	if f.AniList > 0 {
		subject = fmt.Sprintf("AniList %d (specials of AniDB %d)", f.AniList, id)
	}
	var title string
	var b strings.Builder
	switch f.Cause {
	case drift.Landed:
		title = fmt.Sprintf("overlay: %s fix landed upstream, delete the entry", subject)
		if f.AniList > 0 {
			fmt.Fprintf(&b, "anime-offline-database now links AniList %d to AniDB %d, so `%s` is no longer needed.\n\n", f.AniList, f.Linked, file)
		} else {
			fmt.Fprintf(&b, "Anime-Lists now carries every value `%s` sets, so the entry has nothing left to patch.\n\n", file)
		}
		fmt.Fprintf(&b, "Delete it with:\n\n```sh\ngit rm %s\n```\n", file)
	case drift.NodeChanged:
		title = fmt.Sprintf("overlay: %s, the Anime-Lists node changed", subject)
		fmt.Fprintf(&b, "The Anime-Lists node that `%s` patches changed since the entry was captured. The entry still applies.\n\n", file)
		b.WriteString("Review it against the new node. Then either delete the entry, fix it, or re-capture it with `animap overlay capture`.\n")
	case drift.TVDBLayout:
		title = fmt.Sprintf("overlay: %s, the TVDB episode layout changed", subject)
		fmt.Fprintf(&b, "TVDB's official order changed on the seasons `%s` maps onto. The entry still applies.\n\n", file)
		if f.After == nil {
			b.WriteString("SkyHook no longer serves this series.\n\n")
		}
		writeEpisodes(&b, "Before (captured)", f.Before)
		if f.After != nil {
			writeEpisodes(&b, "After (now)", f.After)
		}
	case drift.CountInDatabase:
		title = fmt.Sprintf("counts: AniDB %d now in anime-offline-database", id)
		writeCountBody(&b, f)
	}
	if f.Title != "" {
		fmt.Fprintf(&b, "\nEntry: %s", issues.Untrusted(f.Title))
	}
	fmt.Fprintf(&b, "\nAniDB: https://anidb.net/anime/%d\n", id)
	return issues.Want{Key: f.Key, Title: title, Body: b.String(), Labels: []string{labelAnimap, "drift"}}
}

func writeCountBody(b *strings.Builder, f *drift.Finding) {
	fmt.Fprintf(b, "anime-offline-database now carries AniDB %d, which `%s` counts at %d regular episodes.", f.AniDB, f.Path, f.Counted)
	switch f.DatabaseEpisodes {
	case 0:
		b.WriteString(" The database gives it no episode count yet, so keep the row: without it the collision check cannot place the node. This issue updates once the database counts it.\n")
		return
	case f.Counted:
		fmt.Fprintf(b, " The database counts %d too, so the two agree.\n\n", f.DatabaseEpisodes)
	default:
		fmt.Fprintf(b, " The database counts %d, so the two disagree. Check AniDB's episode list before removing the row, because the collision check then uses the database's count.\n\n", f.DatabaseEpisodes)
	}
	fmt.Fprintf(b, "Remove the row from `%s`. This issue closes on the first run after.\n", f.Path)
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

func watchWant(e *watch.Entry, entryURL string) issues.Want {
	kinds := make([]string, 0, len(e.Gaps))
	for _, g := range e.Gaps {
		kinds = append(kinds, string(g))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "AniList %d (%s) is watched and not fully mapped: `%s`.\n\n", e.AniListID, typeLabel(e.Type), strings.Join(kinds, "`, `"))
	b.WriteString("docs/watch.md defines each gap. Fix it with an overlay entry that meets the accuracy bar, and send the same change to Anime-Lists.\n\n")
	fmt.Fprintf(&b, "- AniList: https://anilist.co/anime/%d\n", e.AniListID)
	fmt.Fprintf(&b, "- SeaDex: %s\n", strings.ReplaceAll(entryURL, "{id}", strconv.Itoa(e.AniListID)))
	if e.AniDBID > 0 {
		fmt.Fprintf(&b, "- AniDB: https://anidb.net/anime/%d\n", e.AniDBID)
	}
	if e.TVDBID > 0 {
		fmt.Fprintf(&b, "- TVDB: https://thetvdb.com/dereferrer/series/%d\n", e.TVDBID)
	}
	return issues.Want{
		Key:    fmt.Sprintf("watch:%d", e.AniListID),
		Title:  fmt.Sprintf("watch: AniList %d (%s) is not fully mapped", e.AniListID, typeLabel(e.Type)),
		Body:   b.String(),
		Labels: []string{labelAnimap, labelWatch},
	}
}

// unmappableWant is the one pinned issue listing the gaps recorded as
// unmappable, as a checklist: a ticked item is now fully mapped and can
// leave checks/unmappable.json. It stays open while the list has entries.
// Ticked items come first and the list stops short of issues.MaxBodyRunes,
// so the items that ask for an edit and the "more" line are never cut.
func unmappableWant(c *watch.Classification) (issues.Want, bool) {
	if len(c.Unmappable)+len(c.Cleared) == 0 {
		return issues.Want{}, false
	}
	var b strings.Builder
	b.WriteString("These watched entries cannot be mapped from the sources animap reads. Each one lists why. `checks/unmappable.json` holds the list, and a run that finds a new gap opens its own issue instead.\n\n")
	items := make([]string, 0, len(c.Unmappable)+len(c.Cleared))
	for i := range c.Cleared {
		e := &c.Cleared[i]
		items = append(items, fmt.Sprintf("- [x] AniList %d (%s): fully mapped now, remove it from `checks/unmappable.json`", e.AniListID, typeLabel(e.Type)))
	}
	for i := range c.Unmappable {
		e := &c.Unmappable[i]
		items = append(items, fmt.Sprintf("- [ ] AniList %d (%s): %s. %s", e.AniListID, typeLabel(e.Type), e.Reason, issues.Untrusted(e.Detail)))
	}
	budget := issues.MaxBodyRunes - utf8.RuneCountInString(b.String()) - unmappableReserve
	for i, it := range items {
		if budget -= utf8.RuneCountInString(it) + 1; budget < 0 {
			fmt.Fprintf(&b, "- ... %d more, see `checks/unmappable.json`\n", len(items)-i)
			break
		}
		b.WriteString(it)
		b.WriteByte('\n')
	}
	return issues.Want{Key: unmappableKey, Title: "Unmappable SeaDex entries", Body: b.String(), Labels: []string{labelAnimap, labelWatch}, Pin: true}, true
}

// typeLabel lets only the published vocabulary into an issue, whatever
// wrote the watch result it came from.
func typeLabel(s string) string {
	switch {
	case s == "":
		return "no type"
	case schema.ValidType(s):
		return s
	}
	return "unrecognised type"
}

func dashboardWant(wr *watchResult, deferred []issues.Want) issues.Want {
	counts := map[string]int{}
	for i := range wr.Gaps {
		e := &wr.Gaps[i]
		for _, g := range e.Gaps {
			counts[typeLabel(e.Type)+" / "+string(g)]++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Watch set `%s`: %d ids watched, %d not fully mapped, %d of them in the backlog.\n\n",
		wr.Set, wr.Watched, len(wr.Gaps), len(wr.Classification.Known))
	b.WriteString("| Type / gap | Count |\n| --- | --- |\n")
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		fmt.Fprintf(&b, "| %s | %d |\n", k, counts[k])
	}
	if len(wr.Classification.Resolved) > 0 {
		b.WriteString("\nResolved, remove from `watch/backlog.json`:\n\n")
		for i := range wr.Classification.Resolved {
			fmt.Fprintf(&b, "- AniList %d\n", wr.Classification.Resolved[i].AniListID)
		}
	}
	if len(deferred) > 0 {
		b.WriteString("\nWaiting for an issue (over this run's limit):\n\n")
		for i, w := range deferred {
			if i == maxListedLines {
				fmt.Fprintf(&b, "- ... %d more\n", len(deferred)-i)
				break
			}
			fmt.Fprintf(&b, "- %s\n", w.Title)
		}
	}
	return issues.Want{Key: dashboardKey, Title: "watch: mapping gaps dashboard", Body: b.String(), Labels: []string{labelAnimap, labelWatch}}
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
