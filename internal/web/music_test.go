package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

const kbID = "b6d2b4a4-9f1e-4c51-9e2a-0d2b3b0f6a11"

type fakeLidarr struct {
	srv   *httptest.Server
	down  atomic.Bool
	mu    sync.Mutex
	added []map[string]any
	inLib bool
}

func newFakeLidarr(t *testing.T) *fakeLidarr {
	f := &fakeLidarr{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "lk" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/artist/lookup":
			if f.down.Load() {
				w.WriteHeader(503)
				return
			}
			id := 0
			if f.inLib {
				id = 12
			}
			json.NewEncoder(w).Encode([]map[string]any{{"id": id, "artistName": "Khruangbin", "foreignArtistId": kbID, "artistType": "Group", "overview": "Trio from Houston",
				"images": []map[string]string{{"coverType": "poster", "remoteUrl": "https://imagecache.lidarr.audio/v1/kb.jpg"}}}, {"artistName": "No id", "foreignArtistId": "x"}})
		case "/api/v1/rootfolder":
			io.WriteString(w, `[{"path":"/music","defaultQualityProfileId":3,"defaultMetadataProfileId":0}]`)
		case "/api/v1/metadataprofile":
			io.WriteString(w, `[{"id":5,"name":"Standard"}]`)
		case "/api/v1/artist":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			f.added = append(f.added, b)
			io.WriteString(w, `{"id":99,"artistName":"Khruangbin"}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func musicServer(t *testing.T, l *fakeLidarr) (*Server, *config.Config) {
	secret.SetKeyForTest(make([]byte, 32))
	cfg, err := config.Load(config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Get()
	c.Requests.Enabled = true
	c.Categories = append(c.Categories, config.Category{Name: "music", Kind: "lidarr", ArrURL: l.srv.URL, ArrAPIKey: "lk"})
	cfg.Set(c)
	return &Server{Cfg: cfg, Log: log.New(io.Discard, "", 0)}, cfg
}

func call(t *testing.T, h http.HandlerFunc, method, target, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	r.RemoteAddr = "10.1.2.3:4"
	h(w, r)
	return w.Code, w.Body.String()
}

func TestMusicRequests(t *testing.T) {
	l := newFakeLidarr(t)
	s, cfg := musicServer(t, l)
	reqLimit = requestLimiter{}
	code, body := call(t, s.musicSearch, "GET", "/?query=khruangbin", "")
	if code != 200 || !strings.Contains(body, kbID) || !strings.Contains(body, `"/img?`) || strings.Contains(body, `"No id"`) || !strings.Contains(body, "Group") {
		t.Fatalf("search: %d %s", code, body)
	}
	if code, body = call(t, s.musicRequestCreate, "POST", "/", `{"mbid":"`+kbID+`","name":"Khruangbin"}`); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if code, _ = call(t, s.musicRequestCreate, "POST", "/", `{"mbid":"`+kbID+`","name":"Khruangbin"}`); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}
	if code, _ = call(t, s.musicRequestCreate, "POST", "/", `{"mbid":"not-an-id","name":"x"}`); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
	waiting, _ := s.musicLists()
	if len(waiting) != 1 || len(l.added) != 0 {
		t.Fatalf("waiting %+v added %d", waiting, len(l.added))
	}
	// Approve: added with every album monitored and searched, Lidarr's defaults.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	r.SetPathValue("id", waiting[0].ID)
	r.SetPathValue("action", "approve")
	s.musicDecide(w, r)
	if w.Code != 200 || len(l.added) != 1 {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	a := l.added[0]
	opts, _ := a["addOptions"].(map[string]any)
	if a["foreignArtistId"] != kbID || a["rootFolderPath"] != "/music" || a["qualityProfileId"] != 3.0 || a["metadataProfileId"] != 5.0 || a["monitored"] != true ||
		opts["monitor"] != "all" || opts["searchForMissingAlbums"] != true {
		t.Fatalf("added %+v", a)
	}
	if _, decided := s.musicLists(); len(decided) != 1 || decided[0].Status != "approved" {
		t.Fatalf("decided %+v", decided)
	}
	// Auto-approve for music: a request goes straight to Lidarr; one already
	// in Lidarr is just marked.
	c := cfg.Get()
	c.Requests.AutoMusic = true
	cfg.Set(c)
	s.musicMem = nil
	l.inLib = true
	if code, body = call(t, s.musicRequestCreate, "POST", "/", `{"mbid":"`+kbID+`","name":"Khruangbin"}`); code != 201 {
		t.Fatalf("create 2: %d %s", code, body)
	}
	if _, decided := s.musicLists(); len(decided) != 1 || decided[0].Note != "already in Lidarr" || len(l.added) != 1 {
		t.Fatalf("auto: %+v %d", decided, len(l.added))
	}
}

func TestMusicSearchFallsBackToMusicBrainz(t *testing.T) {
	l := newFakeLidarr(t)
	l.down.Store(true)
	mb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.UserAgent(), "HyveMynd") || r.URL.Query().Get("query") != "khruangbin" {
			w.WriteHeader(400)
			return
		}
		io.WriteString(w, `{"artists":[{"id":"`+kbID+`","name":"Khruangbin","type":"Group","country":"US"}]}`)
	}))
	defer mb.Close()
	old := musicBrainzBase
	musicBrainzBase = mb.URL
	defer func() { musicBrainzBase = old }()
	s, _ := musicServer(t, l)
	code, body := call(t, s.musicSearch, "GET", "/?query=khruangbin", "")
	if code != 200 || !strings.Contains(body, kbID) || !strings.Contains(body, "Group · US") {
		t.Fatalf("fallback: %d %s", code, body)
	}
}

func TestMusicClosedWithoutLidarr(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	cfg, _ := config.Load(config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()})
	s := &Server{Cfg: cfg}
	if code, _ := call(t, s.musicSearch, "GET", "/?query=abc", ""); code != 503 {
		t.Fatalf("search without Lidarr: %d", code)
	}
}
