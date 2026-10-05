package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/counts"
	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
)

// pace is the minimum gap between two requests to one host.
const pace = time.Second

// driftResult is one drift run. Owned lists every key the overlay's current
// entries and bridges and the current counts rows can hold; Complete says
// the run read all of them, which is what gives it authority over a
// deleted entry's or row's issues.
type driftResult struct {
	Findings  []drift.Finding `json:"findings"`
	Evaluated []string        `json:"evaluated"`
	Owned     []string        `json:"owned"`
	Complete  bool            `json:"complete"`
}

func runDrift(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("drift", flag.ContinueOnError)
	overlayDir := fs.String("overlay", "overlay", "overlay directory")
	listPath := fs.String("list", "", "anime-list-master.xml at Anime-Lists HEAD")
	releasePath := fs.String("release", "", "latest published animap.json, for whether a bridge is still needed")
	cachePath := fs.String("cache", "", "HTTP validator cache file")
	countsPath := fs.String("counts", counts.Path, "tracked episode counts")
	aodPath := fs.String("aod", "", "anime-offline-database .jsonl the latest release was built from; without it the counts rows are not checked")
	out := fs.String("out", "drift.json", "output path")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "list"); err != nil {
		return err
	}
	list, err := readList(*listPath)
	if err != nil {
		return err
	}
	entries, bridges, err := loadOverlay(*overlayDir)
	if err != nil {
		return err
	}
	linked, err := releaseLinks(*releasePath)
	if err != nil {
		return err
	}
	rows, database, err := loadCountInputs(*countsPath, *aodPath)
	if err != nil {
		return err
	}
	client, err := source.New(*cachePath, pace, log)
	if err != nil {
		return err
	}
	o := observer{client: client, log: log}
	res := driftResult{Complete: true}
	for i := range entries {
		e := &entries[i]
		obs := o.observe(ctx, list.Nodes[e.AniDBID], e.AniDBID, e.Captured.TVDB)
		res.add(drift.Decide(drift.OfEntry(e), &obs))
		res.Owned = append(res.Owned, drift.KeysOf(e.AniDBID).All()...)
	}
	patched := overlay.Apply(list.Nodes, entries)
	for i := range bridges {
		b := &bridges[i]
		obs := o.observe(ctx, patched[b.ParentAniDBID], b.ParentAniDBID, b.Captured.TVDB)
		if linked != nil {
			obs.Linked, obs.LinkedRead = linked[b.AniListID], true
		}
		res.add(drift.Decide(drift.OfBridge(b), &obs))
		res.Owned = append(res.Owned, drift.SpecialKeys(b.AniListID).All()...)
	}
	for i := range rows {
		r := &rows[i]
		obs := drift.Observation{DatabaseRead: database != nil}
		obs.DatabaseEpisodes, obs.InDatabase = database[r.AniDBID]
		res.add(drift.Decide(drift.OfRow(r), &obs))
		res.Owned = append(res.Owned, drift.CountKey(r.AniDBID))
	}
	if err := client.Save(ctx); err != nil {
		log.Warn("drift: cache not saved", "error", err)
	}
	log.Info("drift: done", "entries", len(entries), "bridges", len(bridges), "counts", len(rows), "findings", len(res.Findings))
	return writeJSON(ctx, *out, res)
}

func (r *driftResult) add(f []drift.Finding, ev []string) {
	r.Findings = append(r.Findings, f...)
	r.Evaluated = append(r.Evaluated, ev...)
}

// loadCountInputs returns a nil database when no path was given, which
// leaves every row's key unevaluated.
func loadCountInputs(countsPath, aodPath string) ([]counts.Row, map[int]int, error) {
	rows, err := counts.Load(countsPath)
	if err != nil || aodPath == "" {
		return rows, nil, err
	}
	_, entries, err := offlinedb.Load(aodPath, offlinedb.DefaultLimits)
	if err != nil {
		return nil, nil, err
	}
	return rows, episodeCounts(entries), nil
}

func loadOverlay(dir string) ([]overlay.Entry, []overlay.Bridge, error) {
	entries, err := overlay.LoadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	bridges, err := overlay.LoadBridges(dir)
	return entries, bridges, err
}

// releaseLinks is nil when no document was given, which leaves every
// bridge's landed key unevaluated.
func releaseLinks(path string) (map[int]int, error) {
	if path == "" {
		return nil, nil
	}
	doc, err := readPrevious(path)
	if err != nil || doc == nil {
		return nil, err
	}
	out := make(map[int]int, len(doc.Records))
	for i := range doc.Records {
		if r := &doc.Records[i]; r.AniListID > 0 {
			out[r.AniListID] = r.AniDBID
		}
	}
	return out, nil
}

type observer struct {
	client *source.Client
	log    *slog.Logger
}

func (o observer) observe(ctx context.Context, node *animelists.Node, aid int, tv *overlay.CapturedTVDB) drift.Observation {
	obs := drift.Observation{Node: node}
	if tv == nil {
		return obs
	}
	show, err := skyhook.Fetch(ctx, o.client, tv.Series)
	switch {
	case errors.Is(err, source.ErrNotFound):
		obs.LayoutAbsent = true
	case err != nil:
		o.log.Warn("drift: SkyHook unreadable, layout left unevaluated", "anidb", aid, "series", tv.Series, "error", err)
	default:
		obs.Layout, obs.LayoutRead = skyhook.Layout(show, tv.Seasons), true
	}
	return obs
}

// runCapture is how an author writes an entry's fingerprints and how a
// reviewed drift is acknowledged.
func runCapture(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("overlay capture", flag.ContinueOnError)
	entryPath := fs.String("entry", "", "overlay/<anidbid>.json to update in place")
	listPath := fs.String("list", "", "anime-list-master.xml at -commit")
	commit := fs.String("commit", "", "Anime-Lists commit of -list")
	cachePath := fs.String("cache", "", "HTTP validator cache file")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "entry", "list", "commit"); err != nil {
		return err
	}
	body, err := os.ReadFile(*entryPath)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var e overlay.Entry
	if err = dec.Decode(&e); err != nil {
		return fmt.Errorf("overlay capture: %s: %w", *entryPath, err)
	}
	list, err := readList(*listPath)
	if err != nil {
		return err
	}
	client, err := source.New(*cachePath, pace, log)
	if err != nil {
		return err
	}
	upstream := list.Nodes[e.AniDBID]
	tv, err := captureTVDB(ctx, client, overlay.Patch(upstream, &e), &e)
	if err != nil {
		return err
	}
	e.Captured = overlay.Captured{
		At: time.Now().UTC().Format(time.DateOnly), AnimeListsCommit: *commit, NodeSHA256: animelists.HashOf(upstream),
		TVDB: tv,
	}
	e.Create = upstream == nil
	if vErr := e.Validate(); vErr != nil {
		return vErr
	}
	if err = client.Save(ctx); err != nil {
		log.Warn("overlay capture: cache not saved", "error", err)
	}
	out, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(ctx, *entryPath, append(out, '\n'))
}

// captureTVDB returns nil for a node with no TVDB series, a film routed by
// TMDB alone.
func captureTVDB(ctx context.Context, client *source.Client, patched *animelists.Node, e *overlay.Entry) (*overlay.CapturedTVDB, error) {
	series, err := seriesOf(patched)
	if errors.Is(err, errNoSeries) {
		return nil, nil
	}
	show, err := skyhook.Fetch(ctx, client, series)
	if err != nil {
		return nil, fmt.Errorf("overlay capture: SkyHook %d: %w", series, err)
	}
	seasons := overlay.TouchedSeasons(patched, e)
	eps := skyhook.Layout(show, seasons)
	return &overlay.CapturedTVDB{Series: series, Seasons: seasons, Episodes: eps, SHA256: skyhook.Hash(eps)}, nil
}

var errNoSeries = errors.New("patched node has no TVDB series id")

func seriesOf(n *animelists.Node) (int, error) {
	var id int
	if n == nil {
		return 0, errNoSeries
	}
	if _, err := fmt.Sscanf(n.Attr("tvdbid"), "%d", &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("%w (tvdbid %q)", errNoSeries, n.Attr("tvdbid"))
	}
	return id, nil
}

func runCheck(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("overlay check", flag.ContinueOnError)
	overlayDir := fs.String("overlay", "overlay", "overlay directory")
	listPath := fs.String("list", "", "anime-list-master.xml")
	aodPath := fs.String("aod", "", "anime-offline-database .jsonl, for sibling episode counts")
	countsPath := fs.String("counts", counts.Path, "tracked episode counts")
	baselinePath := fs.String("baseline", "checks/collision-baseline.json", "tracked collision baseline")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "list", "aod"); err != nil {
		return err
	}
	list, err := readList(*listPath)
	if err != nil {
		return err
	}
	entries, err := overlay.LoadDir(*overlayDir)
	if err != nil {
		return err
	}
	bridges, err := overlay.LoadBridges(*overlayDir)
	if err != nil {
		return err
	}
	_, aod, err := offlinedb.Load(*aodPath, offlinedb.DefaultLimits)
	if err != nil {
		return err
	}
	episodes, err := placementCounts(aod, *countsPath)
	if err != nil {
		return err
	}
	patched := overlay.Apply(list.Nodes, entries)
	var proofs []error
	for i := range bridges {
		if proofErr := bridges[i].Prove(patched[bridges[i].ParentAniDBID]); proofErr != nil {
			proofs = append(proofs, proofErr)
		}
	}
	rep, _, colErr := collisions(*baselinePath, patched, entries, bridges, episodes, slog.New(slog.DiscardHandler))
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "%s\n", b); err != nil {
		return err
	}
	return errors.Join(append(proofs, colErr)...)
}
