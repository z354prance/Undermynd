package arr

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"
)

// GrabRecord is one grab from an arr's history, with the movie or series it
// was for. Sonarr's per-episode records of one grab are joined.
type GrabRecord struct {
	Key      string // download ID and title
	Title    string
	Quality  string
	Indexer  string
	Size     int64
	Date     time.Time
	Season   int
	Episodes []int
	Absolute []int

	ItemTitle string
	Year      int
	TMDB      int64
	IMDB      string
	TVDB      int64
	Runtime   int
	ProfileID int
}

type grabPage struct {
	Records []struct {
		SourceTitle string `json:"sourceTitle"`
		DownloadID  string `json:"downloadId"`
		Date        string `json:"date"`
		Quality     struct {
			Quality struct {
				Name string `json:"name"`
			} `json:"quality"`
		} `json:"quality"`
		Data struct {
			Indexer string `json:"indexer"`
			Size    string `json:"size"`
		} `json:"data"`
		Episode *struct {
			SeasonNumber          int `json:"seasonNumber"`
			EpisodeNumber         int `json:"episodeNumber"`
			AbsoluteEpisodeNumber int `json:"absoluteEpisodeNumber"`
		} `json:"episode"`
		Series *struct {
			Title            string `json:"title"`
			TVDB             int64  `json:"tvdbId"`
			IMDB             string `json:"imdbId"`
			Runtime          int    `json:"runtime"`
			QualityProfileID int    `json:"qualityProfileId"`
			SeriesType       string `json:"seriesType"`
		} `json:"series"`
		Movie *struct {
			Title            string `json:"title"`
			Year             int    `json:"year"`
			TMDB             int64  `json:"tmdbId"`
			IMDB             string `json:"imdbId"`
			Runtime          int    `json:"runtime"`
			QualityProfileID int    `json:"qualityProfileId"`
		} `json:"movie"`
	} `json:"records"`
}

// RecentGrabs returns the arr's latest grabs, newest first.
func (c *Client) RecentGrabs(ctx context.Context, n int) ([]GrabRecord, error) {
	if c.Kind != "sonarr" && c.Kind != "radarr" {
		return nil, fmt.Errorf("only Sonarr and Radarr")
	}
	q := url.Values{}
	q.Set("page", "1")
	q.Set("pageSize", fmt.Sprint(n*3)) // Sonarr writes one record per episode
	q.Set("sortKey", "date")
	q.Set("sortDirection", "descending")
	q.Set("eventType", "1")
	if c.Kind == "sonarr" {
		q.Set("includeEpisode", "true")
		q.Set("includeSeries", "true")
	} else {
		q.Set("includeMovie", "true")
	}
	var h grabPage
	if err := c.get(ctx, "/history?"+q.Encode(), &h); err != nil {
		return nil, err
	}
	by := map[string]*GrabRecord{}
	var order []string
	for _, r := range h.Records {
		k := r.DownloadID + "|" + r.SourceTitle
		g := by[k]
		if g == nil {
			if len(order) >= n {
				continue
			}
			g = &GrabRecord{Key: k, Title: r.SourceTitle, Quality: r.Quality.Quality.Name, Indexer: r.Data.Indexer, Season: -1}
			fmt.Sscan(r.Data.Size, &g.Size)
			g.Date, _ = time.Parse(time.RFC3339, r.Date)
			by[k] = g
			order = append(order, k)
		}
		if r.Episode != nil {
			g.Season = r.Episode.SeasonNumber
			g.Episodes = append(g.Episodes, r.Episode.EpisodeNumber)
			if r.Episode.AbsoluteEpisodeNumber > 0 {
				g.Absolute = append(g.Absolute, r.Episode.AbsoluteEpisodeNumber)
			}
		}
		if s := r.Series; s != nil {
			g.ItemTitle, g.TVDB, g.IMDB, g.Runtime, g.ProfileID = s.Title, s.TVDB, s.IMDB, s.Runtime, s.QualityProfileID
		}
		if m := r.Movie; m != nil {
			g.ItemTitle, g.Year, g.TMDB, g.IMDB, g.Runtime, g.ProfileID = m.Title, m.Year, m.TMDB, m.IMDB, m.Runtime, m.QualityProfileID
		}
	}
	out := make([]GrabRecord, 0, len(order))
	for _, k := range order {
		g := by[k]
		sort.Ints(g.Episodes)
		sort.Ints(g.Absolute)
		out = append(out, *g)
	}
	return out, nil
}

// ProfileNames maps the arr's quality profile IDs to their names.
func (c *Client) ProfileNames(ctx context.Context) (map[int]string, error) {
	var ps []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := c.get(ctx, "/qualityprofile", &ps); err != nil {
		return nil, err
	}
	m := map[int]string{}
	for _, p := range ps {
		m[p.ID] = p.Name
	}
	return m, nil
}
