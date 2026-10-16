package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/source"
)

// server serves show 1 with the given season numbers, two episodes each,
// and movie 7; it records every request's query.
type server struct {
	t       *testing.T
	seasons []int
	queries []string
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/3/movie/7":
		_, _ = w.Write([]byte(`{"id":7}`))
		return
	case "/3/tv/1":
	default:
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query().Get("append_to_response")
	s.queries = append(s.queries, q)
	body := map[string]any{}
	var list []map[string]int
	for _, n := range s.seasons {
		list = append(list, map[string]int{"season_number": n})
	}
	body["seasons"] = list
	for p := range strings.SplitSeq(q, ",") {
		n, err := strconv.Atoi(strings.TrimPrefix(p, "season/"))
		if err != nil {
			s.t.Errorf("append %q", p)
			continue
		}
		if !slices.Contains(s.seasons, n) {
			continue
		}
		body[p] = map[string]any{"episodes": []map[string]any{
			{"season_number": n, "episode_number": 1, "air_date": "2020-01-01", "name": "One"},
			{"season_number": n, "episode_number": 2, "air_date": nil, "name": "Two"},
		}}
	}
	_ = json.NewEncoder(w).Encode(body)
}

// getter answers in process as source.Client does: a 404 is
// source.ErrNotFound, another non-200 an error, a body over maxBytes an
// error. The transport itself is internal/source's to test.
type getter struct{ h http.Handler }

func (g getter) Get(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody))
	switch {
	case rec.Code == http.StatusNotFound:
		return nil, source.ErrNotFound
	case rec.Code != http.StatusOK:
		return nil, fmt.Errorf("HTTP %d", rec.Code)
	case int64(rec.Body.Len()) > maxBytes:
		return nil, errors.New("body over the bound")
	}
	return rec.Body.Bytes(), nil
}

func TestFetchReadsEverySeasonInOneCall(t *testing.T) {
	s := &server{t: t, seasons: []int{0, 1, 2}}
	show, err := Fetch(t.Context(), getter{s}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.queries) != 1 {
		t.Errorf("requests = %d, want 1", len(s.queries))
	}
	want := []Episode{
		{Season: 0, Number: 1, AirDate: "2020-01-01", Title: "One"},
		{Season: 0, Number: 2, Title: "Two"},
		{Season: 1, Number: 1, AirDate: "2020-01-01", Title: "One"},
		{Season: 1, Number: 2, Title: "Two"},
		{Season: 2, Number: 1, AirDate: "2020-01-01", Title: "One"},
		{Season: 2, Number: 2, Title: "Two"},
	}
	if !slices.Equal(show.Episodes, want) {
		t.Errorf("episodes = %+v, want %+v", show.Episodes, want)
	}
}

func TestFetchChunksSeasonsPastTwenty(t *testing.T) {
	var seasons []int
	for n := range 45 {
		seasons = append(seasons, n)
	}
	s := &server{t: t, seasons: seasons}
	show, err := Fetch(t.Context(), getter{s}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.queries) != 3 {
		t.Fatalf("requests = %d, want 3 (seasons 0-19, 20-39, 40-44)", len(s.queries))
	}
	for i, q := range s.queries {
		if n := len(strings.Split(q, ",")); n > maxAppended {
			t.Errorf("request %d appends %d seasons, over %d", i, n, maxAppended)
		}
	}
	if !strings.HasPrefix(s.queries[2], "season/40,") {
		t.Errorf("third request = %q, want it to start at season 40", s.queries[2])
	}
	if len(show.Episodes) != 90 || show.Episodes[89].Season != 44 {
		t.Errorf("episodes = %d, last %+v; want 90 ending on season 44", len(show.Episodes), show.Episodes[len(show.Episodes)-1])
	}
}

func TestFetchAndMovieAnswers(t *testing.T) {
	c := getter{&server{t: t, seasons: []int{1}}}
	t.Run("a show TMDB does not have is not found", func(t *testing.T) {
		if _, err := Fetch(t.Context(), c, 2); !errors.Is(err, source.ErrNotFound) {
			t.Errorf("Fetch(2) = %v, want ErrNotFound", err)
		}
	})
	t.Run("a movie TMDB does not have is not found", func(t *testing.T) {
		if err := Movie(t.Context(), c, 8); !errors.Is(err, source.ErrNotFound) {
			t.Errorf("Movie(8) = %v, want ErrNotFound", err)
		}
	})
	t.Run("a movie TMDB has exists", func(t *testing.T) {
		if err := Movie(t.Context(), c, 7); err != nil {
			t.Errorf("Movie(7) = %v, want nil", err)
		}
	})
}

func TestFetchRefusals(t *testing.T) {
	t.Run("a body over the bound", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"seasons":[],"pad":"%s"}`, strings.Repeat("x", maxBodyBytes))
		})
		if _, err := Fetch(t.Context(), getter{h}, 1); err == nil {
			t.Error("Fetch read a body over maxBodyBytes")
		}
	})
	t.Run("a season the show lists and does not return", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"seasons":[{"season_number":1}]}`))
		})
		if _, err := Fetch(t.Context(), getter{h}, 1); err == nil {
			t.Error("Fetch returned a show missing a season it lists")
		}
	})
}
