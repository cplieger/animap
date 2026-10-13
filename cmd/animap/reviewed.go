package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/reviewed"
	"github.com/cplieger/atomicfile/v4"
)

func runAccept(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("datecheck accept", flag.ContinueOnError)
	datePath := fs.String("datecheck", "", "datecheck.json from animap datecheck")
	id := fs.Int("id", 0, "AniDB id of the lead")
	reason := fs.String("reason", "", "the episodes and their titles that prove the mapping right")
	var evidence []string
	fs.Func("evidence", "https link to an AniDB, TVDB, TMDB, AniList or SeaDex page, once per link", func(v string) error {
		evidence = append(evidence, v)
		return nil
	})
	dir := fs.String("dir", reviewed.Dir, "reviewed records directory")
	force := fs.Bool("force", false, "replace an existing record")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "datecheck"); err != nil {
		return err
	}
	if *id <= 0 {
		return fmt.Errorf("%w: %s needs -id", errUsage, fs.Name())
	}
	var d dateResult
	if err := readJSON(*datePath, &d); err != nil {
		return err
	}
	i := slices.IndexFunc(d.Leads, func(l dateLead) bool { return l.AniDB == *id })
	if i < 0 {
		return fmt.Errorf("datecheck accept: AniDB %d is not a lead in %s", *id, *datePath)
	}
	l := &d.Leads[i]
	if l.Dead {
		return fmt.Errorf("datecheck accept: AniDB %d maps to TVDB %d, which SkyHook no longer serves, so its mapping cannot be fine", l.AniDB, l.Series)
	}
	title := strings.TrimSpace(l.Name)
	if title == "" {
		title = fmt.Sprintf("AniDB %d", l.AniDB)
	}
	r := reviewed.Record{
		AniDBID: l.AniDB, Title: title, Verdict: reviewed.Verdict, Findings: datecheck.FindingKeys(l.Findings),
		Reason: strings.TrimSpace(*reason), Evidence: evidence,
		Captured: reviewed.Captured{Date: time.Now().UTC().Format(time.DateOnly), Fingerprints: l.Fingerprints},
	}
	return writeRecord(ctx, *dir, &r, *force, log)
}

// Test seams: beforePublish runs between staging a record and publishing
// it; writeAtomic and syncDir are the durable writes accept depends on.
var (
	beforePublish = func() {}
	writeAtomic   = atomicfile.WriteFile
	syncDir       = func(dir string) error {
		d, err := os.Open(dir)
		if err != nil {
			return err
		}
		defer func() { _ = d.Close() }()
		return d.Sync()
	}
)

// writeRecord publishes r without -force through a hard link from a staged
// copy, which fails when the record exists, so of two accepts racing for one
// id exactly one lands; atomicfile alone replaces whatever renamed in last.
func writeRecord(ctx context.Context, dir string, r *reviewed.Record, force bool, log *slog.Logger) error {
	b, encErr := r.Encode()
	if encErr != nil {
		return fmt.Errorf("datecheck accept: %w", encErr)
	}
	path := filepath.Join(dir, strconv.Itoa(r.AniDBID)+".json")
	write := publishNew
	if force {
		write = writeRecordFile
	}
	if err := write(ctx, path, b); err != nil {
		return err
	}
	log.Info("datecheck accept: recorded", "path", path, "findings", len(r.Findings))
	return nil
}

// writeRecordFile writes path, creating missing directories, and fails
// unless atomicfile reports the write durable.
func writeRecordFile(ctx context.Context, path string, b []byte) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	res, err := writeAtomic(ctx, abs, b, atomicfile.WithMaxBytes(reviewed.MaxFileBytes), atomicfile.WithMkdirMode(0o750))
	if err != nil {
		return err
	}
	if !res.Durable {
		return fmt.Errorf("datecheck accept: %s may not survive a crash, because a sync failed", path)
	}
	return nil
}

// publishNew syncs the directory after the link, because the staged write's
// own directory sync ran before the record's name existed.
func publishNew(ctx context.Context, path string, b []byte) error {
	staged := filepath.Join(filepath.Dir(path), atomicfile.TempName())
	defer func() { _ = os.Remove(staged) }()
	if err := writeRecordFile(ctx, staged, b); err != nil {
		return fmt.Errorf("datecheck accept: %s was not recorded: %w", path, err)
	}
	beforePublish()
	switch err := os.Link(staged, path); {
	case errors.Is(err, os.ErrExist):
		return fmt.Errorf("datecheck accept: %s exists; pass -force to replace it", path)
	case err != nil:
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("datecheck accept: wrote %s, but it may not survive a crash: %w", path, err)
	}
	return nil
}

// reviewOf returns lead l's record, nil when none exists, and why the record
// no longer covers l, nil when it does.
func reviewOf(records map[int]reviewed.Record, l *dateLead) (*reviewed.Record, []reviewed.Cause) {
	r, ok := records[l.AniDB]
	if !ok {
		return nil, nil
	}
	return &r, r.Changes(l.Fingerprints, datecheck.FindingKeys(l.Findings))
}

var causeText = map[reviewed.Cause]string{
	reviewed.Node:       "the node changed",
	reviewed.TVDB:       "TVDB's official order changed",
	reviewed.AniDB:      "AniDB's episode list changed",
	reviewed.NewFinding: "a finding the record does not list appeared",
}

// reraised is the sentence a lead's issue gains when its record no longer
// covers it.
func reraised(r *reviewed.Record, causes []reviewed.Cause) string {
	parts := make([]string, 0, len(causes))
	for _, c := range causes {
		parts = append(parts, causeText[c])
	}
	because := parts[len(parts)-1]
	if len(parts) > 1 {
		because = strings.Join(parts[:len(parts)-1], ", ") + " and " + because
	}
	return fmt.Sprintf("%#q marked this lead fine on %s, and it is raised again because %s.\n\n", reviewed.Path(r.AniDBID), r.Captured.Date, because)
}

// reviewedComment closes the open issue of a lead its record covers.
func reviewedComment(r *reviewed.Record) string {
	return fmt.Sprintf("%#q marked this lead fine on %s. The node, TVDB's official order and AniDB's episode list are as it recorded them, and no finding it does not list appeared, so this closes. It opens again when one of them changes or a new finding appears.",
		reviewed.Path(r.AniDBID), r.Captured.Date)
}
