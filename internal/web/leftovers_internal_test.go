package web

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/store"
)

// Leftovers: folders named for downloads that no longer exist are listed;
// those of current downloads, recent ones and anything HyveMynd did not make
// are not.
func TestLeftovers(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d")})
	s0 := cfg.Get()
	s0.Categories = append(s0.Categories, config.Category{Name: "tv", Kind: "sonarr"}, config.Category{Name: "soft", Kind: "files"})
	cfg.Set(s0)
	st, _ := store.Open(filepath.Join(dir, "c", "jobs.json"))
	au, _ := auth.New(filepath.Join(dir, "c"))
	srv := New(st, pipeline.New(st, cfg, log.New(io.Discard, "", 0)), cfg, au, log.New(io.Discard, "", 0))

	live := "SABnzbd_nzo_aaaaaaaaaaaa"
	gone := "SABnzbd_nzo_bbbbbbbbbbbb"
	hash := "0123456789abcdef0123456789abcdef01234567"
	w, c := cfg.Env.Work(), cfg.Env.Complete()
	liveComplete := filepath.Join(c, "tv", "Show.S01E01")
	st.Put(job.Job{ID: live, Name: "x", State: job.Downloading, CompleteDir: liveComplete})
	old := time.Now().Add(-24 * time.Hour)
	mk := func(p string, data string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(data), 0o644)
		os.Chtimes(p, old, old)
		os.Chtimes(filepath.Dir(p), old, old)
	}
	mk(filepath.Join(w, "staging", live, "a.mkv"), "1234")
	mk(filepath.Join(w, "staging", gone, "a.mkv"), "12345678")
	mk(filepath.Join(w, "usenet", hash, "b.bin"), "12")
	mk(filepath.Join(w, "staging", "my-own-folder", "keep.txt"), "x")
	mk(filepath.Join(liveComplete, "e.mkv"), "1")
	mk(filepath.Join(c, "tv", "Old.Show.S01E02", "e.mkv"), "123")
	mk(filepath.Join(c, "soft", "Some.App", "setup.bin"), "1")
	mk(filepath.Join(c, "unknown", "Thing", "f"), "1")
	mk(filepath.Join(cfg.Env.ConfigDir, "nzbs", gone+".nzb"), "nzb")
	recent := filepath.Join(w, "staging", "SABnzbd_nzo_cccccccccccc", "r.mkv")
	os.MkdirAll(filepath.Dir(recent), 0o755)
	os.WriteFile(recent, []byte("r"), 0o644)

	got := map[string]int64{}
	for _, x := range srv.findLeftovers(time.Now()) {
		rel, _ := filepath.Rel(dir, x.Path)
		got[rel] = x.Size
	}
	want := map[string]int64{
		"d/undermynd/staging/" + gone:   8,
		"d/undermynd/usenet/" + hash:    2,
		"d/complete/tv/Old.Show.S01E02": 3,
		"c/nzbs/" + gone + ".nzb":       3,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %d (got %v)", k, got[k], got)
		}
	}
}
