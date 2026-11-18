package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// send makes a request with a JSON body.
func (c *Client) send(ctx context.Context, method, path string, in, out any) error {
	b, err := json.Marshal(in)
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
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The arrs explain refusals as a list of {errorMessage}.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var msgs []struct {
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal(body, &msgs) == nil && len(msgs) > 0 && msgs[0].ErrorMessage != "" {
			return fmt.Errorf("%s: %s", c.Kind, msgs[0].ErrorMessage)
		}
		return &StatusError{Code: resp.StatusCode, Kind: c.Kind}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Get fetches any API path (without the /api/vN prefix) into out.
func (c *Client) Get(ctx context.Context, path string, out any) error { return c.get(ctx, path, out) }

// RootFolder is where the arr keeps a library.
type RootFolder struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

// RootFolders lists the arr's library folders.
func (c *Client) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var out []RootFolder
	return out, c.get(ctx, "/rootfolder", &out)
}

// Profile is a quality profile.
type Profile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// QualityProfiles lists the arr's quality profiles.
func (c *Client) QualityProfiles(ctx context.Context) ([]Profile, error) {
	var out []Profile
	return out, c.get(ctx, "/qualityprofile", &out)
}

// Have is one movie or series in an arr's library.
type Have struct {
	TMDBID    int64 `json:"tmdbId"`
	TVDBID    int64 `json:"tvdbId"`
	Monitored bool  `json:"monitored"`
	HasFile   bool  `json:"hasFile"` // Radarr
	Stats     struct {
		EpisodeFileCount int     `json:"episodeFileCount"`
		EpisodeCount     int     `json:"episodeCount"`
		Percent          float64 `json:"percentOfEpisodes"`
	} `json:"statistics"` // Sonarr
}

// Library lists Radarr's movies or Sonarr's series.
func (c *Client) Library(ctx context.Context) ([]Have, error) {
	path := "/movie"
	if c.Kind == "sonarr" {
		path = "/series"
	}
	var out []Have
	return out, c.get(ctx, path, &out)
}

// Choice is where an added item goes and how it is grabbed: a root folder and
// a quality profile, or the arr's first of each.
type Choice struct {
	RootFolder string
	ProfileID  int64
}

func (c *Client) choose(ctx context.Context, ch Choice) (Choice, error) {
	if ch.RootFolder == "" {
		roots, err := c.RootFolders(ctx)
		if err != nil {
			return ch, err
		}
		if len(roots) == 0 {
			return ch, fmt.Errorf("%s has no root folder", c.Kind)
		}
		ch.RootFolder = roots[0].Path
	}
	if ch.ProfileID == 0 {
		profiles, err := c.QualityProfiles(ctx)
		if err != nil {
			return ch, err
		}
		if len(profiles) == 0 {
			return ch, fmt.Errorf("%s has no quality profile", c.Kind)
		}
		ch.ProfileID = profiles[0].ID
	}
	return ch, nil
}

// AddMovie adds a movie to Radarr, monitored, and starts a search for it.
func (c *Client) AddMovie(ctx context.Context, tmdbID int64, ch Choice) (string, error) {
	var found []map[string]any
	if err := c.get(ctx, fmt.Sprintf("/movie/lookup/tmdb?tmdbId=%d", tmdbID), &found); err != nil {
		var one map[string]any
		if err2 := c.get(ctx, fmt.Sprintf("/movie/lookup/tmdb?tmdbId=%d", tmdbID), &one); err2 != nil {
			return "", err
		}
		found = []map[string]any{one}
	}
	if len(found) == 0 {
		return "", errors.New("Radarr could not find the movie")
	}
	ch, err := c.choose(ctx, ch)
	if err != nil {
		return "", err
	}
	m := found[0]
	m["qualityProfileId"], m["rootFolderPath"], m["monitored"] = ch.ProfileID, ch.RootFolder, true
	m["addOptions"] = map[string]any{"searchForMovie": true}
	title, _ := m["title"].(string)
	if err := c.send(ctx, http.MethodPost, "/movie", m, nil); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			return title, nil
		}
		return "", err
	}
	return title, nil
}

// AddSeries adds a series to Sonarr by its TVDB id, every season monitored,
// and starts a search for the missing episodes.
func (c *Client) AddSeries(ctx context.Context, tvdbID int64, ch Choice) (string, error) {
	var found []map[string]any
	if err := c.get(ctx, fmt.Sprintf("/series/lookup?term=tvdb:%d", tvdbID), &found); err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", errors.New("Sonarr could not find the series")
	}
	ch, err := c.choose(ctx, ch)
	if err != nil {
		return "", err
	}
	s := found[0]
	s["qualityProfileId"], s["rootFolderPath"], s["monitored"], s["seasonFolder"] = ch.ProfileID, ch.RootFolder, true, true
	s["addOptions"] = map[string]any{"searchForMissingEpisodes": true, "monitor": "all"}
	if seasons, ok := s["seasons"].([]any); ok {
		for _, x := range seasons {
			if m, ok := x.(map[string]any); ok {
				m["monitored"] = true
			}
		}
	}
	title, _ := s["title"].(string)
	if err := c.send(ctx, http.MethodPost, "/series", s, nil); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			return title, nil
		}
		return "", err
	}
	return title, nil
}

// Command asks the arr to run a command, such as a search.
func (c *Client) Command(ctx context.Context, body map[string]any) error {
	return c.send(ctx, http.MethodPost, "/command", body, nil)
}
