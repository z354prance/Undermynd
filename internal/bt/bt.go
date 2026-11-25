// Package bt is HyveMynd's own BitTorrent client. Every socket it uses (peer
// connections in and out, trackers, DHT, web seeds) comes from the VPN
// tunnel, and names are looked up through the tunnel too: when the tunnel is
// down the client is stopped, so nothing can leave any other way.
//
// Downloads go to HyveMynd's own work folder, one folder per job, and are
// handed to the pipeline the same way any download client's are.
package bt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/dialer"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"github.com/z354prance/undermynd/internal/backend"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/state"
)

// Net is the network the client may use: the VPN tunnel.
type Net interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	ListenTCP(port int) (net.Listener, error)
	ListenUDP(port int) (net.PacketConn, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	Up() bool
	Generation() uint64 // changes when the tunnel reconnects
	Port() int          // the forwarded port, or 0
}

// UploadLimit is the most the client sends to other people while it
// downloads, in bytes a second.
const UploadLimit = 1 << 20

// Name is the backend name jobs record.
const Name = "local"

// rec is one job's torrent, kept so downloads carry on after a restart.
type rec struct {
	Magnet  string    `json:"magnet,omitempty"`
	Torrent string    `json:"torrent,omitempty"` // base64 .torrent
	Info    string    `json:"info,omitempty"`    // base64 info dictionary, once known (magnets)
	Save    string    `json:"save"`
	Done    bool      `json:"done,omitempty"`
	Added   time.Time `json:"added"`
}

// Client runs the torrent client while the tunnel is up.
type Client struct {
	Net   Net
	Dir   string // one folder per job under here
	State state.Blobs
	Log   *log.Logger
	// Every is how often the client checks the tunnel. Default 2 seconds.
	Every time.Duration

	mu     sync.Mutex
	recs   map[string]*rec
	tors   map[string]*torrent.Torrent
	cl     *torrent.Client
	gen    uint64
	pc     storage.PieceCompletion
	extra  []io.Closer // our listener and DHT socket
	loaded bool
}

const stateName = "bt.json"

func (c *Client) logf(format string, a ...any) {
	if c.Log != nil {
		c.Log.Printf("torrents: "+format, a...)
	}
}

func (c *Client) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	c.recs, c.tors = map[string]*rec{}, map[string]*torrent.Torrent{}
	if c.State == nil {
		return
	}
	if b, ok, _ := c.State.Load(stateName); ok {
		_ = json.Unmarshal(b, &c.recs)
		if c.recs == nil {
			c.recs = map[string]*rec{}
		}
	}
}

func (c *Client) save() {
	if c.State == nil {
		return
	}
	b, _ := json.Marshal(c.recs)
	_ = c.State.Save(stateName, b)
}

// Name implements backend.Backend.
func (c *Client) Name() string { return Name }

// Ready says whether the client can take a download now.
func (c *Client) Ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cl != nil
}

// Run keeps the client in step with the tunnel until ctx ends.
func (c *Client) Run(ctx context.Context) {
	every := c.Every
	if every == 0 {
		every = 2 * time.Second
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		c.sync()
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.stop()
			if c.pc != nil {
				c.pc.Close()
				c.pc = nil
			}
			c.mu.Unlock()
			return
		case <-tk.C:
		}
	}
}

// sync starts, stops or restarts the client to match the tunnel, and saves
// what magnets have learned.
func (c *Client) sync() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	up, gen := c.Net.Up(), c.Net.Generation()
	if c.cl != nil && (!up || gen != c.gen) {
		c.logf("the VPN changed; pausing downloads")
		c.stop()
	}
	if c.cl == nil && up {
		if err := c.start(gen); err != nil {
			c.logf("could not start: %v", err)
			c.stop()
			return
		}
		for id, r := range c.recs {
			if !r.Done {
				if err := c.add(id, r); err != nil {
					c.logf("job %s: %v", id, err)
				}
			}
		}
	}
	changed := false
	for id, t := range c.tors {
		r := c.recs[id]
		if r != nil && r.Info == "" && r.Torrent == "" && t.Info() != nil {
			mi := t.Metainfo()
			r.Info, changed = base64.StdEncoding.EncodeToString(mi.InfoBytes), true
		}
	}
	if changed {
		c.save()
	}
}

func (c *Client) start(gen uint64) error {
	if c.pc == nil {
		if err := os.MkdirAll(filepath.Join(c.Dir, ".state"), 0o755); err != nil {
			return err
		}
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(c.Dir, ".state"))
		if err != nil {
			return fmt.Errorf("opening the download records: %w", err)
		}
		c.pc = pc
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = c.Dir
	cfg.DefaultStorage = c.storage(c.Dir)
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	// No sockets of the library's own: only the tunnel's, added below.
	cfg.DisableTCP, cfg.DisableUTP, cfg.NoDHT = true, true, true
	cfg.NoDefaultPortForwarding = true // no UPnP on the home router
	cfg.DisableIPv6 = true
	cfg.DisableWebtorrent = true // WebRTC would go around the tunnel
	cfg.LocalServiceDiscovery = nil
	cfg.Seed = false // nothing is shared once a download finishes
	// While downloading, share only a little (peers favour those who share).
	cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(UploadLimit), 256<<10)
	cfg.ListenPort = 0
	cfg.HTTPProxy = nil
	cfg.HTTPDialContext = c.Net.DialContext
	cfg.TrackerDialContext = c.Net.DialContext
	cfg.TrackerListenPacket = func(string, string) (net.PacketConn, error) { return c.Net.ListenUDP(0) }
	cfg.DhtStartingNodes = func(string) dht.StartingNodesGetter { return c.bootstrap }
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return err
	}
	c.cl, c.gen = cl, gen
	port := c.Net.Port()
	ln, err := c.Net.ListenTCP(port)
	if err != nil {
		return fmt.Errorf("listening in the VPN: %w", err)
	}
	c.extra = append(c.extra, ln)
	cl.AddListener(ln)
	cl.AddDialer(dialer.WithNetwork{Network: "tcp", Dialer: c.Net})
	if pc, err := c.Net.ListenUDP(port); err == nil {
		sp := &steady{PacketConn: pc, done: make(chan struct{})}
		c.extra = append(c.extra, sp) // closed after the DHT (reverse order)
		if ds, err := cl.NewAnacrolixDhtServer(sp); err == nil {
			cl.AddDhtServer(torrent.AnacrolixDhtServerWrapper{Server: ds})
			c.extra = append(c.extra, closer(ds.Close))
		}
	}
	c.logf("started (incoming port %d)", port)
	return nil
}

// steady is the DHT's socket. The DHT library crashes the process if its
// socket fails before it is shut down, which a dropped tunnel would do; here a
// failed read waits until the socket is closed in order.
type steady struct {
	net.PacketConn
	once sync.Once
	done chan struct{}
}

func (s *steady) ReadFrom(b []byte) (int, net.Addr, error) {
	n, a, err := s.PacketConn.ReadFrom(b)
	if err != nil {
		<-s.done
	}
	return n, a, err
}

func (s *steady) Close() error {
	s.once.Do(func() { close(s.done) })
	return s.PacketConn.Close()
}

type closer func()

func (f closer) Close() error { f(); return nil }

// bootstrap finds DHT starting nodes, looking names up through the tunnel.
func (c *Client) bootstrap() ([]dht.Addr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out []dht.Addr
	for _, hp := range dht.DefaultGlobalBootstrapHostPorts {
		host, port, err := net.SplitHostPort(hp)
		if err != nil {
			continue
		}
		ips, err := c.Net.LookupHost(ctx, host)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if a, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(ip, port)); err == nil && a.IP.To4() != nil {
				out = append(out, dht.NewAddr(a))
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no DHT starting nodes could be looked up")
	}
	return out, nil
}

// stop closes the client; downloads resume from disk when it starts again.
func (c *Client) stop() {
	if c.cl != nil {
		c.cl.Close()
	}
	for i := len(c.extra) - 1; i >= 0; i-- {
		c.extra[i].Close()
	}
	c.cl, c.extra = nil, nil
	c.tors = map[string]*torrent.Torrent{}
}

// keep is the piece records shared by every download; one download's storage
// closing must not close them.
type keep struct{ storage.PieceCompletion }

func (keep) Close() error { return nil }

func (c *Client) storage(dir string) storage.ClientImpl {
	return storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   dir,
		PieceCompletion: keep{c.pc},
		// The pipeline moves finished files away; mapped files must not be
		// changed underneath.
		DisableMmap: true,
		// Write files in place: renaming ".part" files when they finish can
		// lag behind "complete", and the pipeline moves the files right then.
		UsePartFiles: g.Some(false),
	})
}

// add gives a job's torrent to the running client.
func (c *Client) add(id string, r *rec) error {
	var spec *torrent.TorrentSpec
	switch {
	case r.Torrent != "":
		b, err := base64.StdEncoding.DecodeString(r.Torrent)
		if err != nil {
			return err
		}
		mi, err := metainfo.Load(strings.NewReader(string(b)))
		if err != nil {
			return backend.Permanent{Msg: "the torrent file could not be read"}
		}
		if spec, err = torrent.TorrentSpecFromMetaInfoErr(mi); err != nil {
			return backend.Permanent{Msg: "the torrent file could not be read"}
		}
	case r.Magnet != "":
		var err error
		if spec, err = torrent.TorrentSpecFromMagnetUri(r.Magnet); err != nil {
			return backend.Permanent{Msg: "the magnet link could not be read"}
		}
		if r.Info != "" {
			if b, err := base64.StdEncoding.DecodeString(r.Info); err == nil {
				spec.InfoBytes = b
			}
		}
	default:
		return backend.Permanent{Msg: "job has neither a magnet nor a torrent file"}
	}
	c.private(spec)
	spec.Storage = c.storage(r.Save)
	t, _, err := c.cl.AddTorrentSpec(spec)
	if err != nil {
		return err
	}
	c.tors[id] = t
	go func() {
		select {
		case <-t.GotInfo():
			t.DownloadAll()
		case <-t.Closed():
		}
	}()
	return nil
}

// private keeps a torrent's lookups inside the tunnel: UDP tracker names are
// looked up through it now (the library would use the host's DNS), and
// anything that would fetch from outside or look names up elsewhere is dropped.
func (c *Client) private(spec *torrent.TorrentSpec) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var tiers [][]string
	for _, tier := range spec.Trackers {
		var keep []string
		for _, tr := range tier {
			u, err := url.Parse(tr)
			if err != nil {
				continue
			}
			switch u.Scheme {
			case "http", "https":
				keep = append(keep, tr) // dialled through the tunnel by name
			case "udp":
				host := u.Hostname()
				if net.ParseIP(host) == nil {
					ips, err := c.Net.LookupHost(ctx, host)
					if err != nil || len(ips) == 0 {
						continue
					}
					host = ips[0]
				}
				if ip := net.ParseIP(host); ip == nil || ip.To4() == nil {
					continue
				}
				u.Host = net.JoinHostPort(host, u.Port())
				keep = append(keep, u.String())
			}
		}
		if len(keep) > 0 {
			tiers = append(tiers, keep)
		}
	}
	spec.Trackers = tiers
	spec.Sources = nil // fetching the .torrent elsewhere
	var nodes []string
	for _, n := range spec.DhtNodes {
		if h, _, err := net.SplitHostPort(n); err == nil && net.ParseIP(h) != nil {
			nodes = append(nodes, n)
		}
	}
	spec.DhtNodes = nodes
}

// Add implements backend.Backend.
func (c *Client) Add(ctx context.Context, j job.Job) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	if c.cl == nil {
		return "", "", fmt.Errorf("%w: waiting for the VPN", backend.ErrBusy)
	}
	save := filepath.Join(c.Dir, j.ID)
	r := &rec{Save: save, Added: time.Now()}
	switch {
	case j.TorrentPath != "":
		b, err := os.ReadFile(j.TorrentPath)
		if err != nil {
			return "", "", err
		}
		r.Torrent = base64.StdEncoding.EncodeToString(b)
	case j.Magnet != "":
		r.Magnet = j.Magnet
	default:
		return "", "", backend.Permanent{Msg: "job has neither a magnet nor a torrent file"}
	}
	if err := os.MkdirAll(save, 0o755); err != nil {
		return "", "", err
	}
	if old := c.tors[j.ID]; old != nil {
		old.Drop()
	}
	if err := c.add(j.ID, r); err != nil {
		return "", "", err
	}
	c.recs[j.ID] = r
	c.save()
	return j.ID, save, nil
}

// Status implements backend.Backend. While the VPN is down it answers
// ErrBusy, so the pipeline waits instead of counting a stall.
func (c *Client) Status(ctx context.Context, id string) (backend.Item, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	r := c.recs[id]
	if r == nil {
		return backend.Item{Status: backend.Missing}, nil
	}
	if r.Done {
		return backend.Item{Status: backend.Complete, Progress: 1}, nil
	}
	t := c.tors[id]
	if c.cl == nil || t == nil {
		return backend.Item{}, fmt.Errorf("%w: waiting for the VPN", backend.ErrBusy)
	}
	info := t.Info()
	if info == nil {
		return backend.Item{Status: backend.Downloading}, nil
	}
	total := info.TotalLength() // (the library's Length is not safe to read here)
	if total > 0 && t.BytesMissing() == 0 {
		// Finished and verified: close it so the files can be moved.
		t.Drop()
		<-t.Closed()
		delete(c.tors, id)
		r.Done = true
		c.save()
		return backend.Item{Status: backend.Complete, Progress: 1}, nil
	}
	p := 0.0
	if total > 0 {
		p = float64(t.BytesCompleted()) / float64(total)
	}
	return backend.Item{Status: backend.Downloading, Progress: p}, nil
}

// Files implements backend.Backend: the files in the job's folder.
func (c *Client) Files(ctx context.Context, id string) ([]backend.File, error) {
	c.mu.Lock()
	r := c.recs[id]
	c.mu.Unlock()
	if r == nil {
		return nil, backend.Permanent{Msg: "the built-in client has no such download"}
	}
	var out []backend.File
	err := filepath.WalkDir(r.Save, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(r.Save, p)
		out = append(out, backend.File{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	return out, err
}

// Remove implements backend.Backend.
func (c *Client) Remove(ctx context.Context, id string, deleteFiles bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	if t := c.tors[id]; t != nil {
		t.Drop()
		<-t.Closed()
		delete(c.tors, id)
	}
	r := c.recs[id]
	delete(c.recs, id)
	c.save()
	if deleteFiles {
		dir := filepath.Join(c.Dir, id)
		if r != nil && r.Save != "" {
			dir = r.Save
		}
		// Only ever a job folder of ours.
		if filepath.Dir(dir) == filepath.Clean(c.Dir) && id != "" {
			return os.RemoveAll(dir)
		}
	}
	return nil
}

// Stats is what the admin shows.
type Stats struct {
	Running    bool  `json:"running"`
	Active     int   `json:"active"`
	Peers      int   `json:"peers"`
	DownRate   int64 `json:"downRate"` // bytes a second, roughly
	Downloaded int64 `json:"downloaded"`
	Uploaded   int64 `json:"uploaded"`
	Port       int   `json:"port"`
}

// Stats reports the client's activity.
func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Stats{Running: c.cl != nil, Active: len(c.tors), Port: c.Net.Port()}
	if c.cl == nil {
		return s
	}
	cs := c.cl.Stats()
	s.Downloaded = cs.BytesReadData.Int64()
	s.Uploaded = cs.BytesWrittenData.Int64()
	for _, t := range c.tors {
		s.Peers += t.Stats().ActivePeers
	}
	return s
}

var _ backend.Backend = (*Client)(nil)
