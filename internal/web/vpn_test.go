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

func TestVPNProviderSettings(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	env := config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()}
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: cfg}
	put := func(body any) (int, string) {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		s.adminVPNSave(w, httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(b)))
		return w.Code, w.Body.String()
	}
	if c, _ := put(map[string]any{"on": true, "provider": "proton"}); c != 400 {
		t.Fatalf("proton without a file accepted: %d", c)
	}
	if c, body := put(map[string]any{"on": true, "provider": "proton", "wireguard": "[Interface]\nPrivateKey = junk\n"}); c != 400 || !bytes.Contains([]byte(body), []byte("PrivateKey")) {
		t.Fatalf("bad file: %d %s", c, body)
	}
	k := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	conf := "[Interface]\nPrivateKey = " + k + "\nAddress = 10.2.0.2/32\n[Peer]\nPublicKey = " + k + "\nEndpoint = 1.2.3.4:51820\n"
	if c, body := put(map[string]any{"on": true, "provider": "airvpn", "wireguard": conf, "port": 41234}); c != 200 {
		t.Fatalf("save: %d %s", c, body)
	}
	if c, _ := put(map[string]any{"on": true, "provider": "airvpn", "port": 41000}); c != 200 { // file kept
		t.Fatalf("save without file: %d", c)
	}
	if v := cfg.Get().VPN; v.Kind() != "airvpn" || v.Port != 41000 || v.WireGuard == "" {
		t.Fatalf("saved %+v", v)
	}
	w := httptest.NewRecorder()
	s.adminVPN(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if !bytes.Contains(w.Body.Bytes(), []byte(`"wgServer":"1.2.3.4:51820"`)) || bytes.Contains(w.Body.Bytes(), []byte(k)) {
		t.Fatalf("view leaks or lacks the file: %s", w.Body.String())
	}
	if c, _ := put(map[string]any{"on": true, "provider": "nord"}); c != 400 {
		t.Fatal("nord without token accepted")
	}
	if c, _ := put(map[string]any{"on": true, "provider": "nord", "token": "tok", "country": "nl"}); c != 200 || cfg.Get().VPN.Country != "NL" {
		t.Fatalf("nord: %d %+v", c, cfg.Get().VPN)
	}
	if c, _ := put(map[string]any{"provider": "expressvpn"}); c != 400 {
		t.Fatal("unknown provider accepted")
	}
}
