package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/source"
	"github.com/cplieger/animap/internal/watch"
)

type watchResult struct {
	Set            string               `json:"set"`
	EntryURL       string               `json:"entry_url"`
	Gaps           []watch.Entry        `json:"gaps"`
	Classification watch.Classification `json:"classification"`
	Watched        int                  `json:"watched"`
	Complete       bool                 `json:"complete"`
}

func runWatch(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	configPath := fs.String("config", "watch/seadex.json", "watch-set config")
	backlogPath := fs.String("backlog", "watch/backlog.json", "tracked backlog")
	unmappablePath := fs.String("unmappable", "checks/unmappable.json", "gaps recorded as unmappable, with reasons")
	releasePath := fs.String("release", "", "published animap.json to check")
	cachePath := fs.String("cache", "", "HTTP validator cache file")
	out := fs.String("out", "watch.json", "output path")
	writeBacklog := fs.Bool("write-backlog", false, "replace the backlog with this run's gaps")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "release"); err != nil {
		return err
	}
	cfg, backlog, doc, err := loadWatchInputs(*configPath, *backlogPath, *releasePath)
	if err != nil {
		return err
	}
	unmappable, err := loadList(*unmappablePath, cfg.Name)
	if err != nil {
		return err
	}
	client, err := source.New(*cachePath, pace, log)
	if err != nil {
		return err
	}
	res := watchResult{Set: cfg.Name, EntryURL: cfg.EntryURL}
	ids, err := watch.FetchIDs(ctx, client, cfg)
	if saveErr := client.Save(ctx); saveErr != nil {
		log.Warn("watch: cache not saved", "error", saveErr)
	}
	if err != nil {
		log.Warn("watch: listing unusable, nothing evaluated this run", "set", cfg.Name, "error", err)
		return writeJSON(ctx, *out, res)
	}
	res.Complete, res.Watched = true, len(ids)
	res.Gaps = watch.Evaluate(ids, doc)
	res.Classification = watch.Classify(res.Gaps, backlog, unmappable)
	log.Info("watch: done", "set", cfg.Name, "watched", len(ids), "gaps", len(res.Gaps),
		"new", len(res.Classification.New), "resolved", len(res.Classification.Resolved))
	if *writeBacklog {
		if err := saveBacklog(ctx, *backlogPath, cfg.Name, res.Gaps); err != nil {
			return err
		}
	}
	return writeJSON(ctx, *out, res)
}

func loadWatchInputs(configPath, backlogPath, releasePath string) (*watch.Config, *watch.Backlog, *schema.Document, error) {
	cfg, err := watch.LoadConfig(configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	backlog, err := loadList(backlogPath, cfg.Name)
	if err != nil {
		return nil, nil, nil, err
	}
	f, err := os.Open(releasePath)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = f.Close() }()
	doc, err := schema.Decode(f)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, backlog, doc, nil
}

// loadList reads a backlog-shaped list; a missing file is an empty one.
func loadList(path, set string) (*watch.Backlog, error) {
	b, err := watch.LoadBacklog(path)
	if errors.Is(err, os.ErrNotExist) {
		return &watch.Backlog{Version: 1, WatchSet: set}, nil
	}
	return b, err
}

func saveBacklog(ctx context.Context, path, set string, gaps []watch.Entry) error {
	b := watch.Backlog{Version: 1, WatchSet: set, CapturedAt: time.Now().UTC().Format(time.DateOnly), Entries: gaps}
	return writeJSON(ctx, path, b)
}
