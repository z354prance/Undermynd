package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func (s *Server) trashReady(w http.ResponseWriter) bool {
	if s.Trash == nil {
		jsonErr(w, http.StatusConflict, "the TRaSH Guides need the database")
		return false
	}
	return true
}

// trashCatalog lists the guides' profiles and quality sizes.
func (s *Server) trashCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.trashReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	g, err := s.Trash.Client.Get(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	type prof struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	out := map[string]any{}
	for app, a := range g.Apps {
		var ps []prof
		for _, p := range a.Profiles {
			ps = append(ps, prof{p.TrashID, p.Name, p.Description})
		}
		var sizes []string
		for _, sz := range a.Sizes {
			sizes = append(sizes, sz.Type)
		}
		out[app] = map[string]any{"profiles": ps, "sizes": sizes, "formats": len(a.Formats)}
	}
	writeJSON(w, map[string]any{"apps": out, "settings": s.Cfg.Get().Trash, "at": g.At})
}

type trashReq struct {
	App      string   `json:"app"`
	Profiles []string `json:"profiles"`
	Size     string   `json:"size"`
	Keep     bool     `json:"keep"`
}

func (s *Server) trashPreview(w http.ResponseWriter, r *http.Request) {
	if !s.trashReady(w) {
		return
	}
	var in trashReq
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	ch, skipped, err := s.Trash.Preview(ctx, in.App, in.Profiles, in.Size)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"changes": ch, "unchecked": skipped})
}

// trashApply stores the chosen guide profiles; with keep, they are synced daily.
func (s *Server) trashApply(w http.ResponseWriter, r *http.Request) {
	if !s.trashReady(w) {
		return
	}
	var in trashReq
	json.NewDecoder(r.Body).Decode(&in)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	ch, err := s.Trash.Apply(ctx, in.App, in.Profiles, in.Size)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	st := s.Cfg.Get()
	if in.App == "radarr" {
		st.Trash.Radarr, st.Trash.SizeRadarr = in.Profiles, in.Size
	} else {
		st.Trash.Sonarr, st.Trash.SizeSonarr = in.Profiles, in.Size
	}
	st.Trash.Sync = in.Keep
	if err := s.Cfg.Set(st); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"changes": ch})
}
