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
	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/indexers"
)

func (s *Server) tvReady(w http.ResponseWriter) bool {
	if s.TV == nil {
		jsonErr(w, http.StatusConflict, "TV needs the database (UNDERMYND_STORE is set to json)")
		return false
	}
	return true
}

func (s *Server) tvList(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	list, err := s.TV.List(r.URL.Query().Get("category"))
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
		if c.Kind == "sonarr" {
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
	writeJSON(w, map[string]any{"series": list, "categories": cats, "settings": st.TV, "profiles": profiles})
}

func (s *Server) tvGet(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	x, eps, err := s.TV.Get(movieID(r))
	if errors.Is(err, catalog.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "no such series")
		return
	} else if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"series": x, "episodes": eps})
}

func (s *Server) tvEdit(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	var in struct {
		Monitored bool   `json:"monitored"`
		Profile   string `json:"profile"`
		Type      string `json:"type"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := s.TV.EditSeries(movieID(r), in.Monitored, in.Profile, in.Type); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) tvDelete(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	if err := s.TV.Remove(movieID(r)); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) tvMonitor(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	var in struct {
		Season    int   `json:"season"`
		EpisodeID int64 `json:"episodeId"`
		On        bool  `json:"on"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if err := s.TV.Monitor(movieID(r), in.Season, in.EpisodeID, in.On); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

type verdictRow struct {
	Decision decision.Decision `json:"decision"`
	Result   indexers.Result   `json:"result"`
}

func verdictRows(ds []decision.Decision, res []indexers.Result) []verdictRow {
	byKey := map[string]indexers.Result{}
	for _, x := range res {
		byKey[x.Indexer+"|"+x.Title] = x
	}
	out := make([]verdictRow, 0, len(ds))
	for _, d := range ds {
		x := byKey[d.Candidate.Indexer+"|"+d.Candidate.Title]
		x.Link = ""
		out = append(out, verdictRow{d, x})
	}
	return out
}

// tvSearch searches a season (?season=) or an episode (?episode=):
// interactively, or with ?grab=1 to grab the best.
func (s *Server) tvSearch(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	grab := r.URL.Query().Get("grab") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var ds []decision.Decision
	var res []indexers.Result
	var err error
	if ep, _ := strconv.ParseInt(r.URL.Query().Get("episode"), 10, 64); ep > 0 {
		ds, res, err = s.TV.SearchEpisode(ctx, ep, grab)
	} else {
		sn, _ := strconv.Atoi(r.URL.Query().Get("season"))
		ds, res, err = s.TV.SearchSeason(ctx, movieID(r), sn, grab)
	}
	if errors.Is(err, catalog.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "no such episode")
		return
	} else if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"rows": verdictRows(ds, res)})
}

func (s *Server) tvGrab(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) || !s.indexersReady(w) {
		return
	}
	var in struct {
		IndexerID int64  `json:"indexerId"`
		GUID      string `json:"guid"`
		Title     string `json:"title"`
		Season    int    `json:"season"`
		EpisodeID int64  `json:"episodeId"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	// Search again for the download address, which is not sent to the page.
	var res []indexers.Result
	var err error
	if in.EpisodeID > 0 {
		_, res, err = s.TV.SearchEpisode(ctx, in.EpisodeID, false)
	} else {
		_, res, err = s.TV.SearchSeason(ctx, movieID(r), in.Season, false)
	}
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	for _, x := range res {
		if x.IndexerID == in.IndexerID && (x.GUID == in.GUID || x.Title == in.Title) {
			j, err := s.TV.GrabResult(ctx, movieID(r), x)
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

func (s *Server) tvImport(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	var in struct {
		Category string `json:"category"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	res, err := s.TV.Import(ctx, in.Category)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, res)
}

func (s *Server) tvSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Category      string `json:"category"`
		Native        bool   `json:"native"`
		LibraryDir    string `json:"libraryDir"`
		DryRun        bool   `json:"dryRun"`
		Folder        string `json:"folder"`
		Season        string `json:"season"`
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
		if strings.EqualFold(c.Name, in.Category) && c.Kind == "sonarr" {
			if in.Native && strings.TrimSpace(in.LibraryDir) == "" {
				jsonErr(w, http.StatusBadRequest, "a native category needs its TV library folder")
				return
			}
			st.Categories[i].Native, st.Categories[i].LibraryDir = in.Native, strings.TrimSpace(in.LibraryDir)
			found = true
		}
	}
	if !found {
		jsonErr(w, http.StatusBadRequest, "pick a Sonarr category")
		return
	}
	if in.SearchPerHour < 0 || in.SearchPerHour > 500 {
		jsonErr(w, http.StatusBadRequest, "searches an hour: 0 to 500")
		return
	}
	st.TV.DryRun, st.TV.Folder, st.TV.Season, st.TV.File, st.TV.SearchPerHour = in.DryRun, strings.TrimSpace(in.Folder), strings.TrimSpace(in.Season), strings.TrimSpace(in.File), in.SearchPerHour
	if err := s.Cfg.Set(st); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) tvSearchWanted(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	n, err := s.TV.SearchWanted(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	g, err := s.TV.RecentSync(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"searched": n, "recent": g})
}

func (s *Server) tvLookup(w http.ResponseWriter, r *http.Request) {
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
		if m.MediaType == "tv" {
			out = append(out, m)
		}
	}
	writeJSON(w, map[string]any{"results": out})
}

func (s *Server) tvAdd(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	var in struct {
		Category string `json:"category"`
		TMDB     int64  `json:"tmdbId"`
		Profile  string `json:"profile"`
		All      bool   `json:"all"`
		Search   bool   `json:"search"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	x, err := s.TV.Add(ctx, in.Category, in.TMDB, in.Profile, in.All)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Search {
		go s.TV.SearchWanted(context.Background())
	}
	writeJSON(w, x)
}
