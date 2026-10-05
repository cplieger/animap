package guard

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func col(series int, target string, nodes ...int) Collision {
	return Collision{Series: series, Target: target, Nodes: nodes}
}

func TestBaselineSplit(t *testing.T) {
	b := NewBaseline([]Collision{col(10, "TVDB 0x1", 1, 2), col(20, "TVDB 1x1", 3, 4)}, nil, "c")
	baselined, novel, stale := b.Split([]Collision{col(10, "TVDB 0x1", 1, 2), col(10, "TVDB 0x2", 1, 2), col(20, "TVDB 1x1", 3, 9)})
	if len(baselined) != 1 || len(novel) != 2 || len(stale) != 1 || stale[0].Series != 20 {
		t.Errorf("Split = baselined %v, novel %v, stale %v; want 10/0x1 accepted, a new target and a new claimant refused, 20/1x1 stale", baselined, novel, stale)
	}
	if narrowed := b.Prune([]Collision{col(10, "TVDB 0x1", 1)}, nil); len(narrowed.Collisions) != 1 || len(narrowed.Collisions[0].Nodes) != 1 {
		t.Errorf("Prune = %+v, want 10/0x1 narrowed to node 1 and 20/1x1 gone", narrowed.Collisions)
	}
}

func TestCheckShrink(t *testing.T) {
	base := NewBaseline([]Collision{col(10, "TVDB 0x1", 1, 2), col(20, "TVDB 1x1", 3, 4)}, nil, "c")
	for _, tc := range []struct {
		name string
		head []Collision
		ok   bool
	}{
		{"unchanged", []Collision{col(10, "TVDB 0x1", 1, 2), col(20, "TVDB 1x1", 3, 4)}, true},
		{"an entry removed", []Collision{col(10, "TVDB 0x1", 1, 2)}, true},
		{"a node removed", []Collision{col(10, "TVDB 0x1", 1)}, true},
		{"an entry added", []Collision{col(10, "TVDB 0x1", 1, 2), col(30, "TVDB 0x9", 5, 6)}, false},
		{"a node added", []Collision{col(10, "TVDB 0x1", 1, 2, 7)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckShrink(base, NewBaseline(tc.head, nil, "c"))
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, ErrBaselineGrew)) {
				t.Errorf("CheckShrink(%s) = %v", tc.name, err)
			}
		})
	}
}

func TestBaselineUncounted(t *testing.T) {
	a, b, c := Uncounted{Series: 10, AniDB: 1}, Uncounted{Series: 10, AniDB: 2}, Uncounted{Series: 20, AniDB: 3}
	base := NewBaseline(nil, []Uncounted{b, a, a}, "c")
	if !slices.Equal(base.Uncounted, []Uncounted{a, b}) {
		t.Errorf("NewBaseline uncounted = %+v, want %+v sorted and deduplicated", base.Uncounted, []Uncounted{a, b})
	}
	baselined, novel, stale := base.SplitUncounted([]Uncounted{a, c})
	if !slices.Equal(baselined, []Uncounted{a}) || !slices.Equal(novel, []Uncounted{c}) || !slices.Equal(stale, []Uncounted{b}) {
		t.Errorf("SplitUncounted = baselined %v, novel %v, stale %v; want a accepted, c refused, b stale", baselined, novel, stale)
	}
	if pruned := base.Prune(nil, []Uncounted{a, c}); !slices.Equal(pruned.Uncounted, []Uncounted{a}) {
		t.Errorf("Prune uncounted = %+v, want only a", pruned.Uncounted)
	}
	if err := CheckShrink(base, NewBaseline(nil, []Uncounted{a}, "c")); err != nil {
		t.Errorf("CheckShrink with an uncounted node removed = %v", err)
	}
	if err := CheckShrink(base, NewBaseline(nil, []Uncounted{a, b, c}, "c")); !errors.Is(err, ErrBaselineGrew) {
		t.Errorf("CheckShrink with an uncounted node added = %v, want ErrBaselineGrew", err)
	}
}

func TestLoadBaseline(t *testing.T) {
	dir := t.TempDir()
	if b, err := LoadBaseline(filepath.Join(dir, "absent.json")); err != nil || len(b.Collisions) != 0 {
		t.Errorf("LoadBaseline(absent) = %+v, %v; want empty", b, err)
	}
	p := filepath.Join(dir, "b.json")
	if err := os.WriteFile(p, []byte(`{"version":2,"collisions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(p); err == nil {
		t.Error("LoadBaseline accepted version 2")
	}
	if err := os.WriteFile(p, []byte(`{"version":1,"collisions":[],"extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(p); err == nil {
		t.Error("LoadBaseline accepted an unknown field")
	}
}
