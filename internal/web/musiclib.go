package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/music"
)

func (s *Server) musicLibReady(w http.ResponseWriter) bool {
	if s.Music == nil {
		jsonErr(w, http.StatusConflict, "music needs the database")
		return false
	}
	return true
}

func (s *Server) musicList(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	list, err := s.Music.Artists(r.URL.Query().Get("category"))
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type cat struct {
		Name       string `json:"name"`
		Native     bool   `json:"native"`
		LibraryDir string `json:"libraryDir"`
	}
	st := s.Cfg.Get()
	var cats []cat
	for _, c := range st.Categories {
		if c.Kind == "lidarr" {
			cats = append(cats, cat{c.Name, c.Native, c.LibraryDir})
		}
	}
	writeJSON(w, map[string]any{"artists": list, "categories": cats, "settings": st.Music, "qualities": music.Qualities})
}

func (s *Server) musicArtist(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	a, als, err := s.Music.Artist(movieID(r))
	if err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, map[string]any{"artist": a, "albums": als})
}

func (s *Server) musicArtistEdit(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	var in struct {
		Monitored bool `json:"monitored"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := s.Music.MonitorArtist(movieID(r), in.Monitored); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) musicArtistDelete(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	if err := s.Music.RemoveArtist(movieID(r)); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) musicAlbumEdit(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	var in struct {
		Monitored bool `json:"monitored"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := s.Music.MonitorAlbum(movieID(r), in.Monitored); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) musicAlbumSearch(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	vs, err := s.Music.Search(ctx, movieID(r), r.URL.Query().Get("grab") == "1")
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	for i := range vs {
		vs[i].Result.Link = ""
	}
	writeJSON(w, map[string]any{"verdicts": vs})
}

func (s *Server) musicAlbumGrab(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) || !s.indexersReady(w) {
		return
	}
	var in struct {
		IndexerID int64  `json:"indexerId"`
		GUID      string `json:"guid"`
		Title     string `json:"title"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	vs, err := s.Music.Search(ctx, movieID(r), false)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	for _, v := range vs {
		if v.Result.IndexerID == in.IndexerID && (v.Result.GUID == in.GUID || v.Result.Title == in.Title) {
			j, err := s.Music.GrabResult(ctx, movieID(r), v.Result)
			if err != nil {
				jsonErr(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, map[string]any{"job": j.ID})
			return
		}
	}
	jsonErr(w, http.StatusConflict, "the indexer no longer offers that release")
}

func (s *Server) musicAdd(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	var in struct {
		Category string `json:"category"`
		MBID     string `json:"mbid"`
		Search   bool   `json:"search"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	a, err := s.Music.AddArtist(ctx, in.Category, in.MBID)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Search {
		go s.Music.SearchWanted(context.Background())
	}
	writeJSON(w, a)
}

func (s *Server) musicImport(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	var in struct {
		Category string `json:"category"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	res, err := s.Music.Import(ctx, in.Category)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, res)
}

func (s *Server) musicSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Category      string   `json:"category"`
		Native        bool     `json:"native"`
		LibraryDir    string   `json:"libraryDir"`
		DryRun        bool     `json:"dryRun"`
		Qualities     []string `json:"qualities"`
		Cutoff        string   `json:"cutoff"`
		Folder        string   `json:"folder"`
		SearchPerHour int      `json:"searchPerHour"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad settings")
		return
	}
	known := map[string]bool{}
	for _, q := range music.Qualities {
		known[q] = true
	}
	for _, q := range in.Qualities {
		if !known[q] {
			jsonErr(w, http.StatusBadRequest, "unknown quality "+strconv.Quote(q))
			return
		}
	}
	if in.Cutoff != "" && !known[in.Cutoff] {
		jsonErr(w, http.StatusBadRequest, "unknown cutoff")
		return
	}
	st := s.Cfg.Get()
	found := false
	for i, c := range st.Categories {
		if strings.EqualFold(c.Name, in.Category) && c.Kind == "lidarr" {
			if in.Native && strings.TrimSpace(in.LibraryDir) == "" {
				jsonErr(w, http.StatusBadRequest, "a native category needs its music library folder")
				return
			}
			st.Categories[i].Native, st.Categories[i].LibraryDir = in.Native, strings.TrimSpace(in.LibraryDir)
			found = true
		}
	}
	if !found {
		jsonErr(w, http.StatusBadRequest, "pick a Lidarr category")
		return
	}
	st.Music.DryRun, st.Music.Qualities, st.Music.Cutoff, st.Music.Folder, st.Music.SearchPerHour = in.DryRun, in.Qualities, in.Cutoff, strings.TrimSpace(in.Folder), in.SearchPerHour
	if err := s.Cfg.Set(st); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) musicSearchWanted(w http.ResponseWriter, r *http.Request) {
	if !s.musicLibReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	n, err := s.Music.SearchWanted(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"searched": n})
}
