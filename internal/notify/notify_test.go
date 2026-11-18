package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

func TestSend(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Path] = string(b) + "|" + r.Header.Get("Title")
		mu.Unlock()
		if r.URL.Path == "/bad" {
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	n := &Notifier{HTTP: srv.Client()}
	ctx := context.Background()
	e := Event{Event: "imported", Item: "Heat (1995)", Quality: "Bluray-1080p", Release: "Heat.1995.1080p.BluRay.x264-GRP"}
	for _, k := range []string{"webhook", "discord", "ntfy"} {
		if err := n.Send(ctx, config.Notification{Name: k, Kind: k, URL: secret.String(srv.URL + "/" + k)}, e); err != nil {
			t.Fatal(k, err)
		}
	}
	var wh Event
	json.Unmarshal([]byte(strings.Split(got["/webhook"], "|")[0]), &wh)
	if wh.Item != "Heat (1995)" || !strings.Contains(got["/discord"], `"content":"Imported: Heat (1995) (Bluray-1080p)`) || !strings.HasSuffix(got["/ntfy"], "|HyveMynd: imported") {
		t.Fatalf("%v", got)
	}
	start := time.Now()
	if err := n.Send(ctx, config.Notification{Kind: "webhook", URL: secret.String(srv.URL + "/bad")}, e); err == nil || time.Since(start) > time.Second {
		t.Fatal("a 404 is not tried again")
	}
	if wants(config.Notification{Events: []string{"failed"}}, "imported") || !wants(config.Notification{}, "imported") {
		t.Fatal("events")
	}
}
