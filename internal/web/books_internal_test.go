package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/store"
)

// Book requests: search marks what BookOrbit has, a request waits, and
// approving it creates the request in BookOrbit with the key.
func TestBookRequests(t *testing.T) {
	var created map[string]any
	bo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bo_key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/book-requests/availability":
			var in struct {
				Items []map[string]any `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			out := []map[string]any{}
			for _, it := range in.Items {
				if it["title"] == "Dune" {
					out = append(out, map[string]any{"ownedBookId": 5, "existingRequestId": nil})
				} else {
					out = append(out, map[string]any{"ownedBookId": nil, "existingRequestId": nil})
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/api/v1/book-requests":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"request":{"id":9}}`)
		case "/api/v1/libraries":
			_, _ = io.WriteString(w, `[{"id":1},{"id":2}]`)
		case "/api/v1/libraries/1/stats":
			_, _ = io.WriteString(w, `{"totalBooks":30,"formatCounts":{"epub":25,"m4b":5}}`)
		case "/api/v1/libraries/2/stats":
			_, _ = io.WriteString(w, `{"totalBooks":8,"formatCounts":{"mp3":8}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer bo.Close()
	ol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"docs":[{"key":"/works/OL1W","title":"Dune","author_name":["Frank Herbert"],"first_publish_year":1965,"cover_i":42,"isbn":["0441013597","9780441013593"]},
			{"key":"/works/OL2W","title":"Dune Messiah","author_name":["Frank Herbert"],"first_publish_year":1969}]}`)
	}))
	defer ol.Close()
	old := openLibraryBase
	openLibraryBase = ol.URL
	defer func() { openLibraryBase = old }()

	dir := t.TempDir()
	cfg, _ := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d")})
	s0 := cfg.Get()
	s0.Requests.Enabled = true
	s0.BookOrbit = config.BookOrbit{URL: bo.URL, APIKey: "bo_key"}
	cfg.Set(s0)
	st, _ := store.Open(filepath.Join(dir, "c", "jobs.json"))
	au, _ := auth.New(filepath.Join(dir, "c"))
	srv := New(st, pipeline.New(st, cfg, log.New(io.Discard, "", 0)), cfg, au, log.New(io.Discard, "", 0))

	rec := httptest.NewRecorder()
	srv.bookSearch(rec, httptest.NewRequest("GET", "/api/v1/book-request/search?kind=ebook&query=dune", nil))
	var res struct {
		Results []bookResult `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Results) != 2 || !res.Results[0].InLibrary || res.Results[1].InLibrary || res.Results[0].ISBN13 != "9780441013593" || !strings.HasPrefix(res.Results[0].Image, "/img?") {
		t.Fatalf("%s", rec.Body.String())
	}

	body, _ := json.Marshal(map[string]any{"key": "/works/OL2W", "title": "Dune Messiah", "authors": []string{"Frank Herbert"}, "year": 1969, "kind": "audiobook"})
	rec = httptest.NewRecorder()
	srv.bookRequestCreate(rec, httptest.NewRequest("POST", "/api/v1/book-request", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.bookRequestCreate(rec, httptest.NewRequest("POST", "/api/v1/book-request", bytes.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a second request for the same book: %d", rec.Code)
	}
	waiting, _ := srv.bookLists()
	if len(waiting) != 1 {
		t.Fatalf("waiting %+v", waiting)
	}
	m, err := srv.decideBook(context.Background(), waiting[0].ID, "approve")
	if err != nil || m.Status != "approved" {
		t.Fatalf("%v %+v", err, m)
	}
	if created["title"] != "Dune Messiah" || created["mediaKind"] != "audiobook" || created["publishedYear"] != 1969.0 {
		t.Fatalf("created %v", created)
	}
	eb, aud, err := srv.bookOrbit().bookCounts(context.Background())
	if err != nil || eb != 25 || aud != 13 {
		t.Fatalf("counts %d %d %v", eb, aud, err)
	}
}
