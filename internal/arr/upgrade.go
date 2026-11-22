package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// send makes a request with a JSON body and reads a JSON answer (out may be nil).
func (c *Client) send(ctx context.Context, method, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+c.prefix()+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s is not reachable", c.Kind)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{Code: resp.StatusCode, Kind: c.Kind}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Profile is one of the arr's quality profiles.
type Profile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Profiles lists the arr's quality profiles.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	var out []Profile
	return out, c.get(ctx, "/qualityprofile", &out)
}

// ProfileByName finds a quality profile by name (ignoring case).
func (c *Client) ProfileByName(ctx context.Context, name string) (Profile, error) {
	ps, err := c.Profiles(ctx)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(name)) {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("%s has no quality profile named %q", c.Kind, name)
}

// Item is a series or movie the arr manages.
type Item struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	ProfileID int64  `json:"qualityProfileId"`
	Monitored bool   `json:"monitored"`
	Seasons   []struct {
		Number    int  `json:"seasonNumber"`
		Monitored bool `json:"monitored"`
	} `json:"seasons,omitempty"`
}

// SeriesByTVDB finds a Sonarr series by its TVDB id (found false when Sonarr
// does not have it).
func (c *Client) SeriesByTVDB(ctx context.Context, tvdb int64) (Item, bool, error) {
	var out []Item
	if err := c.get(ctx, fmt.Sprintf("/series?tvdbId=%d", tvdb), &out); err != nil || len(out) == 0 {
		return Item{}, false, err
	}
	return out[0], true, nil
}

// MovieByTMDB finds a Radarr movie by its TMDB id.
func (c *Client) MovieByTMDB(ctx context.Context, tmdb int64) (Item, bool, error) {
	var out []Item
	if err := c.get(ctx, fmt.Sprintf("/movie?tmdbId=%d", tmdb), &out); err != nil || len(out) == 0 {
		return Item{}, false, err
	}
	return out[0], true, nil
}

// SetProfile moves a series (Sonarr) or movie (Radarr) to a quality profile
// through the arr's own bulk editor, which changes nothing else.
func (c *Client) SetProfile(ctx context.Context, id, profileID int64) error {
	switch c.Kind {
	case "sonarr":
		return c.send(ctx, http.MethodPut, "/series/editor", map[string]any{"seriesIds": []int64{id}, "qualityProfileId": profileID}, nil)
	case "radarr":
		return c.send(ctx, http.MethodPut, "/movie/editor", map[string]any{"movieIds": []int64{id}, "qualityProfileId": profileID}, nil)
	}
	return fmt.Errorf("%s has no profiles to set", c.Kind)
}

// SearchSeason asks Sonarr to search one season of a series.
func (c *Client) SearchSeason(ctx context.Context, seriesID int64, season int) error {
	return c.send(ctx, http.MethodPost, "/command", map[string]any{"name": "SeasonSearch", "seriesId": seriesID, "seasonNumber": season}, nil)
}

// SearchSeries asks Sonarr to search a whole series.
func (c *Client) SearchSeries(ctx context.Context, seriesID int64) error {
	return c.send(ctx, http.MethodPost, "/command", map[string]any{"name": "SeriesSearch", "seriesId": seriesID}, nil)
}

// SearchMovie asks Radarr to search a movie.
func (c *Client) SearchMovie(ctx context.Context, movieID int64) error {
	return c.send(ctx, http.MethodPost, "/command", map[string]any{"name": "MoviesSearch", "movieIds": []int64{movieID}}, nil)
}

// QueuedFor counts the arr's queue entries (downloads in progress) for a series.
func (c *Client) QueuedFor(ctx context.Context, seriesID int64) (int, error) {
	var q struct {
		Records []struct {
			SeriesID int64 `json:"seriesId"`
		} `json:"records"`
	}
	if err := c.get(ctx, "/queue?page=1&pageSize=1000", &q); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range q.Records {
		if r.SeriesID == seriesID {
			n++
		}
	}
	return n, nil
}
