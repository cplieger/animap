package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/counts"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/join"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/schema"
)

type buildStats struct {
	PreviousPopulations *schema.Populations   `json:"previous_populations,omitempty"`
	ContentHash         string                `json:"content_hash"`
	PreviousHash        string                `json:"previous_hash,omitempty"`
	Collisions          guard.CollisionReport `json:"collisions"`
	Specials            specialsStats         `json:"specials"`
	Baseline            baselineStats         `json:"baseline"`
	Join                join.Stats            `json:"join"`
	Populations         schema.Populations    `json:"populations"`
	Bytes               int                   `json:"bytes"`
	OverlayEntries      int                   `json:"overlay_entries"`
	Changed             bool                  `json:"changed"`
}

type baselineStats struct {
	Stale              []guard.BaselineEntry `json:"stale"`
	StaleUncounted     []guard.Uncounted     `json:"stale_uncounted"`
	Baselined          int                   `json:"baselined"`
	BaselinedUncounted int                   `json:"baselined_uncounted"`
}

type specialsStats struct {
	Skipped []join.Skipped `json:"skipped"`
	Applied int            `json:"applied"`
}

type buildConfig struct {
	aodPath, aodRelease, aodSHA string
	listPath, listCommit        string
	overlayDir, prevPath        string
	baselinePath, countsPath    string
	out, statsPath              string
	acceptShrink                bool
}

func runBuild(ctx context.Context, args []string, stdout io.Writer, log *slog.Logger) error {
	var c buildConfig
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.StringVar(&c.aodPath, "aod", "", "anime-offline-database .jsonl path")
	fs.StringVar(&c.aodRelease, "aod-release", "", "anime-offline-database release tag")
	fs.StringVar(&c.aodSHA, "aod-sha256", "", "expected SHA-256 of the .jsonl asset")
	fs.StringVar(&c.listPath, "list", "", "anime-list-master.xml path")
	fs.StringVar(&c.listCommit, "list-commit", "", "Anime-Lists commit the list was read at")
	fs.StringVar(&c.overlayDir, "overlay", "overlay", "overlay directory")
	fs.StringVar(&c.prevPath, "previous", "", "previous release's animap.json (absent file: first release)")
	fs.StringVar(&c.baselinePath, "baseline", "checks/collision-baseline.json", "tracked collision baseline (absent file: empty)")
	fs.StringVar(&c.countsPath, "counts", counts.Path, "tracked episode counts (absent file: none)")
	fs.StringVar(&c.out, "out", "animap.json", "output path")
	fs.StringVar(&c.statsPath, "stats", "", "write build statistics as JSON here")
	fs.BoolVar(&c.acceptShrink, "accept-shrink", false, "accept a population below 90% of the previous release")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "aod", "aod-release", "aod-sha256", "list", "list-commit"); err != nil {
		return err
	}
	doc, st, err := assemble(&c, log)
	if err != nil {
		return err
	}
	body, err := schema.Encode(doc)
	if err != nil {
		return err
	}
	if _, decErr := schema.Decode(bytes.NewReader(body)); decErr != nil {
		return fmt.Errorf("build: output does not decode through the schema: %w", decErr)
	}
	st.Bytes = len(body)
	if cmpErr := compare(&c, doc, st); cmpErr != nil {
		return cmpErr
	}
	if writeErr := writeFile(ctx, c.out, body); writeErr != nil {
		return writeErr
	}
	if c.statsPath != "" {
		if statsErr := writeJSON(ctx, c.statsPath, st); statsErr != nil {
			return statsErr
		}
	}
	log.Info("build: done", "records", st.Populations.Records, "bytes", st.Bytes, "overlay", st.OverlayEntries,
		"dropped_rows", st.Join.DroppedRows, "changed", st.Changed)
	_, err = fmt.Fprintf(stdout, "content_hash=%s\nprevious_hash=%s\nchanged=%t\n", st.ContentHash, st.PreviousHash, st.Changed)
	return err
}

func assemble(c *buildConfig, log *slog.Logger) (*schema.Document, *buildStats, error) {
	digest, err := fileSHA256(c.aodPath)
	if err != nil {
		return nil, nil, err
	}
	if digest != c.aodSHA {
		return nil, nil, fmt.Errorf("build: %s has sha256 %s, want %s", c.aodPath, digest, c.aodSHA)
	}
	_, entries, err := offlinedb.Load(c.aodPath, offlinedb.DefaultLimits)
	if err != nil {
		return nil, nil, err
	}
	list, err := readList(c.listPath)
	if err != nil {
		return nil, nil, err
	}
	ov, err := overlay.LoadDir(c.overlayDir)
	if err != nil {
		return nil, nil, err
	}
	bridges, err := overlay.LoadBridges(c.overlayDir)
	if err != nil {
		return nil, nil, err
	}
	patched := overlay.Apply(list.Nodes, ov)
	records, jst := join.Build(entries, patched)
	var sp specialsStats
	sp.Applied, sp.Skipped = join.ApplySpecials(records, specialsOf(bridges), patched)
	for _, s := range sp.Skipped {
		log.Warn("build: special-of-parent bridge not applied", "anilist", s.AniList, "reason", s.Reason)
	}
	episodes, err := placementCounts(entries, c.countsPath)
	if err != nil {
		return nil, nil, err
	}
	rep, bs, err := collisions(c.baselinePath, patched, ov, bridges, episodes, log)
	if err != nil {
		return nil, nil, err
	}
	ovHash, err := overlay.SetHash(ov, bridges)
	if err != nil {
		return nil, nil, err
	}
	doc := &schema.Document{
		Version:     schema.Version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Sources: schema.Sources{
			AnimeOfflineDatabase: schema.OfflineDatabaseSource{
				Repository: "https://github.com/cedya77/anime-offline-database",
				Release:    c.aodRelease, Asset: "anime-offline-database.jsonl", SHA256: digest,
			},
			AnimeLists: schema.AnimeListsSource{
				Repository: "https://github.com/Anime-Lists/anime-lists",
				Commit:     c.listCommit, File: "anime-list-master.xml",
			},
			Overlay: schema.OverlaySource{Entries: len(ov), SpecialOfParent: len(bridges), SHA256: ovHash},
		},
		Attribution: schema.DefaultAttribution,
		Records:     records,
	}
	st := &buildStats{Populations: schema.Census(records), Join: jst, OverlayEntries: len(ov), Collisions: rep, Baseline: bs, Specials: sp}
	return doc, st, nil
}

// collisions fails the build on a node with no regular episode count
// unless the baseline holds it, on a touched-series sibling whose unread
// AniDB specials could collide, on a collision on a series the overlay
// touches, and on a collision the baseline does not hold.
func collisions(baselinePath string, patched map[int]*animelists.Node, ov []overlay.Entry, bridges []overlay.Bridge,
	episodes map[int]int, log *slog.Logger,
) (guard.CollisionReport, baselineStats, error) {
	var bs baselineStats
	base, err := guard.LoadBaseline(baselinePath)
	if err != nil {
		return guard.CollisionReport{}, bs, err
	}
	rep := guard.Collisions(patched, ov, bridges, episodes)
	baselined, novel, stale := base.Split(rep.Upstream)
	countedOK, uncounted, staleUnc := base.SplitUncounted(rep.UpstreamUncounted)
	bs.Baselined, bs.Stale = len(baselined), stale
	bs.BaselinedUncounted, bs.StaleUncounted = len(countedOK), staleUnc
	if len(stale)+len(staleUnc) > 0 {
		log.Info("build: baselined items no longer seen; run animap baseline prune", "collisions", len(stale), "uncounted", len(staleUnc))
	}
	uncounted = append(slices.Clone(rep.Uncounted), uncounted...)
	if len(uncounted) > 0 {
		u := uncounted[0]
		return rep, bs, fmt.Errorf("build: %d node(s) on an overlay-touched series or outside the baseline have no regular episode count, so the collision check cannot place them; first: AniDB %d on TVDB %d (record its count in an entry's siblings or in checks/counts.json, or wait for anime-offline-database to list it)",
			len(uncounted), u.AniDB, u.Series)
	}
	if len(rep.Unresolved) > 0 {
		u := rep.Unresolved[0]
		return rep, bs, fmt.Errorf("build: %d node(s) on an overlay-touched series have unread AniDB specials that could land on a season-0 episode another node claims; first: AniDB %d on TVDB %d, episodes %v (record its specials in the entry's siblings)",
			len(rep.Unresolved), u.AniDB, u.Series, u.Episodes)
	}
	blocking := append(slices.Clone(rep.Blocking), novel...)
	if len(blocking) > 0 {
		return rep, bs, fmt.Errorf("build: %d collision(s) on an overlay-touched series or outside the baseline, first: %s on TVDB %d claimed by %v",
			len(blocking), blocking[0].Target, blocking[0].Series, blocking[0].Claims)
	}
	return rep, bs, nil
}

func specialsOf(bridges []overlay.Bridge) []join.Special {
	out := make([]join.Special, 0, len(bridges))
	for i := range bridges {
		b := &bridges[i]
		out = append(out, join.Special{AniList: b.AniListID, Parent: b.ParentAniDBID, Specials: b.Specials})
	}
	return out
}

func compare(c *buildConfig, doc *schema.Document, st *buildStats) error {
	var err error
	if st.ContentHash, err = schema.ContentHash(doc); err != nil {
		return err
	}
	prev, err := readPrevious(c.prevPath)
	if err != nil {
		return err
	}
	if prev != nil {
		pp := schema.Census(prev.Records)
		st.PreviousPopulations = &pp
		if st.PreviousHash, err = schema.ContentHash(prev); err != nil {
			return err
		}
	}
	st.Changed = st.ContentHash != st.PreviousHash
	return guard.Coverage(st.PreviousPopulations, st.Populations, c.acceptShrink)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, offlinedb.DefaultLimits.MaxFileBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readList reads the list at an operator-given path. gosec's G703 taint
// rule flags that path, and its verdict varies run to run on the same
// source (https://github.com/securego/gosec/issues/1608).
func readList(path string) (*animelists.List, error) {
	st, err := os.Stat(path) //nolint:gosec,nolintlint // G703: the operator's own command-line path
	if err != nil {
		return nil, err
	}
	if st.Size() > animelists.MaxBodyBytes {
		return nil, animelists.ErrTooBig
	}
	body, err := os.ReadFile(path) //nolint:gosec,nolintlint // G703: the operator's own command-line path
	if err != nil {
		return nil, err
	}
	return animelists.Parse(body)
}

func readPrevious(path string) (*schema.Document, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return schema.Decode(f)
}

// placementCounts is the regular count per AniDB id the collision check
// falls back on when no overlay entry recorded one: a checks/counts.json
// row, else the offline database's.
func placementCounts(entries []offlinedb.Entry, countsPath string) (map[int]int, error) {
	rows, err := counts.Load(countsPath)
	if err != nil {
		return nil, err
	}
	return counts.Merge(episodeCounts(entries), rows), nil
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
