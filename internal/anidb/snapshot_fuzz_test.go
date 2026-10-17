package anidb

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cplieger/jsoncap/v2"
)

// distinctFromOne reports whether every number is 1 or more and none is
// listed twice.
func distinctFromOne(numbers []int) bool {
	seen := map[int]bool{}
	for _, n := range numbers {
		if n < 1 || seen[n] {
			return false
		}
		seen[n] = true
	}
	return true
}

func FuzzParse_acceptsOnlyAWellFormedList(f *testing.F) {
	for _, seed := range []struct {
		body string
		aid  int
	}{
		{`{"anime_id":7,"end_date":"2005","episodes":{"REGULAR":[{"number":2,"episode_id":1001},{"number":1,"episode_id":1002,"air_date":"2005-04-01"}],"SPECIAL":[{"number":1,"episode_id":1003}]}}`, 7},
		{`{"anime_id":7,"episodes":{"SPECIAL":[{"number":2,"episode_id":1004},{"number":1,"episode_id":1005}]}}`, 7},
		{`{"anime_id":7,"episodes":{"SPECIAL":[{"number":1,"episode_id":1006},{"number":1,"episode_id":1007}]}}`, 7},
		{`{"anime_id":7,"episodes":{}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1,"episode_id":1008,"titles":[{"language":"ENGLISH","title":"A"}]}],"SPECIAL":[{"number":3,"episode_id":1009,"titles":[]}]}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1,"episode_id":1010},{"number":3,"episode_id":1011}]}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":9,"episode_id":1022},{"number":2,"episode_id":1023},{"number":2,"episode_id":1024}]}}`, 7},
		{`{"anime_id":7,"episodes":{"SPECIAL":[{"number":101,"episode_id":1012}],"CREDITS":[{"number":1,"episode_id":1013},{"number":1,"episode_id":1014},{"number":150,"episode_id":1015}],"OTHER":[{"number":2,"episode_id":1016,"air_date":"x"},{"number":1,"episode_id":1017}]}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":0,"episode_id":1018}]}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1,"episode_id":1019,"air_date":"2005-13-01"}]}}`, 7},
		{`{"anime_id":8,"episodes":{"REGULAR":[{"number":1,"episode_id":1020}]}}`, 7},
		{`{"anime_id":7}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1,"episode_id":1021}]}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1}],"SPECIAL":[{"number":1,"episode_id":-3}]}}`, 7},
		{`{"anime_id":7,"episodes":{"CREDITS":[{"number":0},{"number":1,"episode_id":9}],"TRAILER":[{"number":1}]}}`, 7},
		{`{"anime_id":7,"episodes":{"TRAILER":[{"number":1,"episode_id":303}]}}`, 7},
		{`{"anime_id":7,"episodes":{"CREDITS":[{"number":1,"episode_id":101},{"number":1,"episode_id":202}]}}`, 7},
		{`{"anime_id":7,"episodes":{"PARODY":[{"number":120,"episode_id":35847}],"PROMOTION":[{"number":2,"episode_id":11}]}}`, 7},
		{`{"anime_id":7,"episodes":{"CREDITS":[{"number":1,"episode_id":101,"episode_id":202}]}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1,"episode_id":1}],"regular":[{"number":2,"episode_id":2}]}}`, 7},
		{`404: Not Found`, 7},
		{"{\"anime_id\":10,\"epis\xffdes\":{\"REGULAR\":[{\"number\":1,\"episode_id\":101}]}}", 10},
		{"{\"anime_id\":7,\"episodes\":{\"REGULAR\":[{\"number\":1,\"episode_id\":1,\"titles\":[{\"title\":\"a\xed\xa0\x80\"}]}]}}", 7},
	} {
		f.Add([]byte(seed.body), seed.aid)
	}
	f.Fuzz(func(t *testing.T, body []byte, aid int) {
		a, listed, err := parse(aid, body)
		var file animeFile
		decodeErr := json.Unmarshal(body, &file)
		// The strict read is strictjson's, tested there; this target owns
		// what parse does with a body it passes.
		repeated := jsoncap.Preflight(bytes.NewReader(body)) != nil
		numbers := map[string][]int{}
		ids := map[string][]int{}
		dated, identified, known := true, true, true
		for kind, eps := range file.Episodes {
			if _, ok := mirrorKinds[kind]; !ok {
				known = false
			}
			for _, e := range eps {
				if e.EpisodeID <= 0 {
					identified = false
				}
				numbers[kind] = append(numbers[kind], e.Number)
				ids[kind] = append(ids[kind], e.EpisodeID)
				if (kind == "REGULAR" || kind == "SPECIAL") && e.AirDate != nil && *e.AirDate != "" {
					if _, perr := time.Parse(time.DateOnly, *e.AirDate); perr != nil {
						dated = false
					}
				}
			}
		}
		distinct := true
		for _, ns := range numbers {
			distinct = distinct && distinctFromOne(ns)
		}
		valid := decodeErr == nil && !repeated && utf8.Valid(body) && file.AnimeID == aid &&
			(file.Episodes == nil || (known && distinct && dated && identified))
		if !valid {
			if err == nil || !errors.Is(err, errInvalid) {
				t.Fatalf("parse(%d, %q) = %+v, %v; want errInvalid", aid, body, a, err)
			}
			return
		}
		if err != nil || listed != (file.Episodes != nil) {
			t.Fatalf("parse(%d, %q) = %+v, listed %t, %v; want the list kept", aid, body, a, listed, err)
		}
		got := map[string][]int{"REGULAR": {}, "SPECIAL": {}}
		for _, e := range a.Regular {
			got["REGULAR"] = append(got["REGULAR"], e.ID)
		}
		for _, e := range a.Specials {
			got["SPECIAL"] = append(got["SPECIAL"], e.ID)
		}
		for _, x := range a.Extras {
			mirror := ""
			for k, v := range mirrorKinds {
				if v == x.Kind {
					mirror = k
				}
			}
			got[mirror] = append(got[mirror], x.ID)
		}
		for kind := range mirrorKinds {
			want := slices.Sorted(slices.Values(ids[kind]))
			if have := slices.Sorted(slices.Values(got[kind])); !slices.Equal(have, want) {
				t.Fatalf("parse(%d, %q) %s ids = %v, want every one the file lists, %v", aid, body, kind, have, want)
			}
		}
		if listed {
			if cerr := checkListed(aid, &a); aid > 0 && cerr != nil {
				t.Fatalf("parse(%d, %q) = %+v, which the snapshot loader refuses: %v", aid, body, a, cerr)
			}
		}
		for _, eps := range [][]Episode{a.Regular, a.Specials} {
			for i := 1; i < len(eps); i++ {
				if eps[i].Number <= eps[i-1].Number {
					t.Fatalf("parse(%d, %q) episodes %+v are not sorted by number", aid, body, eps)
				}
			}
		}
	})
}

// mirrorKinds are the mirror's six episode keys and the kind each gives.
var mirrorKinds = map[string]string{"REGULAR": Regular, "SPECIAL": Special, "CREDITS": Credit, "PROMOTION": Promotion, "PARODY": Parody, "OTHER": Other}
