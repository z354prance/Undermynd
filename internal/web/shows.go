package web

import (
	"context"
	"encoding/json"
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
	writeJSON(w, map[string]any{"shows": list, "roots": s.Shows.Roots(), "scan": s.Shows.Scanning(), "active": s.Shows.ActiveCounts(list)})
}

type showEpisode struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	File    string `json:"file,omitempty"`
	Res     string `json:"res,omitempty"`
	Group   string `json:"group,omitempty"`
	Size    int64  `json:"size,omitempty"`
	State   string `json:"state"`         // have, gap, processing
	Job     string `json:"job,omitempty"` // the job's state while processing
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
	active, _ := s.Shows.Activity(sh)
	// working says what a job is doing for an episode, "" when nothing is.
	working := func(season, episode int) string {
		for _, a := range active {
			if a.Episode == episode && (a.Season == 0 || a.Season == season) {
				return a.State
			}
		}
		return ""
	}
	var eps []showEpisode
	have := map[[2]int]bool{}
	for _, f := range files {
		if !f.OK {
			continue
		}
		have[[2]int{f.Season, f.Episode}] = true
		e := showEpisode{Season: f.Season, Episode: f.Episode, File: f.Rel, Res: f.Res, Size: f.Size, State: "have"}
		if w := working(f.Season, f.Episode); w != "" {
			e.State, e.Job = "processing", w // a new copy is on its way
		}
		eps = append(eps, e)
	}
	for _, ss := range sh.Summary.Seasons {
		for _, g := range ss.Gaps {
			e := showEpisode{Season: ss.Season, Episode: g, State: "gap"}
			if w := working(ss.Season, g); w != "" {
				e.State, e.Job = "processing", w
			}
			have[[2]int{ss.Season, g}] = true
			eps = append(eps, e)
		}
	}
	// Episodes past the newest on disk that a job is working on.
	newest := sh.Summary.Season
	if newest == 0 {
		newest = 1
	}
	for _, a := range active {
		season := a.Season
		if season == 0 {
			season = newest
		}
		if !have[[2]int{season, a.Episode}] {
			have[[2]int{season, a.Episode}] = true
			eps = append(eps, showEpisode{Season: season, Episode: a.Episode, State: "processing", Job: a.State, File: a.Name})
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

// showsTidy is one folder's naming plan (?folder=).
func (s *Server) showsTidy(w http.ResponseWriter, r *http.Request) {
	if s.Shows == nil {
		jsonErr(w, http.StatusServiceUnavailable, "the anime page is not ready")
		return
	}
	p, err := s.Shows.Tidy(r.URL.Query().Get("folder"))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, p)
}

// showsTidyApply renames the chosen files of a folder's plan.
func (s *Server) showsTidyApply(w http.ResponseWriter, r *http.Request) {
	if s.Shows == nil {
		jsonErr(w, http.StatusServiceUnavailable, "the anime page is not ready")
		return
	}
	var in struct {
		Folder  string         `json:"folder"`
		Renames []shows.Rename `json:"renames"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	n, skipped, err := s.Shows.ApplyTidy(in.Folder, in.Renames)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"renamed": n, "skipped": skipped})
}

// showsNaming lists the folders that do not fit their own naming.
func (s *Server) showsNaming(w http.ResponseWriter, r *http.Request) {
	if s.Shows == nil {
		jsonErr(w, http.StatusServiceUnavailable, "the anime page is not ready")
		return
	}
	list, at, err := s.Shows.TidyList()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"folders": list, "checked": at})
}
