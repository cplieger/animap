package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
)

// dateLead is a lead with what its issue shows: the node as checked, the
// overlay entry that patched it, and the series slug for TVDB links.
type dateLead struct {
	Node    string `json:"node"`
	Overlay string `json:"overlay,omitempty"`
	Slug    string `json:"slug,omitempty"`
	datecheck.Lead
}

// dateResult is one date-check run. Evaluated lists the keys of the nodes
// it judged; Owned lists every node's key, judged or not, so a lead whose
// key it lacks belongs to a node that left the list.
type dateResult struct {
	Leads     []dateLead `json:"leads"`
	Evaluated []string   `json:"evaluated"`
	Owned     []string   `json:"owned"`
	Stats     dateStats  `json:"stats"`
}

type dateStats struct {
	Kinds       map[string]int `json:"kinds"`
	Elapsed     string         `json:"elapsed"`
	Nodes       int            `json:"nodes"`
	Evaluated   int            `json:"evaluated"`
	Unlisted    int            `json:"unlisted"`
	NoLayout    int            `json:"no_layout"`
	Stale       int            `json:"stale"`
	Series      int            `json:"series"`
	Due         int            `json:"due"`
	Requests    int            `json:"requests"`
	Absent      int            `json:"absent"`
	FetchFailed int            `json:"fetch_failed"`
	Leads       int            `json:"leads"`
}

type dateConfig struct {
	listPath, overlayDir, mirrorPath string
	layoutsPath, out                 string
	maxAge, budget                   time.Duration
}

func runDateCheck(ctx context.Context, args []string, log *slog.Logger) error {
	var c dateConfig
	fs := flag.NewFlagSet("datecheck", flag.ContinueOnError)
	fs.StringVar(&c.listPath, "list", "", "anime-list-master.xml at Anime-Lists HEAD")
	fs.StringVar(&c.overlayDir, "overlay", "overlay", "overlay directory")
	fs.StringVar(&c.mirrorPath, "mirror", "", "AniDB mirror snapshot written by animap mirror extract")
	fs.StringVar(&c.layoutsPath, "layouts", "", "TVDB layouts kept between runs")
	fs.DurationVar(&c.maxAge, "max-age", datecheck.RefetchAge, "refetch a series layout older than this")
	fs.DurationVar(&c.budget, "budget", 90*time.Minute, "stop fetching SkyHook after this long")
	fs.StringVar(&c.out, "out", "datecheck.json", "output path")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "list", "mirror"); err != nil {
		return err
	}
	start := time.Now()
	list, err := readList(c.listPath)
	if err != nil {
		return err
	}
	entries, err := overlay.LoadDir(c.overlayDir)
	if err != nil {
		return err
	}
	snap, err := anidb.LoadSnapshot(c.mirrorPath)
	if err != nil {
		return err
	}
	facts := anidb.NewFacts(snap, nil, nil)
	layouts, err := datecheck.LoadLayouts(c.layoutsPath)
	if err != nil && layouts == nil {
		return err
	}
	if err != nil {
		log.Warn("datecheck: layouts unusable", "error", err)
	}
	patched := overlay.Apply(list.Nodes, entries)
	var st dateStats
	series := checkedSeries(patched, facts)
	st.Series = len(series)
	stale := refresh(ctx, &c, layouts, series, &st, log)
	layouts.Prune(series)
	if c.layoutsPath != "" {
		if saveErr := writeJSON(ctx, c.layoutsPath, layouts); saveErr != nil {
			log.Warn("datecheck: layouts not saved", "error", saveErr)
		}
	}
	res := evaluate(patched, entries, facts, layouts, stale, start, &st)
	st.Elapsed = time.Since(start).Round(time.Second).String()
	res.Stats = st
	log.Info("datecheck: done", "nodes", st.Nodes, "evaluated", st.Evaluated, "leads", st.Leads, "series", st.Series,
		"requests", st.Requests, "fetch_failed", st.FetchFailed, "stale", st.Stale, "elapsed", st.Elapsed)
	return writeJSON(ctx, c.out, res)
}

// checkedSeries are the series of the nodes AniDB lists episodes for, the
// only ones a check reads.
func checkedSeries(patched map[int]*animelists.Node, facts *anidb.Facts) []int {
	set := map[int]bool{}
	for aid, n := range patched {
		if s, ok := datecheck.Series(n); ok {
			if _, listed := facts.Anime(aid); listed {
				set[s] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// refresh fetches the due layouts, one request per pace, until the budget
// is spent, and returns the due series it did not refresh. A series keeps
// its older layout for a later run, but that layout no longer speaks for
// TVDB, so none of its nodes is judged on it.
func refresh(ctx context.Context, c *dateConfig, layouts datecheck.Layouts, series []int, st *dateStats, log *slog.Logger) map[int]bool {
	due := layouts.Due(series, c.maxAge, time.Now())
	st.Due = len(due)
	left := make(map[int]bool, len(due))
	for _, id := range due {
		left[id] = true
	}
	if len(due) == 0 {
		return left
	}
	client := source.NewMemory(pace, log)
	stop := time.Now().Add(c.budget)
	for _, id := range due {
		if ctx.Err() != nil || time.Now().After(stop) {
			log.Warn("datecheck: fetch budget spent, the series left are not checked this run", "left", len(left))
			return left
		}
		st.Requests++
		show, err := skyhook.Fetch(ctx, client, id)
		switch {
		case errors.Is(err, source.ErrNotFound):
			st.Absent++
			layouts[id] = datecheck.Layout{Fetched: time.Now().UTC(), Absent: true}
			delete(left, id)
		case err != nil:
			st.FetchFailed++
			log.Warn("datecheck: SkyHook unreadable, the series is not checked this run", "series", id, "error", err)
		default:
			layouts[id] = datecheck.LayoutOf(show, time.Now().UTC())
			delete(left, id)
		}
	}
	return left
}

func evaluate(patched map[int]*animelists.Node, entries []overlay.Entry, facts *anidb.Facts, layouts datecheck.Layouts, stale map[int]bool, now time.Time, st *dateStats) dateResult {
	paths := make(map[int]string, len(entries))
	for i := range entries {
		paths[entries[i].AniDBID] = entries[i].Path()
	}
	var res dateResult
	st.Kinds = map[string]int{}
	for _, aid := range slices.Sorted(maps.Keys(patched)) {
		n := patched[aid]
		st.Nodes++
		res.Owned = append(res.Owned, datecheck.Key(aid))
		series, mapped := datecheck.Series(n)
		a, listed := facts.Anime(aid)
		lay, laid := layouts[series]
		switch {
		case mapped && !listed:
			st.Unlisted++
			continue
		case mapped && !laid:
			st.NoLayout++
			continue
		case mapped && stale[series]:
			st.Stale++
			continue
		}
		st.Evaluated++
		res.Evaluated = append(res.Evaluated, datecheck.Key(aid))
		if !mapped {
			continue
		}
		if l := datecheck.Check(n, a, &lay, now); l != nil {
			res.Leads = append(res.Leads, dateLead{Lead: *l, Node: n.Canonical(), Overlay: paths[aid], Slug: lay.Slug})
			countKinds(st.Kinds, l)
		}
	}
	st.Leads = len(res.Leads)
	return res
}

func countKinds(kinds map[string]int, l *datecheck.Lead) {
	if l.Dead {
		kinds[string(datecheck.DeadSeries)]++
		return
	}
	seen := map[datecheck.Kind]bool{}
	for _, f := range l.Findings {
		if !seen[f.Kind] {
			seen[f.Kind] = true
			kinds[string(f.Kind)]++
		}
	}
}
