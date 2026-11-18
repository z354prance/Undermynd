package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/metadata"
)

func (s *Server) moviesReady(w http.ResponseWriter) bool {
	if s.Movies == nil {
		jsonErr(w, http.StatusConflict, "movies need the database (UNDERMYND_STORE is set to json)")
		return false
	}
	return true
}

func movieID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func (s *Server) moviesList(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	list, err := s.Movies.List(r.URL.Query().Get("category"))
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
		if c.Kind == "radarr" {
			cats = append(cats, cat{c.Name, c.Native, c.LibraryDir})
		}
	}
	var profiles []string
	if s.Profiles != nil {
		ps, _ := s.Profiles.Profiles()
		for _, p := range ps {
			profiles = append(profiles, p.Name)
		}
	}
	for i := range list {
		list[i].Poster, list[i].Fanart = imgURL(list[i].Poster), imgURL(list[i].Fanart)
	}
	writeJSON(w, map[string]any{"movies": list, "categories": cats, "settings": st.Movies, "profiles": profiles})
}

func (s *Server) moviesImport(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	var in struct {
		Category string `json:"category"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := s.Movies.Import(ctx, in.Category)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, res)
}

func (s *Server) moviesSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Category      string `json:"category"`
		Native        bool   `json:"native"`
		LibraryDir    string `json:"libraryDir"`
		DryRun        bool   `json:"dryRun"`
		Folder        string `json:"folder"`
		File          string `json:"file"`
		SearchPerHour int    `json:"searchPerHour"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad settings")
		return
	}
	st := s.Cfg.Get()
	found := false
	for i, c := range st.Categories {
		if strings.EqualFold(c.Name, in.Category) && c.Kind == "radarr" {
			if in.Native && strings.TrimSpace(in.LibraryDir) == "" {
				jsonErr(w, http.StatusBadRequest, "a native category needs its movie library folder")
				return
			}
			st.Categories[i].Native, st.Categories[i].LibraryDir = in.Native, strings.TrimSpace(in.LibraryDir)
			found = true
		}
	}
	if !found {
		jsonErr(w, http.StatusBadRequest, "pick a Radarr category")
		return
	}
	if in.SearchPerHour < 0 || in.SearchPerHour > 200 {
		jsonErr(w, http.StatusBadRequest, "searches an hour: 0 to 200")
		return
	}
	st.Movies.DryRun, st.Movies.Folder, st.Movies.File, st.Movies.SearchPerHour = in.DryRun, strings.TrimSpace(in.Folder), strings.TrimSpace(in.File), in.SearchPerHour
	if err := s.Cfg.Set(st); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) movieEdit(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	var in struct {
		Monitored bool   `json:"monitored"`
		Profile   string `json:"profile"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := s.Movies.Edit(movieID(r), in.Monitored, in.Profile); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) movieDelete(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	if err := s.Movies.Remove(movieID(r)); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// movieSearch searches for one movie: interactively (the verdicts) or
// automatically (grab the best).
func (s *Server) movieSearch(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	grab := r.URL.Query().Get("grab") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	ds, res, err := s.Movies.Search(ctx, movieID(r), grab)
	if errors.Is(err, catalog.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "no such movie")
		return
	} else if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	// Each verdict carries the result it is about, so one can be grabbed.
	type row struct {
		Decision any             `json:"decision"`
		Result   indexers.Result `json:"result"`
	}
	byKey := map[string]indexers.Result{}
	for _, x := range res {
		byKey[x.Indexer+"|"+x.Title] = x
	}
	out := make([]row, 0, len(ds))
	for _, d := range ds {
		x := byKey[d.Candidate.Indexer+"|"+d.Candidate.Title]
		x.Link = "" // the link may carry a key; grabbing sends the title back
		out = append(out, row{d, x})
	}
	m, _ := s.Movies.Get(movieID(r))
	writeJSON(w, map[string]any{"rows": out, "movie": m})
}

func (s *Server) movieGrab(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) || !s.indexersReady(w) {
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
	// Search again to get the download address, which is not sent to the page.
	_, res, err := s.Movies.Search(ctx, movieID(r), false)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	for _, x := range res {
		if x.IndexerID == in.IndexerID && (x.GUID == in.GUID || x.Title == in.Title) {
			j, err := s.Movies.GrabResult(ctx, movieID(r), x)
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

func (s *Server) moviesSearchWanted(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	n, err := s.Movies.SearchWanted(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"searched": n})
}

func (s *Server) moviesLookup(w http.ResponseWriter, r *http.Request) {
	if s.Metadata == nil || s.Metadata.TMDB == nil {
		jsonErr(w, http.StatusConflict, "set a TMDB key on the Metadata settings tab")
		return
	}
	res, err := s.Metadata.TMDB.SearchMulti(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := res[:0]
	for _, m := range res {
		if m.MediaType == "movie" {
			out = append(out, m)
		}
	}
	writeJSON(w, map[string]any{"results": out})
}

func (s *Server) moviesAdd(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	if s.Metadata == nil || s.Metadata.TMDB == nil {
		jsonErr(w, http.StatusConflict, "set a TMDB key on the Metadata settings tab")
		return
	}
	var in struct {
		Category string `json:"category"`
		TMDB     int64  `json:"tmdbId"`
		Profile  string `json:"profile"`
		Search   bool   `json:"search"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	info, err := s.Metadata.TMDB.Movie(r.Context(), in.TMDB)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	year, _ := strconv.Atoi(strings.SplitN(info.ReleaseDate, "-", 2)[0])
	m, err := s.Movies.Add(r.Context(), in.Category, catalog.Movie{Title: info.Title, Year: year, TMDBID: in.TMDB, IMDBID: info.IMDB}, info.Runtime, in.Profile)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Movies.SetArt(m.ID, metadata.Image(info.PosterPath, "w500"), metadata.Image(info.BackdropPath, "w1280"), info.Overview, genreNames(info))
	if in.Search {
		go s.Movies.Search(context.Background(), m.ID, true)
	}
	writeJSON(w, m)
}

func genreNames(m metadata.MovieInfo) string {
	var g []string
	for _, x := range m.Genres {
		g = append(g, x.Name)
	}
	return strings.Join(g, ", ")
}

// movieGet is one movie with its history, for its page.
func (s *Server) movieGet(w http.ResponseWriter, r *http.Request) {
	if !s.moviesReady(w) {
		return
	}
	m, err := s.Movies.Get(movieID(r))
	if err != nil {
		jsonErr(w, http.StatusNotFound, "no such movie")
		return
	}
	m.Poster, m.Fanart = imgURL(m.Poster), imgURL(m.Fanart)
	var hist any = []any{}
	if s.History != nil {
		if h, err := s.History.List("movie", m.ID, 50); err == nil && h != nil {
			hist = h
		}
	}
	var profiles []string
	if s.Profiles != nil {
		ps, _ := s.Profiles.Profiles()
		for _, p := range ps {
			profiles = append(profiles, p.Name)
		}
	}
	writeJSON(w, map[string]any{"movie": m, "history": hist, "profiles": profiles})
}
