package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/z354prance/undermynd/internal/config"
)

func (s *Server) schedulerView(w http.ResponseWriter, r *http.Request) {
	if s.Budget == nil || s.Backlog == nil {
		jsonErr(w, http.StatusConflict, "the scheduler needs the database")
		return
	}
	recent, err := s.Backlog.Recent(100)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"settings": s.Cfg.Get().Scheduler, "perHour": s.Budget.PerHour(), "used": s.Budget.Used(),
		"left": s.Budget.Left(), "recent": recent})
}

func (s *Server) schedulerPut(w http.ResponseWriter, r *http.Request) {
	var in config.Scheduler
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad settings")
		return
	}
	if in.PerHour < 0 || in.PerHour > 1000 || in.Batch < 0 || in.Batch > 100 || in.GraceHours < 0 || in.GraceHours > 168 ||
		in.MissingDays < 0 || in.MissingDays > 365 || in.CutoffDays < 0 || in.CutoffDays > 365 {
		jsonErr(w, http.StatusBadRequest, "a value is out of range")
		return
	}
	st := s.Cfg.Get()
	st.Scheduler = in
	if err := s.Cfg.Set(st); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, st.Scheduler)
}

func (s *Server) backlogNow(w http.ResponseWriter, r *http.Request) {
	if s.Backlog == nil {
		jsonErr(w, http.StatusConflict, "the scheduler needs the database")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	n, err := s.Backlog.Once(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"searched": n})
}
