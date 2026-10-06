package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/offlinedb"
)

// runMirror reads the mirror's archive from stdin, so the
// archive, which holds AniDB's titles and descriptions, is never written to
// disk; only the snapshot of numbers and dates is.
func runMirror(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, log *slog.Logger) error {
	if len(args) == 0 || args[0] != "extract" {
		return fmt.Errorf("%w: mirror needs extract", errUsage)
	}
	fs := flag.NewFlagSet("mirror extract", flag.ContinueOnError)
	commit := fs.String("commit", "", "AnimeAggregations commit the archive on stdin was read at")
	out := fs.String("out", "", "snapshot file to write")
	if err := parse(fs, args[1:]); err != nil {
		return err
	}
	if err := required(fs, "commit", "out"); err != nil {
		return err
	}
	snap, refusals, err := anidb.Extract(stdin, *commit)
	if err != nil {
		return err
	}
	for _, r := range refusals {
		log.Warn("mirror extract: file refused, AniDB's episode list for it is not used", "error", r)
	}
	if writeErr := writeJSON(ctx, *out, snap); writeErr != nil {
		return writeErr
	}
	_, err = fmt.Fprintf(stdout, "mirror extract: %d anime listed, %d refused\n", len(snap.Anime), len(snap.Refused))
	return err
}

func loadFacts(mirrorPath, countsPath string, aod []offlinedb.Entry) (*anidb.Facts, error) {
	snap, err := anidb.LoadSnapshot(mirrorPath)
	if err != nil {
		return nil, err
	}
	rows, err := anidb.LoadRows(countsPath)
	if err != nil {
		return nil, err
	}
	return anidb.NewFacts(snap, rows, episodeCounts(aod)), nil
}

func episodeCounts(entries []offlinedb.Entry) map[int]int {
	out := map[int]int{}
	for i := range entries {
		for _, ad := range entries[i].AniDB {
			if _, ok := out[ad]; !ok {
				out[ad] = entries[i].Episodes
			}
		}
	}
	return out
}
