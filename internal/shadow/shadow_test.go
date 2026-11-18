package shadow_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/shadow"
)

type fakeSearch struct{ results map[string][]indexers.Result }

func (f fakeSearch) Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error) {
	return indexers.SearchResult{Results: f.results[q.Text]}, nil
}

const history = `{"records":[
 {"sourceTitle":"Heat.1995.1080p.BluRay.x264-GRP","downloadId":"A","date":"2026-11-17T10:00:00Z","quality":{"quality":{"name":"Bluray-1080p"}},"data":{"indexer":"Idx","size":"9000000000"},"movie":{"title":"Heat","year":1995,"tmdbId":949,"runtime":170,"qualityProfileId":1}},
 {"sourceTitle":"Dune.2021.1080p.WEB-DL.H.264-AAA","downloadId":"B","date":"2026-11-17T09:00:00Z","quality":{"quality":{"name":"WEBDL-1080p"}},"data":{"indexer":"Idx"},"movie":{"title":"Dune","year":2021,"qualityProfileId":1}},
 {"sourceTitle":"Alien.1979.720p.BluRay.x264-GRP","downloadId":"C","date":"2026-11-17T08:00:00Z","quality":{"quality":{"name":"Bluray-720p"}},"data":{"indexer":"Idx"},"movie":{"title":"Alien","year":1979,"qualityProfileId":1}},
 {"sourceTitle":"Big.1988.1080p.BluRay.x264-GRP","downloadId":"D","date":"2026-11-17T07:00:00Z","quality":{"quality":{"name":"Bluray-1080p"}},"data":{"indexer":"Idx"},"movie":{"title":"Big","year":1988,"qualityProfileId":9}}
]}`

func TestShadow(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/history":
			w.Write([]byte(history))
		case "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":1,"name":"HD-1080p"},{"id":9,"name":"Nobody"}]`))
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
	st.Categories = []config.Category{{Name: "movies", Kind: "radarr", ArrURL: srv.URL, ArrAPIKey: "k"}}
	st.Shadow.Enabled = true
	cfg.Set(st)
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r := func(title string, size int64) indexers.Result {
		return indexers.Result{Title: title, Size: size, Indexer: "Idx", Protocol: "usenet", Published: now.Add(-48 * time.Hour)}
	}
	gb := int64(1 << 30)
	search := fakeSearch{results: map[string][]indexers.Result{
		"Heat":  {r("Heat.1995.1080p.BluRay.x264-GRP", 9*gb), r("Heat.1995.720p.BluRay.x264-GRP", 5*gb)},
		"Dune":  {r("Dune.2021.1080p.BluRay.x264-BBB", 9*gb)},  // a Bluray the arr did not take
		"Alien": {r("Alien.1979.1080p.BluRay.x264-GRP", 9*gb)}, // the arr's 720p is not in HD-1080p
	}}
	s := &shadow.Service{DB: h, Cfg: cfg, Profiles: &decision.Store{DB: h}, Search: search, Log: log.New(io.Discard, "", 0), Now: time.Now,
		Arr: func(string) *arr.Client { return arr.New("radarr", srv.URL, "k") }}
	n, err := s.Check(context.Background())
	if err != nil || n != 4 {
		t.Fatalf("checked %d: %v", n, err)
	}
	if n, _ := s.Check(context.Background()); n != 0 {
		t.Fatalf("compared twice: %d", n)
	}
	rep, err := s.Report(now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, x := range rep.Rows {
		got[x.Item] = x.Verdict
	}
	want := map[string]string{"Heat (1995)": shadow.Same, "Dune (2021)": shadow.Better, "Alien (1979)": shadow.Rejected, "Big (1988)": shadow.Skipped}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s (%+v)", k, got[k], v, rep.Rows)
		}
	}
	if rep.Compared != 3 || rep.Agree != 1 {
		t.Fatalf("report %+v", rep)
	}
}
