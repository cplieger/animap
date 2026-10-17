package mapread

import (
	"bytes"
	"os"
	"reflect"
	"testing"
	"testing/iotest"
)

// FuzzDecode_canonical holds Decode to its contract on any input: it
// accepts or refuses alike read whole or one byte at a time; a refusal
// returns no document; every Statement an accepted document gives is in
// one of its closed states, and a reference naming no episode reads false;
// an accepted document that Encode can express
// re-encodes to a canonical form Decode accepts again and reads back
// equal, and Routes, TVDBSeasons, Episodes, Episode and Series over it
// answer without panicking.
func FuzzDecode_canonical(f *testing.F) {
	b, err := Encode(sample())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	for _, path := range []string{"testdata/mini.json", "testdata/place.json"} {
		body, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(body)
		// The mini golden's AniDB 18 entry names no other service.
		f.Add(bytes.Replace(body, []byte(`"ids":{"anidb":[18]}`), []byte(`"ids":{"anpdb":[18]}`), 1))
		f.Add(bytes.Replace(body, []byte(`"notice":"a`), []byte("\"notice\":\"\xffa"), 1))
		f.Add(bytes.Replace(body, []byte(`"notice":"a`), []byte("\"notice\":\"é翼🎌\uFFFDa"), 1))
	}
	f.Add(bytes.Replace(b, []byte(`{"anidb_id":4,"type":"OVA"}`), []byte(`{"anidb_id":4,"future":1}`), 1))
	f.Add(bytes.Replace(b, []byte(`"anidb_episode_ids":[101,102]`), []byte(`"anidb_episode_ids":[101]`), 1))
	f.Add(bytes.Replace(b, []byte(`,"episodes_listed":true`), nil, 1))
	f.Add(bytes.Replace(b, []byte(`"anidb_episode_ids":[105]`), []byte(`"anidb_episode_ids":[105],"tvdb":[]`), 1))
	f.Add([]byte(`{"version":1,"records":[]}`))
	f.Add([]byte(`{"version":2,"generated_at":"","sources":{},"attribution":{},"anime":[],"entries":[]}`))
	f.Add([]byte(`{"version":2}`))
	f.Add([]byte(`{"version":2,"anime":[{"anidb_id":1,"ANIDB_ID":2}]}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, data []byte) {
		lim := Limits{MaxDocumentBytes: 1 << 20, MaxRows: 1 << 10, MaxRowBytes: 1 << 14, MaxEpisodes: 1 << 12}
		doc, err := Decode(bytes.NewReader(data), lim)
		if _, oneByte := Decode(iotest.OneByteReader(bytes.NewReader(data)), lim); (err == nil) != (oneByte == nil) {
			t.Fatalf("Decode = %v whole and %v one byte at a time", err, oneByte)
		}
		if err != nil {
			if doc != nil {
				t.Fatalf("Decode error %v with a document", err)
			}
			return
		}
		x := NewIndex(doc)
		for _, e := range doc.Entries {
			x.Routes(e)
			x.TVDBSeasons(e)
			for _, ep := range x.Episodes(e) {
				for _, ref := range ep.Refs {
					checkEpisode(t, x, ref.Ref)
					if got, _ := x.Episode(ref.Ref); got.ID != ref.ID {
						t.Fatalf("Episodes names %+v, Episode gives id %d", ref, got.ID)
					}
				}
			}
		}
		for _, a := range doc.Anime {
			x.Series(a.TVDBID)
			for _, ref := range []Ref{{AniDB: a.AniDBID, Kind: Regular}, {AniDB: a.AniDBID, Kind: Special, Number: -1}, {AniDB: a.AniDBID, Kind: "bogus", Number: 1}} {
				checkEpisode(t, x, ref)
			}
			for _, r := range a.Episodes {
				for _, n := range []int{r.Start, r.End, r.End + 1} {
					checkEpisode(t, x, Ref{AniDB: a.AniDBID, Kind: r.Kind, Number: n})
				}
			}
		}
		if !expressible(doc) {
			return
		}
		canon, err := Encode(doc)
		if err != nil {
			t.Fatalf("Encode(decoded) = %v", err)
		}
		again, err := Decode(bytes.NewReader(canon), Limits{MaxEpisodes: 1 << 12})
		if err != nil {
			t.Fatalf("Decode(Encode(decoded)) = %v\n%s", err, canon)
		}
		if !reflect.DeepEqual(again, doc) {
			t.Fatalf("Decode(Encode(doc)) != doc")
		}
	})
}

// expressible reports whether Encode can write doc as a valid document:
// no anime row or ids object is filled only by members Decode skipped.
func expressible(doc *Document) bool {
	for i := range doc.Anime {
		a := &doc.Anime[i]
		if a.Type == "" && a.TVDBID == 0 && a.TMDBTVID == 0 && a.TMDBMovieIDs == nil && a.IMDbIDs == nil && a.Last == (Last{}) && !a.EpisodesListed && a.Episodes == nil {
			return false
		}
	}
	for i := range doc.Entries {
		if ids := doc.Entries[i].IDs; ids != nil && reflect.DeepEqual(*ids, IDs{}) {
			return false
		}
	}
	return true
}
