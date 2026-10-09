// Package animelists decodes Anime-Lists' anime-list-master.xml into nodes
// that keep every attribute as its wire string, and computes the canonical
// node text the overlay fingerprints.
package animelists

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/xmlx"
)

// MaxBodyBytes bounds the list before decode (about 9x the real 3.5 MB file).
const MaxBodyBytes = 32 << 20

// Limits sized off the real file (37,837 elements, depth 5, 9 attributes on
// <anime>, longest row text under 1 KiB). The body is untrusted upstream XML.
var limits = xmlx.Limits{
	MaxTextRunBytes: 64 << 10,
	MaxTokenBytes:   64 << 10,
	MaxTagAttrs:     32,
	MaxDepth:        16,
	MaxElements:     1 << 20,
}

const (
	maxFieldBytes = 4 << 10
	maxTextBytes  = 8 << 20
)

// Errors Parse returns. A bound breach wraps *xmlx.LimitError.
var (
	errRoot    = errors.New("animelists: root element is not anime-list")
	errNoNodes = errors.New("animelists: list carries no anime nodes")
	ErrTooBig  = errors.New("animelists: body exceeds the size bound")
)

// Node is one <anime> element. Attrs holds every attribute except anidbid.
type Node struct {
	Attrs   map[string]string
	Name    string
	Before  string
	Rows    []Row
	AniDBID int
}

// Row is one <mapping> element, attributes and text as written.
type Row struct {
	Attrs map[string]string
	Text  string
}

// List is the decoded file: nodes keyed by AniDB id, plus the ids in
// document order. A repeated AniDB id keeps its first node.
type List struct {
	Nodes map[int]*Node
	Order []int
}

// Parse decodes a whole list body under the size, preflight and budget bounds.
func Parse(body []byte) (*List, error) {
	if len(body) > MaxBodyBytes {
		return nil, ErrTooBig
	}
	if err := xmlx.Preflight(body, limits); err != nil {
		return nil, limitError(err)
	}
	budget, err := xmlx.NewBudget(maxFieldBytes, maxTextBytes)
	if err != nil {
		return nil, err
	}
	l := &listXML{budget: budget, list: &List{Nodes: make(map[int]*Node)}}
	if err := xml.Unmarshal(body, l); err != nil {
		return nil, limitError(err)
	}
	if len(l.list.Order) == 0 {
		return nil, errNoNodes
	}
	return l.list, nil
}

func limitError(err error) error {
	if le, ok := errors.AsType[*xmlx.LimitError](err); ok {
		return fmt.Errorf("animelists: list exceeds a decode limit: %w", le)
	}
	return err
}

type listXML struct {
	budget *xmlx.Budget
	list   *List
}

func (l *listXML) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	if start.Name.Local != "anime-list" {
		return errRoot
	}
	return walk(d, func(t xml.StartElement) error {
		if t.Name.Local != "anime" {
			return d.Skip()
		}
		n, err := l.decodeNode(d, t)
		if err != nil {
			return err
		}
		if n == nil {
			return nil
		}
		if _, dup := l.list.Nodes[n.AniDBID]; dup {
			return nil
		}
		l.list.Nodes[n.AniDBID] = n
		l.list.Order = append(l.list.Order, n.AniDBID)
		return nil
	})
}

// walk hands each child start element to onStart, which must consume it
// whole, and returns at the parent's end tag.
func walk(d *xml.Decoder, onStart func(xml.StartElement) error) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := onStart(t); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

func (l *listXML) attrs(t xml.StartElement) (map[string]string, error) {
	out := make(map[string]string, len(t.Attr))
	for _, a := range t.Attr {
		if err := l.budget.Charge(a.Value); err != nil {
			return nil, err
		}
		out[a.Name.Local] = a.Value
	}
	return out, nil
}

// decodeNode returns nil for a node whose anidbid is not a positive integer.
func (l *listXML) decodeNode(d *xml.Decoder, t xml.StartElement) (*Node, error) {
	attrs, err := l.attrs(t)
	if err != nil {
		return nil, err
	}
	n := &Node{Attrs: attrs}
	err = walk(d, func(c xml.StartElement) error {
		var textErr error
		switch c.Name.Local {
		case "name":
			n.Name, textErr = l.budget.DecodeText(d)
		case "before":
			n.Before, textErr = l.budget.DecodeText(d)
		case "mapping-list":
			n.Rows, textErr = l.decodeRows(d)
		default:
			textErr = d.Skip()
		}
		return textErr
	})
	if err != nil {
		return nil, err
	}
	id, convErr := strconv.Atoi(attrs["anidbid"])
	if convErr != nil || id <= 0 {
		return nil, nil
	}
	delete(n.Attrs, "anidbid")
	n.AniDBID = id
	return n, nil
}

func (l *listXML) decodeRows(d *xml.Decoder) ([]Row, error) {
	var rows []Row
	err := walk(d, func(m xml.StartElement) error {
		if m.Name.Local != "mapping" {
			return d.Skip()
		}
		ra, err := l.attrs(m)
		if err != nil {
			return err
		}
		text, err := l.budget.DecodeText(d)
		if err != nil {
			return err
		}
		rows = append(rows, Row{Attrs: ra, Text: text})
		return nil
	})
	return rows, err
}

// Attr returns an attribute trimmed, "" when absent.
func (n *Node) Attr(name string) string { return strings.TrimSpace(n.Attrs[name]) }

// Clone returns a deep copy.
func (n *Node) Clone() *Node {
	c := &Node{Attrs: maps.Clone(n.Attrs), Name: n.Name, Before: n.Before, AniDBID: n.AniDBID}
	if c.Attrs == nil {
		c.Attrs = map[string]string{}
	}
	for _, r := range n.Rows {
		c.Rows = append(c.Rows, Row{Attrs: maps.Clone(r.Attrs), Text: r.Text})
	}
	return c
}

// Canonical is the text the node fingerprint hashes: attributes sorted as
// name=value, then one line per row in document order, then <before>.
// <name> and <supplemental-info> are excluded, so a title edit is not drift.
func (n *Node) Canonical() string {
	var b strings.Builder
	b.WriteString("anime")
	writeAttrs(&b, n.Attrs)
	for _, r := range n.Rows {
		b.WriteString("\nmapping")
		writeAttrs(&b, r.Attrs)
		b.WriteString(" text=")
		b.WriteString(canonicalPairs(r.Text))
	}
	if strings.TrimSpace(n.Before) != "" {
		b.WriteString("\nbefore text=")
		b.WriteString(canonicalPairs(n.Before))
	}
	return b.String()
}

func writeAttrs(b *strings.Builder, attrs map[string]string) {
	for _, k := range slices.Sorted(maps.Keys(attrs)) {
		fmt.Fprintf(b, " %s=%s", k, strings.TrimSpace(attrs[k]))
	}
}

// canonicalPairs trims each ;-separated pair, drops empties and re-joins.
func canonicalPairs(text string) string {
	var parts []string
	for p := range strings.SplitSeq(text, ";") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ";" + strings.Join(parts, ";") + ";"
}

// Hash is the hex SHA-256 of Canonical.
func (n *Node) Hash() string {
	sum := sha256.Sum256([]byte(n.Canonical()))
	return hex.EncodeToString(sum[:])
}

// HashAbsent is the captured node hash of an entry that creates its node.
const HashAbsent = "absent"

// HashOf returns n.Hash, or HashAbsent for a nil node.
func HashOf(n *Node) string {
	if n == nil {
		return HashAbsent
	}
	return n.Hash()
}
