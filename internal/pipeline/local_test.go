package pipeline_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/z354prance/undermynd/internal/backend"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/secret"
)

// fakeLocal stands in for HyveMynd's own torrent client.
type fakeLocal struct {
	mu    sync.Mutex
	dir   string
	added []string
	busy  bool
	done  bool
}

func (f *fakeLocal) Name() string { return "local" }
func (f *fakeLocal) Add(ctx context.Context, j job.Job) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, j.ID)
	save := filepath.Join(f.dir, j.ID)
	os.MkdirAll(filepath.Join(save, j.Name), 0o755)
	os.WriteFile(filepath.Join(save, j.Name, j.Name+".mkv"), make([]byte, 1000), 0o644)
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
