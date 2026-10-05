package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
)

func runCaptureSpecial(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("overlay capture-special", flag.ContinueOnError)
	path := fs.String("bridge", "", "overlay/special-of-parent/<anilistid>.json to update in place")
	overlayDir := fs.String("overlay", "overlay", "overlay directory, whose entries patch the parent first")
	listPath := fs.String("list", "", "anime-list-master.xml at -commit")
	commit := fs.String("commit", "", "Anime-Lists commit of -list")
	cachePath := fs.String("cache", "", "HTTP validator cache file")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "bridge", "list", "commit"); err != nil {
		return err
	}
	b, parent, err := loadBridgeParent(*path, *listPath, *overlayDir)
	if err != nil {
		return err
	}
	series, err := seriesOf(parent)
	if err != nil {
		return fmt.Errorf("overlay capture-special: AniDB %d: %w", b.ParentAniDBID, err)
	}
	seasons, err := specialSeasons(parent, b.Specials)
	if err != nil {
		return err
	}
	client, err := source.New(*cachePath, pace, log)
	if err != nil {
		return err
	}
	show, err := skyhook.Fetch(ctx, client, series)
	if err != nil {
		return fmt.Errorf("overlay capture-special: SkyHook %d: %w", series, err)
	}
	if err = client.Save(ctx); err != nil {
		log.Warn("overlay capture-special: cache not saved", "error", err)
	}
	eps := skyhook.Layout(show, seasons)
	out, err := recapture(b, parent, *commit, &overlay.CapturedTVDB{Series: series, Seasons: seasons, Episodes: eps, SHA256: skyhook.Hash(eps)})
	if err != nil {
		return err
	}
	return writeFile(ctx, *path, out)
}

// recapture replaces b.Captured and leaves every authored field, the
// parent's AniDB specials among them, as the file had it. It returns the
// re-proven bridge's new file contents.
func recapture(b *overlay.Bridge, parent *animelists.Node, commit string, tv *overlay.CapturedTVDB) ([]byte, error) {
	b.Captured = overlay.BridgeCaptured{
		At: time.Now().UTC().Format(time.DateOnly), AnimeListsCommit: commit, NodeSHA256: parent.Hash(), TVDB: tv,
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	if err := b.Prove(parent); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func loadBridgeParent(path, listPath, overlayDir string) (*overlay.Bridge, *animelists.Node, error) {
	b, err := readBridge(path)
	if err != nil {
		return nil, nil, err
	}
	list, err := readList(listPath)
	if err != nil {
		return nil, nil, err
	}
	entries, err := overlay.LoadDir(overlayDir)
	if err != nil {
		return nil, nil, err
	}
	parent := overlay.Apply(list.Nodes, entries)[b.ParentAniDBID]
	if parent == nil {
		return nil, nil, fmt.Errorf("overlay capture-special: AniDB %d has no Anime-Lists node", b.ParentAniDBID)
	}
	return b, parent, nil
}

func readBridge(path string) (*overlay.Bridge, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var b overlay.Bridge
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("overlay capture-special: %s: %w", path, err)
	}
	return &b, nil
}

func specialSeasons(parent *animelists.Node, specials []int) ([]int, error) {
	var out []int
	for _, k := range specials {
		s, _, ok := parent.SpecialTVDB(k)
		if !ok {
			return nil, fmt.Errorf("overlay capture-special: special S%d maps to no single TVDB episode", k)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out, nil
}
