package animelists

import "testing"

func TestSpecialTVDB(t *testing.T) {
	row := func(attrs map[string]string, text string) Row { return Row{Attrs: attrs, Text: text} }
	n := &Node{Attrs: map[string]string{"tvdbid": "10"}, Rows: []Row{
		row(map[string]string{"anidbseason": "0", "tvdbseason": "0"}, ";1-8;2-0;"),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "1"}, ";3-13;"),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "0", "start": "4", "end": "6", "offset": "-1"}, ""),
		row(map[string]string{"anidbseason": "0", "tvdbseason": "0"}, ";9-1+2;"),
	}}
	for _, tc := range []struct {
		name    string
		k, s, e int
		ok      bool
	}{
		{"a pair row", 1, 0, 8, true},
		{"a row to a regular season", 3, 1, 13, true},
		{"a range with an offset", 5, 0, 4, true},
		{"no row, the default", 7, 0, 7, true},
		{"a row to no episode", 2, 0, 0, false},
		{"a row to two episodes", 9, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, ok := n.SpecialTVDB(tc.k)
			if ok != tc.ok || (ok && (s != tc.s || e != tc.e)) {
				t.Errorf("SpecialTVDB(%d) = %d, %d, %t; want %d, %d, %t", tc.k, s, e, ok, tc.s, tc.e, tc.ok)
			}
		})
	}
}
