package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/store"
	"github.com/z354prance/undermynd/internal/web"
)

type env struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
	cfg *config.Config
	au  *auth.Auth
	cl  *http.Client
}

func newEnv(t *testing.T) *env {
	dir := t.TempDir()
	e := config.Env{ConfigDir: filepath.Join(dir, "config"), DataDir: filepath.Join(dir, "data")}
	cfg, err := config.Load(e)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Get()
	s.Categories = []config.Category{{Name: "radarr", Kind: "radarr", ArrURL: "http://radarr", ArrAPIKey: "TOP-SECRET-ARR-KEY"}}
	s.QBit.Password = "TOP-SECRET-QBIT-PW"
	_ = cfg.Set(s)
	st, _ := store.Open(filepath.Join(e.ConfigDir, "jobs.json"))
	au, err := auth.New(e.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	l := log.New(io.Discard, "", 0)
	srv := httptest.NewServer(web.New(st, pipeline.New(st, cfg, l), cfg, au, l).Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &env{t, srv, st, cfg, au, &http.Client{Jar: jar}}
}

func (e *env) do(method, path string, body any, header bool) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if header {
		req.Header.Set("X-Undermynd-Request", "1")
	}
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) signIn() {
	code, b := e.do("POST", "/admin/api/setup", map[string]string{"key": e.au.SetupKey(), "password": "a long enough password"}, true)
	if code != 200 {
		e.t.Fatalf("setup: %d %s", code, b)
	}
	var res struct{ TotpSecret string }
	_ = json.Unmarshal(b, &res)
	if code, b = e.do("POST", "/admin/api/setup/confirm", map[string]string{"password": "a long enough password", "code": auth.CodeAt(res.TotpSecret, time.Now())}, true); code != 200 {
		e.t.Fatalf("confirm: %d %s", code, b)
	}
	if code, b = e.do("POST", "/admin/api/login", map[string]string{"password": "a long enough password", "code": auth.CodeAt(res.TotpSecret, time.Now().Add(30*time.Second))}, true); code != 200 {
		e.t.Fatalf("login: %d %s", code, b)
	}
}

func TestPublicViewLeaksNothing(t *testing.T) {
	e := newEnv(t)
	_ = e.st.Put(job.Job{ID: "abcdef0123456789abcdef0123456789abcdef01", Name: "Public.Title.2024", Category: "radarr", Source: "radarr",
		State: job.Failed, Reason: "internal path /data/undermynd/staging/x failed", SavePath: "/data/secret/path", Backend: "qbittorrent",
		Magnet: "magnet:?xt=urn:btih:abc&tr=http://tracker/announce?passkey=SEKRET", Created: time.Now()})
	code, b := e.do("GET", "/api/public/jobs", nil, false)
	if code != 200 {
		t.Fatal(code)
	}
	body := string(b)
	for _, leak := range []string{"/data", "passkey", "SEKRET", "qbittorrent", "abcdef0123", "internal path"} {
		if strings.Contains(body, leak) {
			t.Fatalf("public view leaked %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "Public.Title.2024") {
		t.Fatal("title should be public")
	}
}

func TestAdminRoutesNeedSignIn(t *testing.T) {
	e := newEnv(t)
	for _, c := range [][2]string{{"GET", "/admin/api/jobs"}, {"GET", "/admin/api/settings"}, {"GET", "/admin/api/jobs/x"}} {
		if code, _ := e.do(c[0], c[1], nil, true); code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session: %d", c[0], c[1], code)
		}
	}
	for _, c := range [][2]string{{"POST", "/admin/api/jobs/x/cancel"}, {"PUT", "/admin/api/settings"}, {"POST", "/admin/api/settings/client-password"}} {
		if code, _ := e.do(c[0], c[1], map[string]any{}, true); code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session: %d", c[0], c[1], code)
		}
	}
}

func TestStateChangesNeedHeaderAndSameOrigin(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	if code, _ := e.do("POST", "/admin/api/jobs/x/cancel", nil, false); code != http.StatusForbidden {
		t.Fatalf("missing header must be refused, got %d", code)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/admin/api/jobs/x/cancel", nil)
	req.Header.Set("X-Undermynd-Request", "1")
	req.Header.Set("Origin", "https://evil.example")
	resp, _ := e.cl.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin must be refused, got %d", resp.StatusCode)
	}
}

func TestSettingsNeverReturnSecrets(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	code, b := e.do("GET", "/admin/api/settings", nil, false)
	if code != 200 {
		t.Fatal(code)
	}
	if strings.Contains(string(b), "TOP-SECRET") || strings.Contains(string(b), string(e.cfg.Get().ClientPassword)) {
		t.Fatalf("settings leaked a secret: %s", b)
	}
	if !strings.Contains(string(b), `"hasArrApiKey":true`) || !strings.Contains(string(b), `"hasPassword":true`) {
		t.Fatalf("should say whether a secret is set: %s", b)
	}
	// saving with blank secret fields keeps the stored values
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	if code, _ = e.do("PUT", "/admin/api/settings", v, true); code != 200 {
		t.Fatal(code)
	}
	got := e.cfg.Get()
	if string(got.Categories[0].ArrAPIKey) != "TOP-SECRET-ARR-KEY" || string(got.QBit.Password) != "TOP-SECRET-QBIT-PW" {
		t.Fatal("blank secret fields must keep the stored secret")
	}
}

func TestSecretsEncryptedOnDisk(t *testing.T) {
	e := newEnv(t)
	raw, err := os.ReadFile(filepath.Join(e.cfg.Env.ConfigDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "TOP-SECRET") {
		t.Fatalf("a secret is stored in plain text: %s", raw)
	}
}

func TestSetupNeedsKeyAndWorksOnce(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do("POST", "/admin/api/setup", map[string]string{"key": "guess", "password": "a long enough password"}, true); code != http.StatusForbidden {
		t.Fatalf("setup without the key must be refused, got %d", code)
	}
	e.signIn()
	if code, _ := e.do("POST", "/admin/api/setup", map[string]string{"key": "anything", "password": "another long password"}, true); code != http.StatusBadRequest && code != http.StatusForbidden {
		t.Fatalf("a second setup must fail, got %d", code)
	}
}

func TestLoginFailureDoesNotSayWhich(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	_, b := e.do("POST", "/admin/api/login", map[string]string{"password": "wrong password!!", "code": "000000"}, true)
	if strings.Contains(strings.ToLower(string(b)), "password") && !strings.Contains(string(b), "sign-in failed") {
		t.Fatalf("login error reveals too much: %s", b)
	}
}

func TestPagesServe(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/", "/admin", "/assets/app.css", "/assets/admin.js", "/assets/public.js", "/assets/favicon.svg", "/health"} {
		if code, _ := e.do("GET", p, nil, false); code != 200 {
			t.Fatalf("%s: %d", p, code)
		}
	}
}

func TestLiveStreamAndNewAdminRoutesNeedSignIn(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/admin/api/events", "/admin/api/system"} {
		if code, _ := e.do("GET", p, nil, false); code != 401 {
			t.Fatalf("%s must need a session, got %d", p, code)
		}
	}
	if code, _ := e.do("POST", "/admin/api/intake", map[string]bool{"paused": true}, true); code != 401 {
		t.Fatalf("intake must need a session, got %d", code)
	}
	if code, _ := e.do("POST", "/admin/api/tdarr/test", map[string]string{"url": "http://x"}, true); code != 401 {
		t.Fatalf("tdarr test must need a session, got %d", code)
	}
	e.signIn()
	_ = e.st.Put(job.Job{ID: "abc", Name: "Stream.Me", Source: "radarr", Category: "radarr", State: job.Downloading, Progress: 0.5,
		Created: time.Now(), StateChanged: time.Now()})
	req, _ := http.NewRequest("GET", e.srv.URL+"/admin/api/events", nil)
	resp, err := e.cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected an event stream, got %q", ct)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "event: jobs") || !strings.Contains(got, "Stream.Me") {
		t.Fatalf("the first event should carry the job list, got %q", got)
	}
	if strings.Contains(got, "TOP-SECRET") {
		t.Fatal("the stream must never carry secrets")
	}
}

func TestPauseIntakeAndSystem(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	if code, b := e.do("POST", "/admin/api/intake", map[string]bool{"paused": true}, true); code != 200 {
		t.Fatalf("pause: %d %s", code, b)
	}
	if !e.cfg.Get().IntakePaused {
		t.Fatal("pause must be saved")
	}
	code, b := e.do("GET", "/admin/api/system", nil, false)
	if code != 200 || !strings.Contains(string(b), `"paused":true`) {
		t.Fatalf("system should report paused: %d %s", code, b)
	}
}

// Renaming a category in the form must not drop the arr key that was saved under the old name.
func TestRenamingACategoryKeepsItsArrKey(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	_, b := e.do("GET", "/admin/api/settings", nil, false)
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	cats := v["categories"].([]any)
	c := cats[0].(map[string]any)
	c["origName"] = c["name"]
	c["name"] = "renamed"
	if code, _ := e.do("PUT", "/admin/api/settings", v, true); code != 200 {
		t.Fatal(code)
	}
	got := e.cfg.Get()
	if got.Categories[0].Name != "renamed" || string(got.Categories[0].ArrAPIKey) != "TOP-SECRET-ARR-KEY" {
		t.Fatalf("a renamed category must keep its key: %+v", got.Categories[0])
	}
}
