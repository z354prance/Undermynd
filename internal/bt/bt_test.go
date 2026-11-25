package bt

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/z354prance/undermynd/internal/backend"
	"github.com/z354prance/undermynd/internal/job"
)

// fakeNet is a "tunnel" on loopback that can be taken down.
type fakeNet struct {
	up      atomic.Bool
	gen     atomic.Uint64
	mu      sync.Mutex
	lookups []string
	dials   atomic.Int32
}

func (f *fakeNet) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if !f.up.Load() {
		return nil, errors.New("down")
	}
	f.dials.Add(1)
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}
func (f *fakeNet) ListenTCP(port int) (net.Listener, error) {
	return net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
}
func (f *fakeNet) ListenUDP(port int) (net.PacketConn, error) {
	return net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
}
func (f *fakeNet) LookupHost(ctx context.Context, host string) ([]string, error) {
	f.mu.Lock()
	f.lookups = append(f.lookups, host)
	f.mu.Unlock()
	if host == "tracker.example" {
		return []string{"10.1.2.3"}, nil
	}
	return nil, errors.New("no such host")
}
func (f *fakeNet) Up() bool           { return f.up.Load() }
func (f *fakeNet) Generation() uint64 { return f.gen.Load() }
func (f *fakeNet) Port() int          { return 0 }

type blobs struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (b *blobs) Load(n string) ([]byte, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[n]
	return v, ok, nil
}
func (b *blobs) Save(n string, v []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string][]byte{}
	}
	b.m[n] = append([]byte(nil), v...)
	return nil
}

// seeder makes a torrent of a small folder and seeds it on loopback.
func seeder(t *testing.T) (magnet string, content map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	content = map[string][]byte{
		"Show.S01E01.mkv": make([]byte, 3<<20),
		"Show.S01E01.nfo": []byte("info"),
	}
	root := filepath.Join(dir, "Show.S01E01")
	os.MkdirAll(root, 0o755)
	for n, b := range content {
		rand.Read(b)
		os.WriteFile(filepath.Join(root, n), b, 0o644)
	}
	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{}
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.Seed = true
	cfg.NoDHT, cfg.DisableUTP, cfg.DisableIPv6 = true, true, true
	cfg.NoDefaultPortForwarding = true
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	tor, err := cl.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	<-tor.GotInfo()
	tor.VerifyData()
	h := mi.HashInfoBytes()
	return fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=Show.S01E01&x.pe=127.0.0.1:%d", h.HexString(), cl.LocalPort()), content
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDownloadThroughTheTunnel(t *testing.T) {
	magnet, content := seeder(t)
	n := &fakeNet{}
	st := &blobs{}
	c := &Client{Net: n, Dir: t.TempDir(), State: st, Every: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	j := job.Job{ID: "job1", Name: "Show.S01E01", Magnet: magnet, Kind: "torrent"}

	// VPN down: nothing is taken, and nothing dials.
	if _, _, err := c.Add(ctx, j); !errors.Is(err, backend.ErrBusy) {
		t.Fatalf("add while down = %v, want ErrBusy", err)
	}

	n.up.Store(true)
	n.gen.Store(1)
	waitFor(t, "client running", 5*time.Second, c.Ready)
	id, save, err := c.Add(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if id != "job1" || save != filepath.Join(c.Dir, "job1") {
		t.Fatalf("add = %q %q", id, save)
	}

	// Take the VPN down at once: status waits (ErrBusy), nothing is lost.
	n.up.Store(false)
	waitFor(t, "client paused", 5*time.Second, func() bool { return !c.Ready() })
	if _, err := c.Status(ctx, id); !errors.Is(err, backend.ErrBusy) {
		t.Fatalf("status while down = %v, want ErrBusy", err)
	}

	// Back up on a new tunnel: the download carries on and finishes.
	n.gen.Store(2)
	n.up.Store(true)
	var it backend.Item
	waitFor(t, "download complete", 30*time.Second, func() bool {
		it, err = c.Status(ctx, id)
		return err == nil && it.Status == backend.Complete
	})
	if n.dials.Load() == 0 {
		t.Fatal("no peer connection went through the tunnel's dialer")
	}

	files, err := c.Files(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(save, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, content[filepath.Base(f.Path)]) {
			t.Fatalf("%s differs", f.Path)
		}
		if filepath.Dir(f.Path) != "Show.S01E01" {
			t.Fatalf("path = %s", f.Path)
		}
	}

	// Finished stays finished after a restart of HyveMynd.
	c2 := &Client{Net: n, Dir: c.Dir, State: st}
	if it, err := c2.Status(ctx, id); err != nil || it.Status != backend.Complete {
		t.Fatalf("after restart = %+v %v", it, err)
	}

	// Remove deletes only the job's folder.
	if err := c.Remove(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(save); !os.IsNotExist(err) {
		t.Fatal("job folder left behind")
	}
	if _, err := os.Stat(c.Dir); err != nil {
		t.Fatal("the downloads folder itself was removed")
	}
	if it, _ := c.Status(ctx, id); it.Status != backend.Missing {
		t.Fatalf("after remove = %+v", it)
	}
}

func TestTrackerNamesStayInTheTunnel(t *testing.T) {
	n := &fakeNet{}
	c := &Client{Net: n}
	spec, err := torrent.TorrentSpecFromMagnetUri("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567" +
		"&tr=udp%3A%2F%2Ftracker.example%3A1337%2Fannounce&tr=udp%3A%2F%2Fgone.example%3A80&tr=https%3A%2F%2Fweb.example%2Fannounce" +
		"&tr=wss%3A%2F%2Frtc.example&xs=http%3A%2F%2Fleak.example%2Fx.torrent")
	if err != nil {
		t.Fatal(err)
	}
	spec.DhtNodes = []string{"router.example:6881", "1.2.3.4:6881"}
	c.private(spec)
	want := []string{"udp://10.1.2.3:1337/announce", "https://web.example/announce"}
	if len(spec.Trackers) != 1 || fmt.Sprint(spec.Trackers[0]) != fmt.Sprint(want) {
		t.Fatalf("trackers = %v", spec.Trackers)
	}
	if len(spec.Sources) != 0 || fmt.Sprint(spec.DhtNodes) != "[1.2.3.4:6881]" {
		t.Fatalf("sources %v nodes %v", spec.Sources, spec.DhtNodes)
	}
	if fmt.Sprint(n.lookups) != "[tracker.example gone.example]" {
		t.Fatalf("lookups = %v", n.lookups)
	}
}
