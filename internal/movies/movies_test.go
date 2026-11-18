package movies_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/history"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/movies"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/store"
)

type fakeIdx struct {
	st      *store.Store
	results []indexers.Result
	grabbed []string
}

func (f *fakeIdx) Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error) {
	if q.TMDB != 949 {
		return indexers.SearchResult{}, nil
	}
	return indexers.SearchResult{Results: f.results}, nil
}

func (f *fakeIdx) Grab(ctx context.Context, r indexers.Result, category string) (job.Job, error) {
	f.grabbed = append(f.grabbed, r.Title)
	j := job.Job{ID: "job" + string(rune('0'+len(f.grabbed))), Name: r.Title, Category: category, State: job.Downloading, Source: "indexer"}
	return j, f.st.Put(j)
}

func TestNativeMovies(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie":
			w.Write([]byte(`[
				{"title":"Heat","year":1995,"tmdbId":949,"imdbId":"tt0113277","path":"/movies/Heat (1995)","monitored":true,"runtime":170,"qualityProfileId":1},
				{"title":"Alien","year":1979,"tmdbId":348,"path":"/movies/Alien (1979)","monitored":true,"runtime":117,"qualityProfileId":1,
				 "movieFile":{"path":"/movies/Alien (1979)/Alien.mkv","size":8000000000,"quality":{"quality":{"name":"Bluray-1080p"},"revision":{"version":1}}}}]`))
		case "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":1,"name":"HD-1080p"}]`))
		case "/api/v3/rootfolder":
			w.Write([]byte(`[{"id":1,"path":"/movies"}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Categories = []config.Category{{Name: "movies", Kind: "radarr", ArrURL: srv.URL, ArrAPIKey: "k", LibraryDir: lib, Native: true}}
	st.Movies.DryRun = true
	cfg.Set(st)
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.OpenDB(h, "")
	if err != nil {
		t.Fatal(err)
	}
	gb := int64(1 << 30)
	idx := &fakeIdx{st: jobs, results: []indexers.Result{
		{Title: "Heat.1995.1080p.BluRay.x264-GOOD", Size: 9 * gb, Indexer: "I", IndexerID: 1, Protocol: "usenet", Published: time.Now().Add(-48 * time.Hour)},
		{Title: "Heat.1995.1080p.WEB-DL.H.264-OTHER", Size: 6 * gb, Indexer: "I", IndexerID: 1, Protocol: "usenet", Published: time.Now().Add(-48 * time.Hour)},
		{Title: "Heat.1995.720p.BluRay.x264-LOW", Size: 4 * gb, Indexer: "I", IndexerID: 1, Protocol: "usenet"},
	}}
	s := &movies.Service{History: &history.Log{DB: h}, DB: h, Catalog: catalog.New(h), Cfg: cfg, Profiles: &decision.Store{DB: h}, Indexers: idx, Jobs: jobs,
		Move: pipeline.MoveFile, IsVideo: pipeline.IsVideo, Log: log.New(io.Discard, "", 0), Now: time.Now,
		Arr: func(string) *arr.Client { return arr.New("radarr", srv.URL, "k") }}
	ctx := context.Background()

	res, err := s.Import(ctx, "movies")
	if err != nil || res.Added != 2 {
		t.Fatalf("import: %+v %v", res, err)
	}
	if res, _ := s.Import(ctx, "movies"); res.Added != 0 || res.Updated != 2 {
		t.Fatalf("import again: %+v", res)
	}
	list, _ := s.List("movies")
	byTitle := map[string]movies.Movie{}
	for _, m := range list {
		byTitle[m.Title] = m
	}
	heat, alien := byTitle["Heat"], byTitle["Alien"]
	if heat.Status != "missing" || alien.Status != "have" || alien.File != filepath.Join(lib, "Alien (1979)", "Alien.mkv") || heat.Profile != "HD-1080p" {
		t.Fatalf("imported: %+v %+v", heat, alien)
	}

	// The wanted search grabs the best release; a missing movie is not
	// searched again within 12 hours.
	if n, err := s.SearchWanted(ctx); err != nil || n != 1 || len(idx.grabbed) != 1 || idx.grabbed[0] != "Heat.1995.1080p.BluRay.x264-GOOD" {
		t.Fatalf("search wanted: %d %v %v", n, err, idx.grabbed)
	}
	if heat, _ = s.Get(heat.ID); heat.Status != "downloading" {
		t.Fatalf("downloading: %+v", heat)
	}
	// The download fails: blocklisted, and the next search takes the other release.
	jobs.Update("job1", func(j *job.Job) error { j.Move(job.Failed, "bad archive", time.Now()); return nil })
	s.Follow()
	if _, _, err := s.Search(ctx, heat.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(idx.grabbed) != 2 || idx.grabbed[1] != "Heat.1995.1080p.WEB-DL.H.264-OTHER" {
		t.Fatalf("after a failure: %v", idx.grabbed)
	}

	// The download finishes. A dry run only says where it would go.
	done := filepath.Join(dir, "complete", "job2")
	os.MkdirAll(done, 0o755)
	os.WriteFile(filepath.Join(done, "heat.mkv"), make([]byte, 2048), 0o644)
	os.WriteFile(filepath.Join(done, "sample.mkv"), make([]byte, 4096), 0o644)
	j, _ := jobs.Get("job2")
	j.CompleteDir, j.Files = done, []string{"heat.mkv", "sample.mkv"}
	own, placed, left, err := s.ImportJob(ctx, j)
	want := filepath.Join(lib, "Heat (1995)", "Heat (1995) WEBDL-1080p.mkv")
	if !own || err != nil || len(placed) != 0 || len(left) != 1 || !strings.Contains(left[0], want) {
		t.Fatalf("dry run: %v %v %v %v", own, placed, left, err)
	}
	if other, _, _, _ := s.ImportJob(ctx, job.Job{ID: "unknown"}); other {
		t.Fatal("a job HyveMynd did not grab was taken")
	}
	st.Movies.DryRun = false
	cfg.Set(st)
	own, placed, left, err = s.ImportJob(ctx, j)
	if !own || err != nil || len(placed) != 1 || placed[0] != want || len(left) != 0 {
		t.Fatalf("import: %v %v %v %v", own, placed, left, err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	if heat, _ = s.Get(heat.ID); heat.Status != "cutoff unmet" || heat.Quality != "WEBDL-1080p" || heat.File != want {
		t.Fatalf("after import: %+v", heat)
	}

	// A Bluray imported by hand replaces it; the WEBDL file is set aside.
	hand := filepath.Join(dir, "complete", "Heat.1995.1080p.BluRay.x264-HAND.mkv")
	os.WriteFile(hand, make([]byte, 4096), 0o644)
	dst, err := s.ImportFile(heat.ID, hand)
	if err != nil || dst != filepath.Join(lib, "Heat (1995)", "Heat (1995) Bluray-1080p.mkv") {
		t.Fatalf("manual import: %s %v", dst, err)
	}
	if _, err := os.Stat(want); err == nil {
		t.Fatal("the replaced file is still in the movie folder")
	}
	if heat, _ = s.Get(heat.ID); heat.Status != "have" {
		t.Fatalf("after manual import: %+v", heat)
	}
	events, _ := s.History.List("movie", heat.ID, 20)
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.Event]++
	}
	if kinds["grabbed"] != 2 || kinds["failed"] != 1 || kinds["dry run"] != 1 || kinds["imported"] != 2 || kinds["replaced"] != 1 {
		t.Fatalf("history: %v", kinds)
	}
}
