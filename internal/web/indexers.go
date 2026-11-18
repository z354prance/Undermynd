package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/indexers"
)

func (s *Server) indexersReady(w http.ResponseWriter) bool {
	if s.Indexers == nil {
		jsonErr(w, http.StatusConflict, "indexers need the database (UNDERMYND_STORE is set to json)")
		return false
	}
	return true
}

func (s *Server) indexersList(w http.ResponseWriter, r *http.Request) {
	if !s.indexersReady(w) {
		return
	}
	list, err := s.Indexers.List()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var cats []string
	for _, c := range s.Cfg.Get().Categories {
		cats = append(cats, c.Name)
	}
	writeJSON(w, map[string]any{"indexers": indexers.Public(list), "categories": cats})
}

func (s *Server) indexersSave(w http.ResponseWriter, r *http.Request) {
	if !s.indexersReady(w) {
		return
	}
	var in indexers.Indexer
	if !readJSON(w, r, &in) {
		return
	}
	i, err := s.Indexers.Save(in)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	i.APIKey = ""
	writeJSON(w, i)
}

func (s *Server) indexersDelete(w http.ResponseWriter, r *http.Request) {
	if !s.indexersReady(w) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Indexers.Delete(id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) indexersTest(w http.ResponseWriter, r *http.Request) {
	if !s.indexersReady(w) {
		return
	}
	var in indexers.Indexer
	if !readJSON(w, r, &in) {
		return
	}
	// A saved indexer tested without a new key uses its stored one.
	if in.ID != 0 && strings.TrimSpace(in.APIKey) == "" {
		if old, err := s.Indexers.Get(in.ID); err == nil {
			in.APIKey = old.APIKey
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	caps, err := s.Indexers.Test(ctx, in)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, caps)
}

func (s *Server) indexersImport(w http.ResponseWriter, r *http.Request) {
	if !s.indexersReady(w) {
		return
	}
	var in struct {
		URL string `json:"url"`
		Key string `json:"key"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	added, err := s.Indexers.ImportProwlarr(ctx, in.URL, strings.TrimSpace(in.Key))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if added == nil {
		added = []string{}
	}
	writeJSON(w, map[string]any{"added": added})
}

func (s *Server) indexersSearch(w http.ResponseWriter, r *http.Request) {
	if !s.indexersReady(w) {
		return
	}
	q := r.URL.Query()
	query := indexers.Query{Text: q.Get("q"), Type: q.Get("type")}
	query.Season, _ = strconv.Atoi(q.Get("season"))
	query.Episode, _ = strconv.Atoi(q.Get("ep"))
	query.TVDB, _ = strconv.ParseInt(q.Get("tvdbid"), 10, 64)
	query.TMDB, _ = strconv.ParseInt(q.Get("tmdbid"), 10, 64)
	query.IMDB = q.Get("imdbid")
	for _, c := range strings.Split(q.Get("cat"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
			query.Categories = append(query.Categories, n)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.Indexers.Search(ctx, query, q.Get("protocol"))
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, res)
}

func (s *Server) indexersGrab(w http.ResponseWriter, r *http.Request) {
	if !s.indexersReady(w) {
		return
	}
	var in struct {
		indexers.Result
		Category string `json:"category"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if _, ok := s.Cfg.Get().Category(in.Category); !ok {
		jsonErr(w, http.StatusBadRequest, "choose a category")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	j, err := s.Indexers.Grab(ctx, in.Result, in.Category)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]string{"job": j.ID})
}
