package music_test

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
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/history"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/music"
	"github.com/z354prance/undermynd/internal/musicbrainz"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/store"
)

type fakeIdx struct {
	st      *store.Store
	grabbed []string
}

func r(title string, seeders int) indexers.Result {
	return indexers.Result{Title: title, Size: 400 << 20, Indexer: "I", IndexerID: 1, Protocol: "torrent", Seeders: seeders}
}

func (f *fakeIdx) Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error) {
	if q.Type != "music" || !strings.Contains(q.Text, "OK Computer") {
		return indexers.SearchResult{}, nil
	}
	return indexers.SearchResult{Results: []indexers.Result{
		r("Radiohead - OK Computer (1997) [MP3 320]", 50),
		r("Radiohead - OK Computer (1997) [FLAC]", 20),
		r("Radiohead - OK Computer (1997) [FLAC 24-96]", 0),
		r("Radiohead - Discography 1993-2016 [FLAC]", 90),
		r("Radiohead - OK Computer OKNOTOK 1997 2017 [FLAC]", 10),
	}}, nil
}

func (f *fakeIdx) Grab(ctx context.Context, x indexers.Result, category string) (job.Job, error) {
	f.grabbed = append(f.grabbed, x.Title)
	j := job.Job{ID: fmt.Sprintf("job%d", len(f.grabbed)), Name: x.Title, Category: category, State: job.Downloading, Source: "indexer"}
	return j, f.st.Put(j)
}

func TestNativeMusic(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	mb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artist/a1":
			w.Write([]byte(`{"id":"a1","name":"Radiohead","sort-name":"Radiohead"}`))
		case "/release-group":
			w.Write([]byte(`{"release-group-count":3,"release-groups":[
				{"id":"g1","title":"OK Computer","primary-type":"Album","first-release-date":"1997-05-21"},
				{"id":"g2","title":"Airbag / How Am I Driving?","primary-type":"EP","first-release-date":"1998-04-21"},
				{"id":"g3","title":"I Might Be Wrong","primary-type":"Album","secondary-types":["Live"],"first-release-date":"2001-11-12"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer mb.Close()
	lidarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/artist":
			w.Write([]byte(`[{"id":1,"artistName":"Portishead","foreignArtistId":"p1","path":"/music/Portishead","monitored":true}]`))
		case "/api/v1/album":
			w.Write([]byte(`[{"id":10,"title":"Dummy","foreignAlbumId":"pg1","releaseDate":"1994-08-22T00:00:00Z","albumType":"Album","monitored":true,"statistics":{"trackFileCount":11,"trackCount":11,"totalTrackCount":11,"sizeOnDisk":400000000}},
				{"id":11,"title":"Third","foreignAlbumId":"pg2","releaseDate":"2008-04-28T00:00:00Z","albumType":"Album","monitored":true,"statistics":{"trackFileCount":0,"trackCount":11,"totalTrackCount":11}}]`))
		case "/api/v1/trackfile":
			w.Write([]byte(`[{"albumId":10,"path":"/music/Portishead/Dummy (1994)/01.mp3","quality":{"quality":{"name":"MP3-320"}}}]`))
		case "/api/v1/rootfolder":
			w.Write([]byte(`[{"id":1,"path":"/music"}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer lidarr.Close()
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Categories = []config.Category{{Name: "music", Kind: "lidarr", ArrURL: lidarr.URL, ArrAPIKey: "k", LibraryDir: lib, Native: true}}
	cfg.Set(st)
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.OpenDB(h, "")
	idx := &fakeIdx{st: jobs}
	s := &music.Service{DB: h, MB: &musicbrainz.Client{HTTP: mb.Client(), BaseURL: mb.URL, Gap: time.Millisecond}, Cfg: cfg, Indexers: idx, Jobs: jobs,
		Arr: func(string) *arr.Client { return arr.New("lidarr", lidarr.URL, "k") }, Move: pipeline.MoveFile, History: &history.Log{DB: h},
		Log: log.New(io.Discard, "", 0), Now: time.Now}
	ctx := context.Background()

	res, err := s.Import(ctx, "music")
	if err != nil || res.Artists != 1 || res.Albums != 2 {
		t.Fatalf("import: %+v %v", res, err)
	}
	ar, err := s.AddArtist(ctx, "music", "a1")
	if err != nil {
		t.Fatal(err)
	}
	_, als, _ := s.Artist(ar.ID)
	mon := map[string]bool{}
	for _, a := range als {
		mon[a.Title] = a.Monitored
	}
	if len(als) != 3 || !mon["OK Computer"] || mon["I Might Be Wrong"] || mon["Airbag / How Am I Driving?"] {
		t.Fatalf("albums: %+v", als)
	}
	// Portishead's Dummy is MP3 (below the FLAC cutoff); Third is missing.
	_, wanted, _ := s.Wanted()
	if len(wanted) != 3 {
		t.Fatalf("wanted: %+v", wanted)
	}
	var okc music.Album
	for _, a := range als {
		if a.Title == "OK Computer" {
			okc = a
		}
	}
	vs, err := s.Search(ctx, okc.ID, true)
	if err != nil || len(idx.grabbed) != 1 || idx.grabbed[0] != "Radiohead - OK Computer (1997) [FLAC]" {
		t.Fatalf("grab: %v %v", idx.grabbed, err)
	}
	why := map[string]string{}
	for _, v := range vs {
		why[v.Result.Title] = strings.Join(v.Rejected, "; ")
	}
	if !strings.Contains(why["Radiohead - OK Computer (1997) [MP3 320]"], "not wanted") || !strings.Contains(why["Radiohead - Discography 1993-2016 [FLAC]"], "discography") ||
		!strings.Contains(why["Radiohead - OK Computer (1997) [FLAC 24-96]"], "no seeders") {
		t.Fatalf("verdicts: %v", why)
	}
	// The album finishes downloading, in its own folder with a cover.
	done := filepath.Join(dir, "complete", "job1")
	for _, f := range []string{"OK Computer/01 Airbag.flac", "OK Computer/02 Paranoid Android.flac", "OK Computer/cover.jpg", "OK Computer/info.nfo"} {
		os.MkdirAll(filepath.Dir(filepath.Join(done, f)), 0o755)
		os.WriteFile(filepath.Join(done, f), make([]byte, 100), 0o644)
	}
	j, _ := jobs.Get("job1")
	j.CompleteDir, j.Files = done, []string{"OK Computer/01 Airbag.flac", "OK Computer/02 Paranoid Android.flac", "OK Computer/cover.jpg", "OK Computer/info.nfo"}
	own, placed, left, err := s.ImportJob(ctx, j)
	want := filepath.Join(lib, "Radiohead", "OK Computer (1997)")
	if !own || err != nil || len(left) != 0 || len(placed) != 1 || placed[0] != want {
		t.Fatalf("import: %v %v %v %v", own, placed, left, err)
	}
	for _, f := range []string{"01 Airbag.flac", "02 Paranoid Android.flac", "cover.jpg"} {
		if _, err := os.Stat(filepath.Join(want, f)); err != nil {
			t.Fatal(err)
		}
	}
	_, als, _ = s.Artist(ar.ID)
	for _, a := range als {
		if a.Title == "OK Computer" && (a.Status != "have" || a.Quality != "FLAC" || a.Files != 2) {
			t.Fatalf("after import: %+v", a)
		}
	}
	if other, _, _, _ := s.ImportJob(ctx, job.Job{ID: "nope"}); other {
		t.Fatal("took a job it did not grab")
	}
}
