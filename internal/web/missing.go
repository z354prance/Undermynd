package web

import (
	"net/http"
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
