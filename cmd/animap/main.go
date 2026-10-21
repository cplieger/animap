// Command animap verifies a mapping export before it is published as a
// release of animap.json.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/release"
	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/slogx"
)

const usage = `usage: animap verify -in file [-previous file] [-accept-shrink] [-accept-stale]

commands:
  verify  decode a mapping export, check its census against the latest
          release and its freshness, and print what the release notes carry
`

var (
	// errUsage marks a bad invocation (exit 2) rather than a failed run (exit 1).
	errUsage = errors.New("usage")
	errStale = errors.New("verify: the export is stale")
)

// What -previous held, as verdict.Previous names it.
const (
	previousNone       = "none"
	previousComparable = "comparable"
	// previousEarlier is a release in the earlier shape of version 2,
	// which mapread refuses: the export is the first of its shape.
	previousEarlier = "earlier-shape"
	previousOther   = "other-version"
)

func main() {
	slogx.Setup(slogx.Options{Output: os.Stderr, Format: slogx.Text, Level: slog.LevelInfo})
	err := run(os.Args[1:], os.Stdout, slog.Default(), time.Now())
	switch {
	case err == nil:
	case errors.Is(err, errUsage):
		fmt.Fprint(os.Stderr, usage)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	default:
		slog.Error("animap: failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, log *slog.Logger, now time.Time) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: no command", errUsage)
	}
	if args[0] != "verify" {
		return fmt.Errorf("%w: unknown command %q", errUsage, args[0])
	}
	return runVerify(args[1:], stdout, log, now)
}

// verdict is what verify prints: one JSON object, which the publish script
// reads for the release notes.
type verdict struct {
	PreviousCensus *release.Populations `json:"previous_census,omitempty"`
	ContentHash    string               `json:"content_hash"`
	Previous       string               `json:"previous"`
	PreviousHash   string               `json:"previous_hash,omitempty"`
	GeneratedAt    string               `json:"generated_at"`
	Revision       string               `json:"revision"`
	TitlesDump     string               `json:"titles_dump"`
	Notice         string               `json:"notice"`
	Stale          []string             `json:"stale,omitempty"`
	Census         release.Populations  `json:"census"`
	Changed        bool                 `json:"changed"`
}

func runVerify(args []string, stdout io.Writer, log *slog.Logger, now time.Time) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	in := fs.String("in", "", "the export's animap.json")
	prevPath := fs.String("previous", "", "the latest release's animap.json (a missing file: no release)")
	acceptShrink := fs.Bool("accept-shrink", false, "accept a population below 90% of the latest release")
	acceptStale := fs.Bool("accept-stale", false, "accept an export older than the freshness bounds")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: verify: %w", errUsage, err)
	}
	switch {
	case fs.NArg() > 0:
		return fmt.Errorf("%w: verify: unexpected argument %q", errUsage, fs.Arg(0))
	case *in == "":
		return fmt.Errorf("%w: verify needs -in", errUsage)
	}
	doc, err := readDocument(*in)
	if err != nil {
		return fmt.Errorf("verify: %s: %w", *in, err)
	}
	v := verdict{
		GeneratedAt: doc.GeneratedAt, Revision: doc.Sources.Mapping.Revision, TitlesDump: doc.Sources.AniDB.TitlesDump,
		Notice: doc.Attribution.Notice, Census: release.Census(doc),
	}
	if v.ContentHash, err = mapread.ContentHash(doc); err != nil {
		return err
	}
	stale, err := release.Stale(doc, now)
	switch {
	case err != nil:
		return err
	case len(stale) > 0 && !*acceptStale:
		return fmt.Errorf("%w: %s", errStale, strings.Join(stale, "; "))
	case len(stale) > 0:
		log.Warn("verify: the export is stale, accepted with -accept-stale", "reasons", stale)
		v.Stale = stale
	}
	prev, err := readPrevious(*prevPath, &v)
	if err != nil {
		return fmt.Errorf("verify: %s: %w", *prevPath, err)
	}
	if prev != nil {
		pp := release.Census(prev)
		v.PreviousCensus = &pp
		if v.PreviousHash, err = mapread.ContentHash(prev); err != nil {
			return err
		}
	}
	v.Changed = v.ContentHash != v.PreviousHash
	if err := release.Coverage(v.PreviousCensus, v.Census, *acceptShrink); err != nil {
		return err
	}
	log.Info("verify: the export passes", "content_hash", v.ContentHash, "previous", v.Previous, "changed", v.Changed,
		"revision", v.Revision, "titles_dump", v.TitlesDump)
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(&v)
}

func readDocument(path string) (*mapread.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return mapread.Decode(f, mapread.DefaultLimits())
}

// readPrevious reads the latest release into a document mapread can
// compare, recording in v.Previous what it held. A missing path or file is
// no release; a release of another version or of the earlier shape is
// none to compare, so the coverage guard checks the floors.
func readPrevious(path string, v *verdict) (*mapread.Document, error) {
	v.Previous = previousNone
	if path == "" {
		return nil, nil
	}
	doc, err := readDocument(path)
	_, other := errors.AsType[*mapread.VersionError](err)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case other:
		v.Previous = previousOther
		return nil, nil
	case errors.Is(err, mapread.ErrVersion):
		v.Previous = previousEarlier
		return nil, nil
	case err != nil:
		return nil, err
	}
	v.Previous = previousComparable
	return doc, nil
}
