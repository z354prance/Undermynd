package scheduler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/scheduler"
	"github.com/z354prance/undermynd/internal/secret"
)

func TestBudgetAndBacklog(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	now := time.Now()
	old := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	recent := now.Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	var mu sync.Mutex
	var commands []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v3/wanted/missing":
			var recs []string
			for i := 1; i <= 8; i++ {
				recs = append(recs, fmt.Sprintf(`{"id":%d,"seasonNumber":1,"episodeNumber":%d,"airDateUtc":%q,"series":{"title":"Show"}}`, i, i, old))
			}
			recs = append(recs, fmt.Sprintf(`{"id":99,"seasonNumber":2,"episodeNumber":1,"airDateUtc":%q,"series":{"title":"Show"}}`, recent))
			fmt.Fprintf(w, `{"records":[%s]}`, join(recs))
		case "/api/v3/wanted/cutoff":
			w.Write([]byte(`{"records":[]}`))
		case "/api/v3/command":
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			commands = append(commands, m)
			w.WriteHeader(201)
			w.Write([]byte(`{}`))
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
	st := cfg.Get()
	st.Categories = []config.Category{{Name: "tv", Kind: "sonarr", ArrURL: srv.URL, ArrAPIKey: "k"}, {Name: "native", Kind: "sonarr", ArrURL: srv.URL, ArrAPIKey: "k", Native: true, LibraryDir: dir}}
	st.Scheduler = config.Scheduler{PerHour: 7, Batch: 5, Backlog: true}
	cfg.Set(st)
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	b := &scheduler.Budget{DB: h, Cfg: cfg, Now: time.Now}
	bl := &scheduler.Backlog{DB: h, Cfg: cfg, Budget: b, Log: log.New(io.Discard, "", 0), Now: time.Now,
		Arr: func(string) *arr.Client { return arr.New("sonarr", srv.URL, "k") }}
	ctx := context.Background()

	// A batch of five; the native category is left alone; the episode that
	// aired half an hour ago waits for the grace window.
	if n, err := bl.Once(ctx); err != nil || n != 5 || len(commands) != 1 {
		t.Fatalf("first: %d %v %v", n, err, commands)
	}
	ids := commands[0]["episodeIds"].([]any)
	if commands[0]["name"] != "EpisodeSearch" || len(ids) != 5 || ids[0].(float64) != 1 {
		t.Fatalf("command %v", commands[0])
	}
	// Next run: the five rest; three are left, but the budget (7) has two.
	if n, _ := bl.Once(ctx); n != 2 {
		t.Fatalf("second: %d", n)
	}
	if b.Left() != 0 || b.Take("movies", "x") {
		t.Fatal("the budget is spent")
	}
	if b.Used()["backlog"] != 7 {
		t.Fatalf("used %v", b.Used())
	}
	var none *scheduler.Budget
	if !none.Take("x", "") || !none.Aired("2001-01-01") || none.Aired("2999-01-01") {
		t.Fatal("a nil budget")
	}
	items, _ := bl.Recent(10)
	if len(items) != 7 || items[0].Label == "" {
		t.Fatalf("recent %+v", items)
	}
}

func join(v []string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
