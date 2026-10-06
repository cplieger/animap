package anidb

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/animap/internal/strictjson"
)

// CountsPath is checks/counts.json, relative to the repository root: the
// regular counts, proven from AniDB's episode list, that override every
// source in Facts.Count.
const CountsPath = "checks/counts.json"

const (
	maxRows           = 1000
	maxCountsFileSize = 256 << 10
)

// Row is one counted node.
type Row struct {
	Evidence        string `json:"evidence"`
	Date            string `json:"date"`
	Justification   string `json:"justification"`
	AniDBID         int    `json:"anidb_id"`
	RegularEpisodes int    `json:"regular_episodes"`
}

// LoadRows reads and validates the counts file at path; a missing file has
// no rows.
func LoadRows(path string) ([]Row, error) {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if st.Size() > maxCountsFileSize {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrInvalid, path, st.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows, err := DecodeRows(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rows, nil
}

// DecodeRows parses and validates a JSON list of rows, which must be sorted
// by AniDB id with no id twice.
func DecodeRows(body []byte) ([]Row, error) {
	var rows []Row
	if err := strictjson.Decode(body, &rows); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
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
