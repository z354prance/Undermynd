package backend

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/job"
)

func TestInfoHashAndMagnet(t *testing.T) {
	info := "d6:lengthi12e4:name5:a.mkv12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae"
	file := "d8:announce9:http://tr4:info" + info + "e"
	sum := sha1.Sum([]byte(info))
	if got, want := InfoHash([]byte(file)), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("info hash %s want %s", got, want)
	}
	if InfoHash([]byte("not a torrent")) != "" {
		t.Fatal("junk should give no hash")
	}
	if got := magnetHash("magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=x"); got != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("hex magnet %q", got)
	}
	if got := magnetHash("magnet:?xt=urn:btih:VPG7XQETAZ5JRJ56KJ7UQCL4WECBMJ5Q"); len(got) != 40 {
		t.Fatalf("base32 magnet %q", got)
	}
}

// A 429 on a create holds back only creates: status checks keep going, and
// the refused create gives its slot back.
func TestCreateLimitDoesNotStopStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/usenet/createusenetdownload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1800")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	mux.HandleFunc("/v1/api/usenet/mylist", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":7,"name":"x","download_state":"downloading","progress":0.5}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lim := NewLimiter("", 60, 0)
	tb := NewTorBox(srv.URL, "k", lim)
	nzb := filepath.Join(t.TempDir(), "a.nzb")
	_ = os.WriteFile(nzb, []byte("<nzb/>"), 0o600)
	_, _, err := tb.Add(context.Background(), job.Job{Name: "a", NZBPath: nzb})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("create: %v", err)
	}
	if lim.Used() != 0 {
		t.Fatalf("the refused create should not count: %d", lim.Used())
	}
	if _, err := tb.Status(context.Background(), "7"); err != nil {
		t.Fatalf("status during a create wait: %v", err)
	}
	if _, _, err := tb.Add(context.Background(), job.Job{Name: "a", NZBPath: nzb}); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second create should wait: %v", err)
	}
	_, _, until := lim.Status()
	if d := time.Until(until); d < 29*time.Minute {
		t.Fatalf("wait %s", d)
	}
}

// A torrent TorBox already has does not use the hourly allowance.
func TestCachedTorrentNotCounted(t *testing.T) {
	const h = "abcdef0123456789abcdef0123456789abcdef01"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/torrents/checkcached", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"` + h + `":{"name":"x"}}}`))
	})
	mux.HandleFunc("/v1/api/torrents/createtorrent", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"torrent_id":9}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lim := NewLimiter("", 60, 0)
	tb := NewTorBoxTorrents(srv.URL, "k", lim)
	id, _, err := tb.Add(context.Background(), job.Job{Name: "a", Magnet: "magnet:?xt=urn:btih:" + h})
	if err != nil || id != "9" {
		t.Fatalf("add %q %v", id, err)
	}
	if lim.Used() != 0 {
		t.Fatalf("a cached torrent counted: %d", lim.Used())
	}
}
