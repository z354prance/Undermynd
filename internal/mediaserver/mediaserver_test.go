package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer(t *testing.T) {
	var got struct {
		auth, token string
		updates     []string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.token, got.auth = r.Header.Get("X-Emby-Token"), r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/System/Info":
			w.Write([]byte(`{"ServerName":"den"}`))
		case "/Library/VirtualFolders":
			w.Write([]byte(`[{"Name":"Anime","ItemId":"9","CollectionType":"tvshows","Locations":["/media/Anime"]}]`))
		case "/Library/Media/Updated":
			var b struct{ Updates []struct{ Path string } }
			json.NewDecoder(r.Body).Decode(&b)
			for _, u := range b.Updates {
				got.updates = append(got.updates, u.Path)
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	emby := Server{URL: srv.URL, Key: "k"}
	if n, err := emby.Check(ctx); err != nil || n != "den" || got.token != "k" || got.auth != "" {
		t.Fatalf("emby: %q %v %+v", n, err, got)
	}
	jf := Server{Kind: "jellyfin", URL: srv.URL, Key: "k"}
	libs, err := jf.Libraries(ctx)
	if err != nil || len(libs) != 1 || libs[0].Paths[0] != "/media/Anime" || got.auth != `MediaBrowser Token="k"` {
		t.Fatalf("jellyfin: %+v %v %+v", libs, err, got)
	}
	if err := jf.Updated(ctx, "/media/Anime/Show/Season 1/x.mkv"); err != nil || len(got.updates) != 1 {
		t.Fatalf("updated: %v %+v", err, got)
	}
	if err := (Server{URL: srv.URL}).Do(ctx, "GET", "/nope", nil, nil); err == nil {
		t.Fatal("404 should fail")
	}
	if p := MapPath("/media/Anime/Show/x.mkv", "/media", "/mnt/user/media"); p != "/mnt/user/media/Anime/Show/x.mkv" {
		t.Fatal(p)
	}
	if p := MapPath("/mediax/a", "/media", "/m"); p != "/mediax/a" {
		t.Fatal(p)
	}
}
