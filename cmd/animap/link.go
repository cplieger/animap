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
	"github.com/cplieger/animap/internal/schema"
)

// provenLinks are the overlay links as the build applies them: the
// identity links the join folds in, and every other link compiled.
type provenLinks struct {
	compiled   []overlay.Compiled
	linked     []join.Linked
	identities []join.Identity
}

// proveLinks checks every link against the pinned mirror and the patched
// list: each source is listed with its kind, every non-identity link
// compiles, no AniDB episode is in two links, no TVDB episode is taken by
// two linked AniList episodes, and an identity link's AniDB id is not one
// anime-offline-database already gives another AniList id. guard.Collisions
// cannot see the last two: both claims come from one node, or from none.
func proveLinks(links []overlay.Link, patched map[int]*animelists.Node, entries []overlay.Entry, facts *anidb.Facts, aod []offlinedb.Entry) (provenLinks, error) {
	var out provenLinks
	var errs []error
	byAniDB := make(map[int]*overlay.Entry, len(entries))
	for i := range entries {
		byAniDB[entries[i].AniDBID] = &entries[i]
	}
	carriers := join.Carriers(aod)
	sources := map[[3]int]int{}
	targets := map[[3]int]int{}
	for i := range links {
		l := &links[i]
		if err := l.Listed(facts); err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, claimSources(l, facts, sources)...)
		if aid, ok := l.Identity(facts); ok {
			if other := otherCarrier(carriers[aid], l.AniListID); other > 0 {
				errs = append(errs, fmt.Errorf("AniList %d: anime-offline-database already gives AniDB %d to AniList %d", l.AniListID, aid, other))
				continue
			}
			out.identities = append(out.identities, join.Identity{AniList: l.AniListID, AniDB: aid})
			continue
		}
		c, err := l.Compile(patched, byAniDB, facts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, claimTargets(&c, targets)...)
		out.compiled = append(out.compiled, c)
		out.linked = append(out.linked, linkedOf(l, &c))
	}
	return out, errors.Join(errs...)
}

// otherCarrier is the first of carriers that is not al, or 0. al carrying
// the id itself is the landed case join.Build skips softly.
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

// linkedOf states anidb_parent only for season-zero episodes of one anime,
// the shape its published form holds.
func linkedOf(l *overlay.Link, c *overlay.Compiled) join.Linked {
	j := join.Linked{AniList: c.AniList, TVDBID: c.Series, Season: c.Season, Targets: c.Targets}
	if p := l.Parts[0]; len(l.Parts) == 1 && len(p.Specials) > 0 {
		j.Specials = &schema.ParentSpecials{AniDBID: p.AniDBID, Specials: p.Specials}
	}
	return j
}

func runCaptureLink(ctx context.Context, args []string, _ *slog.Logger) error {
	fs := flag.NewFlagSet("overlay capture-link", flag.ContinueOnError)
	path := fs.String("link", "", "overlay/links/<anilistid>.json to update in place")
	mirrorPath := fs.String("mirror", "", "AniDB mirror snapshot, for the linked episodes")
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
	snap, err := anidb.LoadSnapshot(*mirrorPath)
	if err != nil {
		return err
	}
	out, err := captureLink(&l, anidb.NewFacts(snap, nil, nil), time.Now().UTC())
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
