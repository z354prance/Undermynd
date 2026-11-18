package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// missingItem is one episode, movie or album an arr wants.
type missingItem struct {
	Detail string     `json:"detail"`
	Date   *time.Time `json:"date,omitempty"`
}

// missingGroup is one poster on the Missing page: a series with its missing
// episodes, a movie, or an album.
type missingGroup struct {
	Title  string        `json:"title"`
	Sub    string        `json:"sub,omitempty"` // the album's name, the movie's year
	Kind   string        `json:"kind"`          // episode | movie | album
	Poster string        `json:"poster,omitempty"`
	Link   string        `json:"link,omitempty"` // opens it in the arr
	Items  []missingItem `json:"items"`
	Newest *time.Time    `json:"newest,omitempty"`
}

// missingArr is one arr category's missing list.
type missingArr struct {
	Category string         `json:"category"`
	Kind     string         `json:"kind"` // sonarr | radarr | lidarr
	URL      string         `json:"url,omitempty"`
	Groups   []missingGroup `json:"groups"`
	Count    int            `json:"count"`
}

// adminMissing is the Missing page: what each arr reports as wanted and not
// on disk, grouped per arr category, then per series, movie or album, with
// artwork from the arr (through the image proxy).
func (s *Server) adminMissing(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"arrs": []missingArr{}, "errors": []string{}}
	if s.Progress == nil {
		writeJSON(w, out)
		return
	}
	snap := s.Progress.Snapshot()
	byCat := map[string]*missingArr{}
	var order []string
	for _, c := range s.Cfg.Get().Categories {
		if c.Kind == "sonarr" || c.Kind == "radarr" || c.Kind == "lidarr" {
			byCat[c.Name] = &missingArr{Category: c.Name, Kind: c.Kind, URL: strings.TrimRight(c.ArrURL, "/"), Groups: []missingGroup{}}
			order = append(order, c.Name)
		}
	}
	idx := map[string]map[string]int{}
	for _, m := range snap.Missing {
		a := byCat[m.Category]
		if a == nil {
			continue
		}
		if idx[m.Category] == nil {
			idx[m.Category] = map[string]int{}
		}
		key := m.Group
		if key == "" {
			key = m.Title + "\x00" + m.Detail
		}
		i, ok := idx[m.Category][key]
		if !ok {
			g := missingGroup{Title: m.Title, Kind: m.Kind, Poster: imgURL(m.Poster), Items: []missingItem{}}
			if m.Cover != "" {
				// The arr's own copy first: it has one whenever it shows art.
				g.Poster = "/admin/api/missing/art?" + url.Values{"c": {m.Category}, "p": {m.Cover}}.Encode()
			}
			if m.Kind != "episode" {
				g.Sub = m.Detail
			}
			if m.Path != "" && a.URL != "" {
				g.Link = a.URL + m.Path
			}
			a.Groups = append(a.Groups, g)
			i = len(a.Groups) - 1
			idx[m.Category][key] = i
		}
		g := &a.Groups[i]
		if m.Kind == "episode" {
			g.Items = append(g.Items, missingItem{Detail: m.Detail, Date: m.Date})
		}
		if m.Date != nil && (g.Newest == nil || m.Date.After(*g.Newest)) {
			g.Newest = m.Date
		}
		a.Count++
	}
	arrs := []missingArr{}
	for _, name := range order {
		a := byCat[name]
		for gi := range a.Groups {
			sort.Slice(a.Groups[gi].Items, func(i, j int) bool { return a.Groups[gi].Items[i].Detail < a.Groups[gi].Items[j].Detail })
		}
		// Most recently aired or released first; undated last.
		sort.SliceStable(a.Groups, func(i, j int) bool {
			x, y := a.Groups[i].Newest, a.Groups[j].Newest
			if (x == nil) != (y == nil) {
				return x != nil
			}
			return x != nil && x.After(*y)
		})
		arrs = append(arrs, *a)
	}
	out["arrs"] = arrs
	out["errors"] = snap.Errors
	out["updatedAt"] = snap.UpdatedAt
	writeJSON(w, out)
}

var arrImageClient = &http.Client{
	Timeout:       20 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// adminMissingArt serves an arr's own copy of a poster or cover
// (/MediaCover/...), asked for with the category's API key and kept in the
// image cache.
func (s *Server) adminMissingArt(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.Cfg.Get().Category(r.URL.Query().Get("c"))
	p := r.URL.Query().Get("p")
	if !ok || !cat.IsArr() || cat.ArrURL == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || !strings.Contains(strings.ToLower(p), "/mediacover/") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	base, err := url.Parse(strings.TrimRight(cat.ArrURL, "/"))
	if err != nil || base.Host == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// The arr's image paths already carry its URL base (/sonarr/MediaCover/...).
	full := base.Scheme + "://" + base.Host + p
	if base.Path != "" && !strings.HasPrefix(p, base.Path+"/") {
		full = base.Scheme + "://" + base.Host + base.Path + p
	}
	s.serveCachedImage(w, r, "arr:"+cat.Name+":"+p, func(ctx context.Context) ([]byte, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return nil, "", err
		}
		key := string(cat.ArrAPIKey)
		if key == "" {
			return nil, "", errors.New("no api key")
		}
		req.Header.Set("X-Api-Key", key)
		return readImage(arrImageClient, req)
	})
}
