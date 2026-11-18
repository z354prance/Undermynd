package seerr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestTitleSeasonsAndSeasonRequest(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/tv/7":
			w.Write([]byte(`{"name":"Severance","firstAirDate":"2022-02-18","overview":"o","posterPath":"/p.jpg","backdropPath":"../x",
				"status":"Returning Series","episodeRunTime":[55],"genres":[{"name":"Drama"}],"networks":[{"name":"Apple TV+"}],
				"credits":{"cast":[{"name":"Adam Scott","profilePath":"/a.jpg"}]},
				"relatedVideos":[{"site":"YouTube","type":"Teaser","key":"t"},{"site":"YouTube","type":"Trailer","key":"k"}],
				"seasons":[{"seasonNumber":0,"episodeCount":3,"airDate":"2022-01-01"},{"seasonNumber":1,"episodeCount":9,"airDate":"2022-02-18"},
					{"seasonNumber":2,"episodeCount":10,"airDate":"2025-01-17"},{"seasonNumber":3,"episodeCount":0,"airDate":""},{"seasonNumber":4,"episodeCount":8,"airDate":"2024-01-01"}],
				"mediaInfo":{"status":4,"seasons":[{"seasonNumber":1,"status":5},{"seasonNumber":2,"status":4}],
					"requests":[{"status":2,"seasons":[{"seasonNumber":4,"status":2}]},{"status":3,"seasons":[{"seasonNumber":3,"status":1}]}]}}`))
		case r.URL.Path == "/api/v1/discover/tv":
			if r.URL.Query().Get("genre") != "16" || r.URL.Query().Get("keywords") == "" {
				t.Errorf("anime list asked %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"results":[{"id":1,"name":"Frieren","posterPath":"/f.jpg"}]}`))
		case r.Method == "POST" && r.URL.Path == "/api/v1/request":
			json.NewDecoder(r.Body).Decode(&posted)
			w.Write([]byte(`{"id":9}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k", 2)
	ctx := context.Background()

	ti, err := c.Title(ctx, "tv", 7)
	if err != nil {
		t.Fatal(err)
	}
	if ti.Title != "Severance" || ti.Year != 2022 || ti.Runtime != 55 || ti.Network != "Apple TV+" || ti.Trailer != "k" || ti.Status != 4 {
		t.Fatalf("title %+v", ti)
	}
	if ti.Poster != "/p.jpg" || ti.Backdrop != "" || len(ti.Cast) != 1 || ti.Cast[0].Photo != "/a.jpg" {
		t.Fatalf("images %+v", ti)
	}
	got := map[int]string{}
	for _, s := range ti.Seasons {
		got[s.Number] = s.Status
	}
	want := map[int]string{1: "available", 2: "partial", 3: "unreleased", 4: "requested"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("seasons %v, want %v", got, want)
	}

	list, err := c.Discover(ctx, "anime")
	if err != nil || len(list) != 1 || list[0].MediaType != "tv" {
		t.Fatalf("anime %v %v", list, err)
	}

	if _, err := c.Request(ctx, 7, "tv", 2, 4); err != nil {
		t.Fatal(err)
	}
	if s, ok := posted["seasons"].([]any); !ok || len(s) != 2 || s[0].(float64) != 2 {
		t.Fatalf("posted %v", posted)
	}
	if _, err := c.Request(ctx, 7, "tv"); err != nil || posted["seasons"] != "all" {
		t.Fatalf("all seasons: %v %v", posted, err)
	}
}
