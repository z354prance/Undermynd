package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/bt"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/vpn"
)

// VPN is the tunnel and the client that uses it, as the admin sees them.
type VPN struct {
	Tunnel *vpn.Tunnel
	Client *bt.Client
}

type vpnView struct {
	On          bool       `json:"on"`
	User        string     `json:"user"`
	HasPassword bool       `json:"hasPassword"`
	Region      string     `json:"region"`
	Torrents    string     `json:"torrents"`
	Status      vpn.Status `json:"status"`
	Client      bt.Stats   `json:"client"`
	TorBox      bool       `json:"torbox"` // a TorBox token is set
}

func (s *Server) adminVPN(w http.ResponseWriter, r *http.Request) {
	c := s.Cfg.Get()
	v := vpnView{On: c.VPN.On, User: c.VPN.User, HasPassword: c.VPN.Pass != "", Region: c.VPN.Region, Torrents: c.VPN.Torrents, TorBox: c.TorBox.Token != ""}
	if s.VPN != nil && s.VPN.Tunnel != nil {
		v.Status = s.VPN.Tunnel.Status()
	} else {
		v.Status.State = "off"
	}
	if s.VPN != nil && s.VPN.Client != nil {
		v.Client = s.VPN.Client.Stats()
	}
	writeJSON(w, v)
}

func (s *Server) adminVPNSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On       bool   `json:"on"`
		User     string `json:"user"`
		Password string `json:"password"` // blank: unchanged
		Region   string `json:"region"`
		Torrents string `json:"torrents"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad settings")
		return
	}
	cur := s.Cfg.Get()
	next := config.VPN{On: in.On, User: strings.TrimSpace(in.User), Pass: cur.VPN.Pass, Region: strings.TrimSpace(in.Region)}
	if in.Password != "" {
		next.Pass = secret.String(in.Password)
	}
	switch in.Torrents {
	case "", "local", "torbox":
		next.Torrents = in.Torrents
	default:
		jsonErr(w, http.StatusBadRequest, "unknown torrent choice")
		return
	}
	if next.On && (next.User == "" || next.Pass == "") {
		jsonErr(w, http.StatusBadRequest, "enter your PIA username and password")
		return
	}
	cur.VPN = next
	if err := s.Cfg.Set(cur); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.VPN != nil && s.VPN.Tunnel != nil {
		s.VPN.Tunnel.Restart()
	}
	writeJSON(w, map[string]any{"ok": true})
}

type regionView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PortForward bool   `json:"portForward"`
}

func (s *Server) adminVPNRegions(w http.ResponseWriter, r *http.Request) {
	api := vpn.PIA()
	if s.VPN != nil && s.VPN.Tunnel != nil && s.VPN.Tunnel.API != nil {
		api = s.VPN.Tunnel.API
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	regions, err := api.Regions(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]regionView, 0, len(regions))
	for _, g := range regions {
		out = append(out, regionView{g.ID, g.Name, g.PortForward})
	}
	writeJSON(w, out)
}
