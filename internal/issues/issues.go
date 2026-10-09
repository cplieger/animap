// Package issues plans the issue tracker changes a run wants: create,
// reopen, edit and close, deduplicated by a key marker in each body. It
// only plans; a script applies the plan with gh.
package issues

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/runesafe/v2"
)

// Want is one issue a run wants open.
type Want struct {
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
	Pin    bool     `json:"pin,omitempty"`
}

// Issue is one existing issue, as gh issue list --json number,state,title,body.
type Issue struct {
	State  string `json:"state"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Number int    `json:"number"`
}

// Kind is what an Action does.
type Kind string

// scripts/apply-issues.sh has one case per Kind and stops on any other.
const (
	Create Kind = "create"
	reopen Kind = "reopen"
	edit   Kind = "edit"
	Close  Kind = "close"
)

// Action is one planned change. Number is 0 for Create.
type Action struct {
	Kind    Kind     `json:"kind"`
	Key     string   `json:"key"`
	Title   string   `json:"title,omitempty"`
	Body    string   `json:"body,omitempty"`
	Comment string   `json:"comment,omitempty"`
	Labels  []string `json:"labels,omitempty"`
	Number  int      `json:"number,omitempty"`
	Pin     bool     `json:"pin,omitempty"`
}

// MaxBodyRunes stays under GitHub's 65,536-character body limit.
const MaxBodyRunes = 60000

var markerRE = regexp.MustCompile(`<!-- animap:key=([a-z0-9:_-]+) -->`)

// Marker is the identity line every planned body carries.
func Marker(key string) string { return "<!-- animap:key=" + key + " -->" }

// KeyOf returns the key marker in body, "" when there is none.
func KeyOf(body string) string {
	m := markerRE.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// Plan reconciles wants against existing issues. A key in evaluated that is
// not wanted closes its open issue; a key outside evaluated is left alone,
// because the run could not look. At most limit creates and reopens are
// planned; the rest are returned as deferred for the dashboard.
func Plan(want []Want, evaluated map[string]bool, existing []Issue, limit int) (actions []Action, deferred []Want) {
	open, closed := index(existing)
	wanted := map[string]bool{}
	for i := range want {
		w := &want[i]
		wanted[w.Key] = true
		body := Render(w)
		if is, ok := open[w.Key]; ok {
			if is.Body != body || is.Title != w.Title {
				actions = append(actions, Action{Kind: edit, Key: w.Key, Number: is.Number, Title: w.Title, Body: body})
			}
			continue
		}
		if limit <= 0 {
			deferred = append(deferred, *w)
			continue
		}
		limit--
		a := Action{Kind: Create, Key: w.Key, Title: w.Title, Body: body, Labels: w.Labels, Pin: w.Pin}
		if is, ok := closed[w.Key]; ok {
			a.Kind, a.Number = reopen, is.Number
		}
		actions = append(actions, a)
	}
	for _, k := range slices.Sorted(maps.Keys(open)) {
		if !wanted[k] && evaluated[k] {
			actions = append(actions, Action{Kind: Close, Key: k, Number: open[k].Number, Comment: "The cause cleared on this run, so this closes automatically."})
		}
	}
	return actions, deferred
}

// index keys the marked issues by state, keeping the newest per key.
func index(existing []Issue) (open, closed map[string]Issue) {
	open, closed = map[string]Issue{}, map[string]Issue{}
	for _, is := range existing {
		k := KeyOf(is.Body)
		if k == "" {
			continue
		}
		dst := closed
		if strings.EqualFold(is.State, "open") {
			dst = open
		}
		if cur, ok := dst[k]; !ok || is.Number > cur.Number {
			dst[k] = is
		}
	}
	return open, closed
}

// Render is the body as published: the want's body, bounded, then the
// marker on its own line.
func Render(w *Want) string {
	body := w.Body
	if r := []rune(body); len(r) > MaxBodyRunes {
		body = string(r[:MaxBodyRunes]) + "\n\n(truncated)"
	}
	return body + "\n\n" + Marker(w.Key) + "\n"
}

// Untrusted renders upstream text (a title, an episode name) as a bounded
// single-line code span, so it can neither mention a user nor inject markdown.
func Untrusted(s string) string {
	t := runesafe.SanitizeSingleLineBounded(s, 200)
	t = strings.ReplaceAll(t, "`", "'")
	return fmt.Sprintf("%#q", t)
}
