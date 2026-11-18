package arr

import (
	"context"
	"fmt"
	"net/url"
	"sort"
)

// Sample is one grab from an arr's history: the release name and what the arr
// decided about it.
type Sample struct {
	Title    string
	Quality  string
	Season   int // -1 for movies
	Episodes []int
	Absolute []int
	Year     int
	Series   string
}

type historyPage struct {
	TotalRecords int `json:"totalRecords"`
	Records      []struct {
		SourceTitle string `json:"sourceTitle"`
		DownloadID  string `json:"downloadId"`
		Quality     struct {
			Quality struct {
				Name string `json:"name"`
			} `json:"quality"`
		} `json:"quality"`
		Episode *struct {
			SeasonNumber          int `json:"seasonNumber"`
			EpisodeNumber         int `json:"episodeNumber"`
			AbsoluteEpisodeNumber int `json:"absoluteEpisodeNumber"`
		} `json:"episode"`
		Series *struct {
			Title string `json:"title"`
		} `json:"series"`
		Movie *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
		} `json:"movie"`
	} `json:"records"`
}

// Samples reads up to max grabbed releases from the arr's history, newest
// first. Sonarr gives one record per episode of a grab; those are joined.
func (c *Client) Samples(ctx context.Context, max int) ([]Sample, error) {
	if c.Kind != "sonarr" && c.Kind != "radarr" {
		return nil, fmt.Errorf("only Sonarr and Radarr keep comparable history")
	}
	type key struct{ id, title string }
	byKey := map[key]*Sample{}
	var order []key
	for page := 1; len(order) < max && page <= 100; page++ {
		q := url.Values{}
		q.Set("page", fmt.Sprint(page))
		q.Set("pageSize", "250")
		q.Set("sortKey", "date")
		q.Set("sortDirection", "descending")
		q.Set("eventType", "1") // grabbed
		if c.Kind == "sonarr" {
			q.Set("includeEpisode", "true")
			q.Set("includeSeries", "true")
		} else {
			q.Set("includeMovie", "true")
		}
		var h historyPage
		if err := c.get(ctx, "/history?"+q.Encode(), &h); err != nil {
			return nil, err
		}
		for _, r := range h.Records {
			if r.SourceTitle == "" {
				continue
			}
			k := key{r.DownloadID, r.SourceTitle}
			s := byKey[k]
			if s == nil {
				if len(order) >= max {
					continue
				}
				s = &Sample{Title: r.SourceTitle, Quality: r.Quality.Quality.Name, Season: -1}
				byKey[k] = s
				order = append(order, k)
			}
			if r.Episode != nil {
				s.Season = r.Episode.SeasonNumber
				s.Episodes = append(s.Episodes, r.Episode.EpisodeNumber)
				if r.Episode.AbsoluteEpisodeNumber > 0 {
					s.Absolute = append(s.Absolute, r.Episode.AbsoluteEpisodeNumber)
				}
			}
			if r.Series != nil {
				s.Series = r.Series.Title
			}
			if r.Movie != nil {
				s.Series, s.Year = r.Movie.Title, r.Movie.Year
			}
		}
		if len(h.Records) < 250 || page*250 >= h.TotalRecords {
			break
		}
	}
	out := make([]Sample, 0, len(order))
	for _, k := range order {
		s := byKey[k]
		sort.Ints(s.Episodes)
		sort.Ints(s.Absolute)
		out = append(out, *s)
	}
	return out, nil
}
