package web

import (
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

// Test everything tries each connection that is set up and the folders, and
// says which failed and why.
func TestTestAll(t *testing.T) {
	tb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/user/me" || r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"email":"a@b","premium_expires_at":"2099-01-01T00:00:00Z"}}`)
	}))
	defer tb.Close()
	dir := t.TempDir()
	cfg, _ := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d")})
	s0 := cfg.Get()
	s0.TorBox.Token = "good"
	s0.TorBox.BaseURL = tb.URL
	s0.Categories = append(s0.Categories, config.Category{Name: "tv", Kind: "sonarr", ArrURL: "http://127.0.0.1:1", ArrAPIKey: "k"}, config.Category{Name: "movies", Kind: "radarr"})
	cfg.Set(s0)
	st, _ := store.Open(filepath.Join(dir, "c", "jobs.json"))
	au, _ := auth.New(filepath.Join(dir, "c"))
	srv := New(st, pipeline.New(st, cfg, log.New(io.Discard, "", 0)), cfg, au, log.New(io.Discard, "", 0))

	rec := httptest.NewRecorder()
	srv.adminTestAll(rec, httptest.NewRequest("POST", "/admin/api/testall", nil))
	var out struct {
		Results []testResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err, rec.Body.String())
	}
	got := map[string]testResult{}
	for _, r := range out.Results {
		got[r.Name] = r
	}
	if r := got["TorBox"]; !r.OK || !strings.Contains(r.Detail, "2099") {
		t.Errorf("TorBox %+v", r)
	}
	if r := got["Sonarr · tv"]; r.OK || r.Detail == "" {
		t.Errorf("Sonarr with nothing listening: %+v", r)
	}
	if r := got["Radarr · movies"]; r.OK || !strings.Contains(r.Detail, "API key") {
		t.Errorf("Radarr with no key: %+v", r)
	}
	if r := got["Work folder"]; !r.OK {
		t.Errorf("work folder %+v", r)
	}
	if out.Results[0].Group != "Folders" {
		t.Errorf("order: %+v", out.Results)
	}
}
