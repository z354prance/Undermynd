package arr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/z354prance/undermynd/internal/arr"
)

func TestMissingArtwork(t *testing.T) {
	bodies := map[string]string{
		"sonarr": `{"records":[{"monitored":true,"seasonNumber":2,"episodeNumber":8,"airDateUtc":"2025-03-07T02:00:00Z","series":{"title":"Severance","titleSlug":"severance","images":[{"coverType":"banner","remoteUrl":"https://artworks.thetvdb.com/b.jpg"},{"coverType":"poster","remoteUrl":"https://artworks.thetvdb.com/p.jpg"}]}}]}`,
		"radarr": `{"records":[{"monitored":true,"title":"Sinners","year":2025,"tmdbId":1233413,"images":[{"coverType":"poster","remoteUrl":"https://image.tmdb.org/t/p/original/s.jpg"}]}]}`,
		"lidarr": `{"records":[{"monitored":true,"title":"OK Computer","foreignAlbumId":"abc","images":[],"artist":{"artistName":"Radiohead","images":[{"coverType":"poster","remoteUrl":"https://assets.fanart.tv/r.jpg"}]}}]}`,
	}
	want := map[string]arr.Missing{
		"sonarr": {Group: "Severance", Poster: "https://artworks.thetvdb.com/p.jpg", Path: "/series/severance", Detail: "S02E08"},
		"radarr": {Group: "Sinners", Poster: "https://image.tmdb.org/t/p/original/s.jpg", Path: "/movie/1233413", Detail: "2025"},
		"lidarr": {Group: "Radiohead — OK Computer", Poster: "https://assets.fanart.tv/r.jpg", Path: "/album/abc", Detail: "OK Computer"},
	}
	for kind, body := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		c := &arr.Client{Kind: kind, BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
		got, err := c.MissingItems(context.Background())
		srv.Close()
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: %v %+v", kind, err, got)
		}
		g, w := got[0], want[kind]
		if g.Group != w.Group || g.Poster != w.Poster || g.Path != w.Path || g.Detail != w.Detail {
			t.Errorf("%s: got %+v want %+v", kind, g, w)
		}
	}
}
