package vpn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/state"
)

// ErrDown is returned by every network call while the tunnel is not up: the
// kill switch. Nothing the built-in clients do can leave without the tunnel.
var ErrDown = errors.New("the VPN is not connected")

// Status is what the admin shows.
type Status struct {
	State       string    `json:"state"` // off | connecting | up | down
	Region      string    `json:"region,omitempty"`
	RegionName  string    `json:"regionName,omitempty"`
	Server      string    `json:"server,omitempty"`
	Address     string    `json:"address,omitempty"` // this end's address in the tunnel
	Port        int       `json:"port,omitempty"`    // the forwarded port (0: none)
	PortExpires time.Time `json:"portExpires,omitempty"`
	Since       time.Time `json:"since,omitempty"`
	Error       string    `json:"error,omitempty"`
	Generation  uint64    `json:"generation"` // goes up each time the tunnel comes up
}

// Tunnel keeps the PIA tunnel up while the setting is on.
type Tunnel struct {
	Cfg   func() config.VPN
	API   *API
	Log   *log.Logger
	State state.Blobs
	Bind  conn.Bind // nil: the host's UDP sockets

	// Health checks: every Check, WireGuard's last handshake with the
	// server; none for Stale (WireGuard renews it every two minutes while
	// the tunnel works) reconnects. BindEvery keeps the forwarded port open.
	Check     time.Duration
	Stale     time.Duration
	BindEvery time.Duration
	// PFRetry is the gap between port-forward tries right after connecting
	// (later tries are 40 times further apart). Default 3 seconds.
	PFRetry time.Duration

	mu       sync.RWMutex
	dev      *device.Device
	tnet     *netstack.Net
	gw       Server
	local    netip.Addr // this end's address in the tunnel
	pfWanted bool       // the region forwards ports
	token    string
	sig      Signature
	status   Status
	restart  chan struct{}
	once     sync.Once
}

func (t *Tunnel) init() {
	t.once.Do(func() {
		t.restart = make(chan struct{}, 1)
		if t.API == nil {
			t.API = PIA()
		}
		if t.Check == 0 {
			t.Check = time.Minute
		}
		if t.Stale == 0 {
			t.Stale = 5 * time.Minute
		}
		if t.PFRetry == 0 {
			t.PFRetry = 3 * time.Second
		}
		if t.BindEvery == 0 {
			t.BindEvery = 15 * time.Minute
		}
		t.status.State = "off"
	})
}

// Restart reconnects (settings changed).
func (t *Tunnel) Restart() {
	t.init()
	select {
	case t.restart <- struct{}{}:
	default:
	}
}

// Status reports the tunnel's state.
func (t *Tunnel) Status() Status {
	t.init()
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// Up says whether the tunnel is up.
func (t *Tunnel) Up() bool { return t.Status().State == "up" }

// Port is the forwarded port, or 0.
func (t *Tunnel) Port() int { return t.Status().Port }

func (t *Tunnel) net() (*netstack.Net, error) {
	n, _, err := t.netAddr()
	return n, err
}

func (t *Tunnel) netAddr() (*netstack.Net, netip.Addr, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.tnet == nil || t.status.State != "up" {
		return nil, netip.Addr{}, ErrDown
	}
	return t.tnet, t.local, nil
}

// DialContext connects through the tunnel (names are looked up through it too).
func (t *Tunnel) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	n, err := t.net()
	if err != nil {
		return nil, err
	}
	switch network {
	case "tcp6", "udp6":
		return nil, errors.New("the VPN carries IPv4 only")
	case "tcp4":
		network = "tcp"
	case "udp4":
		network = "udp"
	}
	return n.DialContext(ctx, network, addr)
}

// ListenTCP listens on a port of this end of the tunnel.
func (t *Tunnel) ListenTCP(port int) (net.Listener, error) {
	n, local, err := t.netAddr()
	if err != nil {
		return nil, err
	}
	return n.ListenTCPAddrPort(netip.AddrPortFrom(local, uint16(port)))
}

// ListenUDP opens a UDP socket on a port of this end of the tunnel.
func (t *Tunnel) ListenUDP(port int) (net.PacketConn, error) {
	n, local, err := t.netAddr()
	if err != nil {
		return nil, err
	}
	// The address must be given: the network library panics on an empty one.
	return n.ListenUDPAddrPort(netip.AddrPortFrom(local, uint16(port)))
}

func (t *Tunnel) set(f func(*Status)) {
	t.mu.Lock()
	f(&t.status)
	t.mu.Unlock()
}

func (t *Tunnel) logf(format string, a ...any) {
	if t.Log != nil {
		t.Log.Printf("vpn: "+format, a...)
	}
}

// Run keeps the tunnel up while the setting is on, reconnecting after
// failures (30 seconds, doubling to 5 minutes) and when settings change.
func (t *Tunnel) Run(ctx context.Context) {
	t.init()
	for ctx.Err() == nil {
		if !t.runSafe(ctx) {
			return
		}
		// A crash inside: report it, keep HyveMynd running, try again later.
		if !t.sleep(ctx, 5*time.Minute) {
			return
		}
	}
}

// runSafe runs the tunnel, turning a panic into an error on the VPN page.
// It returns true after a panic.
func (t *Tunnel) runSafe(ctx context.Context) (crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			t.logf("crashed: %v (trying again in 5 minutes)", r)
			t.down("down", fmt.Sprintf("the VPN stopped after an internal error: %v", r))
			crashed = true
		}
	}()
	t.run(ctx)
	return false
}

func (t *Tunnel) run(ctx context.Context) {
	wait := 30 * time.Second
	for ctx.Err() == nil {
		c := t.Cfg()
		if !c.On || c.User == "" || c.Pass == "" {
			t.down("off", "")
			if !t.sleep(ctx, time.Minute) {
				return
			}
			continue
		}
		t.set(func(s *Status) { s.State, s.Error, s.Region, s.RegionName = "connecting", "", c.Region, "" })
		if err := t.connect(ctx, c); err != nil {
			t.logf("%v", err)
			t.down("down", err.Error())
			if !t.sleep(ctx, wait) {
				return
			}
			if wait *= 2; wait > 5*time.Minute {
				wait = 5 * time.Minute
			}
			continue
		}
		wait = 30 * time.Second
		reason := t.maintain(ctx, c)
		t.logf("reconnecting: %s", reason)
		t.down("connecting", reason)
	}
	t.down("off", "")
}

// sleep waits, ending early on a restart; false when ctx is done.
func (t *Tunnel) sleep(ctx context.Context, d time.Duration) bool {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.restart:
	case <-tm.C:
	}
	return true
}

// down closes the tunnel; every dial and listener through it fails from here.
func (t *Tunnel) down(state, why string) {
	t.mu.Lock()
	dev := t.dev
	t.dev, t.tnet = nil, nil
	t.status.State, t.status.Error, t.status.Port = state, why, 0
	t.mu.Unlock()
	if dev != nil {
		dev.Close()
	}
}

func (t *Tunnel) connect(ctx context.Context, c config.VPN) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	token, err := t.API.Token(ctx, c.User, string(c.Pass))
	if err != nil {
		return err
	}
	regions, err := t.API.Regions(ctx)
	if err != nil {
		return err
	}
	var region Region
	if c.Region == "" {
		region, err = t.API.Fastest(ctx, regions)
	} else {
		region, err = Pick(regions, c.Region)
	}
	if err != nil {
		return err
	}
	srv := region.Servers.WG[randInt(len(region.Servers.WG))]
	priv, pub, err := keyPair()
	if err != nil {
		return err
	}
	k, err := t.API.AddKey(ctx, srv, token, base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		return err
	}
	local, err := netip.ParseAddr(k.PeerIP)
	if err != nil {
		return errors.New("PIA gave an address that could not be read")
	}
	var dns []netip.Addr
	for _, d := range k.DNSServers {
		if a, err := netip.ParseAddr(d); err == nil {
			dns = append(dns, a)
		}
	}
	serverKey, err := base64.StdEncoding.DecodeString(k.ServerKey)
	if err != nil || len(serverKey) != 32 {
		return errors.New("PIA gave a server key that could not be read")
	}
	endpointIP := k.ServerIP
	if endpointIP == "" {
		endpointIP = srv.IP
	}
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{local}, dns, 1420)
	if err != nil {
		return fmt.Errorf("starting the tunnel: %w", err)
	}
	bind := t.Bind
	if bind == nil {
		bind = conn.NewDefaultBind()
	}
	dev := device.NewDevice(tdev, bind, device.NewLogger(device.LogLevelError, "vpn: "))
	cfg := fmt.Sprintf("private_key=%s\npublic_key=%s\nendpoint=%s\nallowed_ip=0.0.0.0/0\npersistent_keepalive_interval=25\n",
		hex.EncodeToString(priv), hex.EncodeToString(serverKey), net.JoinHostPort(endpointIP, fmt.Sprint(k.ServerPort)))
	if err := dev.IpcSet(cfg); err != nil {
		dev.Close()
		return fmt.Errorf("setting up the tunnel: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return fmt.Errorf("starting the tunnel: %w", err)
	}
	// Port forwarding is asked of the server's public address, through the
	// tunnel (as PIA's own scripts do), not of its address inside the tunnel.
	gw := Server{IP: srv.IP, CN: srv.CN}
	t.mu.Lock()
	t.dev, t.tnet, t.gw, t.local, t.pfWanted = dev, tnet, gw, local, region.PortForward
	t.status = Status{State: "up", Region: region.ID, RegionName: region.Name, Server: srv.CN, Address: k.PeerIP, Since: time.Now(), Generation: t.status.Generation + 1}
	t.mu.Unlock()
	t.logf("connected to PIA %s (%s)", region.Name, srv.CN)
	if region.PortForward {
		// The server may need a moment after the handshake: a few tries now,
		// then again every couple of minutes while connected (maintain).
		var err error
		for try := 0; try < 3; try++ {
			if err = t.forward(ctx, token); err == nil {
				break
			}
			if !sleepCtx(ctx, t.PFRetry) {
				break
			}
		}
		if err != nil {
			t.logf("%v (trying again in a couple of minutes)", err)
			t.set(func(s *Status) { s.Error = "no forwarded port yet, trying again: " + err.Error() })
		}
	} else {
		t.set(func(s *Status) {
			s.Error = "this region has no port forwarding; downloads work, but fewer peers can connect"
		})
	}
	return nil
}

// forward gets (or reuses) the forwarded port and binds it.
func (t *Tunnel) forward(ctx context.Context, token string) error {
	sg, ok := t.loadSig()
	if !ok || time.Until(sg.Expires) < 24*time.Hour {
		var err error
		if sg, err = t.API.GetSignature(ctx, t.DialContext, t.gw, token); err != nil {
			return err
		}
		t.saveSig(sg)
	}
	if err := t.API.BindPort(ctx, t.DialContext, t.gw, sg); err != nil {
		// A saved grant can be refused after a long gap: ask for a new one.
		if ok {
			if sg, err = t.API.GetSignature(ctx, t.DialContext, t.gw, token); err != nil {
				return err
			}
			t.saveSig(sg)
			err = t.API.BindPort(ctx, t.DialContext, t.gw, sg)
		}
		if err != nil {
			return err
		}
	}
	t.mu.Lock()
	t.sig = sg
	t.status.Port, t.status.PortExpires, t.status.Error = sg.Port, sg.Expires, ""
	t.mu.Unlock()
	t.logf("forwarded port %d", sg.Port)
	return nil
}

// maintain watches the tunnel until it should be reconnected; it returns why.
func (t *Tunnel) maintain(ctx context.Context, c config.VPN) string {
	check := time.NewTicker(t.Check)
	defer check.Stop()
	bind := time.NewTicker(t.BindEvery)
	defer bind.Stop()
	retry := time.NewTicker(t.PFRetry * 40)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return "stopping"
		case <-t.restart:
			return "settings changed"
		case <-check.C:
			if !t.healthy() {
				return "the tunnel stopped answering"
			}
		case <-retry.C:
			t.mu.RLock()
			need := t.pfWanted && t.sig.Payload == ""
			t.mu.RUnlock()
			if !need {
				continue
			}
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			token, err := t.API.Token(rctx, c.User, string(c.Pass))
			if err == nil {
				err = t.forward(rctx, token)
			}
			cancel()
			if err != nil {
				t.set(func(s *Status) { s.Error = "no forwarded port yet, trying again: " + err.Error() })
			} else {
				t.set(func(s *Status) { s.Error = "" })
			}
		case <-bind.C:
			t.mu.RLock()
			sg, gw := t.sig, t.gw
			t.mu.RUnlock()
			if sg.Payload == "" {
				continue
			}
			bctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := t.API.BindPort(bctx, t.DialContext, gw, sg)
			if err != nil || time.Until(sg.Expires) < 24*time.Hour {
				// Renew the grant with a fresh token.
				if token, terr := t.API.Token(bctx, c.User, string(c.Pass)); terr == nil {
					t.forgetSig()
					err = t.forward(bctx, token)
				}
			}
			cancel()
			if err != nil {
				t.set(func(s *Status) { s.Error = err.Error() })
			}
		}
	}
}

// healthy says whether WireGuard has shaken hands with the server recently
// (or the tunnel is still new).
func (t *Tunnel) healthy() bool {
	t.mu.RLock()
	dev, since := t.dev, t.status.Since
	t.mu.RUnlock()
	if dev == nil {
		return false
	}
	last := lastHandshake(dev)
	if last.IsZero() {
		last = since
	}
	return time.Since(last) < t.Stale
}

// lastHandshake reads the peer's last handshake time from WireGuard.
func lastHandshake(dev *device.Device) time.Time {
	cfg, err := dev.IpcGet()
	if err != nil {
		return time.Time{}
	}
	var sec, nsec int64
	for _, line := range strings.Split(cfg, "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "last_handshake_time_sec":
			sec, _ = strconv.ParseInt(v, 10, 64)
		case "last_handshake_time_nsec":
			nsec, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, nsec)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-tm.C:
		return true
	}
}

// forgetSig drops the saved grant so forward asks for a new one.
func (t *Tunnel) forgetSig() {
	if t.State != nil {
		_ = t.State.Save(sigName, []byte("{}"))
	}
}

const sigName = "vpn-port.json"

// saved is the forwarded-port grant kept between restarts, so the same port
// is reused for its two months.
type saved struct {
	Payload   string    `json:"payload"`
	Signature string    `json:"signature"`
	Port      int       `json:"port"`
	Expires   time.Time `json:"expires"`
}

func (t *Tunnel) loadSig() (Signature, bool) {
	if t.State == nil {
		return Signature{}, false
	}
	b, ok, _ := t.State.Load(sigName)
	if !ok {
		return Signature{}, false
	}
	var v saved
	if json.Unmarshal(b, &v) != nil || v.Payload == "" || v.Port == 0 {
		return Signature{}, false
	}
	return Signature{Status: "OK", Payload: v.Payload, Signature: v.Signature, Port: v.Port, Expires: v.Expires}, true
}

func (t *Tunnel) saveSig(sg Signature) {
	if t.State == nil {
		return
	}
	b, _ := json.Marshal(saved{sg.Payload, sg.Signature, sg.Port, sg.Expires})
	_ = t.State.Save(sigName, b)
}

// keyPair makes a WireGuard key pair.
func keyPair() (priv, pub []byte, err error) {
	priv = make([]byte, 32)
	if _, err = rand.Read(priv); err != nil {
		return nil, nil, err
	}
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	pub, err = curve25519.X25519(priv, curve25519.Basepoint)
	return priv, pub, err
}

func randInt(n int) int {
	if n <= 1 {
		return 0
	}
	var b [1]byte
	_, _ = rand.Read(b[:])
	return int(b[0]) % n
}

// LookupHost resolves a name through the tunnel (PIA's DNS), never the host's.
func (t *Tunnel) LookupHost(ctx context.Context, host string) ([]string, error) {
	n, err := t.net()
	if err != nil {
		return nil, err
	}
	return n.LookupContextHost(ctx, host)
}

// Generation goes up each time the tunnel comes up (sockets from an earlier
// one no longer work).
func (t *Tunnel) Generation() uint64 { return t.Status().Generation }
