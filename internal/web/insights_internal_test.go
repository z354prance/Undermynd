package web

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/store"
)

type memBlobs map[string][]byte

func (m memBlobs) Load(n string) ([]byte, bool, error) { b, ok := m[n]; return b, ok, nil }
func (m memBlobs) Save(n string, b []byte) error       { m[n] = b; return nil }

// The storage panel counts imports by day and kind, reads the library from
// the arrs, and keeps its day totals in a blob across restarts.
func TestInsights(t *testing.T) {
	arrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/series"):
			_, _ = io.WriteString(w, `[{"id":1,"title":"Suits","year":2011,"seriesType":"standard","statistics":{"episodeFileCount":20},"seasons":[{"seasonNumber":0,"statistics":{"episodeFileCount":1}},{"seasonNumber":1,"statistics":{"episodeFileCount":12}},{"seasonNumber":2,"statistics":{"episodeFileCount":8}}]},
				{"id":2,"title":"Naruto","seriesType":"anime","statistics":{"episodeFileCount":99}}]`)
		case strings.HasSuffix(p, "/movie"):
			_, _ = io.WriteString(w, `[{"id":3,"title":"Arrival","year":2016,"tmdbId":329865,"hasFile":true},{"id":4,"title":"Missing","hasFile":false}]`)
		case strings.HasSuffix(p, "/wanted/cutoff"):
			_, _ = io.WriteString(w, `{"totalRecords":3}`)
		case strings.HasSuffix(p, "/history/since"):
			_, _ = io.WriteString(w, `[{"eventType":"episodeFileDeleted","data":{"reason":"Upgrade"}},{"eventType":"episodeFileDeleted","data":{"reason":"Manual"}},{"eventType":"movieFileDeleted","data":{"reason":"Upgrade"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer arrSrv.Close()

	dir := t.TempDir()
	cfg, _ := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d")})
	s0 := cfg.Get()
	s0.Categories = []config.Category{{Name: "tv", Kind: "sonarr", ArrURL: arrSrv.URL, ArrAPIKey: "k"}, {Name: "movies", Kind: "radarr", ArrURL: arrSrv.URL, ArrAPIKey: "k"}}
	cfg.Set(s0)
	st, _ := store.Open(filepath.Join(dir, "c", "jobs.json"))
	au, _ := auth.New(filepath.Join(dir, "c"))
	l := log.New(io.Discard, "", 0)
	srv := New(st, pipeline.New(st, cfg, l), cfg, au, l)
	blobs := memBlobs{}
	srv.Blobs = blobs

	now := time.Now()
	add := func(id, cat string, files []string) {
		j := job.Job{ID: id, Category: cat, Files: files, Created: now, StateChanged: now}
		j.Move(job.Imported, "imported", now)
		if err := st.Put(j); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "tv", []string{"Show.S01E01.mkv", "Show.S01E02.mkv", "Show.S01E01.srt"})
	add("b", "movies", []string{"Movie.mkv"})
	srv.countImports(now)
	srv.countImports(now) // counted once
	srv.refreshSlow(context.Background())

	get := func() map[string]any {
		rec := httptest.NewRecorder()
		srv.v1Insights(rec, httptest.NewRequest("GET", "/api/v1/insights", nil))
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := get()
	days := m["days"].([]any)
	if len(days) != 10 {
		t.Fatalf("%d days", len(days))
	}
	today := days[9].(map[string]any)["added"].(map[string]any)
	if today["tv"] != 2.0 || today["movie"] != 1.0 {
		t.Fatalf("added today %v", today)
	}
	lib := m["library"].(map[string]any)
	tv := lib["tv"].(map[string]any)
	if tv["shows"] != 1.0 || tv["seasons"] != 2.0 || tv["episodes"] != 20.0 {
		t.Fatalf("tv counts %v (anime series and specials left out)", tv)
	}
	if lib["movie"].(map[string]any)["movies"] != 1.0 {
		t.Fatalf("movies %v", lib["movie"])
	}
	up := m["upgrades"].(map[string]any)
	if up["episodes"] != 1.0 || up["movies"] != 1.0 {
		t.Fatalf("upgrades %v", up)
	}
	// 21 files with the better quality asked of; 3+3 below the cutoff.
	if sh := up["share"].(float64); sh < 0.7 || sh > 0.72 {
		t.Fatalf("share %v", sh)
	}
	if picks := m["picks"].([]any); len(picks) != 2 || !strings.Contains(picks[0].(map[string]any)["poster"].(string), "/api/v1/playback/poster?") {
		t.Fatalf("picks %v", picks)
	}

	// Kept across a restart.
	srv.ins.dirty = true
	srv.insSave()
	srv2 := New(st, pipeline.New(st, cfg, l), cfg, au, l)
	srv2.Blobs = blobs
	rec := httptest.NewRecorder()
	srv2.v1Insights(rec, httptest.NewRequest("GET", "/api/v1/insights", nil))
	if !strings.Contains(rec.Body.String(), `"tv":2`) {
		t.Fatalf("after restart: %s", rec.Body.String())
	}
}

func TestKindAndCount(t *testing.T) {
	for c, want := range map[config.Category]string{{Name: "anime", Kind: "sonarr"}: "anime", {Name: "x", Kind: "rss"}: "anime", {Name: "tv", Kind: "sonarr"}: "tv", {Name: "Books", Kind: "files"}: "book", {Name: "software", Kind: "files"}: ""} {
		if got := kindOf(c); got != want {
			t.Errorf("%+v: %q want %q", c, got, want)
		}
	}
	if addedCount("movie", []string{"a.mkv", "b.mkv"}) != 1 || addedCount("tv", []string{"a.srt"}) != 1 || addedCount("anime", []string{"1.mkv", "2.mp4"}) != 2 {
		t.Fatal("counts")
	}
}

// With the media server connected, its own counts are used: every library,
// anime and books included, by kind and by library name.
func TestInsightsFromMediaServer(t *testing.T) {
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Library/VirtualFolders":
			_, _ = io.WriteString(w, `[{"Name":"TV","ItemId":"1","CollectionType":"tvshows"},{"Name":"Anime","ItemId":"2","CollectionType":"tvshows"},
				{"Name":"Movies","ItemId":"3","CollectionType":"movies"},{"Name":"Music","ItemId":"4","CollectionType":"music"},{"Name":"Books","ItemId":"5","CollectionType":"books"},{"Name":"Home videos","ItemId":"6","CollectionType":"homevideos"}]`)
		case "/Artists/AlbumArtists":
			_, _ = io.WriteString(w, `{"TotalRecordCount":210}`)
		case "/Items":
			if r.URL.Query().Get("MinDateCreated") != "" {
				if r.URL.Query().Get("ParentId") == "1" {
					now := time.Now().UTC().Format(time.RFC3339)
					_, _ = io.WriteString(w, `{"Items":[{"DateCreated":"`+now+`"},{"DateCreated":"`+now+`"},{"DateCreated":"2001-01-01T00:00:00Z"}]}`)
				} else {
					_, _ = io.WriteString(w, `{"Items":[]}`)
				}
				return
			}
			n := map[string]map[string]int{"1": {"Series": 412, "Season": 1960, "Episode": 18240}, "2": {"Series": 186, "Season": 402, "Episode": 6120},
				"3": {"Movie": 1284}, "4": {"MusicAlbum": 932, "Audio": 11480}, "5": {"Book": 28, "AudioBook": 13}}[r.URL.Query().Get("ParentId")][r.URL.Query().Get("IncludeItemTypes")]
			_, _ = io.WriteString(w, `{"TotalRecordCount":`+strconv.Itoa(n)+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ms.Close()
	dir := t.TempDir()
	cfg, _ := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d")})
	s0 := cfg.Get()
	s0.Playback.EmbyURL, s0.Playback.EmbyKey = ms.URL, "k"
	cfg.Set(s0)
	st, _ := store.Open(filepath.Join(dir, "c", "jobs.json"))
	au, _ := auth.New(filepath.Join(dir, "c"))
	l := log.New(io.Discard, "", 0)
	srv := New(st, pipeline.New(st, cfg, l), cfg, au, l)
	srv.refreshSlow(context.Background())
	lib, byName := srv.ins.slow.Library, srv.ins.slow.ByName
	if lib["tv"].Episodes != 18240 || lib["anime"].Shows != 186 || lib["movie"].Movies != 1284 || lib["music"].Artists != 210 || lib["music"].Tracks != 11480 || lib["book"].Ebooks != 28 || lib["book"].Audio != 13 {
		t.Fatalf("by kind %+v", lib)
	}
	if byName["anime"].Seasons != 402 || len(byName) != 5 {
		t.Fatalf("by name %+v", byName)
	}
	// What the media server added, by day: two TV episodes today.
	rec := httptest.NewRecorder()
	srv.v1Insights(rec, httptest.NewRequest("GET", "/api/v1/insights", nil))
	var m struct {
		Days []struct {
			Added map[string]int `json:"added"`
		} `json:"days"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if len(m.Days) != 10 || m.Days[9].Added["tv"] != 2 {
		t.Fatalf("added from the media server: %s", rec.Body.String())
	}
}

// Watched time from Tracearr's history; when a play has no time played, the
// media server's runtime times the share played.
func TestWatchHistory(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	tr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/public/history" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("media_type") {
		case "movie":
			_, _ = io.WriteString(w, `{"data":[{"media_type":"movie","media_title":"Arrival","rating_key":"m1","percent_complete":50,"started_at":"`+now+`","is_transcode":true}],"meta":{}}`)
		case "episode":
			_, _ = io.WriteString(w, `{"data":[{"media_type":"episode","show_title":"Suits","rating_key":"e1","watched":true,"started_at":"`+now+`","duration_ms":1800000,"total_duration_ms":2700000,"progress_ms":2700000}],"meta":{}}`)
		default:
			_, _ = io.WriteString(w, `{"data":[],"meta":{}}`)
		}
	}))
	defer tr.Close()
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Items" && r.URL.Query().Get("Ids") == "m1" {
			_, _ = io.WriteString(w, `{"Items":[{"Id":"m1","RunTimeTicks":72000000000}]}`) // 2 h
			return
		}
		_, _ = io.WriteString(w, `{"Items":[]}`)
	}))
	defer ms.Close()
	dir := t.TempDir()
	cfg, _ := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d")})
	s0 := cfg.Get()
	s0.Playback.TracearrURL, s0.Playback.TracearrKey = tr.URL, "k"
	s0.Playback.EmbyURL, s0.Playback.EmbyKey = ms.URL, "k"
	cfg.Set(s0)
	st, _ := store.Open(filepath.Join(dir, "c", "jobs.json"))
	au, _ := auth.New(filepath.Join(dir, "c"))
	srv := New(st, pipeline.New(st, cfg, log.New(io.Discard, "", 0)), cfg, au, log.New(io.Discard, "", 0))
	days, direct, trans := srv.watchHistory(context.Background())
	today := days[dayKey(time.Now())]
	if today["movie"] < 0.99 || today["movie"] > 1.01 || today["tv"] < 0.49 || today["tv"] > 0.51 {
		t.Fatalf("hours %v", today)
	}
	if trans < 0.99 || direct != 0 {
		t.Fatalf("direct %v transcode %v", direct, trans)
	}
}
