package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient(t *testing.T) {
	var hits int32
	var busy int32 = 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("User-Agent") == "" || r.URL.Query().Get("fmt") != "json" {
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/artist":
			if atomic.CompareAndSwapInt32(&busy, 1, 0) {
				w.WriteHeader(503) // over the rate limit once
				return
			}
			w.Write([]byte(`{"artists":[{"id":"a1","name":"Radiohead","score":100}]}`))
		case "/release-group":
			if r.URL.Query().Get("offset") == "0" {
				w.Write([]byte(`{"release-group-count":2,"release-groups":[{"id":"g1","title":"OK Computer","primary-type":"Album","first-release-date":"1997-05-21"},{"id":"g2","title":"Live","primary-type":"Album","secondary-types":["Live"]}]}`))
			}
		case "/release":
			w.Write([]byte(`{"releases":[
				{"id":"r1","title":"OK Computer","status":"Bootleg","media":[{"position":1,"tracks":[{"position":1,"title":"Airbag"}]}]},
				{"id":"r2","title":"OK Computer","status":"Official","media":[{"position":1,"tracks":[{"position":1,"title":"Airbag"},{"position":2,"title":"Paranoid Android"}]}]},
				{"id":"r3","title":"OK Computer","status":"Official","media":[{"position":1,"tracks":[{"position":1,"title":"Airbag"},{"position":2,"title":"Paranoid Android"}]}]}]}`))
		case "/artist/missing":
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Gap: 50 * time.Millisecond}
	ctx := context.Background()
	start := time.Now()
	as, err := c.SearchArtists(ctx, "Radiohead")
	if err != nil || len(as) != 1 || as[0].ID != "a1" {
		t.Fatalf("%v %v", as, err)
	}
	al, err := c.Albums(ctx, "a1")
	if err != nil || len(al) != 2 || al[0].Year() != 1997 || !al[0].Studio() || al[1].Studio() {
		t.Fatalf("%+v %v", al, err)
	}
	rs, err := c.Releases(ctx, "g1")
	if err != nil || rs[0].Status != "Official" || len(rs[0].Tracks) != 2 {
		t.Fatalf("%+v %v", rs, err)
	}
	if _, err := c.Artist(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	// Five requests, one retried: never closer than the gap.
	if n := atomic.LoadInt32(&hits); n != 5 || time.Since(start) < 4*50*time.Millisecond {
		t.Fatalf("%d requests in %v", n, time.Since(start))
	}
}
