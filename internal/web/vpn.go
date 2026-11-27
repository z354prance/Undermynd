package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
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
	On           bool       `json:"on"`
	Provider     string     `json:"provider"`
	Providers    []string   `json:"providers"`
	User         string     `json:"user"`
	HasPassword  bool       `json:"hasPassword"`
	Region       string     `json:"region"`
	HasWireGuard bool       `json:"hasWireGuard"`
	WGServer     string     `json:"wgServer,omitempty"` // the file's server, to show which file is saved
	HasToken     bool       `json:"hasToken"`
	Country      string     `json:"country"`
	Port         int        `json:"port"`
	Torrents     string     `json:"torrents"`
	Status       vpn.Status `json:"status"`
	Client       bt.Stats   `json:"client"`
	TorBox       bool       `json:"torbox"` // a TorBox token is set
}

func (s *Server) adminVPN(w http.ResponseWriter, r *http.Request) {
	c := s.Cfg.Get()
	v := vpnView{On: c.VPN.On, Provider: c.VPN.Kind(), Providers: config.VPNProviders, User: c.VPN.User, HasPassword: c.VPN.Pass != "", Region: c.VPN.Region,
		HasWireGuard: c.VPN.WireGuard != "", HasToken: c.VPN.Token != "", Country: c.VPN.Country, Port: c.VPN.Port,
		Torrents: c.VPN.Torrents, TorBox: c.TorBox.On()}
	if wc, err := vpn.ParseWG(string(c.VPN.WireGuard)); err == nil {
		v.WGServer = wc.Endpoint
	}
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
		On        bool   `json:"on"`
		Provider  string `json:"provider"`
		User      string `json:"user"`
		Password  string `json:"password"` // blank: unchanged
		Region    string `json:"region"`
		WireGuard string `json:"wireguard"` // blank: unchanged
		Token     string `json:"token"`     // blank: unchanged
		Country   string `json:"country"`
		Port      int    `json:"port"`
		Torrents  string `json:"torrents"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad settings")
		return
	}
	cur := s.Cfg.Get()
	prov := strings.TrimSpace(in.Provider)
	if prov == "" {
		prov = "pia"
	}
	if !slices.Contains(config.VPNProviders, prov) {
		jsonErr(w, http.StatusBadRequest, "unknown VPN provider")
		return
	}
	next := config.VPN{On: in.On, Provider: prov, User: strings.TrimSpace(in.User), Pass: cur.VPN.Pass, Region: strings.TrimSpace(in.Region),
		WireGuard: cur.VPN.WireGuard, Token: cur.VPN.Token, Country: strings.ToUpper(strings.TrimSpace(in.Country))}
	if in.Password != "" {
		next.Pass = secret.String(in.Password)
	}
	if strings.TrimSpace(in.WireGuard) != "" {
		if _, err := vpn.ParseWG(in.WireGuard); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		next.WireGuard = secret.String(in.WireGuard)
	}
	if t := strings.TrimSpace(in.Token); t != "" {
		next.Token = secret.String(t)
	}
	if in.Port < 0 || in.Port > 65535 {
		jsonErr(w, http.StatusBadRequest, "the forwarded port must be between 1 and 65535")
		return
	}
	next.Port = in.Port
	switch in.Torrents {
	case "", "local", "torbox":
		next.Torrents = in.Torrents
	default:
		jsonErr(w, http.StatusBadRequest, "unknown torrent choice")
		return
	}
	if next.On && !next.Ready() {
		switch next.Kind() {
		case "pia":
			jsonErr(w, http.StatusBadRequest, "enter your PIA username and password")
		case "nord":
			jsonErr(w, http.StatusBadRequest, "paste a NordVPN access token")
		default:
			jsonErr(w, http.StatusBadRequest, "add the WireGuard file from your VPN provider")
		}
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
