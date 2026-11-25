package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

func TestUsenetProviders(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	cfg, err := config.Load(config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: cfg}
	put := func(body string) int {
		w := httptest.NewRecorder()
		s.adminUsenetSave(w, httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(body)))
		return w.Code
	}
	if c := put(`{"servers":[{"name":"Main","host":"news.a.com","tls":true,"user":"u1","password":"p1","connections":30,"on":true},
		{"name":"Block","host":"news.b.com","user":"u2","password":"p2","priority":1,"on":true},{"host":""}]}`); c != 200 {
		t.Fatalf("save %d", c)
	}
	// Reordered, passwords left blank: each keeps its own.
	if c := put(`{"mode":"local","servers":[{"name":"Block","host":"news.b.com","user":"u2","priority":1,"on":true},{"name":"Main","host":"news.a.com","tls":true,"user":"u1","connections":500,"on":true}]}`); c != 200 {
		t.Fatalf("save %d", c)
	}
	u := cfg.Get().Usenet
	if len(u.Servers) != 2 || u.Mode != "local" || u.Servers[0].Pass != "p2" || u.Servers[1].Pass != "p1" || u.Servers[1].Conns != 100 || !u.Ready() {
		t.Fatalf("saved %+v", u)
	}
	w := httptest.NewRecorder()
	s.adminUsenet(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if bytes.Contains(w.Body.Bytes(), []byte(`"p1"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"hasPassword":true`)) {
		t.Fatalf("view %s", w.Body.String())
	}
	if c := put(`{"mode":"nope","servers":[]}`); c != 400 {
		t.Fatalf("bad mode accepted")
	}
}
