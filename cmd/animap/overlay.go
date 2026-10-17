package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
)

// pace is the minimum gap between two requests to one host.
const pace = time.Second

// aniDBByAniList is the AniDB ids anime-offline-database gives each
// AniList id, sorted.
func aniDBByAniList(entries []offlinedb.Entry) map[int][]int {
	out := map[int][]int{}
	for i := range entries {
		for _, al := range entries[i].AniList {
			for _, ad := range entries[i].AniDB {
				if !slices.Contains(out[al], ad) {
					out[al] = append(out[al], ad)
				}
			}
		}
	}
	for _, ids := range out {
		slices.Sort(ids)
	}
	return out
}

func loadOverlay(dir string) ([]overlay.Entry, []overlay.Link, error) {
	entries, err := overlay.LoadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	links, err := overlay.LoadLinks(dir)
	return entries, links, err
}

// runCapture is how an author writes an entry's fingerprints and how a
// reviewed drift is acknowledged.
func runCapture(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("overlay capture", flag.ContinueOnError)
	entryPath := fs.String("entry", "", "overlay/<anidbid>.json to update in place")
	listPath := fs.String("list", "", "anime-list-master.xml at -commit")
	commit := fs.String("commit", "", "Anime-Lists commit of -list")
	mirrorPath := fs.String("mirror", "", "AniDB mirror snapshot, for whether AniDB lists specials")
	cachePath := fs.String("cache", "", "HTTP validator cache file")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "entry", "list", "commit", "mirror"); err != nil {
		return err
	}
	snap, err := anidb.LoadSnapshot(*mirrorPath)
	if err != nil {
		return err
	}
	e, err := overlay.ReadEntry(*entryPath)
	if err != nil {
		return fmt.Errorf("overlay capture: %w", err)
	}
	list, err := readList(*listPath)
	if err != nil {
		return err
	}
	client, err := source.New(*cachePath, pace, log)
	if err != nil {
		return err
	}
	upstream := list.Nodes[e.AniDBID]
	tv, err := captureTVDB(ctx, client, overlay.Patch(upstream, &e), len(snap.Anime[e.AniDBID].Specials) > 0)
	if err != nil {
		return err
	}
	e.Captured = overlay.Captured{
		At: time.Now().UTC().Format(time.DateOnly), AnimeListsCommit: *commit, NodeSHA256: animelists.HashOf(upstream),
		TVDB: tv,
	}
	e.Create = upstream == nil
	if vErr := e.Validate(); vErr != nil {
		return vErr
	}
	if err = client.Save(ctx); err != nil {
		log.Warn("overlay capture: cache not saved", "error", err)
	}
	out, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(ctx, *entryPath, append(out, '\n'))
}

// captureTVDB returns nil for a node with no TVDB series, a film routed by
// TMDB alone.
func captureTVDB(ctx context.Context, client *source.Client, patched *animelists.Node, hasSpecials bool) (*overlay.CapturedTVDB, error) {
	series, err := seriesOf(patched)
	if errors.Is(err, errNoSeries) {
		return nil, nil
	}
	show, err := skyhook.Fetch(ctx, client, series)
	if err != nil {
		return nil, fmt.Errorf("overlay capture: SkyHook %d: %w", series, err)
	}
	seasons := overlay.TouchedSeasons(patched, hasSpecials)
	eps := skyhook.Layout(show, seasons)
	return &overlay.CapturedTVDB{Series: series, Seasons: seasons, Episodes: eps, SHA256: skyhook.Hash(eps)}, nil
}

var errNoSeries = errors.New("patched node has no TVDB series id")

func seriesOf(n *animelists.Node) (int, error) {
	var id int
	if n == nil {
		return 0, errNoSeries
	}
	if _, err := fmt.Sscanf(n.Attr("tvdbid"), "%d", &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("%w (tvdbid %q)", errNoSeries, n.Attr("tvdbid"))
	}
	return id, nil
}

func runCheck(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("overlay check", flag.ContinueOnError)
	overlayDir := fs.String("overlay", "overlay", "overlay directory")
	listPath := fs.String("list", "", "anime-list-master.xml")
	aodPath := fs.String("aod", "", "anime-offline-database .jsonl, for the episode counts the mirror does not decide")
	countsPath := fs.String("counts", anidb.CountsPath, "tracked episode counts")
	mirrorPath := fs.String("mirror", "", "AniDB mirror snapshot written by animap mirror extract")
	baselinePath := fs.String("baseline", "checks/collision-baseline.json", "tracked collision baseline")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "list", "aod", "mirror"); err != nil {
		return err
	}
	list, err := readList(*listPath)
	if err != nil {
		return err
	}
	entries, links, err := loadOverlay(*overlayDir)
	if err != nil {
		return err
	}
	aod, err := offlinedb.Load(*aodPath, offlinedb.DefaultLimits)
	if err != nil {
		return err
	}
	facts, err := loadFacts(*mirrorPath, *countsPath, aod)
	if err != nil {
		return err
	}
	proven, proofErr := proveLinks(links, overlay.Apply(list.Nodes, entries), entries, facts, aod)
	rep := guard.Collisions(list.Nodes, entries, proven.applied, facts)
	_, colErr := collisions(*baselinePath, &rep, slog.New(slog.DiscardHandler))
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "%s\n", b); err != nil {
		return err
	}
	return errors.Join(proofErr, colErr)
}
