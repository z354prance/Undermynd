package vpn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Nord gets a WireGuard (NordLynx) setup from NordVPN's API with an access
// token from the Nord account (NordVPN › Manual setup).
type Nord struct {
	Base   string // default https://api.nordvpn.com
	Client *http.Client
}

func (n *Nord) base() string {
	if n.Base != "" {
		return strings.TrimRight(n.Base, "/")
	}
	return "https://api.nordvpn.com"
}

func (n *Nord) get(ctx context.Context, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.base()+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.SetBasicAuth("token", token)
	}
	hc := n.Client
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("NordVPN could not be reached: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("NordVPN refused the access token: create a new one in your Nord account (NordVPN › Manual setup)")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("NordVPN answered %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Conf builds the WireGuard setup for the recommended server, in country
// (a two-letter code; "" = anywhere).
func (n *Nord) Conf(ctx context.Context, token, country string) (WGConf, string, error) {
	var cred struct {
		Key string `json:"nordlynx_private_key"`
	}
	if err := n.get(ctx, "/v1/users/services/credentials", token, &cred); err != nil {
		return WGConf{}, "", err
	}
	priv, err := base64.StdEncoding.DecodeString(cred.Key)
	if err != nil || len(priv) != 32 {
		return WGConf{}, "", errors.New("NordVPN gave no WireGuard key for this account")
	}
	q := url.Values{}
	q.Set("filters[servers_technologies][identifier]", "wireguard_udp")
	q.Set("limit", "1")
	if country != "" {
		var countries []struct {
			ID   int    `json:"id"`
			Code string `json:"code"`
		}
		if err := n.get(ctx, "/v1/servers/countries", "", &countries); err != nil {
			return WGConf{}, "", err
		}
		found := false
		for _, c := range countries {
			if strings.EqualFold(c.Code, country) {
				q.Set("filters[country_id]", fmt.Sprint(c.ID))
				found = true
			}
		}
		if !found {
			return WGConf{}, "", fmt.Errorf("NordVPN has no servers in %q", country)
		}
	}
	var servers []struct {
		Hostname     string `json:"hostname"`
		Station      string `json:"station"`
		Technologies []struct {
			Identifier string `json:"identifier"`
			Metadata   []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"metadata"`
		} `json:"technologies"`
	}
	if err := n.get(ctx, "/v1/servers/recommendations?"+q.Encode(), "", &servers); err != nil {
		return WGConf{}, "", err
	}
	for _, s := range servers {
		for _, t := range s.Technologies {
			if t.Identifier != "wireguard_udp" {
				continue
			}
			for _, m := range t.Metadata {
				if m.Name != "public_key" {
					continue
				}
				pub, err := base64.StdEncoding.DecodeString(m.Value)
				if err != nil || len(pub) != 32 || s.Station == "" {
					continue
				}
				return WGConf{
					Private:  priv,
					Address:  netip.MustParseAddr("10.5.0.2"),
					DNS:      []netip.Addr{netip.MustParseAddr("103.86.96.100"), netip.MustParseAddr("103.86.99.100")},
					Public:   pub,
					Endpoint: net.JoinHostPort(s.Station, "51820"),
					MTU:      1420,
				}, s.Hostname, nil
			}
		}
	}
	return WGConf{}, "", errors.New("NordVPN recommended no WireGuard server")
}
