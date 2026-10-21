package mapread

import (
	"bytes"
	"os"
	"testing"
)

func TestContentHash(t *testing.T) {
	hash := func(t *testing.T, doc *Document) string {
		t.Helper()
		h, err := ContentHash(doc)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash(t, sample())
	for _, tc := range []struct {
		edit func(*Document)
		name string
		same bool
	}{
		{name: "generated_at", same: true, edit: func(d *Document) { d.GeneratedAt = "2027-01-01T00:00:00Z" }},
		{name: "the mapping revision", same: true, edit: func(d *Document) { d.Sources.Mapping.Revision = "another" }},
		{name: "the titles dump date", same: true, edit: func(d *Document) { d.Sources.AniDB.TitlesDump = "2027-01-01" }},
		{name: "one target's series", edit: func(d *Document) { (*d.Anime[0].Episodes[6].TVDB)[0].Series = 12 }},
		{name: "one tmdb_movies id", edit: func(d *Document) { d.Anime[2].Episodes[0].TMDBMovies[1] = 32 }},
		{name: "one mal_ids id", edit: func(d *Document) { d.Anime[2].MALIDs[0] = 4 }},
		{name: "the attribution", edit: func(d *Document) { d.Attribution.Notice = "m" }},
		{name: "an entry's ids", edit: func(d *Document) { d.Entries[0].IDs.MAL[0] = 2 }},
		{name: "one target's TVDB episode id", edit: func(d *Document) { (*d.Anime[0].Episodes[0].TVDB)[0].EpisodeIDs[1] = 7003 }},
		{name: "the version", edit: func(d *Document) { d.Version = 4 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := sample()
			tc.edit(doc)
			if got := hash(t, doc); (got == base) != tc.same {
				t.Errorf("ContentHash after changing %s = %s, base %s; want same=%t", tc.name, got, base, tc.same)
			}
		})
	}
	b, err := os.ReadFile("testdata/mini.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Decode(bytes.NewReader(b), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hash(t, doc), "798de3d1480a9be324f5f87aba072578a383b4c7866135ba809bcd278e7f2170"; got != want {
		t.Errorf("ContentHash(mini.json) = %s, want %s", got, want)
	}
}
