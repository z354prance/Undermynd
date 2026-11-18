package tv_test

import (
	"context"
	"fmt"
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
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/store"
	"github.com/z354prance/undermynd/internal/tv"
)

type fakeIdx struct {
	st      *store.Store
	recent  []indexers.Result
	grabbed []string
}

func res(title string) indexers.Result {
	return indexers.Result{Title: title, Size: 2 << 30, Indexer: "I", IndexerID: 1, Protocol: "usenet", Published: time.Now().Add(-48 * time.Hour)}
}

func (f *fakeIdx) Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error) {
	switch {
	case q.Text == "":
		return indexers.SearchResult{Results: f.recent}, nil
	case q.TVDB != 371980:
		return indexers.SearchResult{}, nil
	case q.Season == 1 && q.Episode == 0:
		return indexers.SearchResult{Results: []indexers.Result{res("Severance.S01.1080p.WEB-DL.H.264-GRP"), res("Severance.S01E01.1080p.WEB-DL.H.264-GRP")}}, nil
	case q.Season == 2 && q.Episode == 2:
		return indexers.SearchResult{Results: []indexers.Result{res("Severance.S02E02.1080p.WEB-DL.H.264-GRP"), res("Severance.S02E02.720p.WEB-DL.H.264-GRP")}}, nil
	}
	return indexers.SearchResult{}, nil
}

func (f *fakeIdx) Grab(ctx context.Context, r indexers.Result, category string) (job.Job, error) {
	f.grabbed = append(f.grabbed, r.Title)
	j := job.Job{ID: fmt.Sprintf("job%d", len(f.grabbed)), Name: r.Title, Category: category, State: job.Downloading, Source: "indexer"}
	return j, f.st.Put(j)
}

func TestNativeTV(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	future := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/series":
			w.Write([]byte(`[{"id":1,"title":"Severance","year":2022,"tvdbId":371980,"tmdbId":95396,"path":"/tv/Severance","monitored":true,"runtime":55,"qualityProfileId":1,"seriesType":"standard"},
				{"id":2,"title":"Some Anime","tvdbId":5,"path":"/tv/Some Anime","seriesType":"anime"}]`))
		case "/api/v3/episode":
			w.Write([]byte(`[{"seasonNumber":1,"episodeNumber":1,"title":"Good News About Hell","airDate":"2022-02-18","monitored":true},
				{"seasonNumber":1,"episodeNumber":2,"title":"Half Loop","airDate":"2022-02-18","monitored":true},
				{"seasonNumber":2,"episodeNumber":1,"title":"Hello, Ms. Cobel","airDate":"2025-01-17","monitored":true,"episodeFileId":7},
				{"seasonNumber":2,"episodeNumber":2,"title":"Goodbye, Mrs. Selvig","airDate":"2025-01-24","monitored":true},
				{"seasonNumber":2,"episodeNumber":3,"title":"Who Is Alive?","airDate":"` + future + `","monitored":true}]`))
		case "/api/v3/episodefile":
			w.Write([]byte(`[{"id":7,"path":"/tv/Severance/Season 2/s02e01.mkv","size":3000000000,"quality":{"quality":{"name":"WEBDL-1080p"},"revision":{"version":1}}}]`))
		case "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":1,"name":"HD-1080p"}]`))
		case "/api/v3/rootfolder":
			w.Write([]byte(`[{"id":1,"path":"/tv"}]`))
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
	st.Categories = []config.Category{{Name: "tv", Kind: "sonarr", ArrURL: srv.URL, ArrAPIKey: "k", LibraryDir: lib, Native: true}}
	st.TV.DryRun = true
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
	idx := &fakeIdx{st: jobs}
	s := &tv.Service{DB: h, Catalog: catalog.New(h), Cfg: cfg, Profiles: &decision.Store{DB: h}, Indexers: idx, Jobs: jobs,
		Move: pipeline.MoveFile, IsVideo: pipeline.IsVideo, Log: log.New(io.Discard, "", 0), Now: time.Now,
		Arr: func(string) *arr.Client { return arr.New("sonarr", srv.URL, "k") }}
	ctx := context.Background()

	r, err := s.Import(ctx, "tv")
	if err != nil || r.Added != 1 || r.Episodes != 5 || len(r.Problems) != 1 {
		t.Fatalf("import: %+v %v", r, err)
	}
	if r, _ := s.Import(ctx, "tv"); r.Added != 0 || r.Updated != 1 {
		t.Fatalf("import again: %+v", r)
	}
	list, _ := s.List("tv")
	if len(list) != 1 || list[0].Have != 1 || list[0].Missing != 3 || list[0].Episodes != 4 || list[0].NextAir != future || list[0].Folder != filepath.Join(lib, "Severance") {
		t.Fatalf("list: %+v", list)
	}
	sid := list[0].ID

	// Season 1 is all missing and finished: one season search. S02E02: one
	// episode. S02E01 (WEBDL, below the Bluray cutoff): an upgrade search.
	if n, err := s.SearchWanted(ctx); err != nil || n != 3 {
		t.Fatalf("search wanted: %d %v", n, err)
	}
	got := strings.Join(idx.grabbed, " ")
	if !strings.Contains(got, "Severance.S01.1080p.WEB-DL.H.264-GRP") || !strings.Contains(got, "Severance.S02E02.1080p.WEB-DL.H.264-GRP") || len(idx.grabbed) != 2 {
		t.Fatalf("grabbed %v", idx.grabbed)
	}
	if n, _ := s.SearchWanted(ctx); n != 0 {
		t.Fatalf("searched again at once: %d", n)
	}
	packJob := "job1"
	if !strings.Contains(idx.grabbed[0], "S01.") {
		packJob = "job2"
	}

	// The pack finishes: a dry run describes, a real import places both.
	done := filepath.Join(dir, "complete", packJob)
	os.MkdirAll(done, 0o755)
	for _, f := range []string{"Severance.S01E01.1080p.WEB-DL.H.264-GRP.mkv", "Severance.S01E02.1080p.WEB-DL.H.264-GRP.mkv"} {
		os.WriteFile(filepath.Join(done, f), make([]byte, 1024), 0o644)
	}
	j, _ := jobs.Get(packJob)
	j.CompleteDir, j.Files = done, []string{"Severance.S01E01.1080p.WEB-DL.H.264-GRP.mkv", "Severance.S01E02.1080p.WEB-DL.H.264-GRP.mkv"}
	own, placed, left, err := s.ImportJob(ctx, j)
	if !own || err != nil || len(placed) != 0 || len(left) != 2 || !strings.HasPrefix(left[0], "dry run") {
		t.Fatalf("dry run: %v %v %v %v", own, placed, left, err)
	}
	st.TV.DryRun = false
	cfg.Set(st)
	// The dry run ended the download; the job is imported again as a retry would.
	s.Catalog.SetJobTarget(packJob, catalog.Target{EpisodeIDs: episodeIDs(t, s, sid, 1)})
	own, placed, left, err = s.ImportJob(ctx, j)
	want := filepath.Join(lib, "Severance", "Season 1", "Severance - S01E01 - Good News About Hell WEBDL-1080p.mkv")
	if !own || err != nil || len(placed) != 2 || placed[0] != want || len(left) != 0 {
		t.Fatalf("import: %v %v %v %v", own, placed, left, err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}

	// S02E02's download fails: blocklisted; the recent releases offer another.
	other := "job1"
	if packJob == "job1" {
		other = "job2"
	}
	jobs.Update(other, func(j *job.Job) error { j.Move(job.Failed, "bad", time.Now()); return nil })
	s.Follow()
	idx.recent = []indexers.Result{res("Severance.S02E02.1080p.WEB-DL.H.264-GRP"), res("Severance.S02E02.1080p.WEB-DL.H.264-OTHER"), res("Unrelated.S01E01.1080p.WEB-DL.H.264-X")}
	if n, err := s.RecentSync(ctx); err != nil || n != 1 || idx.grabbed[len(idx.grabbed)-1] != "Severance.S02E02.1080p.WEB-DL.H.264-OTHER" {
		t.Fatalf("recent: %d %v %v", n, err, idx.grabbed)
	}
	_, eps, _ := s.Get(sid)
	for _, e := range eps {
		if e.Season == 1 && e.Status != "cutoff unmet" || e.Season == 2 && e.Number == 2 && e.Status != "downloading" || e.Number == 3 && e.Status != "unaired" {
			t.Errorf("S%dE%d: %s", e.Season, e.Number, e.Status)
		}
	}
}

func episodeIDs(t *testing.T, s *tv.Service, sid int64, season int) []int64 {
	_, eps, err := s.Get(sid)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, e := range eps {
		if e.Season == season {
			out = append(out, e.ID)
		}
	}
	return out
}
