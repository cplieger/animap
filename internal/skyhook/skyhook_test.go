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
	if s.TVDBID != 5000 || len(s.Episodes) != 4 {
		t.Fatalf("show = %+v", s)
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
