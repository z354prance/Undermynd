package requests_test

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/requests"
	"github.com/z354prance/undermynd/internal/secret"
)

func TestNativeRequests(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	var mu sync.Mutex
	var added []string
	movies := []map[string]any{{"tmdbId": 603, "hasFile": true, "monitored": true}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		enc := json.NewEncoder(w)
		switch r.URL.Path {
		case "/search/multi":
			enc.Encode(map[string]any{"results": []map[string]any{
				{"id": 603, "media_type": "movie", "title": "The Matrix", "release_date": "1999-03-31", "poster_path": "/m.jpg"},
				{"id": 1, "media_type": "movie", "title": "New Movie", "release_date": "2026-01-01"},
				{"id": 2, "media_type": "tv", "name": "New Show", "first_air_date": "2026-01-01"},
				{"id": 9, "media_type": "person", "name": "Someone"},
			}})
		case "/trending/all/week":
			enc.Encode(map[string]any{"results": []map[string]any{{"id": 1, "media_type": "movie", "title": "New Movie"}}})
		case "/movie/1":
			enc.Encode(map[string]any{"id": 1, "title": "New Movie", "release_date": "2026-01-01"})
		case "/tv/2":
			enc.Encode(map[string]any{"id": 2, "name": "New Show", "first_air_date": "2026-01-01", "original_language": "en", "external_ids": map[string]any{"tvdb_id": 4242}})
		case "/tv/3":
			enc.Encode(map[string]any{"id": 3, "name": "Gorilla God", "original_name": "ゴリラ", "original_language": "ja", "genres": []map[string]any{{"id": 16}}})
		case "/api/v3/movie":
			if r.Method == http.MethodPost {
				var m map[string]any
				json.NewDecoder(r.Body).Decode(&m)
				added = append(added, "movie:"+m["title"].(string)+":"+m["rootFolderPath"].(string))
				w.WriteHeader(201)
				w.Write([]byte(`{}`))
				return
			}
			enc.Encode(movies)
		case "/api/v3/movie/lookup/tmdb":
			enc.Encode(map[string]any{"title": "New Movie", "tmdbId": 1})
		case "/api/v3/series":
			if r.Method == http.MethodPost {
				var m map[string]any
				json.NewDecoder(r.Body).Decode(&m)
				added = append(added, "series:"+m["title"].(string))
				w.WriteHeader(201)
				w.Write([]byte(`{}`))
				return
			}
			enc.Encode([]any{})
		case "/api/v3/series/lookup":
			if r.URL.Query().Get("term") != "tvdb:4242" {
				enc.Encode([]any{})
				return
			}
			enc.Encode([]map[string]any{{"title": "New Show", "tvdbId": 4242, "seasons": []map[string]any{{"seasonNumber": 1}}}})
		case "/api/v3/rootfolder":
			enc.Encode([]map[string]any{{"id": 1, "path": "/media/Movies"}})
		case "/api/v3/qualityprofile":
			enc.Encode([]map[string]any{{"id": 4, "name": "HD"}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Get()
	s.Categories = []config.Category{{Name: "movies", Kind: "radarr", ArrURL: srv.URL, ArrAPIKey: "k"}, {Name: "tv", Kind: "sonarr", ArrURL: srv.URL, ArrAPIKey: "k"}}
	s.Requests = config.Requests{Enabled: true, Mode: "native"}
	s.Metadata.TMDBKey = "key"
	if err := cfg.Set(s); err != nil {
		t.Fatal(err)
	}
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	tm := metadata.NewTMDB(h, func() string { return "key" })
	tm.BaseURL, tm.Gap = srv.URL, 0
	svc := &requests.Service{DB: h, TMDB: tm, Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: time.Now,
		Arr: func(cat string) *arr.Client {
			c, _ := cfg.Get().Category(cat)
			return arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey))
		}}
	ctx := context.Background()

	res, err := svc.Search(ctx, "matrix")
	if err != nil || len(res) != 3 {
		t.Fatalf("search: %+v %v", res, err)
	}
	if res[0].MediaInfo == nil || res[0].MediaInfo.Status != 5 || res[1].MediaInfo != nil {
		t.Fatalf("availability: %+v %+v", res[0].MediaInfo, res[1].MediaInfo)
	}
	if _, err := svc.Create(ctx, "movie", 603, ""); err != requests.ErrAlready {
		t.Fatalf("in the library: %v", err)
	}
	m, err := svc.Create(ctx, "movie", 1, "Sam")
	if err != nil || m.Status != "pending" || m.Title != "New Movie" || m.Year != 2026 {
		t.Fatalf("create: %+v %v", m, err)
	}
	if _, err := svc.Create(ctx, "movie", 1, ""); err != requests.ErrAlready {
		t.Fatalf("twice: %v", err)
	}
	tv, _ := svc.Create(ctx, "tv", 2, "")
	anime, _ := svc.Create(ctx, "tv", 3, "")
	if anime.Kind != "anime" {
		t.Fatalf("anime: %+v", anime)
	}
	p, _ := svc.Pending()
	if len(p) != 3 || p[2].RequestedBy != "Sam" {
		t.Fatalf("pending: %+v", p)
	}
	if err := svc.Decide(ctx, m.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Decide(ctx, tv.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Decide(ctx, anime.ID, "decline", "not now"); err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || added[0] != "movie:New Movie:/media/Movies" || added[1] != "series:New Show" {
		t.Fatalf("added: %v", added)
	}
	if got, _ := svc.Get(anime.ID); got.Status != "declined" || got.Reason != "not now" {
		t.Fatalf("declined: %+v", got)
	}
	if err := svc.Decide(ctx, m.ID, "approve", ""); err == nil || !strings.Contains(err.Error(), "approved already") {
		t.Fatalf("twice: %v", err)
	}
	// The movie arrives: the request becomes available.
	mu.Lock()
	movies = append(movies, map[string]any{"tmdbId": 1, "hasFile": true})
	mu.Unlock()
	svc.Refresh(ctx) // the library is re-read only after 10 minutes...
	svc = &requests.Service{DB: h, TMDB: tm, Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: time.Now, Arr: svc.Arr}
	svc.Refresh(ctx) // ...or by a fresh service
	if got, _ := svc.Get(m.ID); got.Status != "available" {
		t.Fatalf("available: %+v", got)
	}
}
