// Package vpn runs a Private Internet Access WireGuard tunnel inside
// HyveMynd, in user space (no changes to the host or container network), and
// gives the built-in download clients a network that only goes through it.
//
// It follows PIA's own manual-connections scripts
// (github.com/pia-foss/manual-connections, MIT): a token from the username and
// password, the server list, a key registered with a WireGuard server of the
// chosen region (port 1337), and, inside the tunnel, a forwarded port
// (getSignature once, then bindPort every 15 minutes; PIA keeps a port about
// two months). pia-ca.crt is PIA's certificate from that repository.
package vpn

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

//go:embed pia-ca.crt
var piaCA []byte

// API is PIA's service: where it lives, and the certificate its VPN servers
// use. Tests point it at fakes.
type API struct {
	TokenURL  string // POST username and password, answers {"token": ...}
	ServerURL string // the server list
	Roots     *x509.CertPool
	// Dial reaches a VPN server outside the tunnel (key registration). Nil:
	// a plain dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	HTTP *http.Client // token and server list
}

// PIA is the real service.
func PIA() *API {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(piaCA)
	return &API{
		TokenURL:  "https://www.privateinternetaccess.com/api/client/v2/token",
		ServerURL: "https://serverlist.piaservers.net/vpninfo/servers/v6",
		Roots:     pool,
		HTTP:      &http.Client{Timeout: 20 * time.Second},
	}
}

// Token signs in and returns a token (valid about a day).
func (a *API) Token(ctx context.Context, user, pass string) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("username", user)
	_ = w.WriteField("password", pass)
	_ = w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.TokenURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", errors.New("PIA could not be reached to sign in")
	}
	defer resp.Body.Close()
	var r struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&r)
	if resp.StatusCode != http.StatusOK || r.Token == "" {
		return "", errors.New("PIA did not accept the username and password")
	}
	return r.Token, nil
}

// Server is one PIA server: its address and its certificate name.
type Server struct {
	IP string `json:"ip"`
	CN string `json:"cn"`
}

// Region is a PIA location.
type Region struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Country     string `json:"country"`
	PortForward bool   `json:"port_forward"`
	Offline     bool   `json:"offline"`
	Servers     struct {
		WG []Server `json:"wg"`
	} `json:"servers"`
}

// Regions lists PIA's locations that have WireGuard servers, port-forwarding
// ones first, then by name.
func (a *API) Regions(ctx context.Context) ([]Region, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.ServerURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("PIA's server list could not be reached")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, errors.New("PIA's server list could not be read")
	}
	// The list is one line of JSON followed by a signature.
	if i := bytes.IndexByte(b, '\n'); i > 0 {
		b = b[:i]
	}
	var r struct {
		Regions []Region `json:"regions"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, errors.New("PIA's server list could not be read")
	}
	var out []Region
	for _, g := range r.Regions {
		if !g.Offline && len(g.Servers.WG) > 0 {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PortForward != out[j].PortForward {
			return out[i].PortForward
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Pick chooses the region by id, or the first port-forwarding one.
func Pick(regions []Region, id string) (Region, error) {
	for _, g := range regions {
		if id != "" && g.ID == id {
			return g, nil
		}
	}
	if id != "" {
		return Region{}, fmt.Errorf("PIA has no region %q with WireGuard right now", id)
	}
	for _, g := range regions {
		if g.PortForward {
			return g, nil
		}
	}
	return Region{}, errors.New("PIA lists no region with WireGuard")
}

// Key is what a WireGuard server answers when HyveMynd registers its key.
type Key struct {
	Status     string   `json:"status"`
	ServerKey  string   `json:"server_key"`
	ServerPort int      `json:"server_port"`
	ServerIP   string   `json:"server_ip"`
	ServerVIP  string   `json:"server_vip"`
	PeerIP     string   `json:"peer_ip"`
	DNSServers []string `json:"dns_servers"`
	Message    string   `json:"message"`
}

// AddKey registers a WireGuard public key (base64) with a server, outside the
// tunnel, checking the server against PIA's certificate.
func (a *API) AddKey(ctx context.Context, s Server, token, pub string) (Key, error) {
	q := url.Values{"pt": {token}, "pubkey": {pub}}
	var k Key
	if err := a.serverGet(ctx, a.Dial, s, 1337, "/addKey?"+q.Encode(), &k); err != nil {
		return Key{}, fmt.Errorf("registering with the PIA server: %w", err)
	}
	if k.Status != "OK" || k.ServerKey == "" || k.PeerIP == "" || k.ServerPort == 0 {
		return Key{}, fmt.Errorf("the PIA server refused the key: %s", strings.TrimSpace(k.Status+" "+k.Message))
	}
	return k, nil
}

// Signature is PIA's grant of a forwarded port.
type Signature struct {
	Status    string `json:"status"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	Port      int    `json:"-"`
	Expires   time.Time
}

// GetSignature asks for a forwarded port, through the tunnel.
func (a *API) GetSignature(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), gw Server, token string) (Signature, error) {
	var sg Signature
	if err := a.serverGet(ctx, dial, gw, 19999, "/getSignature?"+url.Values{"token": {token}}.Encode(), &sg); err != nil {
		return Signature{}, fmt.Errorf("asking PIA for a forwarded port: %w", err)
	}
	if sg.Status != "OK" || sg.Payload == "" {
		return Signature{}, errors.New("PIA did not grant a forwarded port (is this a port-forwarding region?)")
	}
	raw, err := base64.StdEncoding.DecodeString(sg.Payload)
	if err != nil {
		return Signature{}, errors.New("PIA's port grant could not be read")
	}
	var p struct {
		Port    int       `json:"port"`
		Expires time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Port <= 0 {
		return Signature{}, errors.New("PIA's port grant could not be read")
	}
	sg.Port, sg.Expires = p.Port, p.Expires
	return sg, nil
}

// BindPort keeps the forwarded port open (every 15 minutes), through the tunnel.
func (a *API) BindPort(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), gw Server, sg Signature) error {
	var r struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	q := url.Values{"payload": {sg.Payload}, "signature": {sg.Signature}}
	if err := a.serverGet(ctx, dial, gw, 19999, "/bindPort?"+q.Encode(), &r); err != nil {
		return fmt.Errorf("keeping the forwarded port: %w", err)
	}
	if r.Status != "OK" {
		return fmt.Errorf("PIA did not keep the forwarded port: %s", strings.TrimSpace(r.Status+" "+r.Message))
	}
	return nil
}

// serverGet makes an HTTPS GET to a PIA server by its address, checking its
// certificate name against PIA's own certificate.
func (a *API) serverGet(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), s Server, port int, path string, out any) error {
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}
	addr := net.JoinHostPort(s.IP, fmt.Sprint(port))
	tr := &http.Transport{
		DialContext:     func(ctx context.Context, network, _ string) (net.Conn, error) { return dial(ctx, network, addr) },
		TLSClientConfig: &tls.Config{ServerName: s.CN, RootCAs: a.Roots, MinVersion: tls.VersionTLS12},
	}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+s.CN+":"+fmt.Sprint(port)+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(out)
}
