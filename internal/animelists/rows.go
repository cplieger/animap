package animelists

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cplieger/animap/internal/schema"
)

// errRow reports a mapping row that does not parse in the list's syntax.
var errRow = errors.New("animelists: unparseable mapping row")

// Schema converts a row to its published shape. Any unparseable part fails
// the whole row: a guessed value is worse than a dropped row.
func (r Row) Schema() (schema.Row, error) {
	var out schema.Row
	var err error
	if out.AniDBSeason, err = atoiMin(r.Attrs["anidbseason"], 0); err != nil {
		return schema.Row{}, fmt.Errorf("%w: anidbseason: %w", errRow, err)
	}
	if out.TVDBSeason, err = optSeason(r.Attrs["tvdbseason"]); err != nil {
		return schema.Row{}, fmt.Errorf("%w: tvdbseason: %w", errRow, err)
	}
	if out.TMDBSeason, err = optSeason(r.Attrs["tmdbseason"]); err != nil {
		return schema.Row{}, fmt.Errorf("%w: tmdbseason: %w", errRow, err)
	}
	for _, f := range []struct {
		dst  *int
		name string
		min  int
	}{{&out.Start, "start", 1}, {&out.End, "end", 1}} {
		if v := strings.TrimSpace(r.Attrs[f.name]); v != "" {
			if *f.dst, err = atoiMin(v, f.min); err != nil {
				return schema.Row{}, fmt.Errorf("%w: %s: %w", errRow, f.name, err)
			}
		}
	}
	if v := strings.TrimSpace(r.Attrs["offset"]); v != "" {
		if out.Offset, err = strconv.Atoi(v); err != nil {
			return schema.Row{}, fmt.Errorf("%w: offset: %w", errRow, err)
		}
	}
	if out.Episodes, err = parsePairs(r.Text); err != nil {
		return schema.Row{}, err
	}
	return out, nil
}

// parsePairs reads ";a-b;c-d+e;f-0;" as [[a,b],[c,d,e],[f]]. A "-0" target
// is the list's "no corresponding episode" and yields a one-element pair.
func parsePairs(text string) ([][]int, error) {
	var out [][]int
	for part := range strings.SplitSeq(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair, err := parsePair(part)
		if err != nil {
			return nil, fmt.Errorf("%w: pair %q", errRow, part)
		}
		out = append(out, pair)
	}
	return out, nil
}

func parsePair(part string) ([]int, error) {
	src, dst, ok := strings.Cut(part, "-")
	if !ok {
		return nil, errors.New("no '-'")
	}
	a, err := atoiMin(src, 0)
	if err != nil {
		return nil, err
	}
	pair := []int{a}
	if strings.TrimSpace(dst) == "0" {
		return pair, nil
	}
	for t := range strings.SplitSeq(dst, "+") {
		n, err := atoiMin(t, 1)
		if err != nil {
			return nil, err
		}
		pair = append(pair, n)
	}
	return pair, nil
}

// RowFromSchema renders a published row back into the list's syntax.
func RowFromSchema(s schema.Row) Row {
	attrs := map[string]string{"anidbseason": strconv.Itoa(s.AniDBSeason)}
	if s.TVDBSeason != nil {
		attrs["tvdbseason"] = strconv.Itoa(*s.TVDBSeason)
	}
	if s.TMDBSeason != nil {
		attrs["tmdbseason"] = strconv.Itoa(*s.TMDBSeason)
	}
	if s.Start > 0 {
		attrs["start"] = strconv.Itoa(s.Start)
	}
	if s.End > 0 {
		attrs["end"] = strconv.Itoa(s.End)
	}
	if s.Offset != 0 {
		attrs["offset"] = strconv.Itoa(s.Offset)
	}
	return Row{Attrs: attrs, Text: formatPairs(s.Episodes)}
}

// formatPairs is the inverse of parsePairs.
func formatPairs(pairs [][]int) string {
	if len(pairs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte(';')
	for _, p := range pairs {
		if len(p) == 0 {
			continue
		}
		b.WriteString(strconv.Itoa(p[0]))
		b.WriteByte('-')
		if len(p) == 1 {
			b.WriteByte('0')
		}
		for i, t := range p[1:] {
			if i > 0 {
				b.WriteByte('+')
			}
			b.WriteString(strconv.Itoa(t))
		}
		b.WriteByte(';')
	}
	return b.String()
}

func atoiMin(s string, minimum int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	if n < minimum {
		return 0, fmt.Errorf("%d is below %d", n, minimum)
	}
	return n, nil
}

func optSeason(s string) (*int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	n, err := atoiMin(s, 0)
	if err != nil {
		return nil, err
	}
	return &n, nil
}
