package vpn

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

type memBlobs struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (b *memBlobs) Load(n string) ([]byte, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[n]
	return v, ok, nil
}

func (b *memBlobs) Save(n string, v []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string][]byte{}
	}
	b.m[n] = append([]byte(nil), v...)
	return nil
}

// fakePIA is PIA in miniature: sign-in, server list, and a real WireGuard
// server (on loopback UDP) whose tunnel side runs the port-forward service
// and a small web page.
type fakePIA struct {
	t        *testing.T
	api      *API
	srvDev   *device.Device
	srvNet   *netstack.Net
	srvPriv  []byte
	srvPub   []byte
	wgPort   int
	binds    atomic.Int32
	sigs     atomic.Int32
	keys     atomic.Int32
	refuseIn atomic.Bool
}

func newFakePIA(t *testing.T) *fakePIA {
	f := &fakePIA{t: t}
	f.srvPriv, f.srvPub, _ = keyPair()

	// The server's side of the tunnel.
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.9.0.1")}, nil, 1420)
	if err != nil {
		t.Fatal(err)
	}
	f.srvNet = tnet
	f.srvDev = device.NewDevice(tdev, conn.NewStdNetBind(), device.NewLogger(device.LogLevelError, "srv: "))
	free, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.wgPort = free.LocalAddr().(*net.UDPAddr).Port
	free.Close()
	if err := f.srvDev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\n", hex.EncodeToString(f.srvPriv), f.wgPort)); err != nil {
		t.Fatal(err)
	}
	if err := f.srvDev.Up(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.srvDev.Close)

	// The HTTPS services (addKey outside, port forward inside). httptest's
	// certificate is for example.com, standing in for the server's name.
	mux := http.NewServeMux()
	mux.HandleFunc("/addKey", func(w http.ResponseWriter, r *http.Request) {
		f.keys.Add(1)
		pub, _ := base64.StdEncoding.DecodeString(r.URL.Query().Get("pubkey"))
		if r.URL.Query().Get("pt") != "tok" || len(pub) != 32 {
			json.NewEncoder(w).Encode(map[string]string{"status": "ERROR"})
			return
		}
		if err := f.srvDev.IpcSet("replace_peers=true\npublic_key=" + hex.EncodeToString(pub) + "\nallowed_ip=10.9.0.2/32\n"); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(Key{Status: "OK", ServerKey: base64.StdEncoding.EncodeToString(f.srvPub), ServerPort: f.wgPort,
			ServerIP: "127.0.0.1", ServerVIP: "10.9.0.1", PeerIP: "10.9.0.2", DNSServers: []string{"10.9.0.1"}})
	})
	mux.HandleFunc("/getSignature", func(w http.ResponseWriter, r *http.Request) {
		f.sigs.Add(1)
		p, _ := json.Marshal(map[string]any{"port": 51413, "expires_at": time.Now().Add(60 * 24 * time.Hour)})
		json.NewEncoder(w).Encode(map[string]string{"status": "OK", "payload": base64.StdEncoding.EncodeToString(p), "signature": "sig"})
	})
	mux.HandleFunc("/bindPort", func(w http.ResponseWriter, r *http.Request) {
		f.binds.Add(1)
		st := "OK"
		if r.URL.Query().Get("signature") != "sig" {
			st = "ERROR"
		}
		json.NewEncoder(w).Encode(map[string]string{"status": st})
	})
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "through the tunnel from "+r.RemoteAddr)
	})

	outside := httptest.NewTLSServer(mux)
	t.Cleanup(outside.Close)
	inside := httptest.NewUnstartedServer(mux)
	ln, err := tnet.ListenTCP(&net.TCPAddr{Port: 19999})
	if err != nil {
		t.Fatal(err)
	}
	inside.Listener = ln
	inside.StartTLS()
	t.Cleanup(inside.Close)
	plain := &http.Server{Handler: mux}
	pl, _ := tnet.ListenTCP(&net.TCPAddr{Port: 80})
	go plain.Serve(pl)
	t.Cleanup(func() { plain.Close() })

	roots := x509.NewCertPool()
	roots.AddCert(outside.Certificate())
	roots.AddCert(inside.Certificate())

	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			r.ParseMultipartForm(1 << 16)
			if r.FormValue("username") != "p1234567" || r.FormValue("password") != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `{"token":"tok"}`)
		case "/servers":
			io.WriteString(w, `{"regions":[{"id":"nopf","name":"A Place","port_forward":false,"servers":{"wg":[{"ip":"127.0.0.1","cn":"example.com"}]}},`+
				`{"id":"pf","name":"Port Place","port_forward":true,"servers":{"wg":[{"ip":"127.0.0.1","cn":"example.com"}]}},`+
				`{"id":"gone","name":"Gone","offline":true,"servers":{"wg":[{"ip":"1.1.1.1","cn":"x"}]}}]}`+"\nSIGNATURE\n")
		}
	}))
	t.Cleanup(web.Close)

	outsideAddr := outside.Listener.Addr().String()
	f.api = &API{
		TokenURL:  web.URL + "/token",
		ServerURL: web.URL + "/servers",
		Roots:     roots,
		HTTP:      web.Client(),
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if f.refuseIn.Load() {
				return nil, errors.New("refused")
			}
			var d net.Dialer
			return d.DialContext(ctx, network, outsideAddr) // the server's :1337
		},
	}
	return f
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTunnelEndToEnd(t *testing.T) {
	f := newFakePIA(t)
	var mu sync.Mutex
	cfg := config.VPN{On: true, User: "p1234567", Pass: secret.String("pw")}
	blobs := &memBlobs{}
	tun := &Tunnel{
		Log:       log.New(testWriter{t}, "", 0),
		Cfg:       func() config.VPN { mu.Lock(); defer mu.Unlock(); return cfg },
		API:       f.api,
		State:     blobs,
		Check:     50 * time.Millisecond,
		Stale:     time.Hour,
		BindEvery: 100 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Off: everything refused.
	if _, err := tun.DialContext(ctx, "tcp", "10.9.0.1:80"); !errors.Is(err, ErrDown) {
		t.Fatalf("dial before up = %v, want ErrDown", err)
	}
	done := make(chan struct{})
	go func() { tun.Run(ctx); close(done) }()

	waitFor(t, "tunnel up with a forwarded port", func() bool {
		s := tun.Status()
		if s.Error != "" {
			t.Logf("status: %+v", s)
		}
		return s.State == "up" && s.Port != 0
	})
	s := tun.Status()
	if s.Region != "pf" || s.Port != 51413 || s.Address != "10.9.0.2" || s.Generation != 1 {
		t.Fatalf("status = %+v", s)
	}

	// Traffic really goes through WireGuard: the page inside sees the tunnel address.
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

	// Listening on the forwarded port inside the tunnel, reached from the server side.
	ln, err := tun.ListenTCP(s.Port)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err == nil {
			io.WriteString(c, "peer hello")
			c.Close()
		}
	}()
	pc, err := f.srvNet.DialContext(ctx, "tcp", fmt.Sprintf("10.9.0.2:%d", s.Port))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(pc)
	pc.Close()
	ln.Close()
	if string(got) != "peer hello" {
		t.Fatalf("incoming = %q", got)
	}

	// The port is kept open, and the grant is saved for reuse.
	waitFor(t, "port kept open", func() bool { return f.binds.Load() >= 3 })
	if _, ok := tun.loadSig(); !ok {
		t.Fatal("grant not saved")
	}
	if n := f.sigs.Load(); n != 1 {
		t.Fatalf("asked for a port %d times, want 1", n)
	}

	// Settings change: reconnect, reusing the saved port grant.
	tun.Restart()
	waitFor(t, "reconnected", func() bool { s := tun.Status(); return s.State == "up" && s.Generation == 2 && s.Port != 0 })
	if n := f.sigs.Load(); n != 1 {
		t.Fatalf("asked for a port %d times after reconnect, want 1 (reuse)", n)
	}

	// Turned off: the kill switch refuses everything.
	mu.Lock()
	cfg.On = false
	mu.Unlock()
	tun.Restart()
	waitFor(t, "off", func() bool { return tun.Status().State == "off" })
	if _, err := tun.DialContext(ctx, "tcp", "10.9.0.1:80"); !errors.Is(err, ErrDown) {
		t.Fatalf("dial after off = %v, want ErrDown", err)
	}
	if _, err := tun.ListenUDP(0); !errors.Is(err, ErrDown) {
		t.Fatalf("listen after off = %v, want ErrDown", err)
	}
	cancel()
	<-done
}

func TestTunnelBadLoginAndRegions(t *testing.T) {
	f := newFakePIA(t)
	if _, err := f.api.Token(context.Background(), "p1234567", "nope"); err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Fatalf("bad login = %v", err)
	}
	regions, err := f.api.Regions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 || regions[0].ID != "pf" {
		t.Fatalf("regions = %+v (offline dropped, port-forward first)", regions)
	}
	if g, _ := Pick(regions, ""); g.ID != "pf" {
		t.Fatalf("default pick = %s", g.ID)
	}
	if g, _ := Pick(regions, "nopf"); g.ID != "nopf" {
		t.Fatalf("pick nopf = %s", g.ID)
	}
	if _, err := Pick(regions, "mars"); err == nil {
		t.Fatal("unknown region accepted")
	}

	// A failed connect reports why and stays down.
	tun := &Tunnel{Cfg: func() config.VPN { return config.VPN{On: true, User: "p1234567", Pass: "wrong"} }, API: f.api}
	ctx, cancel := context.WithCancel(context.Background())
	go tun.Run(ctx)
	waitFor(t, "down", func() bool { return tun.Status().State == "down" })
	if !strings.Contains(tun.Status().Error, "did not accept") {
		t.Fatalf("error = %q", tun.Status().Error)
	}
	cancel()
}

func TestKeyPair(t *testing.T) {
	priv, pub, err := keyPair()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := curve25519.X25519(priv, curve25519.Basepoint)
	if hex.EncodeToString(again) != hex.EncodeToString(pub) || priv[0]&7 != 0 {
		t.Fatal("bad key pair")
	}
	_ = rand.Reader
}
