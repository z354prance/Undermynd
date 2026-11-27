package web

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

// A fake Seerr: request 7 (tv) and 8 (movie) wait; new requests get id 9.
func fakeSeerr(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var approved []string
	status := map[string]int{"7": 1, "8": 1, "9": 1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		switch {
		case r.Method == "POST" && p == "request":
			io.WriteString(w, `{"id":9}`)
		case r.Method == "GET" && p == "request":
			io.WriteString(w, `{"results":[{"id":7,"status":1,"media":{"mediaType":"tv","tmdbId":1}},{"id":8,"status":1,"media":{"mediaType":"movie","tmdbId":2}}]}`)
		case r.Method == "GET" && strings.HasPrefix(p, "request/"):
			fmt.Fprintf(w, `{"status":%d}`, status[strings.TrimPrefix(p, "request/")])
		case r.Method == "POST" && strings.HasSuffix(p, "/approve"):
			id := strings.TrimSuffix(strings.TrimPrefix(p, "request/"), "/approve")
			approved = append(approved, id)
			status[id] = 2
			io.WriteString(w, `{}`)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), approved...) }
}

func TestAutoApprovePerKind(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	srv, approved := fakeSeerr(t)
	cfg, err := config.Load(config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Get()
	c.Requests = config.Requests{Enabled: true, URL: srv.URL, APIKey: "k", UserID: 2}
	cfg.Set(c)
	s := &Server{Cfg: cfg, Log: log.New(io.Discard, "", 0)}
	do := func(h http.HandlerFunc, method, body string) string {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(method, "/", bytes.NewBufferString(body)))
		if w.Code >= 300 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	// Switching on movies approves the movie already waiting, not the show.
	out := do(s.requestsAuto, "PUT", `{"movies":true}`)
	if got := approved(); len(got) != 1 || got[0] != "8" || !strings.Contains(out, `"approved":1`) {
		t.Fatalf("approved %v %s", got, out)
	}
	// A new movie request goes ahead by itself; a show waits.
	do(s.requestCreate, "POST", `{"media_id":5,"media_type":"tv","all_seasons":true}`)
	if got := approved(); len(got) != 1 {
		t.Fatalf("show approved: %v", got)
	}
	do(s.requestCreate, "POST", `{"media_id":6,"media_type":"movie"}`)
	if got := approved(); len(got) != 2 || got[1] != "9" {
		t.Fatalf("movie not approved: %v", got)
	}
	if r := cfg.Get().Requests; !r.AutoMovies || r.AutoTV || r.AutoMusic || r.APIKey == "" {
		t.Fatalf("saved %+v", r)
	}
}
