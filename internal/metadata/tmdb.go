// Package metadata fetches show and movie information from TMDB, keeps the
// anime title and ID lists, caches responses in the database, and fills the
// catalog from them. Every call to TMDB goes through one polite limiter.
package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/db"
)

// ErrNoKey means no TMDB key is set.
var ErrNoKey = errors.New("no TMDB API key is set (Settings, Metadata)")

// TMDB is a small client for the TMDB v3 API with a response cache.
type TMDB struct {
	BaseURL string        // default https://api.themoviedb.org/3
	Key     func() string // the API key (v3) or read access token (v4, starts "eyJ")
	HTTP    *http.Client
	DB      *sql.DB       // response cache; nil disables it
	Gap     time.Duration // minimum time between requests (default 250ms)
	Now     func() time.Time

	mu   sync.Mutex
	next time.Time
}

func NewTMDB(h *sql.DB, key func() string) *TMDB {
	return &TMDB{BaseURL: "https://api.themoviedb.org/3", Key: key, HTTP: &http.Client{Timeout: 30 * time.Second}, DB: h, Gap: 250 * time.Millisecond, Now: time.Now}
}

// StatusError is a non-200 answer from TMDB.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	if e.Code == http.StatusUnauthorized {
		return "TMDB refused the API key"
	}
	return fmt.Sprintf("TMDB answered HTTP %d %s", e.Code, e.Message)
}

func (t *TMDB) wait(ctx context.Context) error {
	t.mu.Lock()
	now := time.Now()
	start := t.next
	if start.Before(now) {
		start = now
	}
	t.next = start.Add(t.Gap)
	t.mu.Unlock()
	select {
	case <-time.After(time.Until(start)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// get fetches path with query q into out. A cached answer younger than ttl is
// used instead of asking TMDB; ttl 0 always asks and refreshes the cache.
func (t *TMDB) get(ctx context.Context, path string, q url.Values, ttl time.Duration, out any) error {
	key := strings.TrimSpace(t.Key())
	if key == "" {
		return ErrNoKey
	}
	if q == nil {
		q = url.Values{}
	}
	cacheKey := path + "?" + q.Encode()
	if t.DB != nil && ttl > 0 {
		var body []byte
		var fetched string
		err := t.DB.QueryRowContext(ctx, `SELECT body, fetched FROM meta_cache WHERE key = ?`, cacheKey).Scan(&body, &fetched)
		if err == nil {
			if at, perr := time.Parse(db.TimeFormat, fetched); perr == nil && t.Now().Sub(at) < ttl {
				return json.Unmarshal(body, out)
			}
		}
	}
	if err := t.wait(ctx); err != nil {
		return err
	}
	auth := url.Values{}
	for k, v := range q {
		auth[k] = v
	}
	if !strings.HasPrefix(key, "eyJ") {
		auth.Set("api_key", key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(t.BaseURL, "/")+path+"?"+auth.Encode(), nil)
	if err != nil {
		return err
	}
	if strings.HasPrefix(key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return errors.New("TMDB is not reachable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var m struct {
			Message string `json:"status_message"`
		}
		_ = json.Unmarshal(body, &m)
		return &StatusError{Code: resp.StatusCode, Message: m.Message}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("TMDB answer for %s is unreadable: %w", path, err)
	}
	if t.DB != nil {
		_, _ = t.DB.ExecContext(ctx, `INSERT INTO meta_cache (key, body, fetched) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET body = excluded.body, fetched = excluded.fetched`, cacheKey, body, db.Stamp(t.Now()))
	}
	return nil
}

// Check confirms the key works.
func (t *TMDB) Check(ctx context.Context) error {
	var out map[string]any
	return t.get(ctx, "/configuration", nil, 0, &out)
}

type TVResult struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	OriginalName string   `json:"original_name"`
	FirstAirDate string   `json:"first_air_date"`
	GenreIDs     []int    `json:"genre_ids"`
	Countries    []string `json:"origin_country"`
}

// Animation reports whether TMDB files the show under animation.
func (r TVResult) Animation() bool {
	for _, g := range r.GenreIDs {
		if g == 16 {
			return true
		}
	}
	return false
}

func (t *TMDB) SearchTV(ctx context.Context, query string) ([]TVResult, error) {
	var r struct {
		Results []TVResult `json:"results"`
	}
	err := t.get(ctx, "/search/tv", url.Values{"query": {query}}, 6*time.Hour, &r)
	return r.Results, err
}

type SeasonSummary struct {
	Number       int    `json:"season_number"`
	EpisodeCount int    `json:"episode_count"`
	Name         string `json:"name"`
}

type EpisodeGroupSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         int    `json:"type"` // 1 original air date, 2 absolute, 3 DVD, 4 digital, 5 story arc, 6 production, 7 TV
	EpisodeCount int    `json:"episode_count"`
}

type TV struct {
	ID               int64  `json:"id"`
	OriginalLanguage string `json:"original_language"`
	Genres           []struct {
		ID int `json:"id"`
	} `json:"genres"`
	Name         string          `json:"name"`
	OriginalName string          `json:"original_name"`
	FirstAirDate string          `json:"first_air_date"`
	Status       string          `json:"status"`
	Seasons      []SeasonSummary `json:"seasons"`
	ExternalIDs  struct {
		TVDB int64  `json:"tvdb_id"`
		IMDB string `json:"imdb_id"`
	} `json:"external_ids"`
	AlternativeTitles struct {
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
	} `json:"alternative_titles"`
	EpisodeGroups struct {
		Results []EpisodeGroupSummary `json:"results"`
	} `json:"episode_groups"`
}

// Year is the year the show first aired, or 0.
func (tv TV) Year() int {
	var y int
	if len(tv.FirstAirDate) >= 4 {
		fmt.Sscanf(tv.FirstAirDate[:4], "%d", &y)
	}
	return y
}

// TV fetches a show with its external ids, alternative titles and episode groups.
// fresh skips the cache.
func (t *TMDB) TV(ctx context.Context, id int64, fresh bool) (TV, error) {
	var tv TV
	ttl := 12 * time.Hour
	if fresh {
		ttl = 0
	}
	err := t.get(ctx, fmt.Sprintf("/tv/%d", id), url.Values{"append_to_response": {"external_ids,alternative_titles,episode_groups"}}, ttl, &tv)
	return tv, err
}

type TMDBEpisode struct {
	Season  int    `json:"season_number"`
	Number  int    `json:"episode_number"`
	Name    string `json:"name"`
	AirDate string `json:"air_date"`
	Order   int    `json:"order"` // position inside an episode group
}

func (t *TMDB) Season(ctx context.Context, tvID int64, season int, fresh bool) ([]TMDBEpisode, error) {
	var s struct {
		Episodes []TMDBEpisode `json:"episodes"`
	}
	ttl := 12 * time.Hour
	if fresh {
		ttl = 0
	}
	err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tvID, season), nil, ttl, &s)
	return s.Episodes, err
}

type EpisodeGroup struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   int    `json:"type"`
	Groups []struct {
		Name     string        `json:"name"`
		Order    int           `json:"order"`
		Episodes []TMDBEpisode `json:"episodes"`
	} `json:"groups"`
}

func (t *TMDB) EpisodeGroup(ctx context.Context, id string, fresh bool) (EpisodeGroup, error) {
	var g EpisodeGroup
	ttl := 24 * time.Hour
	if fresh {
		ttl = 0
	}
	err := t.get(ctx, "/tv/episode_group/"+url.PathEscape(id), nil, ttl, &g)
	return g, err
}

// FindByTVDB returns the TMDB show for a TVDB id, or 0.
func (t *TMDB) FindByTVDB(ctx context.Context, tvdbID int64) (int64, error) {
	var r struct {
		TV []struct {
			ID int64 `json:"id"`
		} `json:"tv_results"`
	}
	if err := t.get(ctx, fmt.Sprintf("/find/%d", tvdbID), url.Values{"external_source": {"tvdb_id"}}, 30*24*time.Hour, &r); err != nil {
		return 0, err
	}
	if len(r.TV) == 0 {
		return 0, nil
	}
	return r.TV[0].ID, nil
}

// MediaResult is a movie or show from TMDB's searches and lists.
type MediaResult struct {
	ID               int64   `json:"id"`
	MediaType        string  `json:"media_type"`
	Title            string  `json:"title"`
	Name             string  `json:"name"`
	OriginalTitle    string  `json:"original_title"`
	OriginalName     string  `json:"original_name"`
	ReleaseDate      string  `json:"release_date"`
	FirstAirDate     string  `json:"first_air_date"`
	Overview         string  `json:"overview"`
	PosterPath       string  `json:"poster_path"`
	GenreIDs         []int   `json:"genre_ids"`
	OriginalLanguage string  `json:"original_language"`
	Popularity       float64 `json:"popularity"`
}

// Anime says whether TMDB has it as Japanese animation.
func (m MediaResult) Anime() bool {
	if m.OriginalLanguage != "ja" {
		return false
	}
	for _, g := range m.GenreIDs {
		if g == 16 {
			return true
		}
	}
	return false
}

// SearchMulti searches movies and shows together.
func (t *TMDB) SearchMulti(ctx context.Context, query string) ([]MediaResult, error) {
	var r struct {
		Results []MediaResult `json:"results"`
	}
	if err := t.get(ctx, "/search/multi", url.Values{"query": {query}, "include_adult": {"false"}}, time.Hour, &r); err != nil {
		return nil, err
	}
	out := r.Results[:0]
	for _, m := range r.Results {
		if m.MediaType == "movie" || m.MediaType == "tv" {
			out = append(out, m)
		}
	}
	return out, nil
}

// Trending lists this week's trending movies and shows; kind is "all",
// "movie" or "tv".
func (t *TMDB) Trending(ctx context.Context, kind string) ([]MediaResult, error) {
	var r struct {
		Results []MediaResult `json:"results"`
	}
	if err := t.get(ctx, "/trending/"+kind+"/week", nil, 3*time.Hour, &r); err != nil {
		return nil, err
	}
	for i := range r.Results {
		if r.Results[i].MediaType == "" {
			r.Results[i].MediaType = kind
		}
	}
	return r.Results, nil
}

// MovieInfo is what a movie request needs.
type MovieInfo struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	ReleaseDate      string `json:"release_date"`
	IMDB             string `json:"imdb_id"`
	OriginalLanguage string `json:"original_language"`
}

// Movie looks a movie up.
func (t *TMDB) Movie(ctx context.Context, id int64) (MovieInfo, error) {
	var m MovieInfo
	err := t.get(ctx, fmt.Sprintf("/movie/%d", id), nil, 24*time.Hour, &m)
	return m, err
}

// Anime says whether TMDB has the show as Japanese animation.
func (t TV) Anime() bool {
	if t.OriginalLanguage != "ja" {
		return false
	}
	for _, g := range t.Genres {
		if g.ID == 16 {
			return true
		}
	}
	return false
}
