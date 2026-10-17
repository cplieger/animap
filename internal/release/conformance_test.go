package release

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/animap/mapread"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// goldens are the documents the oracle mutates: the root golden builds.
var goldens = []string{"../../testdata/golden/animap.json", "../../testdata/golden/animap-place.json"}

// schemaRule is one rule docs/schema.md lists as beyond the JSON Schema:
// refused names the decoder's refusal of it, and each edit of
// testdata/golden/animap.json (old to new, once) gives a schema-valid
// document that keeps the rule or breaks it.
type schemaRule struct {
	refused  *regexp.Regexp
	name     string
	accepted [][2]string
	broken   [][2]string
}

var schemaRules = []schemaRule{
	{
		name: "the bytes UTF-8", refused: regexp.MustCompile(`document is not UTF-8`),
		accepted: [][2]string{{`"notice":"a`, "\"notice\":\"\uFFFD翼a"}},
		broken:   [][2]string{{`"notice":"a`, "\"notice\":\"\xffa"}, {`"notice":"a`, "\"notice\":\"\xed\xa0\x80a"}},
	},
	{
		name: "no key repeated in an object, even in another case", refused: regexp.MustCompile(`is repeated|duplicate object key`),
		accepted: [][2]string{{`{"anidb_id":12,`, `{"future":1,"anidb_id":12,`}},
		broken: [][2]string{
			{`{"anidb_id":12,`, `{"anidb_id":12,"anidb_id":12,`},
			{`{"anidb_id":12,`, `{"ANIDB_ID":12,"anidb_id":12,`},
			{`{"version":2,`, `{"version":2,"future":1,"Future":1,`},
		},
	},
	{
		name: "the member order, with `version` first and only once", refused: regexp.MustCompile(`not version|is out of order`),
		accepted: [][2]string{{`{"version":2,`, `{"version":2,"future":1,`}},
		broken: [][2]string{
			{`{"version":2,`, `{"future":1,"version":2,`},
			{`{"version":2,"generated_at":"2026-10-05T00:00:00Z",`, `{"generated_at":"2026-10-05T00:00:00Z","version":2,`},
		},
	},
	{
		name: "every integer written with digits alone, with no fraction or exponent", refused: regexp.MustCompile(`not written as an integer`),
		accepted: [][2]string{{`{"anidb_id":12,`, `{"anidb_id":12,"future":1.5e3,`}},
		broken:   [][2]string{{`{"anidb_id":12,`, `{"anidb_id":12.0,`}, {`{"anidb_id":12,`, `{"anidb_id":1.2e1,`}, {`"version":2`, `"version":2.0`}},
	},
	{
		name: "`generated_at` in UTC, with the offset `Z` or `+00:00`", refused: regexp.MustCompile(`not an RFC 3339 time in UTC`),
		accepted: [][2]string{
			{`"2026-10-05T00:00:00Z"`, `"2026-10-05T00:00:00+00:00"`},
			{`"2026-10-05T00:00:00Z"`, `"2026-10-05t00:00:00.5z"`},
			{`"2026-10-05T00:00:00Z"`, `"2016-12-31T23:59:60Z"`},
		},
		broken: [][2]string{
			{`"2026-10-05T00:00:00Z"`, `"2026-10-05T00:00:00-00:00"`},
			{`"2026-10-05T00:00:00Z"`, `"2026-10-05T02:00:00+02:00"`},
		},
	},
	{
		name: "`start` at most `end`, in a run and in a part", refused: regexp.MustCompile(`starts at \d+ after its end`),
		accepted: [][2]string{{`{"kind":"regular","start":1,"end":2,"tvdb"`, `{"kind":"regular","start":2,"end":2,"tvdb"`}},
		broken: [][2]string{
			{`{"kind":"regular","start":1,"end":2,"tvdb"`, `{"kind":"regular","start":3,"end":2,"tvdb"`},
			{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":12,"kind":"regular","start":2,"end":1}]`},
		},
	},
	{
		name: "every target at most 1,048,576 at the run's last episode: its `episode` plus `end` minus `start`", refused: regexp.MustCompile(`lands episode \d+ on target episode \d+, above`),
		accepted: [][2]string{{`{"kind":"regular","start":1,"end":2,"tvdb":[{"season":0,"episode":1}]}`, `{"kind":"regular","start":1,"end":2,"tvdb":[{"season":0,"episode":1048575}]}`}},
		broken:   [][2]string{{`{"kind":"regular","start":1,"end":2,"tvdb":[{"season":0,"episode":1}]}`, `{"kind":"regular","start":1,"end":2,"tvdb":[{"season":0,"episode":1048576}]}`}},
	},
	{
		name:     "the order of the anime, of the AniList entries, of the runs, of the targets and of each `ids` list",
		refused:  regexp.MustCompile(`is not after|follows an entry with no AniList id|is not ascending and unique`),
		accepted: [][2]string{{`"ids":{"anidb":[16,17]}`, `"ids":{"anidb":[16,17],"kitsu":[1,9]}`}},
		broken: [][2]string{
			{`{"anidb_id":12,`, `{"anidb_id":9,`},
			{`{"anilist_id":101,`, `{"anilist_id":99,`},
			{`{"type":"TV","episodes":3,"ids":{"mal":[1007]}}`, `{"anilist_id":200,"type":"TV","episodes":3,"ids":{"mal":[1007]}}`},
			{`"tvdb":[{"season":0,"episode":4},{"season":0,"episode":5}]`, `"tvdb":[{"season":0,"episode":5},{"season":0,"episode":4}]`},
			{`"ids":{"anidb":[16,17]}`, `"ids":{"anidb":[17,16]}`},
			{
				`{"kind":"special","start":1,"end":1,"tvdb":[{"season":0,"episode":2}],"tmdb":[{"season":0,"episode":1,"default":true}]},{"kind":"special","start":3,"end":3,"tvdb":[],"tmdb":[{"season":0,"episode":3,"default":true}]}`,
				`{"kind":"special","start":3,"end":3,"tvdb":[],"tmdb":[{"season":0,"episode":3,"default":true}]},{"kind":"special","start":1,"end":1,"tvdb":[{"season":0,"episode":2}],"tmdb":[{"season":0,"episode":1,"default":true}]}`,
			},
		},
	},
	{
		name: "every run maximal, so no two runs could be one", refused: regexp.MustCompile(`so the two are one run`),
		accepted: [][2]string{{
			`{"kind":"regular","start":1,"end":2,"tvdb":[{"season":0,"episode":1}]}`,
			`{"kind":"regular","start":1,"end":1,"tvdb":[{"season":0,"episode":1}]},{"kind":"regular","start":2,"end":2,"tvdb":[{"season":0,"episode":3}]}`,
		}},
		broken: [][2]string{{
			`{"kind":"regular","start":1,"end":2,"tvdb":[{"season":0,"episode":1}]}`,
			`{"kind":"regular","start":1,"end":1,"tvdb":[{"season":0,"episode":1}]},{"kind":"regular","start":2,"end":2,"tvdb":[{"season":0,"episode":2}]}`,
		}},
	},
	{
		name: "`end` within `last`, except on a run of regular episodes AniDB lists that states no site", refused: regexp.MustCompile(`ends past last\.`),
		accepted: [][2]string{{`"last":{"regular":24}}`, `"last":{"regular":24},"episodes_listed":true,"episodes":[{"kind":"regular","start":25,"end":25,"anidb_episode_ids":[7]}]}`}},
		broken: [][2]string{
			{`"last":{"regular":2},"episodes":[{"kind":"regular","start":1,"end":2,`, `"last":{"regular":1},"episodes":[{"kind":"regular","start":1,"end":2,`},
			{`"last":{"regular":24}}`, `"last":{"regular":24},"episodes_listed":true,"episodes":[{"kind":"special","start":1,"end":1,"anidb_episode_ids":[7]}]}`},
		},
	},
	{
		name:     "one AniDB episode id per episode of a run, and ids only on a row with `episodes_listed`",
		refused:  regexp.MustCompile(`AniDB episode ids for \d+ episodes|without episodes_listed`),
		accepted: [][2]string{{`"last":{"regular":24}}`, `"last":{"regular":24},"episodes_listed":true,"episodes":[{"kind":"regular","start":24,"end":25,"anidb_episode_ids":[7,8]}]}`}},
		broken: [][2]string{
			{`"last":{"regular":24}}`, `"last":{"regular":24},"episodes_listed":true,"episodes":[{"kind":"regular","start":24,"end":25,"anidb_episode_ids":[7]}]}`},
			{`"last":{"regular":24}}`, `"last":{"regular":24},"episodes":[{"kind":"regular","start":24,"end":24,"anidb_episode_ids":[7]}]}`},
		},
	},
	{
		name: "`tvdb` only with `tvdb_id`, and `tmdb` only with `tmdb_tv_id`", refused: regexp.MustCompile(`states (tvdb|tmdb) on a row with no`),
		accepted: [][2]string{{`{"anidb_id":30,"tvdb_id":5006}`, `{"anidb_id":30,"tvdb_id":5006,"last":{"regular":1},"episodes":[{"kind":"regular","start":1,"end":1,"tvdb":[{"season":1,"episode":1}]}]}`}},
		broken: [][2]string{
			{`{"kind":"regular","start":1,"end":1,"tvdb":[{"season":0,"episode":3}]}`, `{"kind":"regular","start":1,"end":1,"tvdb":[{"season":0,"episode":3}],"tmdb":[]}`},
			{`{"anidb_id":12,"type":"OVA","tvdb_id":5001,`, `{"anidb_id":12,"type":"OVA","tmdb_tv_id":5001,`},
		},
	},
	{
		name:     "no run AniDB lists none of on a row with neither, and no such run that lands nowhere on every site",
		refused:  regexp.MustCompile(`lands nowhere on every site`),
		accepted: [][2]string{{`"last":{"regular":24}}`, `"last":{"regular":24},"episodes_listed":true,"episodes":[{"kind":"regular","start":1,"end":1,"anidb_episode_ids":[7]}]}`}},
		broken: [][2]string{
			{`{"anidb_id":19,"type":"TV","tvdb_id":5004,`, `{"anidb_id":19,"type":"TV",`},
			{`{"kind":"regular","start":1,"end":2,"tvdb":[{"season":0,"episode":1}]}`, `{"kind":"regular","start":1,"end":2,"tvdb":[]}`},
		},
	},
	{
		name: "every part naming an anime row", refused: regexp.MustCompile(`which has no anime row`),
		accepted: [][2]string{{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":30,"kind":"regular"}]`}},
		broken:   [][2]string{{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":11,"kind":"regular"}]`}},
	},
	{
		name: "every ranged part within its anime's `last`", refused: regexp.MustCompile(`past AniDB \d+'s last`),
		accepted: [][2]string{{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":12,"kind":"regular","start":1,"end":2}]`}},
		broken:   [][2]string{{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":12,"kind":"regular","start":1,"end":3}]`}},
	},
	{
		name: "a combined part regular and with no range", refused: regexp.MustCompile(`is combined, so it is regular with no range`),
		accepted: [][2]string{{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":12,"kind":"regular","combined":true}]`}},
		broken: [][2]string{
			{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":12,"kind":"special","combined":true}]`},
			{`"parts":[{"anidb_id":12,"kind":"regular"}]`, `"parts":[{"anidb_id":12,"kind":"regular","start":1,"end":2,"combined":true}]`},
		},
	},
}

// readerLimit reports a refusal of a schema-valid document for the
// reader's limits, not the format's: the Limits caps, and an integer the
// platform's int cannot hold.
func readerLimit(err error) bool {
	return errors.Is(err, mapread.ErrTooLarge) || strings.Contains(err.Error(), "outside this platform's int")
}

func compileSchema(t testing.TB) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open("../../docs/animap.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	if err := c.AddResource("animap.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("animap.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// conformance judges data by the schema and by mapread.Decode. class
// names the outcome; problem is set when the two disagree beyond a listed
// rule or a reader limit. A document that keeps every listed rule has no
// rule to be refused for.
func conformance(sch *jsonschema.Schema, data []byte, keeps bool) (class string, problem error) {
	_, derr := mapread.Decode(bytes.NewReader(data), mapread.DefaultLimits())
	inst, perr := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if perr != nil {
		if derr == nil {
			return "", fmt.Errorf("accepted bytes that are not JSON: %w", perr)
		}
		return "not JSON", nil
	}
	verr := sch.Validate(inst)
	switch {
	case derr == nil && verr != nil:
		return "", fmt.Errorf("accepted a document the schema refuses: %w", verr)
	case derr == nil:
		return "accepted", nil
	case verr != nil:
		return "refused by both", nil
	case keeps:
		return "", fmt.Errorf("refused a schema-valid document that keeps every listed rule: %w", derr)
	case readerLimit(derr):
		return "a reader limit", nil
	}
	for _, r := range schemaRules {
		if r.refused.MatchString(derr.Error()) {
			return r.name, nil
		}
	}
	return "", fmt.Errorf("refused a schema-valid document for no listed rule: %w", derr)
}

// The rules docs/schema.md lists beyond the JSON Schema are schemaRules,
// one each, and each is beyond the schema: a document breaking it is
// schema-valid, and Decode refuses it for that rule.
func TestRulesBeyondTheSchema(t *testing.T) {
	var listed []string
	f, err := os.Open("../../docs/schema.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	in := false
	for sc := bufio.NewScanner(f); sc.Scan(); {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "- The JSON Schema cannot state these rules"):
			in = true
		case in && strings.HasPrefix(line, "  - "):
			listed = append(listed, strings.TrimPrefix(line, "  - "))
		case in:
			in = false
		}
	}
	var names []string
	for _, r := range schemaRules {
		names = append(names, r.name)
	}
	if !slices.Equal(listed, names) {
		t.Errorf("docs/schema.md lists %q,\nthe rules here are %q", listed, names)
	}
	golden, err := os.ReadFile(goldens[0])
	if err != nil {
		t.Fatal(err)
	}
	sch := compileSchema(t)
	edit := func(t *testing.T, e [2]string) []byte {
		t.Helper()
		if !bytes.Contains(golden, []byte(e[0])) {
			t.Fatalf("Setup: the golden lacks %s", e[0])
		}
		return bytes.Replace(golden, []byte(e[0]), []byte(e[1]), 1)
	}
	for _, r := range schemaRules {
		t.Run(r.name, func(t *testing.T) {
			for _, e := range r.accepted {
				if class, problem := conformance(sch, edit(t, e), true); class != "accepted" || problem != nil {
					t.Errorf("kept by %s: %s, %v; want accepted", e[1], class, problem)
				}
			}
			for _, e := range r.broken {
				body := edit(t, e)
				inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
				if err != nil {
					t.Fatalf("broken by %q: not JSON: %v", e[1], err)
				}
				if err := sch.Validate(inst); err != nil {
					t.Errorf("broken by %q: the schema refuses it, so the rule is not beyond the schema: %v", e[1], err)
				}
				_, derr := mapread.Decode(bytes.NewReader(body), mapread.DefaultLimits())
				if derr == nil || !r.refused.MatchString(derr.Error()) {
					t.Errorf("broken by %q: Decode = %v, want it refused for this rule", e[1], derr)
				}
			}
		})
	}
}

// Decode and the JSON Schema agree on every mutation of the goldens: an
// accepted document is schema-valid, and a schema-valid one is accepted
// unless it breaks a listed rule or a reader limit.
func TestDecodeConformsToTheSchema(t *testing.T) {
	sch := compileSchema(t)
	for _, path := range goldens {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			golden, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			root, err := parseTree(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got := root.encode(); !bytes.Equal(got, golden) {
				t.Fatalf("Setup: %s does not re-encode as written", path)
			}
			counts := map[string]int{}
			var problems []string
			mutate(root, "", "", func(where string, keeps bool) {
				counts["documents"]++
				if keeps {
					counts["documents keeping every rule"]++
				}
				doc := root.encode()
				class, problem := conformance(sch, doc, keeps)
				counts[class]++
				if problem != nil && len(problems) < 20 {
					problems = append(problems, fmt.Sprintf("%s: %v\n%.300s", where, problem, doc))
				}
			})
			if !bytes.Equal(root.encode(), golden) {
				t.Fatalf("Setup: mutating %s did not restore it", path)
			}
			for _, p := range problems {
				t.Error(p)
			}
			for _, k := range slices.Sorted(maps.Keys(counts)) {
				t.Logf("%7d %s", counts[k], k)
			}
			if counts["accepted"] == 0 || counts["refused by both"] == 0 || counts["documents"] < 5000 {
				t.Errorf("the mutations reached too little: %v", counts)
			}
		})
	}
}

// tree is a JSON value that keeps its members' order: a scalar's bytes as
// written, an object's members or an array's elements.
type tree struct {
	scalar  []byte
	members []member
	elems   []*tree
	delim   byte
}

type member struct {
	val *tree
	key string
}

func parseTree(b []byte) (*tree, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return parseValue(dec)
}

func parseValue(dec *json.Decoder) (*tree, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		t := &tree{delim: '{', members: []member{}}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			t.members = append(t.members, member{key: k.(string), val: v})
		}
		_, err := dec.Token()
		return t, err
	case json.Delim('['):
		t := &tree{delim: '[', elems: []*tree{}}
		for dec.More() {
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			t.elems = append(t.elems, v)
		}
		_, err := dec.Token()
		return t, err
	}
	if n, ok := tok.(json.Number); ok {
		return &tree{scalar: []byte(n)}, nil
	}
	return &tree{scalar: quote(tok)}, nil
}

// quote writes v as encoding/json does with no HTML escaping, as Encode.
func quote(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func (t *tree) encode() []byte {
	var b bytes.Buffer
	t.write(&b)
	return b.Bytes()
}

func (t *tree) write(w io.Writer) {
	switch t.delim {
	case '{':
		_, _ = io.WriteString(w, "{")
		for i, m := range t.members {
			if i > 0 {
				_, _ = io.WriteString(w, ",")
			}
			_, _ = w.Write(quote(m.key))
			_, _ = io.WriteString(w, ":")
			m.val.write(w)
		}
		_, _ = io.WriteString(w, "}")
	case '[':
		_, _ = io.WriteString(w, "[")
		for i, e := range t.elems {
			if i > 0 {
				_, _ = io.WriteString(w, ",")
			}
			e.write(w)
		}
		_, _ = io.WriteString(w, "]")
	default:
		_, _ = w.Write(t.scalar)
	}
}

// scalarEdits replace any value: every JSON type, the integer bounds of
// the format and of an int, and integers spelled as JSON Schema also reads
// them.
var scalarEdits = []string{
	`null`, `true`, `false`, `""`, `"x"`, `[]`, `{}`, `0`, `-1`, `1`, `2`, `1048576`, `1048577`,
	strconv.Itoa(math.MinInt), strconv.Itoa(math.MaxInt), "9223372036854775808", `1.0`, `1e0`, `0.5`,
}

// instants are spellings of generated_at, RFC 3339's and others, each
// with whether it is an RFC 3339 time in UTC.
var instants = []struct {
	s   string
	utc bool
}{
	{`"2026-10-05T00:00:00+00:00"`, true},
	{`"2026-10-05t00:00:00z"`, true},
	{`"2026-10-05T00:00:00.123456789Z"`, true},
	{`"2016-12-31T23:59:60Z"`, true},
	{`"2028-02-29T00:00:00Z"`, true},
	{`"0000-01-01T00:00:00Z"`, true},
	{`"2026-10-05T00:00:00-00:00"`, false},
	{`"2026-10-05T00:00:00+01:00"`, false},
	{`"2016-12-31T22:59:60Z"`, false},
	{`"2026-02-29T00:00:00Z"`, false},
	{`"2026-10-05T24:00:00Z"`, false},
	{`"2026-10-05 00:00:00Z"`, false},
	{`"2026-10-05T00:00Z"`, false},
	{`"2026-10-05"`, false},
	{`"2026-10-05T00:00:00"`, false},
	{`"2026-10-05T00:00:00.Z"`, false},
	{`"2026-13-05T00:00:00Z"`, false},
	{`"２026-10-05T00:00:00Z"`, false},
}

// mutate calls emit once per mutation of t, made in place and undone after
// the call: each value replaced (scalarEdits, and by kind a number's
// neighbours and spellings, a string's length and encoding, generated_at's
// instants), each object given an unknown member and a repeated or removed
// one, and each array emptied, shortened, repeated or reordered. keeps
// marks a mutation no listed rule reads: an unknown member after version,
// a UTC instant, or valid text in sources or attribution.
func mutate(t *tree, key, at string, emit func(where string, keeps bool)) {
	replace := func(what string, with *tree, keeps bool) {
		saved := *t
		*t = *with
		emit(at+" = "+what, keeps)
		*t = saved
	}
	for _, s := range scalarEdits {
		v, _ := parseTree([]byte(s))
		replace(s, v, false)
	}
	free := strings.HasPrefix(at, ".sources.") || strings.HasPrefix(at, ".attribution.")
	switch {
	case key == "generated_at":
		for _, in := range instants {
			replace(in.s, &tree{scalar: []byte(in.s)}, in.utc)
		}
	case t.delim == 0 && t.scalar[0] == '"':
		var s string
		_ = json.Unmarshal(t.scalar, &s)
		open := bytes.TrimSuffix(quote(s), []byte(`"`))
		for _, v := range [][]byte{
			quote(strings.Repeat("翼", 256)), quote(strings.Repeat("翼", 257)), quote(strings.Repeat("🎌", 257)),
			quote(s + "é"), quote(s + "\uFFFD"), append(slices.Clone(open), `\ud800"`...), append(slices.Clone(open), `\u0041"`...),
		} {
			replace(fmt.Sprintf("%.24q", v), &tree{scalar: v}, free)
		}
		replace("an invalid byte", &tree{scalar: append(slices.Clone(open), "\xff\""...)}, false)
	case t.delim == 0 && t.scalar[0] != 't' && t.scalar[0] != 'f' && t.scalar[0] != 'n':
		n, _ := strconv.Atoi(string(t.scalar))
		for _, s := range []string{strconv.Itoa(n - 1), strconv.Itoa(n + 1), string(t.scalar) + ".0", string(t.scalar) + "e0", "-" + string(t.scalar)} {
			replace(s, &tree{scalar: []byte(s)}, false)
		}
	}
	switch t.delim {
	case '{':
		mutateObject(t, at, emit)
		for _, m := range t.members {
			mutate(m.val, m.key, at+"."+m.key, emit)
		}
	case '[':
		mutateArray(t, at, emit)
		for i, e := range t.elems {
			mutate(e, "", at+"["+strconv.Itoa(i)+"]", emit)
		}
	}
}

func mutateObject(t *tree, at string, emit func(where string, keeps bool)) {
	saved := t.members
	set := func(what string, ms []member, keeps bool) {
		t.members = ms
		emit(at+" "+what, keeps)
		t.members = saved
	}
	unknown := member{key: "future", val: &tree{scalar: []byte(`1`)}}
	set("+future first", append([]member{unknown}, saved...), at != "")
	set("+future last", append(slices.Clone(saved), unknown), true)
	for i, m := range saved {
		set("repeats "+m.key, append(slices.Clone(saved), m), false)
		set("repeats "+m.key+" upper-cased", append(slices.Clone(saved), member{key: strings.ToUpper(m.key), val: m.val}), false)
		set("-"+m.key, slices.Delete(slices.Clone(saved), i, i+1), false)
	}
}

func mutateArray(t *tree, at string, emit func(where string, keeps bool)) {
	saved := t.elems
	set := func(what string, es []*tree) {
		t.elems = es
		emit(at+" "+what, false)
		t.elems = saved
	}
	set("emptied", []*tree{})
	for i := range saved {
		set(fmt.Sprintf("-[%d]", i), slices.Delete(slices.Clone(saved), i, i+1))
		set(fmt.Sprintf("repeats [%d]", i), slices.Insert(slices.Clone(saved), i, saved[i]))
		if i+1 < len(saved) {
			swapped := slices.Clone(saved)
			swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
			set(fmt.Sprintf("swaps [%d] and [%d]", i, i+1), swapped)
		}
	}
}
