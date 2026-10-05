// Package counts reads checks/counts.json: regular episode counts, proven
// from AniDB's episode list, for Anime-Lists nodes whose AniDB id
// anime-offline-database does not carry. The collision check needs a count
// to place a node's episodes.
package counts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"time"
)

// Path is the file, relative to the repository root.
const Path = "checks/counts.json"

const (
	maxRows      = 1000
	maxFileBytes = 256 << 10
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("counts: invalid file")

// Row is one counted node.
type Row struct {
	Evidence        string `json:"evidence"`
	Date            string `json:"date"`
	Justification   string `json:"justification"`
	AniDBID         int    `json:"anidb_id"`
	RegularEpisodes int    `json:"regular_episodes"`
}

// Load reads and validates the file at path; a missing file has no rows.
func Load(path string) ([]Row, error) {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if st.Size() > maxFileBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrInvalid, path, st.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows, err := Decode(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rows, nil
}

// Decode parses and validates a JSON list of rows, which must be sorted by
// AniDB id with no id twice.
func Decode(body []byte) ([]Row, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var rows []Row
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalid)
	}
	if rows == nil {
		return nil, fmt.Errorf("%w: not a JSON list", ErrInvalid)
	}
	if len(rows) > maxRows {
		return nil, fmt.Errorf("%w: %d rows, at most %d", ErrInvalid, len(rows), maxRows)
	}
	for i := range rows {
		if err := rows[i].Validate(); err != nil {
			return nil, err
		}
		if i > 0 && rows[i].AniDBID <= rows[i-1].AniDBID {
			return nil, fmt.Errorf("%w: AniDB %d follows AniDB %d; rows are sorted by AniDB id, each once", ErrInvalid, rows[i].AniDBID, rows[i-1].AniDBID)
		}
	}
	return rows, nil
}

// Validate checks one row: a positive count, the AniDB page of the same
// anime as its evidence, a date and a one-line justification.
func (r *Row) Validate() error {
	var err error
	switch {
	case r.AniDBID <= 0:
		err = errors.New("anidb_id must be positive")
	case r.RegularEpisodes <= 0:
		err = errors.New("regular_episodes must be positive")
	case r.Evidence != "https://anidb.net/anime/"+strconv.Itoa(r.AniDBID):
		err = fmt.Errorf("evidence %q is not https://anidb.net/anime/%d", r.Evidence, r.AniDBID)
	case !isDate(r.Date):
		err = fmt.Errorf("date %q is not YYYY-MM-DD", r.Date)
	case strings.TrimSpace(r.Justification) == "" || strings.ContainsAny(r.Justification, "\r\n"):
		err = errors.New("justification must be one non-empty line")
	}
	if err != nil {
		return fmt.Errorf("%w: AniDB %d: %w", ErrInvalid, r.AniDBID, err)
	}
	return nil
}

func isDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// Merge returns the database's regular episode counts with every row's
// count in place of the database's own. database is not modified.
func Merge(database map[int]int, rows []Row) map[int]int {
	out := maps.Clone(database)
	if out == nil {
		out = make(map[int]int, len(rows))
	}
	for i := range rows {
		out[rows[i].AniDBID] = rows[i].RegularEpisodes
	}
	return out
}
