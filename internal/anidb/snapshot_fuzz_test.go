package anidb

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func isOneToN(numbers []int) bool {
	seen := make([]bool, len(numbers)+1)
	for _, n := range numbers {
		if n < 1 || n > len(numbers) || seen[n] {
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
		{`{"anime_id":7,"end_date":"2005","episodes":{"REGULAR":[{"number":2},{"number":1,"air_date":"2005-04-01"}],"SPECIAL":[{"number":1}]}}`, 7},
		{`{"anime_id":7,"episodes":{"SPECIAL":[{"number":2},{"number":1}]}}`, 7},
		{`{"anime_id":7,"episodes":{"SPECIAL":[{"number":1},{"number":1}]}}`, 7},
		{`{"anime_id":7,"episodes":{}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1},{"number":3}]}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":0}]}}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1,"air_date":"2005-13-01"}]}}`, 7},
		{`{"anime_id":8,"episodes":{"REGULAR":[{"number":1}]}}`, 7},
		{`{"anime_id":7}`, 7},
		{`{"anime_id":7,"episodes":{"REGULAR":[{"number":1}]}`, 7},
		{`404: Not Found`, 7},
	} {
		f.Add([]byte(seed.body), seed.aid)
	}
	f.Fuzz(func(t *testing.T, body []byte, aid int) {
		a, listed, err := parse(aid, body)
		var file animeFile
		decodeErr := json.Unmarshal(body, &file)
		var regular, specials []int
		dated := true
		for kind, eps := range file.Episodes {
			for _, e := range eps {
				if kind == "REGULAR" {
					regular = append(regular, e.Number)
				}
				if kind == "SPECIAL" {
					specials = append(specials, e.Number)
				}
				if (kind == "REGULAR" || kind == "SPECIAL") && e.AirDate != nil && *e.AirDate != "" {
					if _, perr := time.Parse(time.DateOnly, *e.AirDate); perr != nil {
						dated = false
					}
				}
			}
		}
		distinct := map[int]bool{}
		for _, n := range specials {
			if n < 1 || distinct[n] {
				dated = false
			}
			distinct[n] = true
		}
		valid := decodeErr == nil && file.AnimeID == aid && (file.Episodes == nil || (isOneToN(regular) && dated))
		if !valid {
			if err == nil || !errors.Is(err, errInvalid) {
				t.Fatalf("parse(%d, %q) = %+v, %v; want errInvalid", aid, body, a, err)
			}
			return
		}
		if err != nil || listed != (file.Episodes != nil) || len(a.Regular) != len(regular) || len(a.Specials) != len(specials) {
			t.Fatalf("parse(%d, %q) = %+v, listed %t, %v; want %d regular and %d specials", aid, body, a, listed, err, len(regular), len(specials))
		}
		for i := 1; i < len(a.Specials); i++ {
			if a.Specials[i].Number <= a.Specials[i-1].Number {
				t.Fatalf("parse(%d, %q) specials %+v are not sorted by number", aid, body, a.Specials)
			}
		}
	})
}
