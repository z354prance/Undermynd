package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/store"
)

func backupServer(t *testing.T, key byte) (*Server, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	secret.SetKeyForTest(bytes.Repeat([]byte{key}, 32))
	cfg, _ := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d")})
	st, _ := store.Open(filepath.Join(dir, "c", "jobs.json"))
	au, _ := auth.New(filepath.Join(dir, "c"))
	return New(st, pipeline.New(st, cfg, log.New(io.Discard, "", 0)), cfg, au, log.New(io.Discard, "", 0)), cfg
}

// A backup carries the secrets readable only with its passphrase, and
// restores on an install with a different secret key.
func TestSettingsBackupRoundTrip(t *testing.T) {
	a, acfg := backupServer(t, 1)
	s := acfg.Get()
	s.TorBox.Token = "tb-secret-token"
	s.Categories = append(s.Categories, config.Category{Name: "tv", Kind: "sonarr", ArrURL: "http://sonarr", ArrAPIKey: "arr-key"})
	acfg.Set(s)

	rec := httptest.NewRecorder()
	a.adminSettingsExport(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"passphrase":"short"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short passphrase: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	a.adminSettingsExport(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"passphrase":"correct horse"}`)))
	file := rec.Body.Bytes()
	if rec.Code != 200 || bytes.Contains(file, []byte("tb-secret-token")) || bytes.Contains(file, []byte("arr-key")) || bytes.Contains(file, []byte("sonarr")) {
		t.Fatalf("export %d: %s", rec.Code, file)
	}

	b, bcfg := backupServer(t, 2) // another install, another secret key
	ask := func(pass string, apply bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"file": json.RawMessage(file), "passphrase": pass, "apply": apply})
		rec := httptest.NewRecorder()
		b.adminSettingsImport(rec, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
		return rec
	}
	if r := ask("wrong passphrase", false); r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "passphrase") {
		t.Fatalf("wrong passphrase: %d %s", r.Code, r.Body.String())
	}
	r := ask("correct horse", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "TorBox") || !strings.Contains(r.Body.String(), "Categories") {
		t.Fatalf("preview: %d %s", r.Code, r.Body.String())
	}
	if bcfg.Get().TorBox.Token != "" {
		t.Fatal("a preview changed the settings")
	}
	r = ask("correct horse", true)
	if r.Code != 200 {
		t.Fatalf("apply: %d %s", r.Code, r.Body.String())
	}
	got := bcfg.Get()
	if got.TorBox.Token != "tb-secret-token" || len(got.Categories) == 0 || got.Categories[len(got.Categories)-1].ArrAPIKey != "arr-key" {
		t.Fatalf("restored %+v", got.TorBox)
	}
	var out struct {
		SavedOld string `json:"savedOld"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	if _, err := os.Stat(out.SavedOld); err != nil {
		t.Fatalf("old settings not kept: %v", err)
	}
	// Stored encrypted with this install's key.
	raw, _ := os.ReadFile(filepath.Join(bcfg.Env.ConfigDir, "settings.json"))
	if len(raw) == 0 || bytes.Contains(raw, []byte("tb-secret-token")) {
		t.Fatal("restored secrets stored in the clear")
	}
}
