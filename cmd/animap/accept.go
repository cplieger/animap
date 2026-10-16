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
	"strings"
	"time"

	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/atomicfile/v4"
)

type acceptArgs struct {
	from, key, part, verdict, reason, requires, dir string
	evidence                                        []string
	anilist                                         int
	force                                           bool
}

// runAccept records a person's ruling on one section of a subject, from a
// review.json or explain.json that raised it, into reviewed/<file>.
func runAccept(ctx context.Context, args []string, log *slog.Logger) error {
	var a acceptArgs
	fs := flag.NewFlagSet("accept", flag.ContinueOnError)
	fs.StringVar(&a.from, "from", "", "review.json from animap review, or explain.json from animap explain -json")
	fs.StringVar(&a.key, "key", "", "subject key: anime:anidb:<id> or anime:anilist:<id>")
	fs.StringVar(&a.part, "part", "", "date-tvdb, date-tmdb or watch")
	fs.IntVar(&a.anilist, "anilist", 0, "AniList id of the watched record, for -part watch on a subject with several")
	fs.StringVar(&a.verdict, "verdict", "", "fine (date parts), unmappable (watch) or inexpressible (either)")
	fs.StringVar(&a.reason, "reason", "", "the episodes and their titles, or the search, that prove the ruling")
	fs.StringVar(&a.requires, "requires", "", "for inexpressible: the published-format change the truth needs")
	fs.Func("evidence", "https link to an AniDB, TVDB, TMDB, AniList or SeaDex page, once per link", func(v string) error {
		a.evidence = append(a.evidence, v)
		return nil
	})
	fs.StringVar(&a.dir, "dir", review.Dir, "acceptance records directory")
	fs.BoolVar(&a.force, "force", false, "add the part to, or replace it in, an existing record")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "from", "key", "part", "verdict"); err != nil {
		return err
	}
	if review.File(a.key) == "" {
		return fmt.Errorf("%w: accept: -key %q is not a subject key", errUsage, a.key)
	}
	var doc reviewDoc
	if err := readJSON(a.from, &doc); err != nil {
		return err
	}
	i := slices.IndexFunc(doc.Subjects, func(s *subject) bool { return s.Key == a.key })
	if i < 0 {
		return fmt.Errorf("accept: %s raises nothing in %s", a.key, a.from)
	}
	path := filepath.Join(a.dir, review.File(a.key))
	rec, exists, err := existingRecord(path)
	if err != nil {
		return err
	}
	if exists && !a.force {
		return fmt.Errorf("accept: %s exists; pass -force to add or replace the part", path)
	}
	s := doc.Subjects[i]
	if err := a.apply(&rec, s); err != nil {
		return err
	}
	return writeRecord(ctx, path, &rec, exists, log)
}

func existingRecord(path string) (review.Record, bool, error) {
	rec, err := review.Read(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return review.Record{}, false, nil
	case err != nil:
		return review.Record{}, false, err
	}
	return rec, true, nil
}

// apply sets a's part on rec from what subject s raised.
func (a *acceptArgs) apply(rec *review.Record, s *subject) error {
	rec.Key = a.key
	if strings.TrimSpace(rec.Title) == "" {
		rec.Title = strings.TrimSpace(s.Title)
	}
	if rec.Title == "" {
		sub, _ := review.ParseKey(a.key)
		rec.Title = sub.Label()
	}
	ruling := review.Ruling{
		Verdict: a.verdict, Date: time.Now().UTC().Format(time.DateOnly), Reason: strings.TrimSpace(a.reason),
		Requires: strings.TrimSpace(a.requires), Evidence: a.evidence,
	}
	switch a.part {
	case review.DateTVDB, review.DateTMDB:
		d := s.Sections.date(a.part)
		if d == nil || !d.Evaluated || !d.raised() {
			return fmt.Errorf("accept: %s has no %s finding in %s", a.key, a.part, a.from)
		}
		rec.SetDate(a.part, &review.DatePart{Findings: d.Digests, Ruling: ruling})
	case review.Watch:
		f, err := a.watchFinding(s)
		if err != nil {
			return err
		}
		rec.SetWatch(&review.WatchPart{AniListID: f.AniListID, Gaps: f.GapNames(), RecordSHA256: f.RecordSHA256, Ruling: ruling})
	default:
		return fmt.Errorf("%w: accept: -part %q is not date-tvdb, date-tmdb or watch", errUsage, a.part)
	}
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	return nil
}

func (a *acceptArgs) watchFinding(s *subject) (*watchFinding, error) {
	w := s.Sections.Watch
	if w == nil || !w.Evaluated || len(w.Findings) == 0 {
		return nil, fmt.Errorf("accept: %s has no watch gap in %s", a.key, a.from)
	}
	if a.anilist == 0 && len(w.Findings) > 1 {
		return nil, fmt.Errorf("%w: accept: %s has %d watched records with gaps; name one with -anilist", errUsage, a.key, len(w.Findings))
	}
	for i := range w.Findings {
		if a.anilist == 0 || w.Findings[i].AniListID == a.anilist {
			return &w.Findings[i], nil
		}
	}
	return nil, fmt.Errorf("accept: %s has no watch gap for AniList %d in %s", a.key, a.anilist, a.from)
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

// writeRecord publishes a new record through a hard link from a staged
// copy, which fails when the record exists, so of two accepts racing for
// one subject exactly one lands; atomicfile alone replaces whatever
// renamed in last. replace writes over the record the caller read.
func writeRecord(ctx context.Context, path string, r *review.Record, replace bool, log *slog.Logger) error {
	b, err := r.Encode()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	write := publishNew
	if replace {
		write = writeRecordFile
	}
	if err := write(ctx, path, b); err != nil {
		return err
	}
	log.Info("accept: recorded", "path", path)
	return nil
}

// writeRecordFile writes path, creating missing directories, and fails
// unless atomicfile reports the write durable.
func writeRecordFile(ctx context.Context, path string, b []byte) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	res, err := writeAtomic(ctx, abs, b, atomicfile.WithMaxBytes(review.MaxFileBytes), atomicfile.WithMkdirMode(0o750))
	if err != nil {
		return err
	}
	if !res.Durable {
		return fmt.Errorf("accept: %s may not survive a crash, because a sync failed", path)
	}
	return nil
}

// publishNew syncs the directory after the link, because the staged write's
// own directory sync ran before the record's name existed.
func publishNew(ctx context.Context, path string, b []byte) error {
	staged := filepath.Join(filepath.Dir(path), atomicfile.TempName())
	defer func() { _ = os.Remove(staged) }()
	if err := writeRecordFile(ctx, staged, b); err != nil {
		return fmt.Errorf("accept: %s was not recorded: %w", path, err)
	}
	beforePublish()
	switch err := os.Link(staged, path); {
	case errors.Is(err, os.ErrExist):
		return fmt.Errorf("accept: %s exists; pass -force to add or replace the part", path)
	case err != nil:
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("accept: wrote %s, but it may not survive a crash: %w", path, err)
	}
	return nil
}
