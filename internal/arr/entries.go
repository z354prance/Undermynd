package arr

import (
	"context"
	"strings"
)

// Entry is one movie (Radarr) or series (Sonarr) with its folder, and for a
// movie what its file was made from, as the arr recorded it.
type Entry struct {
	TMDB   int64
	TVDB   int64
	Title  string
	Year   int
	Path   string // the folder, as the arr sees it
	Anime  bool   // Sonarr series type anime, or the Anime genre
	Source string // the movie file's source: webdl, webrip, bluray, tv, dvd, …
	Remux  bool   // Radarr's quality modifier says remux
	Scene  string // the release name the file came from, when known
	Group  string // its release group
}

// Entries lists every movie (Radarr) or series (Sonarr) the arr has.
func (c *Client) Entries(ctx context.Context) ([]Entry, error) {
	switch c.Kind {
	case "radarr":
		var ms []struct {
			TMDB   int64    `json:"tmdbId"`
			Title  string   `json:"title"`
			Year   int      `json:"year"`
			Path   string   `json:"path"`
			Genres []string `json:"genres"`
			File   *struct {
				RelativePath string `json:"relativePath"`
				SceneName    string `json:"sceneName"`
				ReleaseGroup string `json:"releaseGroup"`
				Quality      struct {
					Quality struct {
						Source   string `json:"source"`
						Modifier string `json:"modifier"`
					} `json:"quality"`
				} `json:"quality"`
			} `json:"movieFile"`
		}
		if err := c.get(ctx, "/movie", &ms); err != nil {
			return nil, err
		}
		out := make([]Entry, 0, len(ms))
		for _, m := range ms {
			e := Entry{TMDB: m.TMDB, Title: m.Title, Year: m.Year, Path: m.Path}
			if f := m.File; f != nil {
				e.Source = strings.ToLower(f.Quality.Quality.Source)
				e.Remux = strings.EqualFold(f.Quality.Quality.Modifier, "remux")
				e.Scene, e.Group = f.SceneName, f.ReleaseGroup
				if e.Scene == "" {
					e.Scene = f.RelativePath
				}
			}
			out = append(out, e)
		}
		return out, nil
	case "sonarr":
		var ss []struct {
			TVDB       int64    `json:"tvdbId"`
			Title      string   `json:"title"`
			Year       int      `json:"year"`
			Path       string   `json:"path"`
			SeriesType string   `json:"seriesType"`
			Genres     []string `json:"genres"`
		}
		if err := c.get(ctx, "/series", &ss); err != nil {
			return nil, err
		}
		out := make([]Entry, 0, len(ss))
		for _, s := range ss {
			anime := strings.EqualFold(s.SeriesType, "anime")
			for _, g := range s.Genres {
				if strings.EqualFold(g, "anime") {
					anime = true
				}
			}
			out = append(out, Entry{TVDB: s.TVDB, Title: s.Title, Year: s.Year, Path: s.Path, Anime: anime})
		}
		return out, nil
	}
	return nil, nil
}
