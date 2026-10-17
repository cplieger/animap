package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/join"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
)

// provenLinks are the overlay links as join.Applicable classes them: the
// applied ones and the skipped ones, with why.
type provenLinks struct {
	applied join.Applied
	skipped []join.Skipped
}

// classifyLinks proves each link against the pinned mirror and the patched
// list and classes the proven ones (join.Applicable), the one place a
// link's applicability is decided. Each refusal says why a link is not
// proven: a source the mirror does not list, an identity to an AniDB id
// anime-offline-database gives another AniList id, or a link that does
// not compile; an unproven link is in neither class.
func classifyLinks(links []overlay.Link, patched map[int]*animelists.Node, entries []overlay.Entry, facts *anidb.Facts, aod []offlinedb.Entry) (provenLinks, []error) {
	var errs []error
	byAniDB := make(map[int]*overlay.Entry, len(entries))
	for i := range entries {
		byAniDB[entries[i].AniDBID] = &entries[i]
	}
	carriers := join.Carriers(aod)
	var ids []join.Identity
	var linked []join.Linked
	for i := range links {
		l := &links[i]
		if err := l.Listed(facts); err != nil {
			errs = append(errs, err)
			continue
		}
		if aid, ok := l.Identity(facts); ok {
			if other := otherCarrier(carriers[aid], l.AniListID); other > 0 {
				errs = append(errs, fmt.Errorf("AniList %d: anime-offline-database already gives AniDB %d to AniList %d", l.AniListID, aid, other))
				continue
			}
			ids = append(ids, join.Identity{Link: l, AniDB: aid})
			continue
		}
		c, err := l.Compile(patched, byAniDB, facts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		linked = append(linked, join.Linked{Link: l, Compiled: c})
	}
	var out provenLinks
	out.applied, out.skipped = join.Applicable(aod, ids, linked)
	return out, errs
}

// proveLinks classes links and refuses every unproven one, an AniDB
// episode two applied links name, and a TVDB episode two applied linked
// AniList episodes take. guard.Collisions cannot see the last two: both
// claims come from one node, or from none. A skipped link claims nothing,
// so it cannot block an applied one.
func proveLinks(links []overlay.Link, patched map[int]*animelists.Node, entries []overlay.Entry, facts *anidb.Facts, aod []offlinedb.Entry) (provenLinks, error) {
	out, errs := classifyLinks(links, patched, entries, facts, aod)
	sources := map[[3]int]int{}
	for _, l := range out.applied.Links() {
		errs = append(errs, claimSources(l, facts, sources)...)
	}
	targets := map[[3]int]int{}
	compiled := out.applied.Compiled()
	for i := range compiled {
		errs = append(errs, claimTargets(&compiled[i], targets)...)
	}
	return out, errors.Join(errs...)
}

// otherCarrier is the first of carriers that is not al, or 0. al carrying
// the id itself is the landed case join.Applicable skips softly.
func otherCarrier(carriers []int, al int) int {
	for _, c := range carriers {
		if c != al {
			return c
		}
	}
	return 0
}

// claimSources records l's AniDB episodes in taken, refusing one another
// link already took.
func claimSources(l *overlay.Link, facts *anidb.Facts, taken map[[3]int]int) []error {
	var errs []error
	for _, s := range l.Sources(facts) {
		key := [3]int{s.AniDB, s.AniDBSeason, s.Number}
		if other, ok := taken[key]; ok {
			errs = append(errs, fmt.Errorf("AniList %d: AniDB %d %s is linked by AniList %d too", l.AniListID, s.AniDB, animelists.EpisodeLabel(s.AniDBSeason, s.Number), other))
			continue
		}
		taken[key] = l.AniListID
	}
	return errs
}

// claimTargets records c's TVDB episodes in taken, refusing one another
// link already took.
func claimTargets(c *overlay.Compiled, taken map[[3]int]int) []error {
	var errs []error
	for j, ep := range c.Targets {
		at := [3]int{c.Series, c.Season, ep}
		if other, ok := taken[at]; ok {
			errs = append(errs, fmt.Errorf("AniList %d episode %d lands on TVDB %d %dx%d, which AniList %d already takes", c.AniList, j+1, c.Series, c.Season, ep, other))
			continue
		}
		taken[at] = c.AniList
	}
	return errs
}

func runCaptureLink(ctx context.Context, args []string, _ *slog.Logger) error {
	fs := flag.NewFlagSet("overlay capture-link", flag.ContinueOnError)
	path := fs.String("link", "", "overlay/links/<anilistid>.json to update in place")
	mirrorPath := fs.String("mirror", "", "AniDB mirror snapshot, for the linked episodes")
	countsPath := fs.String("counts", anidb.CountsPath, "tracked episode counts, for a whole part (absent file: none)")
	aodPath := fs.String("aod", "", "anime-offline-database .jsonl, for a whole part's count; none counts from the rows and the mirror")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "link", "mirror"); err != nil {
		return err
	}
	l, err := overlay.ReadLink(*path)
	if err != nil {
		return fmt.Errorf("overlay capture-link: %w", err)
	}
	var aod []offlinedb.Entry
	if *aodPath != "" {
		if aod, err = offlinedb.Load(*aodPath, offlinedb.DefaultLimits); err != nil {
			return err
		}
	}
	facts, err := loadFacts(*mirrorPath, *countsPath, aod)
	if err != nil {
		return err
	}
	out, err := captureLink(&l, facts, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeFile(ctx, *path, out)
}

// captureLink records l's AniDB digest at facts and returns the file's new
// contents; every authored field stays as the file had it.
func captureLink(l *overlay.Link, facts *anidb.Facts, now time.Time) ([]byte, error) {
	if err := l.Listed(facts); err != nil {
		return nil, fmt.Errorf("overlay capture-link: %w", err)
	}
	l.Captured = overlay.LinkCaptured{At: now.Format(time.DateOnly), AniDBMirrorCommit: facts.Commit(), AniDBSHA256: l.AniDBDigest(facts)}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
