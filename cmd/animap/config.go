package main

import (
	"errors"
	"fmt"
	"os"
	"unicode"
)

// maxTokenBytes bounds TMDB_TOKEN; a read access token is a JWT of a few
// hundred bytes.
const maxTokenBytes = 4 << 10

var errBadToken = errors.New("TMDB_TOKEN is malformed")

// tmdbToken reads TMDB_TOKEN once. Unset or empty is "" with no error: the
// TMDB side is then skipped, never failed. A value with whitespace or a
// control character, or over maxTokenBytes, is refused; the error never
// carries the value.
func tmdbToken() (string, error) {
	return checkToken(os.Getenv("TMDB_TOKEN"))
}

func checkToken(v string) (string, error) {
	if len(v) > maxTokenBytes {
		return "", fmt.Errorf("%w: over %d bytes", errBadToken, maxTokenBytes)
	}
	for _, r := range v {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r > unicode.MaxASCII {
			return "", fmt.Errorf("%w: it holds whitespace, a control character or a non-ASCII character", errBadToken)
		}
	}
	return v, nil
}
