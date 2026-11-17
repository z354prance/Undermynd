package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/store"
)

// jobStore is what the pipeline needs from a job store. These tests are written
// against it so a later backend (SQLite, roadmap B1) can be run through the same
// contract unchanged.
type jobStore interface {
	Get(id string) (job.Job, bool)
	List() []job.Job
	Put(j job.Job) error
	Update(id string, fn func(*job.Job) error) (job.Job, error)
}

var t0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func openAt(t *testing.T, path string, now time.Time) *store.Store {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return now }
	return s
}

func sample(id string, created time.Time) job.Job {
	return job.Job{
		ID: id, Name: "Show - 01", Category: "anime", Source: "rss", Kind: "torrent",
		State: job.Downloading, Created: created, Updated: created,
		Files:         []string{"a.mkv"},
		PastRemoteIDs: []string{"old-1"},
		History:       []job.Transition{{At: created, From: job.Received, To: job.Downloading}},
	}
}

func TestGetMissing(t *testing.T) {
	var s jobStore = openAt(t, filepath.Join(t.TempDir(), "jobs.json"), t0)
	if _, ok := s.Get("nope"); ok {
		t.Fatal("a missing job must not be found")
	}
	if len(s.List()) != 0 {
		t.Fatal("a new store must be empty")
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	var s jobStore = openAt(t, filepath.Join(t.TempDir(), "jobs.json"), t0)
	in := sample("a", t0)
	if err := s.Put(in); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("a")
	if !ok || got.Name != in.Name || got.State != in.State || !got.Created.Equal(in.Created) ||
		len(got.Files) != 1 || len(got.PastRemoteIDs) != 1 || len(got.History) != 1 {
		t.Fatalf("got %+v", got)
	}
}

// Callers get copies: changing what Get, List or Update returned, or the job
// handed to Put, must not change the stored job.
func TestStoredJobsAreCopies(t *testing.T) {
	var s jobStore = openAt(t, filepath.Join(t.TempDir(), "jobs.json"), t0)
	in := sample("a", t0)
	if err := s.Put(in); err != nil {
		t.Fatal(err)
	}
	in.Files[0], in.PastRemoteIDs[0], in.History[0].Reason = "x", "x", "x"

	g, _ := s.Get("a")
	g.Files[0], g.PastRemoteIDs[0], g.History[0].Reason, g.Name = "y", "y", "y", "y"
	l := s.List()
	l[0].Files[0], l[0].PastRemoteIDs[0], l[0].History[0].Reason = "z", "z", "z"
	u, err := s.Update("a", func(j *job.Job) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	u.Files[0], u.PastRemoteIDs[0], u.History[0].Reason = "w", "w", "w"

	got, _ := s.Get("a")
	if got.Name != "Show - 01" || got.Files[0] != "a.mkv" || got.PastRemoteIDs[0] != "old-1" || got.History[0].Reason != "" {
		t.Fatalf("stored job was changed through a copy: %+v", got)
	}
}

func TestListNewestFirst(t *testing.T) {
	var s jobStore = openAt(t, filepath.Join(t.TempDir(), "jobs.json"), t0)
	for i, id := range []string{"old", "new", "mid"} {
		created := t0.Add(-time.Duration([]int{3, 1, 2}[i]) * time.Hour)
		if err := s.Put(sample(id, created)); err != nil {
			t.Fatal(err)
		}
	}
	l := s.List()
	if len(l) != 3 || l[0].ID != "new" || l[1].ID != "mid" || l[2].ID != "old" {
		t.Fatalf("order: %v %v %v", l[0].ID, l[1].ID, l[2].ID)
	}
}

func TestPutReplaces(t *testing.T) {
	var s jobStore = openAt(t, filepath.Join(t.TempDir(), "jobs.json"), t0)
	j := sample("a", t0)
	_ = s.Put(j)
	j.State = job.Ready
	if err := s.Put(j); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("a"); got.State != job.Ready || len(s.List()) != 1 {
		t.Fatalf("Put must replace, got %s and %d jobs", got.State, len(s.List()))
	}
}

func TestUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	var s jobStore = openAt(t, path, t0)
	_ = s.Put(sample("a", t0))

	got, err := s.Update("a", func(j *job.Job) error { j.State = job.Ready; return nil })
	if err != nil || got.State != job.Ready {
		t.Fatalf("update: %v %s", err, got.State)
	}
	if g, _ := s.Get("a"); g.State != job.Ready {
		t.Fatal("the update must be visible to Get")
	}

	boom := errors.New("boom")
	if _, err := s.Update("a", func(j *job.Job) error { j.State = job.Failed; return boom }); !errors.Is(err, boom) {
		t.Fatalf("the callback's error must be returned, got %v", err)
	}
	if g, _ := s.Get("a"); g.State != job.Ready {
		t.Fatal("a failed update must change nothing")
	}
	if g, _ := openAt(t, path, t0).Get("a"); g.State != job.Ready {
		t.Fatal("a failed update must not reach the file")
	}

	if _, err := s.Update("missing", func(j *job.Job) error { return nil }); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing job: want ErrNotFound, got %v", err)
	}
}

// A restart resumes from what was last stored.
func TestReopenResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	s := openAt(t, path, t0)
	_ = s.Put(sample("a", t0))
	_, _ = s.Update("a", func(j *job.Job) error { j.State = job.Transcoding; j.TranscodeProgress = 0.5; return nil })
	_ = s.Put(sample("b", t0.Add(time.Minute)))

	r := openAt(t, path, t0)
	a, ok := r.Get("a")
	if !ok || a.State != job.Transcoding || a.TranscodeProgress != 0.5 || a.PastRemoteIDs[0] != "old-1" {
		t.Fatalf("after reopen: %+v", a)
	}
	if len(r.List()) != 2 {
		t.Fatalf("want 2 jobs after reopen, got %d", len(r.List()))
	}
}

// Cleaned jobs older than a week are dropped when the store saves; anything still
// in flight is kept however old it is.
func TestPrunesOldCleanedJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	s := openAt(t, path, t0)
	oldDone := sample("old-done", t0.Add(-10*24*time.Hour))
	oldDone.State, oldDone.Updated = job.Cleaned, t0.Add(-8*24*time.Hour)
	newDone := sample("new-done", t0.Add(-2*24*time.Hour))
	newDone.State, newDone.Updated = job.Cleaned, t0.Add(-6*24*time.Hour)
	oldActive := sample("old-active", t0.Add(-30*24*time.Hour))
	oldActive.Updated = t0.Add(-30 * 24 * time.Hour)
	for _, j := range []job.Job{oldDone, newDone, oldActive} {
		if err := s.Put(j); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := s.Get("old-done"); ok {
		t.Fatal("a cleaned job older than a week should be dropped")
	}
	for _, id := range []string{"new-done", "old-active"} {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("%s must be kept", id)
		}
		if _, ok := openAt(t, path, t0).Get(id); !ok {
			t.Fatalf("%s must be kept in the file", id)
		}
	}
}

// Saves go through a temporary file and a rename: no temporary file is left
// behind, a stale one from a crash is ignored, and the file is private.
func TestCrashSafety(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	s := openAt(t, path, t0)
	_ = s.Put(sample("a", t0))
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no temporary file may be left after a save")
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the job file must be private (0600): %v %v", err, fi.Mode().Perm())
	}

	// A crash during the next save leaves a half-written temporary file.
	if err := os.WriteFile(path+".tmp", []byte(`[{"id":"half`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := openAt(t, path, t0)
	if _, ok := r.Get("a"); !ok || len(r.List()) != 1 {
		t.Fatal("a stale temporary file must not affect what is loaded")
	}
	if err := r.Put(sample("b", t0)); err != nil {
		t.Fatalf("saving over a stale temporary file must work: %v", err)
	}
}

// A damaged job file is reported, never silently replaced by an empty store.
func TestCorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(path); err == nil {
		t.Fatal("opening a damaged job file must fail")
	}
}

// Concurrent updates to one job are applied one at a time; none is lost.
func TestConcurrentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	s := openAt(t, path, t0)
	_ = s.Put(sample("a", t0))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Update("a", func(j *job.Job) error { j.Size++; return nil }); err != nil {
				t.Error(err)
			}
			_ = s.List()
		}()
	}
	wg.Wait()
	if g, _ := s.Get("a"); g.Size != 50 {
		t.Fatalf("want 50 updates applied, got %d", g.Size)
	}
	if g, _ := openAt(t, path, t0).Get("a"); g.Size != 50 {
		t.Fatalf("want 50 updates saved, got %d", g.Size)
	}
}
