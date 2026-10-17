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
	"github.com/cplieger/animap/internal/release"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/mapread"
)

type buildStats struct {
	PreviousPopulations *release.Populations  `json:"previous_populations,omitempty"`
	ContentHash         string                `json:"content_hash"`
	PreviousHash        string                `json:"previous_hash,omitempty"`
	Collisions          guard.CollisionReport `json:"collisions"`
	Links               linkStats             `json:"links"`
	CountsRedundant     []int                 `json:"counts_redundant"`
	Baseline            baselineStats         `json:"baseline"`
	Join                join.Stats            `json:"join"`
	Populations         release.Populations   `json:"populations"`
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

type linkStats struct {
	Skipped []join.Skipped `json:"skipped"`
	Applied int            `json:"applied"`
}

type buildConfig struct {
	aodPath, aodRelease, aodSHA string
	listPath, listCommit        string
	mirrorPath, mirrorCommit    string
	overlayDir, prevPath        string
	reviewedDir                 string
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
	fs.StringVar(&c.reviewedDir, "reviewed", review.Dir, "acceptance records, validated only")
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
	body, err := release.Encode(doc)
	if err != nil {
		return err
	}
	if rtErr := roundTrip(body); rtErr != nil {
		return rtErr
	}
	st.Bytes = len(body)
	if cmpErr := compare(&c, doc, st, log); cmpErr != nil {
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
	log.Info("build: done", "entries", st.Populations.Entries, "anime", st.Join.AnimeRows, "bytes", st.Bytes, "overlay", st.OverlayEntries,
		"unparsed_rows", st.Join.UnparsedRows, "changed", st.Changed)
	_, err = fmt.Fprintf(stdout, "content_hash=%s\nprevious_hash=%s\nchanged=%t\n", st.ContentHash, st.PreviousHash, st.Changed)
	return err
}

// roundTrip refuses a body mapread.Decode refuses, or one it does not
// re-encode byte for byte.
func roundTrip(body []byte) error {
	back, err := mapread.Decode(bytes.NewReader(body), mapread.DefaultLimits())
	if err != nil {
		return fmt.Errorf("build: output does not decode: %w", err)
	}
	again, err := mapread.Encode(back)
	if err != nil {
		return err
	}
	if !bytes.Equal(again, body) {
		return errors.New("build: output does not re-encode as written")
	}
	return nil
}

func assemble(c *buildConfig, log *slog.Logger) (*mapread.Document, *buildStats, error) {
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
	links, err := overlay.LoadLinks(c.overlayDir)
	if err != nil {
		return nil, nil, err
	}
	if _, recErr := review.LoadDir(c.reviewedDir); recErr != nil {
		return nil, nil, recErr
	}
	patched := overlay.Apply(list.Nodes, ov)
	proven, proofErr := proveLinks(links, patched, ov, facts, entries)
	if proofErr != nil {
		return nil, nil, fmt.Errorf("build: %w", proofErr)
	}
	anime, rows, jst, ls := joinTables(&join.Inputs{
		Entries: entries, Nodes: patched, Facts: facts, Overlay: ov, Links: proven.applied,
	}, proven.skipped, log)
	rep := guard.Collisions(list.Nodes, ov, proven.applied, facts)
	redundant := facts.RedundantRows()
	if len(redundant) > 0 {
		log.Info("build: counts rows that equal what the sources give; delete them", "path", anidb.CountsPath, "anidb", redundant)
	}
	bs, err := collisions(c.baselinePath, &rep, log)
	if err != nil {
		return nil, nil, err
	}
	ovHash, err := overlay.SetHash(ov, links)
	if err != nil {
		return nil, nil, err
	}
	doc := &mapread.Document{
		Version:     mapread.Version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Sources: mapread.Sources{
			AnimeOfflineDatabase: mapread.OfflineDatabaseSource{
				Repository: "https://github.com/cedya77/anime-offline-database",
				Release:    c.aodRelease, Asset: "anime-offline-database.jsonl", SHA256: digest,
			},
			AnimeLists: mapread.AnimeListsSource{
				Repository: "https://github.com/Anime-Lists/anime-lists",
				Commit:     c.listCommit, File: "anime-list-master.xml",
			},
			AniDBMirror: mapread.AniDBMirrorSource{Repository: anidb.Repository, Commit: facts.Commit()},
			Overlay:     mapread.OverlaySource{Entries: len(ov), Links: ls.Applied, SHA256: ovHash},
		},
		Attribution: release.DefaultAttribution,
		Anime:       anime,
		Entries:     rows,
	}
	st := &buildStats{
		Populations: release.Census(doc), Join: jst, OverlayEntries: len(ov), Collisions: rep, Baseline: bs,
		Links: ls, CountsRedundant: redundant,
	}
	return doc, st, nil
}

// joinTables builds the tables and logs what the join left out, the
// skipped links included.
func joinTables(in *join.Inputs, skipped []join.Skipped, log *slog.Logger) ([]mapread.Anime, []mapread.Entry, join.Stats, linkStats) {
	anime, rows, jst := join.Build(in)
	ls := linkStats{Applied: in.Links.Len(), Skipped: skipped}
	for _, s := range ls.Skipped {
		log.Warn("build: overlay link not applied", "anilist", s.AniList, "reason", s.Reason)
	}
	if jst.UnknownSources > 0 {
		log.Warn("build: anime-offline-database sources on a service animap does not publish", "sources", jst.UnknownSources, "hosts", unknownHosts(in.Entries))
	}
	return anime, rows, jst, ls
}

// unknownHosts are the distinct hosts of the database sources animap does
// not publish, sorted.
func unknownHosts(entries []offlinedb.Entry) []string {
	var out []string
	for i := range entries {
		out = append(out, entries[i].Unknown...)
	}
	slices.Sort(out)
	return slices.Compact(out)
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

func compare(c *buildConfig, doc *mapread.Document, st *buildStats, log *slog.Logger) error {
	var err error
	if st.ContentHash, err = release.ContentHash(doc); err != nil {
		return err
	}
	prev, err := readPrevious(c.prevPath)
	if v, ok := errors.AsType[*mapread.VersionError](err); ok {
		log.Info("build: the previous release is another version, so nothing compares; checking the floors", "previous_version", v.Found, "version", mapread.Version)
		prev, err = nil, nil
	}
	if err != nil {
		return err
	}
	if prev != nil {
		pp := release.Census(prev)
		st.PreviousPopulations = &pp
		if st.PreviousHash, err = release.ContentHash(prev); err != nil {
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

// readPrevious reads the previous release; a missing path or file is none.
func readPrevious(path string) (*mapread.Document, error) {
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
	return mapread.Decode(f, mapread.DefaultLimits())
}
