package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+c.prefix()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s is not reachable", c.Kind)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &StatusError{Code: resp.StatusCode, Kind: c.Kind}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// StatusError is a non-200 answer from an arr.
type StatusError struct {
	Code int
	Kind string
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s answered HTTP %d", e.Kind, e.Code) }

// Grab is what the arr's history knows about a download: which series and episode.
type Grab struct {
	SeriesID, EpisodeID int64
	// Grabbed is when the arr grabbed the release (zero if unknown).
	Grabbed time.Time
}

// Grabs lists the series and episodes a download ID was grabbed for.
func (c *Client) Grabs(ctx context.Context, downloadID string) ([]Grab, error) {
	q := url.Values{"page": {"1"}, "pageSize": {"100"}, "downloadId": {downloadID}}
	var r struct {
		Records []struct {
			SeriesID  int64  `json:"seriesId"`
			EpisodeID int64  `json:"episodeId"`
			EventType string `json:"eventType"`
			Date      string `json:"date"`
		} `json:"records"`
	}
	if err := c.get(ctx, "/history?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	type key struct{ s, e int64 }
	idx := map[key]int{}
	var out []Grab
	for _, x := range r.Records {
		if x.SeriesID == 0 {
			continue
		}
		k := key{x.SeriesID, x.EpisodeID}
		i, ok := idx[k]
		if !ok {
			out = append(out, Grab{SeriesID: x.SeriesID, EpisodeID: x.EpisodeID})
			i = len(out) - 1
			idx[k] = i
		}
		if strings.EqualFold(x.EventType, "grabbed") {
			if t := parseTime(x.Date); t != nil && (out[i].Grabbed.IsZero() || t.Before(out[i].Grabbed)) {
				out[i].Grabbed = *t
			}
		}
	}
	return out, nil
}

// SeriesInfo is the title and runtime (minutes) of a Sonarr series.
func (c *Client) SeriesInfo(ctx context.Context, id int64) (title string, runtime int, err error) {
	var s struct {
		Title   string `json:"title"`
		Runtime int    `json:"runtime"`
	}
	if err = c.get(ctx, fmt.Sprintf("/series/%d", id), &s); err != nil {
		return "", 0, err
	}
	return s.Title, s.Runtime, nil
}

// Episode is the part of a Sonarr episode the season counts need.
type Episode struct {
	ID         int64  `json:"id"`
	Season     int    `json:"seasonNumber"`
	Number     int    `json:"episodeNumber"`
	Monitored  bool   `json:"monitored"`
	HasFile    bool   `json:"hasFile"`
	AirDateUTC string `json:"airDateUtc"`
	File       *struct {
		DateAdded string `json:"dateAdded"`
		// CutoffNotMet: the file is below the series profile's cutoff
		// (for a show on the upgrade profile: not upgraded yet).
		CutoffNotMet bool `json:"qualityCutoffNotMet"`
	} `json:"episodeFile,omitempty"`
}

// FileAdded is when the episode's current file was added (zero if unknown).
func (e Episode) FileAdded() time.Time {
	if e.File != nil {
		if t := parseTime(e.File.DateAdded); t != nil {
			return *t
		}
	}
	return time.Time{}
}

func (c *Client) Episodes(ctx context.Context, seriesID int64) ([]Episode, error) {
	var eps []Episode
	err := c.get(ctx, fmt.Sprintf("/episode?seriesId=%d&includeEpisodeFile=true", seriesID), &eps)
	return eps, err
}

// Missing is one monitored item an arr wants and does not have.
type Missing struct {
	Kind   string     `json:"kind"` // episode | movie | album
	Title  string     `json:"title"`
	Detail string     `json:"detail,omitempty"` // S02E05, year, album name
	Date   *time.Time `json:"date,omitempty"`
	// Group is what the item belongs to on the Missing page: the series, the
	// movie, or the album (artist and album).
	Group string `json:"group,omitempty"`
	// Poster is the artwork address the arr knows (series or movie poster,
	// album cover); Path opens the item in the arr, relative to its address.
	Poster string `json:"poster,omitempty"`
	// Cover is the arr's own copy of the artwork (/MediaCover/...), used when
	// Poster is missing or from a host the image proxy does not fetch.
	Cover string `json:"cover,omitempty"`
	Path  string `json:"path,omitempty"`
}

// arrImage is an entry of an arr's images list.
type arrImage struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl"`
	URL       string `json:"url"`
}

// localImage chooses the arr's own copy of the first image of the wanted types.
func localImage(imgs []arrImage, types ...string) string {
	for _, t := range types {
		for _, i := range imgs {
			if strings.EqualFold(i.CoverType, t) && strings.Contains(strings.ToLower(i.URL), "/mediacover/") {
				return i.URL
			}
		}
	}
	return ""
}

// pickImage chooses the remote address of the first image of the wanted types.
func pickImage(imgs []arrImage, types ...string) string {
	for _, t := range types {
		for _, i := range imgs {
			if strings.EqualFold(i.CoverType, t) && strings.HasPrefix(i.RemoteURL, "https://") {
				return i.RemoteURL
			}
		}
	}
	return ""
}

func parseTime(s string) *time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return &t
	}
	return nil
}

// MissingItems reads the arr's wanted/missing list (monitored items only).
func (c *Client) MissingItems(ctx context.Context) ([]Missing, error) {
	switch c.Kind {
	case "sonarr":
		var r struct {
			Records []struct {
				Title      string `json:"title"`
				Season     int    `json:"seasonNumber"`
				Episode    int    `json:"episodeNumber"`
				AirDateUTC string `json:"airDateUtc"`
				Monitored  bool   `json:"monitored"`
				Series     struct {
					Title     string     `json:"title"`
					TitleSlug string     `json:"titleSlug"`
					Images    []arrImage `json:"images"`
				} `json:"series"`
			} `json:"records"`
		}
		if err := c.get(ctx, "/wanted/missing?page=1&pageSize=1000&includeSeries=true&sortKey=airDateUtc&sortDirection=descending", &r); err != nil {
			return nil, err
		}
		var out []Missing
		for _, x := range r.Records {
			if !x.Monitored {
				continue
			}
			t := x.Series.Title
			if t == "" {
				t = x.Title
			}
			m := Missing{Kind: "episode", Title: t, Detail: fmt.Sprintf("S%02dE%02d", x.Season, x.Episode), Date: parseTime(x.AirDateUTC), Group: t, Poster: pickImage(x.Series.Images, "poster"), Cover: localImage(x.Series.Images, "poster")}
			if x.Series.TitleSlug != "" {
				m.Path = "/series/" + url.PathEscape(x.Series.TitleSlug)
			}
			out = append(out, m)
		}
		return out, nil
	case "radarr":
		var r struct {
			Records []struct {
				Title          string     `json:"title"`
				Year           int        `json:"year"`
				Monitored      bool       `json:"monitored"`
				DigitalRelease string     `json:"digitalRelease"`
				InCinemas      string     `json:"inCinemas"`
				TmdbID         int64      `json:"tmdbId"`
				TitleSlug      string     `json:"titleSlug"`
				Images         []arrImage `json:"images"`
			} `json:"records"`
		}
		if err := c.get(ctx, "/wanted/missing?page=1&pageSize=1000&sortDirection=descending", &r); err != nil {
			return nil, err
		}
		var out []Missing
		for _, x := range r.Records {
			if !x.Monitored {
				continue
			}
			d := parseTime(x.DigitalRelease)
			if d == nil {
				d = parseTime(x.InCinemas)
			}
			m := Missing{Kind: "movie", Title: x.Title, Detail: fmt.Sprint(x.Year), Date: d, Group: x.Title, Poster: pickImage(x.Images, "poster"), Cover: localImage(x.Images, "poster")}
			switch {
			case x.TmdbID > 0:
				m.Path = "/movie/" + fmt.Sprint(x.TmdbID)
			case x.TitleSlug != "":
				m.Path = "/movie/" + url.PathEscape(x.TitleSlug)
			}
			out = append(out, m)
		}
		return out, nil
	case "lidarr":
		var r struct {
			Records []struct {
				Title       string     `json:"title"`
				Monitored   bool       `json:"monitored"`
				ReleaseDate string     `json:"releaseDate"`
				ForeignID   string     `json:"foreignAlbumId"`
				Images      []arrImage `json:"images"`
				Artist      struct {
					Name   string     `json:"artistName"`
					Images []arrImage `json:"images"`
				} `json:"artist"`
			} `json:"records"`
		}
		if err := c.get(ctx, "/wanted/missing?page=1&pageSize=1000&includeArtist=true&sortDirection=descending", &r); err != nil {
			return nil, err
		}
		var out []Missing
		for _, x := range r.Records {
			if !x.Monitored {
				continue
			}
			m := Missing{Kind: "album", Title: x.Artist.Name, Detail: x.Title, Date: parseTime(x.ReleaseDate), Group: x.Artist.Name + " \u2014 " + x.Title,
				Poster: pickImage(x.Images, "cover", "disc"), Cover: localImage(x.Images, "cover", "disc")}
			if m.Poster == "" && m.Cover == "" {
				m.Poster = pickImage(x.Artist.Images, "poster", "fanart")
				m.Cover = localImage(x.Artist.Images, "poster", "fanart")
			}
			if x.ForeignID != "" {
				m.Path = "/album/" + url.PathEscape(x.ForeignID)
			}
			out = append(out, m)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s has no missing list", c.Kind)
}

// Import is one recent import event, named for display.
type Import struct {
	Kind   string    `json:"kind"` // episode | movie | album
	Title  string    `json:"title"`
	Date   time.Time `json:"date"`
	Detail string    `json:"detail,omitempty"` // "S01E02 - Episode title" for episodes
	Year   int       `json:"year,omitempty"`
	Poster string    `json:"poster,omitempty"` // /MediaCover/<id>/poster.jpg on the arr
	Arr    string    `json:"arr,omitempty"`    // category name, filled in by the caller
}

// RecentImports reads the newest import events from the arr's history.
func (c *Client) RecentImports(ctx context.Context) ([]Import, error) {
	var r struct {
		Records []struct {
			EventType string    `json:"eventType"`
			SeriesID  int64     `json:"seriesId"`
			MovieID   int64     `json:"movieId"`
			AlbumID   int64     `json:"albumId"`
			Date      time.Time `json:"date"`
			Series    struct {
				Title string `json:"title"`
			} `json:"series"`
			Episode struct {
				Title  string `json:"title"`
				Season int    `json:"seasonNumber"`
				Number int    `json:"episodeNumber"`
			} `json:"episode"`
			Movie struct {
				Title string `json:"title"`
				Year  int    `json:"year"`
			} `json:"movie"`
			Artist struct {
				Name string `json:"artistName"`
			} `json:"artist"`
			Album struct {
				Title string `json:"title"`
			} `json:"album"`
		} `json:"records"`
	}
	q := "/history?page=1&pageSize=100&sortKey=date&sortDirection=descending&includeSeries=true&includeEpisode=true&includeMovie=true&includeArtist=true&includeAlbum=true"
	if err := c.get(ctx, q, &r); err != nil {
		return nil, err
	}
	var out []Import
	for _, x := range r.Records {
		if !strings.Contains(strings.ToLower(x.EventType), "imported") {
			continue
		}
		switch c.Kind {
		case "sonarr":
			d := fmt.Sprintf("S%02dE%02d", x.Episode.Season, x.Episode.Number)
			if x.Episode.Title != "" {
				d += " - " + x.Episode.Title
			}
			out = append(out, Import{Kind: "episode", Title: x.Series.Title, Date: x.Date, Detail: d, Poster: cover(x.SeriesID)})
		case "radarr":
			out = append(out, Import{Kind: "movie", Title: x.Movie.Title, Date: x.Date, Year: x.Movie.Year, Poster: cover(x.MovieID)})
		case "lidarr":
			t := x.Artist.Name
			if x.Album.Title != "" {
				t += " — " + x.Album.Title
			}
			out = append(out, Import{Kind: "album", Title: t, Date: x.Date, Poster: albumCover(x.AlbumID)})
		}
	}
	return out, nil
}

func cover(id int64) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("/MediaCover/%d/poster.jpg", id)
}

func albumCover(id int64) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("/MediaCover/Albums/%d/cover.jpg", id)
}

// GrabbedResolution is the vertical resolution (480, 720, 1080, 2160) of the quality the
// arr grabbed a download as, or 0 when the arr does not say (music, unknown quality, no
// history for the download).
func (c *Client) GrabbedResolution(ctx context.Context, downloadID string) (int, error) {
	if c.Kind != "sonarr" && c.Kind != "radarr" {
		return 0, nil
	}
	q := url.Values{"page": {"1"}, "pageSize": {"100"}, "downloadId": {downloadID}}
	var r struct {
		Records []struct {
			EventType string `json:"eventType"`
			Quality   struct {
				Quality struct {
					Resolution int `json:"resolution"`
				} `json:"quality"`
			} `json:"quality"`
		} `json:"records"`
	}
	if err := c.get(ctx, "/history?"+q.Encode(), &r); err != nil {
		return 0, err
	}
	best := 0
	for _, x := range r.Records {
		if x.EventType == "grabbed" && x.Quality.Quality.Resolution > best {
			best = x.Quality.Quality.Resolution
		}
	}
	return best, nil
}
