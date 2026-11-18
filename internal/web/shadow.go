package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) shadowReport(w http.ResponseWriter, r *http.Request) {
	if s.Shadow == nil {
		jsonErr(w, http.StatusConflict, "shadow mode needs the database")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 14
	}
	rep, err := s.Shadow.Report(time.Now().AddDate(0, 0, -days), 500)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"report": rep, "settings": s.Cfg.Get().Shadow})
}

func (s *Server) shadowSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
		PerHour int  `json:"perHour"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.PerHour < 0 || in.PerHour > 500 {
		jsonErr(w, http.StatusBadRequest, "searches an hour: 0 to 500")
		return
	}
	st := s.Cfg.Get()
	st.Shadow.Enabled, st.Shadow.PerHour = in.Enabled, in.PerHour
	if err := s.Cfg.Set(st); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, st.Shadow)
}

func (s *Server) shadowCheck(w http.ResponseWriter, r *http.Request) {
	if s.Shadow == nil {
		jsonErr(w, http.StatusConflict, "shadow mode needs the database")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	n, err := s.Shadow.Check(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"compared": n})
}
