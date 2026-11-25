package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/usenet"
)

type newsServerView struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLS         bool   `json:"tls"`
	SkipVerify  bool   `json:"skipVerify"`
	User        string `json:"user"`
	Password    string `json:"password,omitempty"` // in: blank keeps the saved one
	HasPassword bool   `json:"hasPassword"`
	Conns       int    `json:"connections"`
	Priority    int    `json:"priority"`
	On          bool   `json:"on"`
}

func (s *Server) adminUsenet(w http.ResponseWriter, r *http.Request) {
	c := s.Cfg.Get()
	out := struct {
		Servers []newsServerView `json:"servers"`
		Mode    string           `json:"mode"`
		TorBox  bool             `json:"torbox"`
		Stats   usenet.Stats     `json:"stats"`
	}{Mode: c.Usenet.Mode, TorBox: c.TorBox.Token != "", Servers: []newsServerView{}}
	for _, n := range c.Usenet.Servers {
		out.Servers = append(out.Servers, newsServerView{Name: n.Name, Host: n.Host, Port: n.Port, TLS: n.TLS, SkipVerify: n.SkipVerify,
			User: n.User, HasPassword: n.Pass != "", Conns: n.Conns, Priority: n.Priority, On: n.On})
	}
	if s.News != nil {
		out.Stats = s.News.Stats()
	}
	writeJSON(w, out)
}

// toServer turns a form row into a provider, keeping the saved password when
// the row leaves it blank (matched by address and login, else by position).
func toServer(in newsServerView, i int, old []config.NewsServer) config.NewsServer {
	n := config.NewsServer{Name: strings.TrimSpace(in.Name), Host: strings.TrimSpace(in.Host), Port: in.Port, TLS: in.TLS, SkipVerify: in.SkipVerify,
		User: strings.TrimSpace(in.User), Conns: in.Conns, Priority: in.Priority, On: in.On}
	if n.Conns <= 0 {
		n.Conns = 8
	}
	if n.Conns > 100 {
		n.Conns = 100
	}
	if n.Priority < 0 || n.Priority > 9 {
		n.Priority = 0
	}
	if in.Password != "" {
		n.Pass = secret.String(in.Password)
		return n
	}
	for _, o := range old {
		if o.Host == n.Host && o.User == n.User {
			n.Pass = o.Pass
			return n
		}
	}
	if i < len(old) {
		n.Pass = old[i].Pass
	}
	return n
}

func (s *Server) adminUsenetSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Servers []newsServerView `json:"servers"`
		Mode    string           `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad settings")
		return
	}
	switch in.Mode {
	case "", "local", "torbox":
	default:
		jsonErr(w, http.StatusBadRequest, "unknown choice")
		return
	}
	cur := s.Cfg.Get()
	next := config.Usenet{Mode: in.Mode}
	for i, v := range in.Servers {
		n := toServer(v, i, cur.Usenet.Servers)
		if n.Host == "" {
			continue
		}
		next.Servers = append(next.Servers, n)
	}
	cur.Usenet = next
	if err := s.Cfg.Set(cur); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// adminUsenetTest signs in to one provider from the form.
func (s *Server) adminUsenetTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Server newsServerView `json:"server"`
		Index  int            `json:"index"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad request")
		return
	}
	n := toServer(in.Server, in.Index, s.Cfg.Get().Usenet.Servers)
	if n.Host == "" {
		jsonErr(w, http.StatusBadRequest, "enter the server address")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := usenet.Test(ctx, n, nil); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
