package vpn

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

func TestParseWG(t *testing.T) {
	k1, k2 := base64.StdEncoding.EncodeToString(make([]byte, 32)), base64.StdEncoding.EncodeToString(append(make([]byte, 31), 1))
	c, err := ParseWG(`# Proton
[Interface]
PrivateKey = ` + k1 + `
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1, 2a07:b944::2:1

[Peer]
# NL#123
PublicKey = ` + k2 + `
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 185.0.0.1:51820
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Address.String() != "10.2.0.2" || len(c.DNS) != 1 || c.DNS[0].String() != "10.2.0.1" || c.Endpoint != "185.0.0.1:51820" || c.MTU != 1420 || c.Public[31] != 1 {
		t.Fatalf("parsed %+v", c)
	}
	for _, bad := range []string{"", "[Interface]\nPrivateKey = nope\n", "[Interface]\nPrivateKey = " + k1 + "\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = " + k2 + "\n"} {
		if _, err := ParseWG(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// A provider's WireGuard file, with Proton-style NAT-PMP port forwarding.
func TestWireGuardFileWithNATPMP(t *testing.T) {
	f := newFakePIA(t) // its WireGuard server stands in for the provider's
	priv, pub, _ := keyPair()
	if err := f.srvDev.IpcSet("replace_peers=true\npublic_key=" + hex.EncodeToString(pub) + "\nallowed_ip=10.9.0.2/32\n"); err != nil {
		t.Fatal(err)
	}
	// The gateway's NAT-PMP service.
	var maps atomic.Int32
	pc, err := f.srvNet.ListenUDP(&net.UDPAddr{Port: 5351})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			maps.Add(1)
			resp := make([]byte, 16)
			resp[1] = 128 + buf[1]
			binary.BigEndian.PutUint16(resp[8:], 1)
			binary.BigEndian.PutUint16(resp[10:], 53186)
			copy(resp[12:], buf[8:12])
			pc.WriteTo(resp, addr)
		}
	}()
	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.9.0.2/32\nDNS = 10.9.0.1\n\n[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0\nEndpoint = 127.0.0.1:%d\n",
		base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(f.srvPub), f.wgPort)
	cfg := config.VPN{On: true, Provider: "proton", WireGuard: secret.String(conf)}
	tun := &Tunnel{Log: log.New(testWriter{t}, "", 0), Cfg: func() config.VPN { return cfg }, PFRetry: 20 * time.Millisecond, State: &memBlobs{},
		Check: 50 * time.Millisecond, Stale: time.Hour, BindEvery: time.Hour, NATEvery: 50 * time.Millisecond, NATGateway: "10.9.0.1"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tun.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "up with a port", func() bool { s := tun.Status(); return s.State == "up" && s.Port == 53186 })
	if s := tun.Status(); s.RegionName != "Proton VPN" || s.Address != "10.9.0.2" || s.Error != "" {
		t.Fatalf("status %+v", s)
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: tun.DialContext}, Timeout: 5 * time.Second}
	resp, err := hc.Get("http://10.9.0.1/hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "10.9.0.2") {
		t.Fatalf("page said %q", b)
	}
	waitFor(t, "mapping renewed", func() bool { return maps.Load() >= 6 })
}

// A provider with no port forwarding still connects, and says so.
func TestWireGuardFileNoPortForwarding(t *testing.T) {
	f := newFakePIA(t)
	priv, pub, _ := keyPair()
	f.srvDev.IpcSet("replace_peers=true\npublic_key=" + hex.EncodeToString(pub) + "\nallowed_ip=10.9.0.2/32\n")
	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.9.0.2/32\n[Peer]\nPublicKey = %s\nEndpoint = localhost:%d\n",
		base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(f.srvPub), f.wgPort)
	var mu sync.Mutex
	cfg := config.VPN{On: true, Provider: "mullvad", WireGuard: secret.String(conf)}
	tun := &Tunnel{Log: log.New(testWriter{t}, "", 0), Cfg: func() config.VPN { mu.Lock(); defer mu.Unlock(); return cfg }, PFRetry: 20 * time.Millisecond,
		State: &memBlobs{}, Check: 50 * time.Millisecond, Stale: time.Hour, BindEvery: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tun.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "up", func() bool { return tun.Up() && tun.Status().Error != "" })
	if s := tun.Status(); s.Port != 0 || !strings.Contains(s.Error, "no forwarded port") || s.RegionName != "Mullvad" {
		t.Fatalf("status %+v", s)
	}
	// A static port (AirVPN): used as given.
	mu.Lock()
	cfg.Provider, cfg.Port = "airvpn", 41234
	mu.Unlock()
	tun.Restart()
	waitFor(t, "static port", func() bool { s := tun.Status(); return s.State == "up" && s.Port == 41234 && s.Error == "" })
}

func TestNordConf(t *testing.T) {
	pub := base64.StdEncoding.EncodeToString(append(make([]byte, 31), 7))
	key := base64.StdEncoding.EncodeToString(append(make([]byte, 31), 9))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/services/credentials":
			if u, p, _ := r.BasicAuth(); u != "token" || p != "tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(w, `{"nordlynx_private_key":%q}`, key)
		case "/v1/servers/countries":
			io.WriteString(w, `[{"id":153,"code":"NL"},{"id":228,"code":"US"}]`)
		case "/v1/servers/recommendations":
			if r.URL.Query().Get("filters[country_id]") != "153" || r.URL.Query().Get("filters[servers_technologies][identifier]") != "wireguard_udp" {
				io.WriteString(w, `[]`)
				return
			}
			fmt.Fprintf(w, `[{"hostname":"nl900.nordvpn.com","station":"1.2.3.4","technologies":[{"identifier":"openvpn_udp","metadata":[]},{"identifier":"wireguard_udp","metadata":[{"name":"public_key","value":%q}]}]}]`, pub)
		}
	}))
	defer srv.Close()
	n := &Nord{Base: srv.URL}
	c, host, err := n.Conf(context.Background(), "tok", "nl")
	if err != nil {
		t.Fatal(err)
	}
	if host != "nl900.nordvpn.com" || c.Endpoint != "1.2.3.4:51820" || c.Address.String() != "10.5.0.2" || c.Private[31] != 9 || c.Public[31] != 7 {
		t.Fatalf("conf %+v %s", c, host)
	}
	if _, _, err := n.Conf(context.Background(), "bad", ""); err == nil || !strings.Contains(err.Error(), "refused the access token") {
		t.Fatalf("bad token: %v", err)
	}
}

// A PIA server that cannot be reached: the error never shows the token.
func TestPIAErrorHidesToken(t *testing.T) {
	api := &API{Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := api.AddKey(ctx, Server{IP: "127.0.0.1", CN: "nl-1"}, "SECRET-TOKEN", "pub")
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") || !strings.Contains(err.Error(), "nl-1") {
		t.Fatalf("err %v", err)
	}
}
