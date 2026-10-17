package mapread

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

func TestDecodeRoundTrip(t *testing.T) {
	body := encodeSample(t)
	doc, err := Decode(strings.NewReader(body), DefaultLimits())
	if err != nil {
		t.Fatalf("Decode(sample) = %v", err)
	}
	if !reflect.DeepEqual(doc, sample()) {
		t.Errorf("Decode(Encode(sample)) = %+v, want %+v", doc, sample())
	}
	for _, path := range []string{"testdata/mini.json", "testdata/place.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Decode(bytes.NewReader(b), DefaultLimits())
		if err != nil {
			t.Errorf("Decode(%s) = %v", path, err)
			continue
		}
		if again, err := Encode(doc); err != nil || !bytes.Equal(again, b) {
			t.Errorf("%s does not re-encode as written (%v)", path, err)
		}
	}
}

func TestEncodeIsCanonical(t *testing.T) {
	b, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte(`{"version":2,"generated_at":`)) || bytes.HasSuffix(b, []byte("\n")) {
		t.Errorf("encoding = %.40s…, want version first and no trailing newline", b)
	}
	doc := sample()
	doc.Sources.AnimeLists.File = "a<b>&c"
	if b, _ := Encode(doc); !bytes.Contains(b, []byte(`"a<b>&c"`)) {
		t.Errorf("Encode escaped HTML characters: %s", b)
	}
}

// Each refusal is one rule of the format; the sentinel says which class.
func TestDecodeRefuses(t *testing.T) {
	good := encodeSample(t)
	for _, tc := range []struct {
		name, old, new string
		want           error
	}{
		{"version 1", `"version":2`, `"version":1`, ErrVersion},
		{"version 3", `"version":2`, `"version":3`, ErrVersion},
		{"version not first", `{"version":2,"generated_at":"2026-10-05T00:00:00Z",`, `{"generated_at":"2026-10-05T00:00:00Z","version":2,`, ErrNotDocument},
		{"a member before version", `{"version":2,`, `{"x":2,"version":2,`, ErrNotDocument},
		{"an integer member in version's place", `{"version":2,`, `{"x":2,`, ErrNotDocument},
		{"version a string", `"version":2`, `"version":"2"`, ErrNotDocument},
		{"version null", `"version":2`, `"version":null`, ErrNotDocument},
		{"version the smallest int", `"version":2`, `"version":` + strconv.Itoa(math.MinInt), ErrVersion},
		{"version past the int range", `"version":2`, `"version":99999999999999999999`, ErrNotDocument},
		{"version written with a fraction", `"version":2`, `"version":2.0`, ErrNotDocument},
		{"a repeated version", `"kind":"regular"}]}]}`, `"kind":"regular"}]}],"version":3}`, ErrNotDocument},
		{"a case-folded repeated version", `"kind":"regular"}]}]}`, `"kind":"regular"}]}],"Version":3}`, ErrNotDocument},
		{"a case-folded repeated table", `"kind":"regular"}]}]}`, `"kind":"regular"}]}],"ANIME":[]}`, ErrNotDocument},
		{"a case-folded repeated generated_at", `"kind":"regular"}]}]}`, `"kind":"regular"}]}],"Generated_At":"x"}`, ErrNotDocument},
		{"an unknown member repeated case-folded", `{"version":2,`, `{"version":2,"future":1,"FUTURE":2,`, ErrNotDocument},
		{"generated_at not a time", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"not-a-time"`, ErrInvalid},
		{"generated_at not in UTC", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"2026-10-05T02:00:00+02:00"`, ErrInvalid},
		{"generated_at a date only", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"2026-10-05"`, ErrInvalid},
		{"generated_at at an unknown offset", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"2026-10-05T00:00:00-00:00"`, ErrInvalid},
		{"generated_at a leap second before 23:59", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"2026-12-31T22:59:60Z"`, ErrInvalid},
		{"generated_at on a day the month lacks", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"2026-02-29T00:00:00Z"`, ErrInvalid},
		{"generated_at with a signed hour", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"2026-10-05T+1:00:00Z"`, ErrInvalid},
		{"generated_at hour 24", `"generated_at":"2026-10-05T00:00:00Z"`, `"generated_at":"2026-10-05T24:00:00Z"`, ErrInvalid},
		{"a table missing", `,"entries":[`, `,"rows":[`, ErrNotDocument},
		{"tables out of order", `"anime":[`, `"animex":[`, ErrNotDocument},
		{"a repeated member", `"attribution":{`, `"anime":[],"attribution":{`, ErrNotDocument},
		{"a table null", `"entries":[{"anilist_id":1`, `"entries":null,"x":[{"anilist_id":1`, ErrNotDocument},
		{"trailing data", `"kind":"regular"}]}]}`, `"kind":"regular"}]}]} {}`, ErrNotDocument},
		{"anime out of order", `{"anidb_id":2,`, `{"anidb_id":1,`, ErrInvalid},
		{"a run past last", `"last":{"regular":2}`, `"last":{"regular":1}`, ErrInvalid},
		{"a run with no last", `"last":{"regular":2},`, ``, ErrInvalid},
		{"runs out of order", `{"kind":"special","start":1,"end":1,`, `{"kind":"special","start":3,"end":3,`, ErrInvalid},
		{"kinds out of order", `{"kind":"other","start":1`, `{"kind":"regular","start":1`, ErrInvalid},
		{"overlapping runs", `{"kind":"regular","start":3,"end":3`, `{"kind":"regular","start":2,"end":3`, ErrInvalid},
		{"start after end", `"start":3,"end":3`, `"start":3,"end":2`, ErrInvalid},
		{"an unknown kind", `"kind":"other"`, `"kind":"trailer"`, ErrInvalid},
		{"an unlisted run nowhere on every site", `{"kind":"other","start":1,"end":1,"tvdb":[{"season":0,"episode":5}]}`, `{"kind":"other","start":1,"end":1,"tvdb":[],"tmdb":[]}`, ErrInvalid},
		{"a run nowhere on its only site", `"tvdb":[{"season":2,"episode":1,"default":true}]`, `"tvdb":[]`, ErrInvalid},
		{"tmdb with no tmdb_tv_id", `"tvdb":[{"season":2,"episode":1,"default":true}]`, `"tvdb":[{"season":2,"episode":1}],"tmdb":[]`, ErrInvalid},
		{"an unlisted run on a row with no site id", `{"kind":"regular","start":1,"end":1,"anidb_episode_ids":[900]}`, `{"kind":"regular","start":1,"end":1}`, ErrInvalid},
		{"fewer ids than episodes", `"anidb_episode_ids":[101,102]`, `"anidb_episode_ids":[101]`, ErrInvalid},
		{"more ids than episodes", `"anidb_episode_ids":[101,102]`, `"anidb_episode_ids":[101,102,103]`, ErrInvalid},
		{"an id of 0", `"anidb_episode_ids":[101,102]`, `"anidb_episode_ids":[101,0]`, ErrInvalid},
		{"an empty id list", `"anidb_episode_ids":[105]`, `"anidb_episode_ids":[]`, ErrInvalid},
		{"ids as strings", `"anidb_episode_ids":[105]`, `"anidb_episode_ids":["105"]`, ErrInvalid},
		{"ids on a row without AniDB's list", `,"episodes_listed":true`, ``, ErrInvalid},
		{"episodes_listed false", `"episodes_listed":true`, `"episodes_listed":false`, ErrInvalid},
		{"a run past last that states a site", `"anidb_episode_ids":[105]}`, `"anidb_episode_ids":[105],"tvdb":[]}`, ErrInvalid},
		{"a listed special past last", `{"kind":"regular","start":1,"end":1,"anidb_episode_ids":[900]}`, `{"kind":"special","start":1,"end":1,"anidb_episode_ids":[900]}`, ErrInvalid},
		{"targets out of order", `{"season":1,"episode":3},{"season":1,"episode":4}`, `{"season":1,"episode":4},{"season":1,"episode":3}`, ErrInvalid},
		{"a repeated target", `{"season":1,"episode":3},{"season":1,"episode":4}`, `{"season":1,"episode":3},{"season":1,"episode":3,"default":true}`, ErrInvalid},
		{"episode 0", `{"season":1,"episode":3}`, `{"season":1,"episode":0}`, ErrInvalid},
		{"default false", `{"season":1,"episode":1,"default":true}`, `{"season":1,"episode":1,"default":false}`, ErrInvalid},
		{"a row with only its id", `{"anidb_id":4,"type":"OVA"}`, `{"anidb_id":4}`, ErrInvalid},
		{"tvdb_id 0", `"tvdb_id":10,"tmdb_tv_id"`, `"tvdb_id":0,"tmdb_tv_id"`, ErrInvalid},
		{"an empty movie list", `"tmdb_movie_ids":[30,31]`, `"tmdb_movie_ids":[]`, ErrInvalid},
		{"a malformed IMDb id", `"tt0000003"`, `"nm0000003"`, ErrInvalid},
		{"an unknown type", `"type":"OVA"}`, `"type":"TV\n@x"}`, ErrInvalid},
		{"an empty type", `"type":"OVA"}`, `"type":""}`, ErrInvalid},
		{"entries out of order", `{"anilist_id":2,`, `{"anilist_id":1,`, ErrInvalid},
		{"an AniList entry after the others", `{"type":"TV","ids":{"kitsu":[3]}`, `{"anilist_id":9,"type":"TV","ids":{"kitsu":[3]}`, ErrInvalid},
		{"an entry with neither key", `{"type":"OVA","ids":{"animeplanet":["a"],"kitsu":[9]}}`, `{"type":"OVA"}`, ErrInvalid},
		{"ids not ascending", `"anidb":[1,2]`, `"anidb":[2,1]`, ErrInvalid},
		{"empty ids", `"ids":{"kitsu":[3]}`, `"ids":{}`, ErrInvalid},
		{"an empty service key", `"animeplanet":["a"]`, `"animeplanet":[""]`, ErrInvalid},
		{"a service key of 257 characters", `"animeplanet":["a"]`, `"animeplanet":["` + strings.Repeat("翼", 257) + `"]`, ErrInvalid},
		{"a part naming no row", `{"anidb_id":3,"kind":"regular"}`, `{"anidb_id":7,"kind":"regular"}`, ErrInvalid},
		{"a part past last", `"kind":"special","start":1,"end":2}`, `"kind":"special","start":1,"end":4}`, ErrInvalid},
		{"a part with only a start", `"kind":"special","start":1,"end":2}`, `"kind":"special","start":1}`, ErrInvalid},
		{"a combined special", `"kind":"regular","combined":true`, `"kind":"special","combined":true`, ErrInvalid},
		{"a combined range", `"kind":"regular","combined":true`, `"kind":"regular","start":1,"end":1,"combined":true`, ErrInvalid},
		{"empty parts", `,"parts":[{"anidb_id":3,"kind":"regular"}]`, `,"parts":[]`, ErrInvalid},
		{"a null member", `"episodes":4,`, `"episodes":null,`, ErrInvalid},
		{"a wrong JSON type", `"anidb_id":3,`, `"anidb_id":"3",`, ErrInvalid},
		{"a fractional number", `"anidb_id":3,`, `"anidb_id":3.5,`, ErrInvalid},
		{"an integer written with a fraction", `"anidb_id":3,`, `"anidb_id":3.0,`, ErrInvalid},
		{"an integer written with an exponent", `"anidb_id":3,`, `"anidb_id":3e0,`, ErrInvalid},
		{"an id the smallest int", `"anidb_id":3,`, `"anidb_id":` + strconv.Itoa(math.MinInt) + `,`, ErrInvalid},
		{"an id past the int range", `"anidb_id":3,`, `"anidb_id":99999999999999999999,`, ErrInvalid},
		{"a repeated key", `{"anidb_id":4,"type":"OVA"}`, `{"anidb_id":4,"type":"OVA","type":"TV"}`, ErrInvalid},
		{"a case-folded repeated key", `{"anidb_id":4,"type":"OVA"}`, `{"anidb_id":4,"type":"OVA","TYPE":"TV"}`, ErrInvalid},
		{"a key matching only case-insensitively", `{"anidb_id":4,"type":"OVA"}`, `{"ANIDB_ID":4,"type":"OVA"}`, ErrInvalid},
		{"a malformed source digest", `"sha256":"0000`, `"sha256":"zzzz`, ErrInvalid},
		{"a missing source", `"anidb_mirror":{"repository":"r","commit":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},`, ``, ErrInvalid},
		{"another licence", `"license":"ODbL-1.0"`, `"license":"MIT"`, ErrInvalid},
		{"overlay links 0", `"links":1`, `"links":0`, ErrInvalid},
		{"a number past the format's bound", `"last":{"regular":2}`, `"last":{"regular":1048577}`, ErrInvalid},
		{"a TVDB run landing past the format's bound", `"tvdb":[{"season":2,"episode":1,"default":true}]`, `"tvdb":[{"season":2,"episode":1048576,"default":true}]`, ErrInvalid},
		{"a TMDB run landing past the format's bound", `"tmdb":[{"season":1,"episode":1,"default":true}]`, `"tmdb":[{"season":1,"episode":1048576,"default":true}]`, ErrInvalid},
		{
			"a listed run split in two",
			`{"kind":"regular","start":1,"end":2,"anidb_episode_ids":[101,102],"tvdb":[{"season":1,"episode":1}],"tmdb":[{"season":1,"episode":1,"default":true}]}`,
			`{"kind":"regular","start":1,"end":1,"anidb_episode_ids":[101],"tvdb":[{"season":1,"episode":1}],"tmdb":[{"season":1,"episode":1,"default":true}]},` +
				`{"kind":"regular","start":2,"end":2,"anidb_episode_ids":[102],"tvdb":[{"season":1,"episode":2}],"tmdb":[{"season":1,"episode":2,"default":true}]}`,
			ErrInvalid,
		},
		{
			"an unlisted run split in two",
			`{"kind":"regular","start":1,"end":2,"tvdb":[{"season":2,"episode":1,"default":true}]}`,
			`{"kind":"regular","start":1,"end":1,"tvdb":[{"season":2,"episode":1,"default":true}]},{"kind":"regular","start":2,"end":2,"tvdb":[{"season":2,"episode":2,"default":true}]}`,
			ErrInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(good, tc.old) {
				t.Fatalf("Setup: the sample lacks %q", tc.old)
			}
			body := strings.Replace(good, tc.old, tc.new, 1)
			doc, err := Decode(strings.NewReader(body), DefaultLimits())
			if !errors.Is(err, tc.want) || doc != nil {
				t.Errorf("Decode = %v, %v; want %v", doc, err, tc.want)
			}
		})
	}
}

func TestDecodeReportsTheVersion(t *testing.T) {
	for _, v := range []int{1, 3, -1, math.MinInt, math.MaxInt} {
		_, err := Decode(strings.NewReader(`{"version":`+strconv.Itoa(v)+`,"records":[]}`), DefaultLimits())
		if got, ok := errors.AsType[*VersionError](err); !ok || got.Found != v || !errors.Is(err, ErrVersion) {
			t.Errorf("Decode(version %d) = %v, want a *VersionError for version %d matching ErrVersion", v, err, v)
		}
	}
}

// intOf tells null from every integer, the smallest included, and refuses
// any other spelling.
func TestIntOf(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		v       int
		null    bool
		refused string
	}{
		{raw: "null", null: true},
		{raw: "0"},
		{raw: "-0"},
		{raw: "17", v: 17},
		{raw: strconv.Itoa(math.MinInt), v: math.MinInt},
		{raw: strconv.Itoa(math.MaxInt), v: math.MaxInt},
		{raw: "99999999999999999999", refused: "outside this platform's int"},
		{raw: "2.0", refused: "not written as an integer"},
		{raw: "2e0", refused: "not written as an integer"},
		{raw: "2E0", refused: "not written as an integer"},
		{raw: `"2"`, refused: "not an integer"},
		{raw: "true", refused: "not an integer"},
		{raw: "[2]", refused: "not an integer"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			v, null, err := intOf([]byte(tc.raw))
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Errorf("intOf(%s) = %d, %t, %v; want an error saying %q", tc.raw, v, null, err, tc.refused)
				}
				return
			}
			if err != nil || v != tc.v || null != tc.null {
				t.Errorf("intOf(%s) = %d, %t, %v; want %d, %t", tc.raw, v, null, err, tc.v, tc.null)
			}
		})
	}
	_, err := Decode(strings.NewReader(strings.Replace(encodeSample(t), `"anidb_id":3,`, `"anidb_id":`+strconv.Itoa(math.MinInt)+`,`, 1)), DefaultLimits())
	if err == nil || strings.Contains(err.Error(), "null") || !strings.Contains(err.Error(), "below 1") {
		t.Errorf("Decode(anidb_id the smallest int) = %v, want it below 1, not null", err)
	}
}

// An empty, truncated or unreadable body matches no sentinel, so a caller
// can retry it.
func TestDecodeTransientFailures(t *testing.T) {
	good := encodeSample(t)
	midNotice := good[:strings.Index(good, `"notice":"`)+len(`"notice":"`)]
	for name, r := range map[string]io.Reader{
		"empty":              strings.NewReader(""),
		"truncated":          strings.NewReader(good[:len(good)/2]),
		"truncated early":    strings.NewReader(`{"version":2,"generated_at":"2026-10-05T00:00:00Z"`),
		"truncated mid-rune": iotest.OneByteReader(strings.NewReader(midNotice + "\xe7\xbf")),
		"a read error":       io.MultiReader(strings.NewReader(good[:100]), iotest.ErrReader(io.ErrClosedPipe)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(r, DefaultLimits())
			if err == nil {
				t.Fatal("Decode = nil error")
			}
			for _, s := range []error{ErrVersion, ErrNotDocument, ErrTooLarge, ErrInvalid} {
				if errors.Is(err, s) {
					t.Errorf("Decode = %v, matching %v; want a plain error", err, s)
				}
			}
		})
	}
	if _, err := Decode(strings.NewReader("[1]"), DefaultLimits()); !errors.Is(err, ErrNotDocument) {
		t.Errorf("Decode(an array) = %v, want ErrNotDocument", err)
	}
	if _, err := Decode(strings.NewReader("null"), DefaultLimits()); !errors.Is(err, ErrNotDocument) {
		t.Errorf("Decode(null) = %v, want ErrNotDocument", err)
	}
}

// encoding/json reads an invalid byte in a string as U+FFFD, so Decode
// checks the stream itself; a rune split across reads is still UTF-8.
func TestDecodeRefusesInvalidUTF8(t *testing.T) {
	good, err := os.ReadFile("testdata/mini.json")
	if err != nil {
		t.Fatal(err)
	}
	notice := []byte(`"notice":"a`)
	if !bytes.Contains(good, notice) {
		t.Fatalf("Setup: the golden lacks %s", notice)
	}
	swap := func(old, new string) []byte {
		if !bytes.Contains(good, []byte(old)) {
			t.Fatalf("Setup: the golden lacks %s", old)
		}
		return bytes.Replace(good, []byte(old), []byte(new), 1)
	}
	for name, body := range map[string][]byte{
		"an invalid byte in attribution.notice": swap(`"notice":"a`, "\"notice\":\"\xffa"),
		"a rune cut by a later byte":            swap(`"notice":"a`, "\"notice\":\"\xe7\xbfa"),
		"a surrogate":                           swap(`"notice":"a`, "\"notice\":\"\xed\xa0\x80a"),
		"an invalid byte in a skipped member":   swap(`{"version":2,`, "{\"version\":2,\"future\":\"\xff\","),
	} {
		for reader, r := range map[string]func() io.Reader{
			"whole":    func() io.Reader { return bytes.NewReader(body) },
			"one byte": func() io.Reader { return iotest.OneByteReader(bytes.NewReader(body)) },
		} {
			t.Run(name+", "+reader, func(t *testing.T) {
				if _, err := Decode(r(), DefaultLimits()); !errors.Is(err, ErrNotDocument) {
					t.Errorf("Decode = %v, want ErrNotDocument", err)
				}
			})
		}
	}
	// Reads of 1 to 4 bytes cut these runes at shifting offsets inside them.
	valid := swap(`"notice":"a`, "\"notice\":\"é翼🎌\uFFFDxé翼🎌xx\uFFFDé翼🎌a")
	for _, n := range []int{1, 2, 3, 4, len(valid)} {
		t.Run(fmt.Sprintf("valid non-ASCII, reads of %d bytes", n), func(t *testing.T) {
			doc, err := Decode(&chunkReader{b: valid, n: n}, DefaultLimits())
			if err != nil {
				t.Fatalf("Decode = %v", err)
			}
			if want := "é翼🎌\uFFFDxé翼🎌xx\uFFFDé翼🎌a"; !strings.HasPrefix(doc.Attribution.Notice, want) {
				t.Errorf("attribution.notice = %.40q, want it to start with %q", doc.Attribution.Notice, want)
			}
		})
	}
}

// chunkReader reads b at most n bytes at a time.
type chunkReader struct {
	b []byte
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), c.n)], c.b)
	c.b = c.b[n:]
	return n, nil
}

// A zero field takes the default the README states, and so does a
// negative one.
func TestZeroLimitsTakeTheDefaults(t *testing.T) {
	want := Limits{MaxDocumentBytes: 32 << 20, MaxRows: 131072, MaxRowBytes: 256 << 10, MaxEpisodes: 2097152}
	if got := DefaultLimits(); got != want {
		t.Errorf("DefaultLimits() = %+v, want %+v", got, want)
	}
	for _, lim := range []Limits{{}, {MaxDocumentBytes: -1, MaxRows: -1, MaxRowBytes: -1, MaxEpisodes: -1}} {
		if got := lim.orDefault(); got != want {
			t.Errorf("%+v takes %+v, want %+v", lim, got, want)
		}
	}
}

func TestDecodeLimits(t *testing.T) {
	good := encodeSample(t)
	for _, tc := range []struct {
		name string
		lim  Limits
		ok   bool
	}{
		{"the document at its cap", Limits{MaxDocumentBytes: int64(len(good))}, true},
		{"the document over its cap", Limits{MaxDocumentBytes: int64(len(good)) - 1}, false},
		{"four anime rows at their cap", Limits{MaxRows: 7}, true},
		{"rows over their cap", Limits{MaxRows: 3}, false},
		{"the largest row at its cap", Limits{MaxRowBytes: 943}, true},
		{"the entries' episodes at their cap", Limits{MaxEpisodes: 12}, true},
		{"the entries' episodes over their cap", Limits{MaxEpisodes: 11}, false},
		{"the largest row over its cap", Limits{MaxRowBytes: 942}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(good), tc.lim)
			if tc.ok != (err == nil) || !tc.ok && !errors.Is(err, ErrTooLarge) {
				t.Errorf("Decode under %+v = %v, want ok=%t or ErrTooLarge", tc.lim, err, tc.ok)
			}
		})
	}
	big := strings.Replace(good, `"generated_at":"`, `"generated_at":"`+strings.Repeat("x", 1000), 1)
	if _, err := Decode(strings.NewReader(big), Limits{MaxRowBytes: 943}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Decode(a member over the row cap) = %v, want ErrTooLarge", err)
	}
}

// addEpisodes refuses a sum past limit without wrapping past an int: on a
// 32-bit int 4,096 parts of 1<<20 episodes would wrap.
func TestAddEpisodesCannotWrap(t *testing.T) {
	for _, tc := range []struct {
		name            string
		named, n, limit int
		want            int
		ok              bool
	}{
		{"under the cap", 3, 1, 5, 4, true},
		{"at the cap", 3, 2, 5, 5, true},
		{"over the cap", 3, 3, 5, 3, false},
		{"at the cap of a full int", math.MaxInt - 10, 10, math.MaxInt, math.MaxInt, true},
		{"a sum past a full int", math.MaxInt - 5, 10, math.MaxInt, math.MaxInt - 5, false},
		{"a sum past a 32-bit int", math.MaxInt32 - 5, 1 << 20, math.MaxInt32, math.MaxInt32 - 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := addEpisodes(tc.named, tc.n, tc.limit); got != tc.want || ok != tc.ok {
				t.Errorf("addEpisodes(%d, %d, %d) = %d, %t; want %d, %t", tc.named, tc.n, tc.limit, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// A member Decode does not know is ignored at every level, so one added
// within version 2 reads with this reader.
func TestDecodeIgnoresUnknownMembers(t *testing.T) {
	good := encodeSample(t)
	body := good
	for _, r := range [][2]string{
		{`{"version":2,`, `{"version":2,"future":{"a":[1]},`},
		{`"sources":{`, `"sources":{"future":1,`},
		{`"overlay":{`, `"overlay":{"future":1,`},
		{`"attribution":{`, `"attribution":{"future":1,`},
		{`{"anidb_id":1,`, `{"anidb_id":1,"future":[],`},
		{`"last":{"regular":4,`, `"last":{"future":1,"regular":4,`},
		{`{"kind":"regular","start":1,`, `{"future":true,"kind":"regular","start":1,`},
		{`{"season":1,"episode":1}`, `{"season":1,"episode":1,"future":"x"}`},
		{`{"anilist_id":1,`, `{"anilist_id":1,"future":null,`},
		{`"ids":{"anidb":[1],`, `"ids":{"crunchyroll":["x"],"anidb":[1],`},
		{`{"anidb_id":3,"kind":"regular"}`, `{"anidb_id":3,"kind":"regular","future":1}`},
		{`"kind":"regular"}]}]}`, `"kind":"regular"}]}],"later":[]}`},
	} {
		if !strings.Contains(body, r[0]) {
			t.Fatalf("Setup: the sample lacks %q", r[0])
		}
		body = strings.Replace(body, r[0], r[1], 1)
	}
	doc, err := Decode(strings.NewReader(body), DefaultLimits())
	if err != nil {
		t.Fatalf("Decode(unknown members at every level) = %v", err)
	}
	if !reflect.DeepEqual(doc, sample()) {
		t.Errorf("Decode read an unknown member into the document: %+v", doc)
	}
}

// Each document is valid: a newer producer may write it, or the format
// allows it, so refusing it would cost a reader its whole map.
func TestDecodeAccepts(t *testing.T) {
	good := encodeSample(t)
	for _, tc := range []struct {
		check          func(*testing.T, *Document)
		name, old, new string
	}{
		{
			name: "a part with no range on a kind its anime has no last of",
			old:  `{"anidb_id":4,"kind":"regular"}`, new: `{"anidb_id":4,"kind":"regular"}`,
			check: func(t *testing.T, doc *Document) {
				if got := NewIndex(doc).Episodes(doc.Entries[4]); len(got) != 1 || got[0].Refs[0].AniDB != 2 {
					t.Errorf("Episodes(AniList 5) = %+v, want only AniDB 2's combined episode", got)
				}
			},
		},
		{
			name: "entries with no AniList id ordered by a service this reader does not know",
			old:  `{"type":"OVA","ids":{"animeplanet":["a"],"kitsu":[9]}},{"type":"TV","ids":{"kitsu":[3]},"parts":[{"anidb_id":2,"kind":"regular"}]}`,
			new:  `{"type":"TV","ids":{"aaa":[1],"kitsu":[3]},"parts":[{"anidb_id":2,"kind":"regular"}]},{"type":"OVA","ids":{"animeplanet":["a"],"kitsu":[9]}}`,
			check: func(t *testing.T, doc *Document) {
				if got := doc.Entries[5].IDs.Kitsu; !reflect.DeepEqual(got, []int{3}) {
					t.Errorf("entries[5].ids.kitsu = %v, want [3]", got)
				}
			},
		},
		{
			name: "a service key of 256 characters, 768 bytes",
			old:  `"animeplanet":["a"]`, new: `"animeplanet":["` + strings.Repeat("翼", 256) + `"]`,
			check: func(t *testing.T, doc *Document) {
				if got := doc.Entries[5].IDs.AnimePlanet; len(got) != 1 || got[0] != strings.Repeat("翼", 256) {
					t.Errorf("entries[5].ids.animeplanet = %q, want the 256-character key", got)
				}
			},
		},
		{
			name: "ids holding only a service this reader does not know",
			old:  `{"type":"OVA","ids":{"animeplanet":["a"],"kitsu":[9]}}`, new: `{"type":"OVA","ids":{"anpdb":[18]}}`,
			check: func(t *testing.T, doc *Document) {
				if got := doc.Entries[5].IDs; got == nil || !reflect.DeepEqual(*got, IDs{}) {
					t.Errorf("entries[5].ids = %+v, want present with no service this reader knows", got)
				}
			},
		},
		{
			name: "an anime row holding only a member this reader does not know",
			old:  `{"anidb_id":4,"type":"OVA"}`, new: `{"anidb_id":4,"future":1}`,
			check: func(t *testing.T, doc *Document) {
				if !reflect.DeepEqual(doc.Anime[3], Anime{AniDBID: 4}) {
					t.Errorf("anime[3] = %+v, want AniDB 4 with nothing this reader knows", doc.Anime[3])
				}
			},
		},
		{
			name: "a row with AniDB's list and no episode",
			old:  `{"anidb_id":4,"type":"OVA"}`, new: `{"anidb_id":4,"episodes_listed":true}`,
			check: func(t *testing.T, doc *Document) {
				if ep, ok := NewIndex(doc).Episode(Ref{AniDB: 4, Kind: Regular, Number: 1}); !ok || ep.Listing != NotListed {
					t.Errorf("Episode(AniDB 4 regular 1) = %+v, %t; want NotListed", ep, ok)
				}
			},
		},
		{
			name: "a run whose last episode lands on the format's bound",
			old:  `"tvdb":[{"season":2,"episode":1,"default":true}]`, new: `"tvdb":[{"season":2,"episode":1048575,"default":true}]`,
			check: func(t *testing.T, doc *Document) {
				ep, ok := NewIndex(doc).Episode(Ref{AniDB: 2, Kind: Regular, Number: 2})
				if !ok || ep.TVDB == nil || len(ep.TVDB.Targets) != 1 || ep.TVDB.Targets[0].Episode != 1048576 {
					t.Errorf("Episode(AniDB 2 regular 2) = %+v, %t; want TVDB S2E1048576", ep, ok)
				}
			},
		},
		{
			name: "generated_at at the offset +00:00",
			old:  `"generated_at":"2026-10-05T00:00:00Z"`, new: `"generated_at":"2026-10-05T00:00:00+00:00"`,
			check: func(t *testing.T, doc *Document) {
				if doc.GeneratedAt != "2026-10-05T00:00:00+00:00" {
					t.Errorf("generated_at = %q", doc.GeneratedAt)
				}
			},
		},
		{
			name: "generated_at in lower case",
			old:  `"generated_at":"2026-10-05T00:00:00Z"`, new: `"generated_at":"2026-10-05t00:00:00z"`,
			check: func(t *testing.T, doc *Document) {
				if doc.GeneratedAt != "2026-10-05t00:00:00z" {
					t.Errorf("generated_at = %q", doc.GeneratedAt)
				}
			},
		},
		{
			name: "generated_at a leap second",
			old:  `"generated_at":"2026-10-05T00:00:00Z"`, new: `"generated_at":"2016-12-31T23:59:60Z"`,
			check: func(t *testing.T, doc *Document) {
				if doc.GeneratedAt != "2016-12-31T23:59:60Z" {
					t.Errorf("generated_at = %q", doc.GeneratedAt)
				}
			},
		},
		{
			name: "generated_at on a leap day",
			old:  `"generated_at":"2026-10-05T00:00:00Z"`, new: `"generated_at":"2028-02-29T00:00:00Z"`,
			check: func(t *testing.T, doc *Document) {
				if doc.GeneratedAt != "2028-02-29T00:00:00Z" {
					t.Errorf("generated_at = %q", doc.GeneratedAt)
				}
			},
		},
		{
			name: "generated_at with a fraction of a second",
			old:  `"generated_at":"2026-10-05T00:00:00Z"`, new: `"generated_at":"2026-10-05T00:00:00.5Z"`,
			check: func(t *testing.T, doc *Document) {
				if doc.GeneratedAt != "2026-10-05T00:00:00.5Z" {
					t.Errorf("generated_at = %q", doc.GeneratedAt)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(good, tc.old) {
				t.Fatalf("Setup: the sample lacks %q", tc.old)
			}
			doc, err := Decode(strings.NewReader(strings.Replace(good, tc.old, tc.new, 1)), DefaultLimits())
			if err != nil {
				t.Fatalf("Decode = %v, want the document", err)
			}
			tc.check(t, doc)
		})
	}
}
