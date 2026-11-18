package indexers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
)

type fakeEngine struct {
	mu   sync.Mutex
	tor  []pipeline.SubmitRequest
	nzbs []pipeline.SubmitNZBRequest
}

func (f *fakeEngine) Submit(r pipeline.SubmitRequest) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tor = append(f.tor, r)
	return job.Job{ID: "t"}, nil
}

func (f *fakeEngine) SubmitNZB(r pipeline.SubmitNZBRequest) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nzbs = append(f.nzbs, r)
	return job.Job{ID: "n"}, nil
}

func TestIndexers(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	var mu sync.Mutex
	failNext := false
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("apikey") != "TK" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/dl/1.torrent":
			w.Write([]byte("d8:announce0:e"))
		case r.URL.Query().Get("t") == "caps":
			w.Write([]byte(`<caps><server title="Tracker"/><searching><search available="yes"/><tv-search available="yes"/></searching></caps>`))
		case failNext:
			failNext = false
			w.WriteHeader(500)
		default:
			fmt.Fprintf(w, `<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><item><title>Show.S01E02.1080p</title><guid>1</guid><link>%s/dl/1.torrent?apikey=TK</link><size>100</size><torznab:attr name="seeders" value="9"/></item></channel></rss>`, "http://"+r.Host)
		}
	}))
	defer tracker.Close()
	usenet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/getnzb" {
			w.Write([]byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"></nzb>`))
			return
		}
		fmt.Fprintf(w, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Show.S01E02.1080p.WEB</title><guid>u1</guid><link>http://%s/getnzb</link><pubDate>Sun, 16 Nov 2026 15:31:02 +0000</pubDate><newznab:attr name="size" value="200"/></item></channel></rss>`, r.Host)
	}))
	defer usenet.Close()
	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "PK" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 3, "name": "Private Tracker", "protocol": "torrent", "enable": true, "priority": 10, "implementation": "Cardigann", "fields": []any{}},
			{"id": 4, "name": "NZB Geek", "protocol": "usenet", "enable": true, "priority": 20, "implementation": "Newznab",
				"fields": []map[string]any{{"name": "baseUrl", "value": "https://api.nzbgeek.example"}, {"name": "apiPath", "value": "/api"}, {"name": "apiKey", "value": "GEEK"}}},
			{"id": 5, "name": "Masked", "protocol": "usenet", "enable": false, "priority": 25, "implementation": "Newznab",
				"fields": []map[string]any{{"name": "baseUrl", "value": "https://x.example"}, {"name": "apiKey", "value": "********"}}},
		})
	}))
	defer prowlarr.Close()

	h, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	eng := &fakeEngine{}
	svc := &indexers.Service{DB: h, HTTP: tracker.Client(), Engine: eng, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now }}
	ctx := context.Background()

	tr, err := svc.Save(indexers.Indexer{Name: "Tracker", Kind: indexers.Torznab, URL: tracker.URL, APIKey: "TK", Enabled: true, Priority: 10, QueryLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(indexers.Indexer{Name: "Usenet", Kind: indexers.Newznab, URL: usenet.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(indexers.Indexer{Name: "tracker", Kind: indexers.Torznab, URL: tracker.URL}); err == nil {
		t.Fatal("names are unique")
	}
	if pub := indexers.Public([]indexers.Indexer{tr}); pub[0].APIKey != "" || !pub[0].HasKey {
		t.Fatalf("public: %+v", pub[0])
	}
	if c, err := svc.Test(ctx, tr); err != nil || c.Server != "Tracker" {
		t.Fatalf("test: %+v %v", c, err)
	}
	// An empty key on save keeps the stored one.
	tr.APIKey = ""
	if tr, err = svc.Save(tr); err != nil || tr.APIKey != "TK" {
		t.Fatalf("key kept: %v %q", err, tr.APIKey)
	}

	res, err := svc.Search(ctx, indexers.Query{Text: "Show", Type: "tv"}, "")
	if err != nil || len(res.Results) != 2 || len(res.Errors) != 0 {
		t.Fatalf("search: %+v %v", res, err)
	}
	if only, _ := svc.Search(ctx, indexers.Query{Text: "Show"}, "usenet"); len(only.Results) != 1 || only.Results[0].Protocol != "usenet" {
		t.Fatalf("usenet only: %+v", only)
	}
	var tor, nzb indexers.Result
	for _, r := range res.Results {
		if r.Protocol == "torrent" {
			tor = r
		} else {
			nzb = r
		}
	}
	if _, err := svc.Grab(ctx, tor, "tv-sonarr"); err != nil || len(eng.tor) != 1 || len(eng.tor[0].Torrent) == 0 || eng.tor[0].Source != "indexer" {
		t.Fatalf("grab torrent: %v %+v", err, eng.tor)
	}
	if _, err := svc.Grab(ctx, nzb, "tv-sonarr"); err != nil || len(eng.nzbs) != 1 {
		t.Fatalf("grab nzb: %v %+v", err, eng.nzbs)
	}

	// A failure puts the indexer to rest; it is skipped until then.
	mu.Lock()
	failNext = true
	mu.Unlock()
	res, _ = svc.Search(ctx, indexers.Query{Text: "Show"}, "torrent")
	if len(res.Errors) != 1 {
		t.Fatalf("failure: %+v", res)
	}
	res, _ = svc.Search(ctx, indexers.Query{Text: "Show"}, "torrent")
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "resting") {
		t.Fatalf("resting: %+v", res)
	}
	now = now.Add(6 * time.Minute)
	if res, _ = svc.Search(ctx, indexers.Query{Text: "Show"}, "torrent"); len(res.Results) != 1 {
		t.Fatalf("after resting: %+v", res)
	}
	// That was the third search today: the limit is reached.
	res, _ = svc.Search(ctx, indexers.Query{Text: "Show"}, "torrent")
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "searches for today") {
		t.Fatalf("limit: %+v", res)
	}

	// Prowlarr: direct where it shows the key, through Prowlarr otherwise.
	added, err := svc.ImportProwlarr(ctx, prowlarr.URL, "PK")
	if err != nil || len(added) != 3 {
		t.Fatalf("import: %v %v", added, err)
	}
	list, _ := svc.List()
	by := map[string]indexers.Indexer{}
	for _, i := range list {
		by[i.Name] = i
	}
	if g := by["NZB Geek"]; g.Kind != indexers.Newznab || g.URL != "https://api.nzbgeek.example/api" || g.APIKey != "GEEK" || g.Via != "" {
		t.Fatalf("geek: %+v", g)
	}
	if p := by["Private Tracker"]; p.Via != "prowlarr" || p.URL != prowlarr.URL+"/3/api" || p.APIKey != "PK" || p.Priority != 10 {
		t.Fatalf("private: %+v", p)
	}
	if m := by["Masked"]; m.Via != "prowlarr" || m.Enabled {
		t.Fatalf("masked: %+v", m)
	}
	if again, _ := svc.ImportProwlarr(ctx, prowlarr.URL, "PK"); len(again) != 0 {
		t.Fatalf("import twice: %v", again)
	}
}
