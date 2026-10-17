package mapread

import (
	"bytes"
	"encoding/json"
)

// Encode returns doc's canonical encoding: minified, with no HTML escaping,
// no trailing newline and the members in declared order. It does not
// validate; Decode does. A document Decode read may hold an anime row or
// an ids object filled only by members this package does not know, which
// Encode writes empty and Decode then refuses.
func Encode(doc *Document) ([]byte, error) { return marshal(doc) }

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
