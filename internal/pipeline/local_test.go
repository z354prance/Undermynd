package pipeline_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/z354prance/undermynd/internal/backend"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
)

// fakeLocal stands in for HyveMynd's own torrent client.
type fakeLocal struct {
	mu    sync.Mutex
	dir   string
	added []string
	busy  bool
	name  string
	extra map[string][]byte // more files in the download (name -> content)
	video []byte            // the video's content (default: zeros)
	done  bool
	down  bool // the VPN is not connected
}

func (f *fakeLocal) Name() string {
	if f.name != "" {
		return f.name
	}
	return "local"
}
func (f *fakeLocal) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.down
}
func (f *fakeLocal) Add(ctx context.Context, j job.Job) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, j.ID)
	save := filepath.Join(f.dir, j.ID)
	os.MkdirAll(filepath.Join(save, j.Name), 0o755)
	v := f.video
	if v == nil {
		v = make([]byte, 1000)
	}
	os.WriteFile(filepath.Join(save, j.Name, j.Name+".mkv"), v, 0o644)
	for n, b := range f.extra {
		os.WriteFile(filepath.Join(save, j.Name, n), b, 0o644)
	}
	return j.ID, save, nil
}
func (f *fakeLocal) Status(ctx context.Context, id string) (backend.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy {
		return backend.Item{}, fmt.Errorf("%w: waiting for the VPN", backend.ErrBusy)
	}
	if f.done {
		return backend.Item{Status: backend.Complete, Progress: 1}, nil
	}
	return backend.Item{Status: backend.Downloading, Progress: 0.5}, nil
}
func (f *fakeLocal) Files(ctx context.Context, id string) ([]backend.File, error) {
	var out []backend.File
	root := filepath.Join(f.dir, id)
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, backend.File{Path: filepath.ToSlash(rel), Size: fi.Size()})
		}
		return nil
	})
	return out, nil
}
func (f *fakeLocal) Remove(ctx context.Context, id string, del bool) error { return nil }

func TestTorrentsGoToTheBuiltInClientWhenTheVPNIsOn(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	r := newRig(t)
	loc := &fakeLocal{dir: t.TempDir()}
	r.eng.Local = loc
	s := r.cfg.Get()
	s.VPN = config.VPN{On: true, User: "p1", Pass: "pw"}
	r.cfg.Set(s)

	id := r.addTorrent("Movie.2020.1080p", "radarr", 1000)
	r.tick(2)
	j, _ := r.st.Get(id)
	if j.Backend != "local" || len(loc.added) != 1 {
		t.Fatalf("backend = %q, added %v", j.Backend, loc.added)
	}
	r.qb.mu.Lock()
	n := len(r.qb.items)
	r.qb.mu.Unlock()
	if n != 0 {
		t.Fatal("qBittorrent was used too")
	}

	// The VPN drops for longer than the stall time: the job waits, it does not fail.
	loc.mu.Lock()
	loc.busy = true
	loc.mu.Unlock()
	stall := r.cfg.Get().StallMinutes
	r.tick(stall + 30)
	if st := r.state(id); st == job.Failed || st == job.Cleaned {
		t.Fatalf("job failed while the VPN was down: %s", st)
	}
	loc.mu.Lock()
	loc.busy, loc.done = false, true
	loc.mu.Unlock()
	r.tick(3)
	if st := r.state(id); st == job.Failed || st == job.Downloading || st == job.Submitted {
		t.Fatalf("after the VPN came back: %s (%s)", st, func() string { j, _ := r.st.Get(id); return j.Reason }())
	}
}

func TestTorrentsStayOnTheOldClientWhenAskedOrVPNOff(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	for _, v := range []config.VPN{{On: false, User: "p1", Pass: "pw"}, {On: true, User: "p1", Pass: "pw", Torrents: "torbox"}} {
		r := newRig(t)
		loc := &fakeLocal{dir: t.TempDir()}
		r.eng.Local = loc
		s := r.cfg.Get()
		s.VPN = v
		r.cfg.Set(s)
		id := r.addTorrent("Movie.2021.1080p", "radarr", 1000)
		r.tick(2)
		if j, _ := r.st.Get(id); j.Backend == "local" || len(loc.added) != 0 {
			t.Fatalf("%+v: went to the built-in client", v)
		}
	}
}

// torboxBusy is a TorBox that refuses new torrents with its hourly limit
// while busy is set.
func torboxBusy(t *testing.T, busy *atomic.Bool) string {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/torrents/createtorrent":
			if busy.Load() {
				w.Header().Set("Retry-After", "1800")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"success":false,"error":"RATE_LIMIT"}`)
				return
			}
			fmt.Fprintf(w, `{"success":true,"data":{"torrent_id":%d}}`, 100+n.Add(1))
		case "/v1/api/torrents/checkcached":
			fmt.Fprint(w, `{"success":true,"data":{}}`)
		default:
			fmt.Fprint(w, `{"success":true,"data":[]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTorBoxFirstThenBuiltInWhenBusy(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	var busy atomic.Bool
	r := newRig(t)
	loc := &fakeLocal{dir: t.TempDir()}
	r.eng.Local = loc
	s := r.cfg.Get()
	s.VPN = config.VPN{On: true, User: "p1", Pass: "pw"}
	s.TorBox.Token, s.TorBox.BaseURL, s.TorBox.LinkGapSeconds = "tbtoken", torboxBusy(t, &busy), 1
	r.cfg.Set(s)

	// TorBox free: it takes the torrent.
	a := r.addTorrent("Movie.A.2020.1080p", "radarr", 1000)
	r.tick(2)
	if j, _ := r.st.Get(a); j.Backend != "torbox" {
		t.Fatalf("free TorBox: backend = %q (%s)", j.Backend, j.Reason)
	}

	// TorBox at its limit: the built-in client takes it.
	busy.Store(true)
	b := r.addTorrent("Movie.B.2020.1080p", "radarr", 1000)
	r.tick(2)
	if j, _ := r.st.Get(b); j.Backend != "local" {
		t.Fatalf("busy TorBox: backend = %q (%s)", j.Backend, j.Reason)
	}

	// Busy, and the VPN down too: it waits for TorBox instead.
	loc.mu.Lock()
	loc.down = true
	loc.mu.Unlock()
	c := r.addTorrent("Movie.C.2020.1080p", "radarr", 1000)
	r.tick(2)
	if j, _ := r.st.Get(c); j.Backend != "" || len(loc.added) != 1 {
		t.Fatalf("VPN down: backend = %q, local adds %d", j.Backend, len(loc.added))
	}
}

func TestAlwaysTorBoxNeverFallsOver(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	var busy atomic.Bool
	busy.Store(true)
	r := newRig(t)
	loc := &fakeLocal{dir: t.TempDir()}
	r.eng.Local = loc
	s := r.cfg.Get()
	s.VPN = config.VPN{On: true, User: "p1", Pass: "pw", Torrents: "torbox"}
	s.TorBox.Token, s.TorBox.BaseURL, s.TorBox.LinkGapSeconds = "tbtoken", torboxBusy(t, &busy), 1
	r.cfg.Set(s)
	id := r.addTorrent("Movie.D.2020.1080p", "radarr", 1000)
	r.tick(2)
	if j, _ := r.st.Get(id); j.Backend == "local" || len(loc.added) != 0 {
		t.Fatal("went to the built-in client")
	}
}

func TestMaliciousFilesAreBlocked(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	for name, loc := range map[string]*fakeLocal{
		"program":   {extra: map[string][]byte{"Codec.Installer.exe": []byte("MZ")}, done: true},
		"disguised": {video: []byte("MZ\x90\x00 not a video"), done: true},
	} {
		r := newRig(t)
		loc.dir = t.TempDir()
		r.eng.Local = loc
		s := r.cfg.Get()
		s.VPN = config.VPN{On: true, User: "p1", Pass: "pw"}
		r.cfg.Set(s)
		id := r.addTorrent("Movie.Bad.2020.1080p", "radarr", 1000)
		r.tick(4)
		j, _ := r.st.Get(id)
		if j.State != job.Failed && j.State != job.Cleaned || !strings.Contains(j.Reason, "blocked") {
			t.Fatalf("%s: state %s reason %q", name, j.State, j.Reason)
		}
		if ents, _ := os.ReadDir(filepath.Join(r.cfg.Env.DataDir)); len(ents) > 0 {
			// nothing of the release may reach the complete folder
			if _, err := os.Stat(filepath.Join(r.cfg.Env.Complete(), "radarr")); err == nil {
				if e2, _ := os.ReadDir(filepath.Join(r.cfg.Env.Complete(), "radarr")); len(e2) > 0 {
					t.Fatalf("%s: handed to the arr", name)
				}
			}
		}
	}
}

func TestUsenetFallsOverToBuiltInWhenTorBoxIsBusy(t *testing.T) {
	u := newURig(t)
	news := &fakeLocal{dir: t.TempDir(), name: "usenet", done: true}
	u.eng.News = news
	s := u.cfg.Get()
	s.Usenet = config.Usenet{Servers: []config.NewsServer{{Name: "P", Host: "news.example", On: true}}}
	u.cfg.Set(s)

	u.tb.mu.Lock()
	u.tb.busy = true
	u.tb.mu.Unlock()
	id := u.addNZB("Show.S01E02.1080p")
	u.tick(2)
	if j, _ := u.st.Get(id); j.Backend != "usenet" {
		t.Fatalf("busy TorBox: backend %q (%s)", j.Backend, j.Reason)
	}

	// TorBox free again: it takes the next one.
	u.tb.mu.Lock()
	u.tb.busy = false
	u.tb.mu.Unlock()
	id2 := u.addNZB("Show.S01E03.1080p")
	u.tick(2)
	if j, _ := u.st.Get(id2); j.Backend != "torbox" {
		t.Fatalf("free TorBox: backend %q (%s)", j.Backend, j.Reason)
	}
}

// A program in a release that TorBox delivers is blocked too.
func TestTorBoxDeliveryIsGuarded(t *testing.T) {
	u := newURig(t)
	id := u.addNZB("Show.S01E04.1080p")
	u.tick(2)
	u.tb.finish("101", map[string]string{
		"Show.S01E04.1080p/Show.S01E04.1080p.mkv": strings.Repeat("v", 5000),
		"Show.S01E04.1080p/Codec.exe":             "MZ",
	})
	u.tick(4)
	j, _ := u.st.Get(id)
	if (j.State != job.Failed && j.State != job.Cleaned) || !strings.Contains(j.Reason, "blocked") {
		t.Fatalf("state %s reason %q", j.State, j.Reason)
	}
	// And disguised media from TorBox.
	id2 := u.addNZB("Show.S01E05.1080p")
	u.tick(2)
	u.tb.finish("102", map[string]string{"Show.S01E05.1080p/Show.S01E05.1080p.mkv": "MZ\x90\x00" + strings.Repeat("x", 5000)})
	u.tick(4)
	if j, _ := u.st.Get(id2); !strings.Contains(j.Reason, "blocked") {
		t.Fatalf("disguised: %s %q", j.State, j.Reason)
	}
}

// Software in a plain-files category: every file kept as released, no guard,
// delivered to the category's folder.
func TestPlainFilesCategoryKeepsSoftware(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	r := newRig(t)
	loc := &fakeLocal{dir: t.TempDir(), done: true, extra: map[string][]byte{"setup.exe": []byte("MZ program"), "docs/readme.txt": []byte("hi")}}
	r.eng.Local = loc
	dest := t.TempDir()
	s := r.cfg.Get()
	s.VPN = config.VPN{On: true, User: "p1", Pass: "pw"}
	s.Categories = append(s.Categories, config.Category{Name: "software", Kind: "files", LibraryDir: dest, Transcode: true})
	r.cfg.Set(s)
	os.MkdirAll(filepath.Join(loc.dir), 0o755)
	j, err := r.eng.Submit(pipeline.SubmitRequest{Source: "manual", Category: "software", Torrent: torrentBytes("Some.App.v2", 1000), Prefer: "builtin"})
	if err != nil {
		t.Fatal(err)
	}
	// the fake writes docs/readme.txt under a folder that must exist
	os.MkdirAll(filepath.Join(loc.dir, j.ID, "Some.App.v2", "docs"), 0o755)
	r.tick(6)
	j, _ = r.st.Get(j.ID)
	if j.State != job.Cleaned || j.Backend != "local" {
		t.Fatalf("state %s backend %s (%s)", j.State, j.Backend, j.Reason)
	}
	for _, f := range []string{"setup.exe", "docs/readme.txt", "Some.App.v2.mkv"} {
		if !exists(filepath.Join(dest, "Some.App.v2", f)) {
			t.Fatalf("%s not delivered (%s)", f, j.Reason)
		}
	}
}

func TestSendToBuiltIn(t *testing.T) {
	u := newURig(t)
	news := &fakeLocal{dir: t.TempDir(), name: "usenet"}
	u.eng.News = news
	s := u.cfg.Get()
	s.Usenet = config.Usenet{Servers: []config.NewsServer{{Name: "P", Host: "news.example", On: true}}}
	u.cfg.Set(s)
	id := u.addNZB("Show.S01E06.1080p")
	u.tick(2)
	if j, _ := u.st.Get(id); j.Backend != "torbox" {
		t.Fatalf("backend %q", j.Backend)
	}
	if err := u.eng.SendToBuiltIn(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	u.tick(2)
	j, _ := u.st.Get(id)
	if j.Backend != "usenet" || len(news.added) != 1 || len(u.tb.deleted) == 0 {
		t.Fatalf("backend %q added %d torbox deletes %d", j.Backend, len(news.added), len(u.tb.deleted))
	}
}
