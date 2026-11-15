package backend_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/backend"
)

// The list endpoint can leave out a real item (a cap, or a stale cache); asking
// for it by ID must still find it so a good download is not failed.
func TestStatusFindsItemMissingFromTheList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/usenet/mylist", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bypass_cache") != "true" {
			t.Error("every list call must bypass TorBox's cache")
		}
		switch r.URL.Query().Get("id") {
		case "":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"download_state":"cached","download_finished":true,"files":[{"id":1,"name":"a.mkv","size":5}]}]}`))
		case "555":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":555,"progress":1,"download_state":"cached","download_finished":true,"files":[{"id":9,"name":"x.mkv","size":7}]}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":null}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lim := backend.NewLimiter(filepath.Join(t.TempDir(), "l.json"), 60, time.Millisecond)
	lim.APIGap = 0
	tb := backend.NewTorBox(srv.URL, "tok", lim)

	it, err := tb.Status(context.Background(), "555")
	if err != nil || it.Status != backend.Complete {
		t.Fatalf("item outside the list should be found by ID: %+v %v", it, err)
	}
	if files, err := tb.Files(context.Background(), "555"); err != nil || len(files) != 1 || files[0].Path != "x.mkv" {
		t.Fatalf("files by ID: %+v %v", files, err)
	}
	if it, err := tb.Status(context.Background(), "999"); err != nil || it.Status != backend.Missing {
		t.Fatalf("an item TorBox does not have is missing: %+v %v", it, err)
	}
}
