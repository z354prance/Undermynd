package arr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z354prance/undermynd/internal/arr"
)

func server(t *testing.T, key string) *httptest.Server {
	mux := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Api-Key") != key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/v3/system/status", guard(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"appName":"Sonarr","version":"4.0.1"}`))
	}))
	mux.HandleFunc("/api/v3/history", guard(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"records":[]}`))
	}))
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestCheckReportsTheArrAndCatchesABadKey(t *testing.T) {
	s := server(t, "good")
	app, ver, err := arr.New("sonarr", s.URL, "good").Check(context.Background())
	if err != nil || app != "Sonarr" || ver != "4.0.1" {
		t.Fatalf("good key: %q %q %v", app, ver, err)
	}
	if _, _, err := arr.New("sonarr", s.URL, "bad").Check(context.Background()); err == nil || !strings.Contains(err.Error(), "refused the API key") {
		t.Fatalf("a wrong key must say so, got %v", err)
	}
}
