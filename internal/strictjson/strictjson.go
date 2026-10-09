// Package strictjson reads a JSON document the way animap holds every file it
// owns: exactly one value, no member the target type does not declare, and
// nothing after the value but whitespace.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// errTrailing reports input after the document's one value.
var errTrailing = errors.New("trailing data after the JSON value")

// Decode decodes body's one JSON value into v. It returns the decoder's error
// for a malformed value or an unknown member, and errTrailing for anything
// after the value.
func Decode(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Decoder.More looks for another element inside an array or object, so it
	// reports false for a stray top-level ] or }; only io.EOF proves the end.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errTrailing
	}
	return nil
}
