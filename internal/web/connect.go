package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/notify"
	"github.com/z354prance/undermynd/internal/secret"
)

func (s *Server) tokensList(w http.ResponseWriter, r *http.Request) {
	if s.Tokens == nil {
		jsonErr(w, http.StatusConflict, "API tokens need the database")
		return
	}
	list, err := s.Tokens.List()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"tokens": list})
}

func (s *Server) tokensCreate(w http.ResponseWriter, r *http.Request) {
	if s.Tokens == nil {
		jsonErr(w, http.StatusConflict, "API tokens need the database")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	v, t, err := s.Tokens.Create(in.Name)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"token": t, "value": v})
}

func (s *Server) tokensDelete(w http.ResponseWriter, r *http.Request) {
	if s.Tokens == nil {
		jsonErr(w, http.StatusConflict, "API tokens need the database")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Tokens.Delete(id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

type notificationView struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	URL     string   `json:"url"` // shown with its path hidden; empty keeps the old one
	Enabled bool     `json:"enabled"`
	Events  []string `json:"events"`
}

func maskURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.Index(u[i+3:], "/"); j >= 0 {
			return u[:i+3+j] + "/…"
		}
	}
	return u
}

func (s *Server) notificationsList(w http.ResponseWriter, r *http.Request) {
	var out []notificationView
	for _, n := range s.Cfg.Get().Notifications {
		out = append(out, notificationView{n.Name, n.Kind, maskURL(string(n.URL)), n.Enabled, n.Events})
	}
	writeJSON(w, map[string]any{"notifications": out, "events": []string{"grabbed", "imported", "failed", "request", "replaced", "dry run"}})
}

// notificationsPut replaces the list. A target sent without an address keeps
// the address it had under the same name (the page never sees addresses).
func (s *Server) notificationsPut(w http.ResponseWriter, r *http.Request) {
	var in []notificationView
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad list")
		return
	}
	st := s.Cfg.Get()
	old := map[string]secret.String{}
	for _, n := range st.Notifications {
		old[n.Name] = n.URL
	}
	var out []config.Notification
	seen := map[string]bool{}
	for _, n := range in {
		n.Name = strings.TrimSpace(n.Name)
		if n.Name == "" || seen[n.Name] {
			jsonErr(w, http.StatusBadRequest, "each notification needs its own name")
			return
		}
		seen[n.Name] = true
		if n.Kind != "webhook" && n.Kind != "discord" && n.Kind != "ntfy" {
			jsonErr(w, http.StatusBadRequest, n.Name+": kind is webhook, discord or ntfy")
			return
		}
		u := secret.String(strings.TrimSpace(n.URL))
		if u == "" || strings.HasSuffix(string(u), "/…") {
			u = old[n.Name]
		}
		if !strings.HasPrefix(string(u), "http://") && !strings.HasPrefix(string(u), "https://") {
			jsonErr(w, http.StatusBadRequest, n.Name+": give an http(s) address")
			return
		}
		out = append(out, config.Notification{Name: n.Name, Kind: n.Kind, URL: u, Enabled: n.Enabled, Events: n.Events})
	}
	st.Notifications = out
	if err := s.Cfg.Set(st); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) notificationsTest(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		jsonErr(w, http.StatusConflict, "notifications need the database")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	for _, n := range s.Cfg.Get().Notifications {
		if n.Name == in.Name {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if err := s.Notify.Send(ctx, n, notify.Event{Event: "test", Item: "Undermynd can reach " + n.Name}); err != nil {
				jsonErr(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, map[string]any{"ok": true})
			return
		}
	}
	jsonErr(w, http.StatusNotFound, "save the notification first")
}
