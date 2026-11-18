package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/z354prance/undermynd/internal/shows"
)

func (s *Server) showsReady(w http.ResponseWriter) bool {
	if s.Shows == nil {
		jsonErr(w, http.StatusConflict, "the Anime page needs the database (UNDERMYND_STORE is set to json)")
		return false
	}
	return true
}

func (s *Server) showsList(w http.ResponseWriter, r *http.Request) {
	if !s.showsReady(w) {
		return
	}
	list, err := s.Shows.List()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []shows.Show{}
	}
	writeJSON(w, map[string]any{"shows": list, "roots": s.Shows.Roots(), "scan": s.Shows.Scanning()})
}

type showEpisode struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	File    string `json:"file,omitempty"`
	Res     string `json:"res,omitempty"`
	Group   string `json:"group,omitempty"`
	Size    int64  `json:"size,omitempty"`
	State   string `json:"state"` // have, gap, grabbed, upcoming
}

func (s *Server) showsOne(w http.ResponseWriter, r *http.Request) {
	if !s.showsReady(w) {
		return
	}
	folder := r.URL.Query().Get("folder")
	sh, files, err := s.Shows.Get(folder)
	if err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	// AniList status is looked up on first view, then daily for followed shows.
	if sh.AniListChecked.IsZero() {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		if a, err := s.Shows.RefreshAniList(ctx, folder); err == nil {
			sh.AniList = a
		}
		cancel()
	}
	seen, _ := s.Shows.Store.SeenFor(folder)
	grabbed := map[[2]int]shows.Seen{}
	newest := map[int]int{}
	for _, x := range seen {
		season := 1
		if x.Marked {
			season = x.Season
		}
		if !x.Grabbed.IsZero() {
			grabbed[[2]int{season, x.Episode}] = x
		}
		if x.Episode > newest[season] {
			newest[season] = x.Episode
		}
	}
	var eps []showEpisode
	for _, f := range files {
		if !f.OK {
			continue
		}
		eps = append(eps, showEpisode{Season: f.Season, Episode: f.Episode, File: f.Rel, Res: f.Res, Size: f.Size, State: "have"})
	}
	for _, ss := range sh.Summary.Seasons {
		for _, g := range ss.Gaps {
			st := "gap"
			if _, ok := grabbed[[2]int{ss.Season, g}]; ok {
				st = "grabbed"
			}
			eps = append(eps, showEpisode{Season: ss.Season, Episode: g, State: st})
		}
	}
	// Episodes the feeds offered past the newest on disk: grabbed, or still to come.
	last := sh.Summary.Latest
	for k, x := range grabbed {
		if k[1] > last && (k[0] == sh.Summary.Season || k[0] == 1) {
			eps = append(eps, showEpisode{Season: max(k[0], sh.Summary.Season), Episode: k[1], Group: x.Group, Res: x.Res, State: "grabbed"})
		}
	}
	if eps == nil {
		eps = []showEpisode{}
	}
	if seen == nil {
		seen = []shows.Seen{}
	}
	if len(seen) > 60 {
		seen = seen[:60]
	}
	defaults := s.Shows.DefaultGroups(sh)
	writeJSON(w, map[string]any{"show": sh, "episodes": eps, "seen": seen, "defaultGroups": defaults, "searchUrl": s.Shows.Nyaa.SearchURL(sh.Name)})
}

func (s *Server) showsSave(w http.ResponseWriter, r *http.Request) {
	if !s.showsReady(w) {
		return
	}
	var in struct {
		Folder    string `json:"folder"`
		FromStart bool   `json:"fromStart"`
		shows.Settings
	}
	if !readJSON(w, r, &in) {
		return
	}
	sh, err := s.Shows.Save(in.Folder, in.Settings, in.FromStart)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, sh)
}

func (s *Server) showsAction(w http.ResponseWriter, r *http.Request) {
	if !s.showsReady(w) {
		return
	}
	var in struct {
		Folder    string `json:"folder"`
		Title     string `json:"title"`
		Root      string `json:"root"`
		Name      string `json:"name"`
		FromStart bool   `json:"fromStart"`
		shows.Settings
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	switch r.PathValue("action") {
	case "check":
		started, err := s.Shows.Check(ctx, in.Folder)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]any{"started": nonNil(started)})
	case "gaps":
		started, err := s.Shows.SearchGaps(ctx, in.Folder)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]any{"started": nonNil(started)})
	case "grab":
		if err := s.Shows.Grab(ctx, in.Folder, in.Title); err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "anilist":
		a, err := s.Shows.RefreshAniList(ctx, in.Folder)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]any{"anilist": a})
	case "add":
		sh, started, err := s.Shows.Add(ctx, in.Root, in.Name, in.Settings, in.FromStart)
		if err != nil && sh.Folder == "" {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		writeJSON(w, map[string]any{"show": sh, "started": nonNil(started), "warning": msg})
	case "rescan":
		s.Shows.StartScan()
		writeJSON(w, s.Shows.Scanning())
	default:
		jsonErr(w, http.StatusNotFound, "unknown action")
	}
}

func nonNil(x []string) []string {
	if x == nil {
		return []string{}
	}
	return x
}

func (s *Server) showsSearch(w http.ResponseWriter, r *http.Request) {
	if !s.showsReady(w) {
		return
	}
	ep, _ := strconv.Atoi(r.URL.Query().Get("episode"))
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.Shows.Search(ctx, r.URL.Query().Get("folder"), r.URL.Query().Get("q"), ep)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if res == nil {
		res = []shows.Result{}
	}
	writeJSON(w, res)
}

func (s *Server) showsDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.showsReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.Shows.Discover(ctx, r.URL.Query().Get("q"))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"results": res, "roots": s.Shows.Roots()})
}
