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

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
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
	CountsRedundant     []int                 `json:"counts_redundant"`
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
	mirrorPath, mirrorCommit    string
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
	fs.StringVar(&c.mirrorPath, "mirror", "", "AniDB mirror snapshot written by animap mirror extract")
	fs.StringVar(&c.mirrorCommit, "mirror-commit", "", "AniDB mirror commit the build is pinned to")
	fs.StringVar(&c.overlayDir, "overlay", "overlay", "overlay directory")
	fs.StringVar(&c.prevPath, "previous", "", "previous release's animap.json (absent file: first release)")
	fs.StringVar(&c.baselinePath, "baseline", "checks/collision-baseline.json", "tracked collision baseline (absent file: empty)")
	fs.StringVar(&c.countsPath, "counts", anidb.CountsPath, "tracked episode counts (absent file: none)")
	fs.StringVar(&c.out, "out", "animap.json", "output path")
	fs.StringVar(&c.statsPath, "stats", "", "write build statistics as JSON here")
	fs.BoolVar(&c.acceptShrink, "accept-shrink", false, "accept a population below 90% of the previous release")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "aod", "aod-release", "aod-sha256", "list", "list-commit", "mirror", "mirror-commit"); err != nil {
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
	entries, err := offlinedb.Load(c.aodPath, offlinedb.DefaultLimits)
	if err != nil {
		return nil, nil, err
	}
	list, err := readList(c.listPath)
	if err != nil {
		return nil, nil, err
	}
	facts, err := loadFacts(c.mirrorPath, c.countsPath, entries)
	if err != nil {
		return nil, nil, err
	}
	if facts.Commit() != c.mirrorCommit {
		return nil, nil, fmt.Errorf("build: %s holds the AniDB mirror at %s, want %s", c.mirrorPath, facts.Commit(), c.mirrorCommit)
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
	records, jst := join.Build(entries, patched, facts)
	var sp specialsStats
	sp.Applied, sp.Skipped = join.ApplySpecials(records, specialsOf(bridges), patched)
	for _, s := range sp.Skipped {
		log.Warn("build: special-of-parent bridge not applied", "anilist", s.AniList, "reason", s.Reason)
	}
	if proofErr := proveBridges(bridges, patched, facts); proofErr != nil {
		return nil, nil, fmt.Errorf("build: %w", proofErr)
	}
	rep := guard.Collisions(list.Nodes, ov, bridges, facts)
	redundant := facts.RedundantRows()
	if len(redundant) > 0 {
		log.Info("build: counts rows that equal what the sources give; delete them", "path", anidb.CountsPath, "anidb", redundant)
	}
	bs, err := collisions(c.baselinePath, &rep, log)
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
			AniDBMirror: schema.AniDBMirrorSource{Repository: anidb.Repository, Commit: facts.Commit()},
			Overlay:     schema.OverlaySource{Entries: len(ov), SpecialOfParent: len(bridges), SHA256: ovHash},
		},
		Attribution: schema.DefaultAttribution,
		Records:     records,
	}
	st := &buildStats{
		Populations: schema.Census(records), Join: jst, OverlayEntries: len(ov), Collisions: rep, Baseline: bs,
		Specials: sp, CountsRedundant: redundant,
	}
	return doc, st, nil
}

// collisions fails the build on a node with no regular episode count
// unless the baseline holds it, on a node with unread AniDB specials that
// is changed or whose specials could collide with a new claim, on a
// collision with a new claim, and on a collision the baseline does not hold.
func collisions(baselinePath string, rep *guard.CollisionReport, log *slog.Logger) (baselineStats, error) {
	var bs baselineStats
	base, err := guard.LoadBaseline(baselinePath)
	if err != nil {
		return bs, err
	}
	if _, statErr := os.Stat(baselinePath); statErr == nil {
		if err := checkBasis(baselinePath, base); err != nil {
			return bs, err
		}
	}
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
		return bs, fmt.Errorf("build: %d node(s) on a series or TMDB show the overlay touches, or outside the baseline, have no regular episode count, so the collision check cannot place them; first: AniDB %d on %s (record its count in checks/counts.json, or wait for the AniDB mirror or anime-offline-database to count it)",
			len(uncounted), u.AniDB, seriesName(u.Series))
	}
	if len(rep.Unresolved) > 0 {
		u := rep.Unresolved[0]
		return bs, fmt.Errorf("build: %d node(s) the collision check places specials beside have no AniDB episode list in the mirror, and their specials could collide with an episode the overlay claims, or the overlay changes them; first: AniDB %d, season 0 episodes %v of %s (wait for a mirror snapshot that lists it)",
			len(rep.Unresolved), u.AniDB, u.Episodes, unresolvedSide(u))
	}
	blocking := append(slices.Clone(rep.Blocking), novel...)
	if len(blocking) > 0 {
		return bs, fmt.Errorf("build: %d collision(s) on an episode the overlay claims or outside the baseline, first: %s on %s claimed by %v",
			len(blocking), blocking[0].Target, seriesName(blocking[0].Series), blocking[0].Claims)
	}
	return bs, nil
}

func seriesName(series int) string {
	if series == 0 {
		return "no TVDB series"
	}
	return fmt.Sprintf("TVDB %d", series)
}

func unresolvedSide(u guard.Unresolved) string {
	if u.TMDB != 0 {
		return fmt.Sprintf("TMDB %d", u.TMDB)
	}
	return fmt.Sprintf("TVDB %d", u.Series)
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
