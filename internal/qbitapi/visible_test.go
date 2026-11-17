package qbitapi

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/store"
)

// Usenet jobs are reported by the fake SABnzbd only. If the fake qBittorrent
// listed them too, the arrs would show each usenet grab twice in their queue.
func TestOnlyTorrentJobsAreListed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, j := range []job.Job{
		{ID: "aaaa", Name: "Torrent", Category: "tv-sonarr", Kind: "torrent", State: job.Downloading, Created: now},
		{ID: "bbbb", Name: "Usenet", Category: "tv-sonarr", Kind: "usenet", State: job.Downloading, Created: now},
		{ID: "cccc", Name: "Reencode", Category: "tv-sonarr", Kind: "local", State: job.Transcoding, Created: now},
		{ID: "dddd", Name: "Old torrent without a kind", Category: "tv-sonarr", State: job.Downloading, Created: now},
	} {
		if err := st.Put(j); err != nil {
			t.Fatal(err)
		}
	}
	a := &API{Store: st}
	w := httptest.NewRecorder()
	a.info(w, httptest.NewRequest("GET", "/api/v2/torrents/info?category=tv-sonarr", nil))
	var out []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, o := range out {
		got[o["hash"].(string)] = true
	}
	if len(got) != 2 || !got["aaaa"] || !got["dddd"] {
		t.Fatalf("want only the torrent jobs listed, got %v", got)
	}
	w = httptest.NewRecorder()
	a.properties(w, httptest.NewRequest("GET", "/api/v2/torrents/properties?hash=bbbb", nil))
	if w.Code != 404 {
		t.Fatalf("a usenet job must not be found through qBittorrent, got %d", w.Code)
	}
}
