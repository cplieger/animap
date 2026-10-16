package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
)

type getterFunc func() ([]byte, error)

func (g getterFunc) Get(context.Context, string, int64) ([]byte, error) { return g() }

// Drift reads a link on anime-offline-database and the mirror alone, each
// check evaluated only when its source was read.
func TestDriftDecidesLinks(t *testing.T) {
	a := titled(fin(3, 1))
	facts := anidb.NewFacts(&anidb.Snapshot{Commit: mirrorCommit, Anime: map[int]anidb.Anime{1983: a}}, nil, nil)
	l := linkFile(376, overlay.Part{AniDBID: 1983, Specials: []int{1}})
	l.Captured.AniDBSHA256 = l.AniDBDigest(facts)
	retitled := titled(fin(3, 1))
	retitled.Specials[0].Titles = strings.Repeat("9", 64)
	moved := anidb.NewFacts(&anidb.Snapshot{Commit: strings.Repeat("e", 40), Anime: map[int]anidb.Anime{1983: retitled}}, nil, nil)
	for _, tc := range []struct {
		aod       map[int][]int
		facts     *anidb.Facts
		name      string
		want      []string
		evaluated bool
	}{
		{name: "unchanged", aod: map[int][]int{}, facts: facts, evaluated: true},
		{name: "landed", aod: map[int][]int{376: {1983}}, facts: facts, want: []string{"landed"}, evaluated: true},
		{name: "the linked special retitled", aod: map[int][]int{}, facts: moved, want: []string{"anidb-changed"}, evaluated: true},
		{name: "no database", facts: facts},
		{name: "no mirror", aod: map[int][]int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &reviewRun{links: []overlay.Link{l}, aod: tc.aod, facts: tc.facts}
			subjects := map[string]*subject{}
			r.addDrift(subjects)
			s := subjects[review.AniListKey(376)]
			var got []string
			if s != nil {
				for _, f := range s.Sections.Drift.Findings {
					got = append(got, string(f.Cause))
					if f.Cause == drift.AniDBChanged && f.Mirror != strings.Repeat("e", 40) {
						t.Errorf("finding %+v, want the mirror commit it was read at", f)
					}
				}
			}
			evaluated := s == nil || s.Sections.Drift.Evaluated
			if !slices.Equal(got, tc.want) || evaluated != tc.evaluated {
				t.Errorf("findings %v evaluated %t, want %v and %t", got, evaluated, tc.want, tc.evaluated)
			}
		})
	}
}

// Two links on two specials of one parent that its rows put on one TVDB
// episode are refused: the guard sees one node for both.
func TestProveLinksRefusesTwoLinksOnOneTVDBEpisode(t *testing.T) {
	facts := anidb.NewFacts(&anidb.Snapshot{Anime: map[int]anidb.Anime{1983: fin(1, 1, 2)}}, nil, nil)
	parent := func(text string) map[int]*animelists.Node {
		return map[int]*animelists.Node{1983: {
			AniDBID: 1983, Attrs: map[string]string{"tvdbid": "75941", "defaulttvdbseason": "1"},
			Rows: []animelists.Row{{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: text}},
		}}
	}
	links := []overlay.Link{linkFile(376, overlay.Part{AniDBID: 1983, Specials: []int{1}}), linkFile(377, overlay.Part{AniDBID: 1983, Specials: []int{2}})}
	if p, err := proveLinks(links, parent(";1-4;2-5;"), nil, facts, nil); err != nil || len(p.compiled) != 2 {
		t.Errorf("proveLinks(S1 on 0x4, S2 on 0x5) = %+v, %v; want both compiled", p, err)
	}
	const want = "AniList 377 episode 1 lands on TVDB 75941 0x4, which AniList 376 already takes"
	if _, err := proveLinks(links, parent(";1-4;2-4;"), nil, facts, nil); err == nil || err.Error() != want {
		t.Errorf("proveLinks(S1 and S2 both on 0x4) = %v, want %q", err, want)
	}
	twice := []overlay.Link{links[0], linkFile(377, overlay.Part{AniDBID: 1983, Specials: []int{1}})}
	if _, err := proveLinks(twice, parent(";1-4;2-5;"), nil, facts, nil); err == nil || !strings.Contains(err.Error(), "AniList 377: AniDB 1983 S1 is linked by AniList 376 too") {
		t.Errorf("proveLinks(two links on S1) = %v, want a refusal naming AniList 376", err)
	}
}
