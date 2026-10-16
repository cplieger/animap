package skyhook

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type fileGetter string

func (f fileGetter) Get(_ context.Context, _ string, _ int64) ([]byte, error) {
	return os.ReadFile(string(f))
}

func TestFetchAndLayout(t *testing.T) {
	s, err := Fetch(t.Context(), fileGetter("../../testdata/skyhook-5000.json"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if s.Slug != "series-with-season-1" || len(s.Episodes) != 4 {
		t.Fatalf("Fetch(5000) = %+v, want slug series-with-season-1 and 4 episodes", s)
	}
	got := Layout(s, []int{1, 0})
	want := []Episode{{Season: 0, Number: 1, AirDate: "2020-06-01"}, {Season: 1, Number: 1, Absolute: 1, AirDate: "2020-01-01"}, {Season: 1, Number: 2, Absolute: 2, AirDate: "2020-01-08"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Layout(0,1) = %+v, want %+v", got, want)
	}
	if all := Layout(s, nil); len(all) != 3 || all[0].Season != 1 {
		t.Errorf("Layout(nil) = %+v, want every season from 1", all)
	}
}

func TestEpisodeJSONRoundTrip(t *testing.T) {
	in := []Episode{{Season: 2, Number: 13, Absolute: 39, AirDate: "2007-09-26"}, {Season: 0, Number: 1}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `[[2,13,39,"2007-09-26"],[0,1,0,""]]` {
		t.Errorf("Marshal = %s", b)
	}
	var out []Episode
	if err := json.Unmarshal(b, &out); err != nil || !reflect.DeepEqual(out, in) {
		t.Errorf("round trip = %+v, %v", out, err)
	}
	var bad Episode
	if err := json.Unmarshal([]byte(`[1,2,3]`), &bad); err == nil {
		t.Error("a three-element episode decoded")
	}
}

func TestHashIsOrderFreeAndSensitive(t *testing.T) {
	a := []Episode{{Season: 1, Number: 1, AirDate: "2020-01-01"}, {Season: 1, Number: 2, AirDate: "2020-01-08"}}
	b := []Episode{a[1], a[0]}
	if Hash(a) != Hash(b) {
		t.Error("Hash depends on input order")
	}
	for _, mut := range []Episode{
		{Season: 1, Number: 1, AirDate: "2020-01-02"},
		{Season: 1, Number: 1, Absolute: 5, AirDate: "2020-01-01"},
		{Season: 2, Number: 1, AirDate: "2020-01-01"},
	} {
		if Hash([]Episode{mut, a[1]}) == Hash(a) {
			t.Errorf("Hash ignores a change to %+v", mut)
		}
	}
}

func TestAtAbsolute(t *testing.T) {
	eps := []Episode{
		{Season: 0, Number: 1, Absolute: 21},
		{Season: 1, Number: 20, Absolute: 20},
		{Season: 2, Number: 1, Absolute: 21},
		{Season: 2, Number: 2, Absolute: 22},
		{Season: 3, Number: 1, Absolute: 22},
	}
	for _, tc := range []struct {
		name string
		n    int
		at   [2]int
		ok   bool
	}{
		{"a regular episode, beside a special with the same number", 21, [2]int{2, 1}, true},
		{"the end of season 1", 20, [2]int{1, 20}, true},
		{"two episodes with one number", 22, [2]int{}, false},
		{"no episode", 23, [2]int{}, false},
		{"zero, which TVDB uses for none", 0, [2]int{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if at, ok := AtAbsolute(eps, tc.n); at != tc.at || ok != tc.ok {
				t.Errorf("AtAbsolute(%d) = %v, %t; want %v, %t", tc.n, at, ok, tc.at, tc.ok)
			}
		})
	}
	if _, ok := AtAbsolute([]Episode{{Season: 1, Number: 1}}, 0); ok {
		t.Error("AtAbsolute(0) matched an episode TVDB numbers 0, which means none")
	}
}
