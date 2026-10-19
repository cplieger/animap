//go:build integration

package mapread

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// earlierReader decodes each file it is given with mapread v0.1.0, the
// reader of the earlier version 2 shape, and prints one verdict per line.
const earlierReader = `package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/cplieger/animap/mapread"
)

func main() {
	for _, p := range os.Args[1:] {
		f, err := os.Open(p)
		if err != nil {
			panic(err)
		}
		_, err = mapread.Decode(f, mapread.DefaultLimits())
		f.Close()
		fmt.Printf("%s invalid=%t err=%v\n", p, errors.Is(err, mapread.ErrInvalid), err)
	}
}
`

// The two shapes refuse each other: mapread v0.1.0, fetched from the
// module proxy, accepts the earlier-shape fixture and refuses every
// golden of this shape for its missing sources. It needs the go command
// and the network: go test -tags integration -run TestEarlierReader .
func TestEarlierReaderRefusesThisShape(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(earlierReader), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), gobin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("mod", "init", "example.com/earlier")
	run("get", "github.com/cplieger/animap/mapread@v0.1.0")
	files := append([]string{"testdata/earlier-shape.json"}, goldens...)
	paths := make([]string, len(files))
	for i, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			t.Fatal(err)
		}
		paths[i] = abs
	}
	verdicts := strings.Split(strings.TrimSpace(run(append([]string{"run", "."}, paths...)...)), "\n")
	if len(verdicts) != len(paths) {
		t.Fatalf("got %d verdicts for %d files:\n%s", len(verdicts), len(paths), strings.Join(verdicts, "\n"))
	}
	if want := paths[0] + " invalid=false err=<nil>"; verdicts[0] != want {
		t.Errorf("v0.1.0 on the earlier-shape fixture: %s, want %s", verdicts[0], want)
	}
	for i, v := range verdicts[1:] {
		if !strings.HasPrefix(v, paths[i+1]+" invalid=true ") || !strings.Contains(v, "sources.anime_offline_database is missing") {
			t.Errorf("v0.1.0 on %s: %s, want ErrInvalid for its missing sources", files[i+1], v)
		}
	}
}
