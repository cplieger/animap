package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/offlinedb"
)

func runBaseline(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: baseline needs init, prune, rebase or check-shrink", errUsage)
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
	case "init", "prune", "rebase":
		overlayDir := fs.String("overlay", "overlay", "overlay directory")
		listPath := fs.String("list", "", "anime-list-master.xml")
		aodPath := fs.String("aod", "", "anime-offline-database .jsonl")
		countsPath := fs.String("counts", anidb.CountsPath, "tracked episode counts")
		mirrorPath := fs.String("mirror", "", "AniDB mirror snapshot written by animap mirror extract")
		commit := fs.String("commit", "", "Anime-Lists commit of -list")
		if err := parse(fs, args[1:]); err != nil {
			return err
		}
		if err := required(fs, "list", "aod", "mirror", "commit"); err != nil {
			return err
		}
		rep, err := upstreamReport(*overlayDir, *listPath, *aodPath, *countsPath, *mirrorPath)
		if err != nil {
			return err
		}
		return writeBaseline(ctx, args[0], *path, *commit, &rep, stdout)
	}
	return fmt.Errorf("%w: unknown baseline command %q", errUsage, args[0])
}

// checkShrink also holds head to this code's basis, so a basis string
// changed without the counts it names cannot open the baseline to additions.
func checkShrink(basePath, headPath string) error {
	head, err := guard.LoadBaseline(headPath)
	if err != nil {
		return err
	}
	if basisErr := checkBasis(headPath, head); basisErr != nil {
		return basisErr
	}
	if _, statErr := os.Stat(basePath); errors.Is(statErr, os.ErrNotExist) { //nolint:gosec,nolintlint // G703: the operator's own path, see readList
		return nil
	}
	base, err := guard.LoadBaseline(basePath)
	if err != nil {
		return err
	}
	return guard.CheckShrink(base, head)
}

func checkBasis(path string, b *guard.Baseline) error {
	if b.Basis != anidb.CountBasis {
		return fmt.Errorf("baseline %s was measured under basis %q, and the episode counts are now %q; run animap baseline rebase", path, b.Basis, anidb.CountBasis)
	}
	return nil
}

func upstreamReport(overlayDir, listPath, aodPath, countsPath, mirrorPath string) (guard.CollisionReport, error) {
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
	facts, err := loadFacts(mirrorPath, countsPath, aod)
	if err != nil {
		return guard.CollisionReport{}, err
	}
	return guard.Collisions(list.Nodes, entries, bridges, facts), nil
}

func writeBaseline(ctx context.Context, mode, path, commit string, rep *guard.CollisionReport, stdout io.Writer) error {
	var b *guard.Baseline
	if mode == "init" {
		if _, err := os.Stat(path); err == nil { //nolint:gosec,nolintlint // G703: the operator's own path, see readList
			return fmt.Errorf("baseline init: %s exists; the baseline is written once and then only pruned", path)
		}
		b = guard.NewBaseline(rep.Upstream, rep.UpstreamUncounted, commit, anidb.CountBasis)
	} else {
		prev, err := guard.LoadBaseline(path)
		if err != nil {
			return err
		}
		switch {
		case mode == "prune":
			if err := checkBasis(path, prev); err != nil {
				return err
			}
			b = prev.Prune(rep.Upstream, rep.UpstreamUncounted)
		case prev.Basis == anidb.CountBasis:
			return fmt.Errorf("baseline rebase: %s is already measured under %q; the baseline only shrinks, run animap baseline prune", path, anidb.CountBasis)
		default:
			b = guard.NewBaseline(rep.Upstream, rep.UpstreamUncounted, commit, anidb.CountBasis)
		}
	}
	if _, err := fmt.Fprintf(stdout, "baseline %s: %d collision(s), %d uncounted node(s)\n", mode, len(b.Collisions), len(b.Uncounted)); err != nil {
		return err
	}
	return writeJSON(ctx, path, b)
}
