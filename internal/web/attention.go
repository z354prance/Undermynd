package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
)

// adminAttention is the Upgrades page: the setting, the profiles to choose
// from, and what has been moved.
func (s *Server) adminAttention(w http.ResponseWriter, r *http.Request) {
	set := s.Cfg.Get()
	out := map[string]any{"settings": set.Attention, "tracearr": set.Playback.TracearrURL != "" && set.Playback.TracearrKey != ""}
	profiles := map[string][]string{"sonarr": {}, "radarr": {}}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	for _, kind := range []string{"sonarr", "radarr"} {
		for _, c := range set.Categories {
			if c.Kind != kind || c.ArrURL == "" {
				continue
			}
			ps, err := arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey)).Profiles(ctx)
			if err != nil {
				continue
			}
			for _, p := range ps {
				profiles[kind] = append(profiles[kind], p.Name)
			}
			break
		}
	}
	out["profiles"] = profiles
	if s.Attention != nil {
		out["status"] = s.Attention.Snapshot()
	}
	writeJSON(w, out)
}

func (s *Server) adminAttentionSave(w http.ResponseWriter, r *http.Request) {
	var in config.Attention
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad settings")
		return
	}
	in.TVProfile, in.MovieProfile = strings.TrimSpace(in.TVProfile), strings.TrimSpace(in.MovieProfile)
	if in.PerDay < 1 || in.PerDay > 20 {
		in.PerDay = 3
	}
	if in.MoviePerDay < 1 || in.MoviePerDay > 20 {
		in.MoviePerDay = 3
	}
	if in.On && in.TVProfile == "" && in.MovieProfile == "" {
		jsonErr(w, http.StatusBadRequest, "choose the upgrade profile for shows, movies or both")
		return
	}
	cur := s.Cfg.Get()
	cur.Attention = in
	if err := s.Cfg.Set(cur); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// adminAttentionRun checks now instead of waiting for the hourly pass.
func (s *Server) adminAttentionRun(w http.ResponseWriter, r *http.Request) {
	if s.Attention == nil {
		jsonErr(w, http.StatusServiceUnavailable, "not running")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s.Attention.Check(ctx)
	writeJSON(w, s.Attention.Snapshot())
}

// adminAttentionDecide adds a waiting movie to Radarr or dismisses it.
func (s *Server) adminAttentionDecide(w http.ResponseWriter, r *http.Request) {
	if s.Attention == nil {
		jsonErr(w, http.StatusServiceUnavailable, "not running")
		return
	}
	var in struct {
		TMDB int64 `json:"tmdb"`
		Add  bool  `json:"add"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.TMDB <= 0 {
		jsonErr(w, http.StatusBadRequest, "bad request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.Attention.Decide(ctx, in.TMDB, in.Add); err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, s.Attention.Snapshot())
}

// adminAttentionUndo moves a title back to the profile it had.
func (s *Server) adminAttentionUndo(w http.ResponseWriter, r *http.Request) {
	if s.Attention == nil {
		jsonErr(w, http.StatusServiceUnavailable, "not running")
		return
	}
	var in struct {
		Kind     string `json:"kind"`
		Category string `json:"category"`
		ArrID    int64  `json:"arrId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil || in.ArrID <= 0 {
		jsonErr(w, http.StatusBadRequest, "bad request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.Attention.Undo(ctx, in.Kind, in.Category, in.ArrID); err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, s.Attention.Snapshot())
}
