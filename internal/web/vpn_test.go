package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

func TestVPNSettings(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	env := config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()}
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: cfg}
	put := func(body string) int {
		w := httptest.NewRecorder()
		s.adminVPNSave(w, httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(body)))
		return w.Code
	}
	if c := put(`{"on":true,"user":"p1"}`); c != 400 {
		t.Fatalf("no password accepted: %d", c)
	}
	if c := put(`{"on":true,"user":" p1 ","password":"pw","torrents":"local"}`); c != 200 {
		t.Fatalf("save = %d", c)
	}
	if c := put(`{"on":true,"user":"p1","region":"ca"}`); c != 200 { // blank password keeps the saved one
		t.Fatalf("save without password = %d", c)
	}
	v := cfg.Get().VPN
	if v.User != "p1" || v.Pass != "pw" || v.Region != "ca" || v.Torrents != "" {
		t.Fatalf("saved %+v", v)
	}
	if c := put(`{"on":true,"user":"p1","torrents":"bogus"}`); c != 400 {
		t.Fatalf("bad choice accepted: %d", c)
	}
	w := httptest.NewRecorder()
	s.adminVPN(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["hasPassword"] != true || out["password"] != nil || bytes.Contains(w.Body.Bytes(), []byte(`"pw"`)) {
		t.Fatalf("view = %s", w.Body.String())
	}
}
