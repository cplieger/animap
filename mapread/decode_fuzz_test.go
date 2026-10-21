package mapread

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"testing"
	"testing/iotest"
)

// FuzzDecode_canonical holds Decode to its contract on any input: alike
// read whole or one byte at a time; no document with a refusal; every
// Statement in a closed state and a reference naming no episode false;
// AtTVDB, AtTVDBEpisodeID and AtTMDBMovie finding each run's first episode
// where it lands; TVDBSeasons counting every entry episode on every series
// as TVDBReading.On reads it; and a document Encode can express
// re-encoding to a canonical form Decode accepts and reads back equal.
func FuzzDecode_canonical(f *testing.F) {
	b, err := Encode(sample())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	for _, path := range goldens {
		body, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(body)
		f.Add(bytes.Replace(body, []byte(`"notice":"a`), []byte("\"notice\":\"\xffa"), 1))
		f.Add(bytes.Replace(body, []byte(`"notice":"a`), []byte("\"notice\":\"é翼🎌\uFFFDa"), 1))
	}
	earlier, err := os.ReadFile("testdata/earlier-shape.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(earlier)
	mini, err := os.ReadFile("testdata/mini.json")
	if err != nil {
		f.Fatal(err)
	}
	// The mini golden's AniDB 18 entry names no other service.
	f.Add(bytes.Replace(mini, []byte(`"ids":{"anidb":[18],"mal":[1018]}`), []byte(`"ids":{"anpdb":[18]}`), 1))
	f.Add(bytes.Replace(b, []byte(`{"anidb_id":4,"type":"OVA"}`), []byte(`{"anidb_id":4,"future":1}`), 1))
	f.Add(bytes.Replace(b, []byte(`"anidb_episode_ids":[101,102]`), []byte(`"anidb_episode_ids":[101]`), 1))
	f.Add(bytes.Replace(b, []byte(`,"episodes_listed":true`), nil, 1))
	f.Add(bytes.Replace(b, []byte(`"anidb_episode_ids":[105]`), []byte(`"anidb_episode_ids":[105],"tvdb":[]`), 1))
	f.Add(bytes.Replace(b, []byte(`"tmdb_movies":[30,31]`), []byte(`"tmdb_movies":[]`), 1))
	f.Add(bytes.Replace(b, []byte(`{"series":11,`), []byte(`{"series":10,`), 1))
	f.Add(bytes.Replace(b, []byte(`"episode_ids":[7001,7002]`), []byte(`"episode_ids":[7001]`), 1))
	f.Add(bytes.Replace(b, []byte(`"tvdb":[{"season":0,"episode":5}]`), []byte(`"tvdb":[{"season":0,"episode":5,"episode_ids":[7006]}]`), 1))
	f.Add([]byte(`{"version":3,"records":[]}`))
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
		readEntries(t, x, doc)
		readAnime(t, x, doc)
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

// readEntries runs every entry lookup over doc and checks each episode an
// entry names.
func readEntries(t *testing.T, x *Index, doc *Document) {
	t.Helper()
	for _, e := range doc.Entries {
		x.Routes(e)
		if got, want := x.TVDBSeasons(e), seasonsOn(x, e); !reflect.DeepEqual(got, want) {
			t.Fatalf("TVDBSeasons(%d) = %+v, want %+v as On reads every series", e.AniListID, got, want)
		}
		x.Movies(e)
		for _, ep := range x.Episodes(e) {
			for _, ref := range ep.Refs {
				checkEpisode(t, x, ref.Ref)
				if got, _ := x.Episode(ref.Ref); got.ID != ref.ID {
					t.Fatalf("Episodes names %+v, Episode gives id %d", ref, got.ID)
				}
			}
		}
	}
}

// readAnime checks every anime's episodes and finds each run's first
// episode through the lookups that invert its statements.
func readAnime(t *testing.T, x *Index, doc *Document) {
	t.Helper()
	for i := range doc.Anime {
		a := &doc.Anime[i]
		for _, s := range a.TVDBSeries() {
			x.Series(s)
		}
		for _, ref := range []Ref{{AniDB: a.AniDBID, Kind: Regular}, {AniDB: a.AniDBID, Kind: Special, Number: -1}, {AniDB: a.AniDBID, Kind: "bogus", Number: 1}} {
			checkEpisode(t, x, ref)
		}
		for j := range a.Episodes {
			r := &a.Episodes[j]
			for _, n := range []int{r.Start, r.End, r.End + 1} {
				checkEpisode(t, x, Ref{AniDB: a.AniDBID, Kind: r.Kind, Number: n})
			}
			first := func(l Landing) bool { return l.AniDB == a.AniDBID && l.Kind == r.Kind && l.Number == r.Start }
			if r.TVDB != nil {
				for _, tg := range *r.TVDB {
					if at := (TVDBEpisode{tg.on(a.TVDBID), tg.Season, tg.Episode}); !slices.ContainsFunc(x.AtTVDB(at), first) {
						t.Fatalf("AtTVDB(%+v) misses AniDB %d %s %d", at, a.AniDBID, r.Kind, r.Start)
					}
					if tg.EpisodeIDs != nil && !slices.ContainsFunc(x.AtTVDBEpisodeID(tg.EpisodeIDs[0]), first) {
						t.Fatalf("AtTVDBEpisodeID(%d) misses AniDB %d %s %d", tg.EpisodeIDs[0], a.AniDBID, r.Kind, r.Start)
					}
				}
			}
			for _, m := range r.TMDBMovies {
				if !slices.ContainsFunc(x.AtTMDBMovie(m), first) {
					t.Fatalf("AtTMDBMovie(%d) misses AniDB %d %s %d", m, a.AniDBID, r.Kind, r.Start)
				}
			}
		}
	}
}

// seasonsOn is e's TVDBSeasons read one entry episode and one series at a
// time through TVDBReading.On, on each series a reading names in first
// order.
func seasonsOn(x *Index, e Entry) []SeriesSeasons {
	eps := x.Episodes(e)
	var readings []TVDBReading
	out := []SeriesSeasons{}
	for _, ep := range eps {
		r := x.OnTVDB(ep)
		readings = append(readings, r)
		for _, sr := range r.Series {
			if !slices.ContainsFunc(out, func(ss SeriesSeasons) bool { return ss.Series == sr.Series }) {
				out = append(out, SeriesSeasons{Series: sr.Series})
			}
		}
	}
	for i := range out {
		for j, r := range readings {
			sr := r.On(out[i].Series)
			switch {
			case len(sr.Seasons) > 0:
				out[i].add(sr, eps[j].Number)
			case sr.Unstated:
				out[i].Unstated++
			default:
				out[i].Nowhere++
			}
		}
	}
	return out
}

// expressible reports whether Encode can write doc as a valid document:
// no anime row or ids object is filled only by members Decode skipped.
func expressible(doc *Document) bool {
	for i := range doc.Anime {
		a := &doc.Anime[i]
		if a.Type == "" && a.TVDBID == 0 && a.MALIDs == nil && a.IMDbIDs == nil && a.Last == (Last{}) && !a.EpisodesListed && a.Episodes == nil {
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
