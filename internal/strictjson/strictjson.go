// Package strictjson is the one way animap reads a JSON body, whoever wrote
// it: under a byte cap, valid UTF-8, exactly one value with nothing after
// it but whitespace, and no member repeated in any object, matched
// case-insensitively as encoding/json matches fields. encoding/json would
// replace an invalid byte with U+FFFD and keep the last of a repeated
// member, so either would change a fact silently; a body breaking any rule
// is refused whole, never repaired.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/cplieger/jsoncap/v2"
)

// Errors the reads return besides encoding/json's and jsoncap's, whose
// ErrDuplicateKey reports a repeated member.
var (
	ErrTooLarge = errors.New("strictjson: body over its byte cap")
	ErrNotUTF8  = errors.New("strictjson: body is not valid UTF-8")
)

// Decode reads a body animap writes, a committed file or a cache: a member
// v does not declare is refused too.
func Decode(body []byte, maxBytes int, v any) error {
	if err := check(body, maxBytes); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Unmarshal reads a body another service writes, which may add members:
// one v does not declare is skipped, as json.Unmarshal skips it.
func Unmarshal(body []byte, maxBytes int, v any) error {
	if err := check(body, maxBytes); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// ReadFile reads path, refusing one over maxBytes with ErrTooLarge; it
// reads at most maxBytes+1 bytes, the one past the cap proving oversize.
// A missing file is os.ErrNotExist.
func ReadFile(path string, maxBytes int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBytes {
		return nil, fmt.Errorf("%w: %s is over %d bytes", ErrTooLarge, path, maxBytes)
	}
	return body, nil
}

func check(body []byte, maxBytes int) error {
	switch {
	case len(body) > maxBytes:
		return fmt.Errorf("%w: %d bytes, over %d", ErrTooLarge, len(body), maxBytes)
	case !utf8.Valid(body):
		return ErrNotUTF8
	}
	return jsoncap.Preflight(bytes.NewReader(body))
}
