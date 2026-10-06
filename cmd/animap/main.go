// Command animap builds the animap.json mapping and runs the checks that
// keep it honest: overlay drift, the watch set and the issue plan.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/slogx"
)

const usage = `usage: animap <command> [flags]

commands:
  build            join the upstreams and the overlay into animap.json
  drift            check every overlay entry against upstream
  watch            check a watch set against a published animap.json
  issues           plan issue changes from drift and watch results
  overlay capture  record an entry's fingerprints
  overlay capture-special
                   record a special-of-parent bridge's fingerprints
  overlay check    run the collision check and the bridge proofs
  baseline         write, prune, rebase or check the collision baseline
  mirror extract   keep AniDB's episode lists from the mirror's archive on stdin
`

// errUsage marks a bad invocation (exit 2) rather than a failed run (exit 1).
var errUsage = errors.New("usage")

func main() {
	slogx.Setup(slogx.Options{Output: os.Stderr, Format: slogx.Text, Level: slog.LevelInfo})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
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

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: no command", errUsage)
	}
	log := slog.Default()
	switch args[0] {
	case "build":
		return runBuild(ctx, args[1:], stdout, log)
	case "drift":
		return runDrift(ctx, args[1:], log)
	case "watch":
		return runWatch(ctx, args[1:], log)
	case "issues":
		return runIssues(ctx, args[1:])
	case "baseline":
		return runBaseline(ctx, args[1:], stdout)
	case "mirror":
		return runMirror(ctx, args[1:], os.Stdin, stdout, log)
	case "overlay":
		if len(args) < 2 {
			return fmt.Errorf("%w: overlay needs capture or check", errUsage)
		}
		switch args[1] {
		case "capture":
			return runCapture(ctx, args[2:], log)
		case "capture-special":
			return runCaptureSpecial(ctx, args[2:], log)
		case "check":
			return runCheck(args[2:], stdout)
		}
	}
	return fmt.Errorf("%w: unknown command %q", errUsage, args[0])
}

func parse(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %s: %w", errUsage, fs.Name(), err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: %s: unexpected argument %q", errUsage, fs.Name(), fs.Arg(0))
	}
	return nil
}

func required(fs *flag.FlagSet, names ...string) error {
	for _, n := range names {
		if fs.Lookup(n).Value.String() == "" {
			return fmt.Errorf("%w: %s needs -%s", errUsage, fs.Name(), n)
		}
	}
	return nil
}

func writeJSON(ctx context.Context, path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(ctx, path, append(b, '\n'))
}

// writeFile replaces path atomically; atomicfile wants an absolute path.
func writeFile(ctx context.Context, path string, data []byte) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	_, err = atomicfile.WriteFile(ctx, abs, data)
	return err
}

func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxInputBytes {
		return fmt.Errorf("%s exceeds %d bytes", path, maxInputBytes)
	}
	return json.Unmarshal(body, v)
}
