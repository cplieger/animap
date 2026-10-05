package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cplieger/animap/internal/counts"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
)

func runBaseline(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: baseline needs init, prune or check-shrink", errUsage)
	}
	fs := flag.NewFlagSet("baseline "+args[0], flag.ContinueOnError)
	path := fs.String("baseline", "checks/collision-baseline.json", "baseline file")
	switch args[0] {
	case "check-shrink":
		basePath := fs.String("base", "", "the baseline on the target branch")
		if err := parse(fs, args[1:]); err != nil {
			return err
		}
		if err := required(fs, "base"); err != nil {
			return err
		}
		return checkShrink(*basePath, *path)
	case "init", "prune":
		overlayDir := fs.String("overlay", "overlay", "overlay directory")
		listPath := fs.String("list", "", "anime-list-master.xml")
		aodPath := fs.String("aod", "", "anime-offline-database .jsonl")
		countsPath := fs.String("counts", counts.Path, "tracked episode counts")
		commit := fs.String("commit", "", "Anime-Lists commit of -list")
		if err := parse(fs, args[1:]); err != nil {
			return err
		}
		if err := required(fs, "list", "aod", "commit"); err != nil {
			return err
		}
		rep, err := upstreamReport(*overlayDir, *listPath, *aodPath, *countsPath)
		if err != nil {
			return err
		}
		return writeBaseline(ctx, args[0], *path, *commit, &rep, stdout)
	}
	return fmt.Errorf("%w: unknown baseline command %q", errUsage, args[0])
}

func checkShrink(basePath, headPath string) error {
	if _, err := os.Stat(basePath); errors.Is(err, os.ErrNotExist) { //nolint:gosec,nolintlint // G703: the operator's own path, see readList
		return nil
	}
	base, err := guard.LoadBaseline(basePath)
	if err != nil {
		return err
	}
	head, err := guard.LoadBaseline(headPath)
	if err != nil {
		return err
	}
	return guard.CheckShrink(base, head)
}

func upstreamReport(overlayDir, listPath, aodPath, countsPath string) (guard.CollisionReport, error) {
	list, err := readList(listPath)
	if err != nil {
		return guard.CollisionReport{}, err
	}
	entries, bridges, err := loadOverlay(overlayDir)
	if err != nil {
		return guard.CollisionReport{}, err
	}
	_, aod, err := offlinedb.Load(aodPath, offlinedb.DefaultLimits)
	if err != nil {
		return guard.CollisionReport{}, err
	}
	episodes, err := placementCounts(aod, countsPath)
	if err != nil {
		return guard.CollisionReport{}, err
	}
	return guard.Collisions(overlay.Apply(list.Nodes, entries), entries, bridges, episodes), nil
}

func writeBaseline(ctx context.Context, mode, path, commit string, rep *guard.CollisionReport, stdout io.Writer) error {
	var b *guard.Baseline
	if mode == "init" {
		if _, err := os.Stat(path); err == nil { //nolint:gosec,nolintlint // G703: the operator's own path, see readList
			return fmt.Errorf("baseline init: %s exists; the baseline is written once and then only pruned", path)
		}
		b = guard.NewBaseline(rep.Upstream, rep.UpstreamUncounted, commit)
	} else {
		prev, err := guard.LoadBaseline(path)
		if err != nil {
			return err
		}
		b = prev.Prune(rep.Upstream, rep.UpstreamUncounted)
	}
	if _, err := fmt.Fprintf(stdout, "baseline %s: %d collision(s), %d uncounted node(s)\n", mode, len(b.Collisions), len(b.Uncounted)); err != nil {
		return err
	}
	return writeJSON(ctx, path, b)
}
