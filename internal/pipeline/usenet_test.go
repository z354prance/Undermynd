package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/sabapi"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/store"
)

type fakeTorBox struct {
	mu       sync.Mutex
	srv      *httptest.Server
	items    map[string]map[string]any
	data     map[string]string // file id -> content
	creates  int
	state    string
	busy     bool
	deleted  []string
	linkHits int
}

func newFakeTorBox() *fakeTorBox {
	f := &fakeTorBox{items: map[string]map[string]any{}, data: map[string]string{}, state: "downloading"}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer tbtoken" {
			http.Error(w, `{"success":false,"error":"BAD_TOKEN"}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/v1/api/usenet/createusenetdownload", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.busy {
			fmt.Fprint(w, `{"success":false,"error":"ACTIVE_LIMIT","detail":"no slot"}`)
			return
		}
		_ = r.ParseMultipartForm(1 << 20)
		if len(r.MultipartForm.File["file"]) == 0 {
			http.Error(w, "no file", 400)
			return
		}
		f.creates++
		id := fmt.Sprint(100 + f.creates)
		f.items[id] = map[string]any{"id": json.Number(id), "name": r.FormValue("name"), "size": 40, "progress": 0.2,
			"download_state": "downloading", "download_finished": false, "files": []any{}}
		fmt.Fprintf(w, `{"success":true,"data":{"usenetdownload_id":%s}}`, id)
	})
	mux.HandleFunc("/v1/api/usenet/mylist", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		list := []map[string]any{}
		for _, it := range f.items {
			list = append(list, it)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": list})
	})
	mux.HandleFunc("/v1/api/usenet/controlusenetdownload", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct {
			ID json.Number `json:"usenet_id"`
			Op string      `json:"operation"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.items, body.ID.String())
		f.deleted = append(f.deleted, body.ID.String())
		fmt.Fprint(w, `{"success":true}`)
	})
	mux.HandleFunc("/v1/api/usenet/requestdl", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.linkHits++
		content := f.data[r.URL.Query().Get("file_id")]
		f.mu.Unlock()
		if r.URL.Query().Get("token") != "tbtoken" {
			http.Error(w, "no", 403)
			return
		}
		http.ServeContent(w, r, "x", time.Time{}, strings.NewReader(content))
	})
	f.srv = httptest.NewServer(mux)
	return f
}

// finish marks an item done with the given files (path -> content).
func (f *fakeTorBox) finish(id string, files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var fl []map[string]any
	n := 0
	for p, c := range files {
		n++
		fid := fmt.Sprintf("%s%d", id, n)
		f.data[fid] = c
		fl = append(fl, map[string]any{"id": json.Number(fid), "name": p, "size": len(c)})
	}
	it := f.items[id]
	it["files"], it["progress"], it["download_finished"], it["download_state"] = fl, 1.0, true, "cached"
}

type urig struct {
	t   *testing.T
	eng *pipeline.Engine
	tb  *fakeTorBox
	arr *fakeArr
	api *httptest.Server
	st  *store.Store
	cfg *config.Config
	now time.Time
}

func newURig(t *testing.T) *urig {
	t.Helper()
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	env := config.Env{ConfigDir: filepath.Join(dir, "config"), DataDir: filepath.Join(dir, "data"), TickInterval: time.Second}
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	tb, ar := newFakeTorBox(), newFakeArr()
	t.Cleanup(tb.srv.Close)
	t.Cleanup(ar.srv.Close)
	s := cfg.Get()
	s.Categories = []config.Category{{Name: "tv-sonarr", Kind: "sonarr", ArrURL: ar.srv.URL, ArrAPIKey: "arrkey"}}
	s.TorBox.Token, s.TorBox.BaseURL, s.TorBox.LinkGapSeconds = "tbtoken", tb.srv.URL, 1
	s.ClientUser, s.ClientPassword = "undermynd", "clientpw"
	if err := cfg.Set(s); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(env.ConfigDir, "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	u := &urig{t: t, tb: tb, arr: ar, cfg: cfg, st: st, now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	st.Now = func() time.Time { return u.now }
	u.eng = pipeline.New(st, cfg, log.New(os.Stderr, "", 0))
	u.eng.Now = func() time.Time { return u.now }
	api := sabapi.New(st, u.eng, cfg, log.New(os.Stderr, "", 0))
	u.api = httptest.NewServer(api.Handler())
	t.Cleanup(u.api.Close)
	return u
}

func (u *urig) tick(n int) {
	for i := 0; i < n; i++ {
		u.now = u.now.Add(time.Minute)
		u.eng.Tick(context.Background())
		u.eng.Wait()
	}
}

func (u *urig) sab(mode string, extra url.Values) map[string]any {
	q := url.Values{"mode": {mode}, "apikey": {"clientpw"}, "output": {"json"}}
	for k, v := range extra {
		q[k] = v
	}
	resp, err := http.Get(u.api.URL + "/sabnzbd/api?" + q.Encode())
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func (u *urig) addNZB(name string) string {
	nzb := `<?xml version="1.0"?><nzb><file subject="x"><segments><segment bytes="40" number="1">a@b</segment></segments></file></nzb>` + name
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("name", name+".nzb")
	_, _ = fw.Write([]byte(nzb))
	_ = w.Close()
	resp, err := http.Post(u.api.URL+"/sabnzbd/api?mode=addfile&cat=tv-sonarr&apikey=clientpw&output=json", w.FormDataContentType(), &buf)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Status bool     `json:"status"`
		IDs    []string `json:"nzo_ids"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	if !out.Status || len(out.IDs) != 1 {
		u.t.Fatalf("addfile: %s", b)
	}
	return out.IDs[0]
}

func (u *urig) slot(list, id string) map[string]any {
	res := u.sab(list, nil)
	box, _ := res[list].(map[string]any)
	slots, _ := box["slots"].([]any)
	for _, s := range slots {
		m := s.(map[string]any)
		if m["nzo_id"] == id {
			return m
		}
	}
	return nil
}

func TestUsenetHappyPath(t *testing.T) {
	u := newURig(t)
	if u.sab("version", nil)["version"] == nil {
		t.Fatal("version should answer without a key")
	}
	if res := u.sab("queue", url.Values{"apikey": {"wrong"}}); res["status"] != false {
		t.Fatalf("a wrong key must be refused: %v", res)
	}
	id := u.addNZB("Show.S01E01.1080p")
	if again := u.addNZB("Show.S01E01.1080p"); again != id {
		t.Fatal("the same NZB must be the same job")
	}
	u.tick(2)
	if u.tb.creates != 1 {
		t.Fatalf("expected one TorBox create, got %d", u.tb.creates)
	}
	if s := u.slot("queue", id); s == nil || s["status"] != "Downloading" {
		t.Fatalf("queue should show it downloading: %v", s)
	}
	body := strings.Repeat("v", 5000)
	u.tb.finish("101", map[string]string{
		"Show.S01E01.1080p/Show.S01E01.1080p.mkv": body,
		"Show.S01E01.1080p/readme.nfo":            "x",
		"Show.S01E01.1080p/Sample/sample.mkv":     "tiny",
	})
	u.tick(3)
	j, _ := u.st.Get(id)
	if j.State != job.Ready {
		t.Fatalf("expected ready, got %s (%s)", j.State, j.Reason)
	}
	got, err := os.ReadFile(filepath.Join(j.CompleteDir, "Show.S01E01.1080p.mkv"))
	if err != nil || string(got) != body {
		t.Fatalf("file not delivered intact: %v", err)
	}
	if exists(filepath.Join(j.CompleteDir, "readme.nfo")) {
		t.Fatal("non-media must not be copied")
	}
	if len(u.tb.deleted) != 1 {
		t.Fatal("the TorBox item must be deleted once the local copy is verified")
	}
	if s := u.slot("history", id); s == nil || s["status"] != "Completed" || s["storage"] != j.CompleteDir {
		t.Fatalf("history should hand the arr the final path: %v", s)
	}
	u.arr.mu.Lock()
	u.arr.imported[strings.ToLower(id)] = true
	u.arr.mu.Unlock()
	u.tick(3)
	j, _ = u.st.Get(id)
	if j.State != job.Cleaned {
		t.Fatalf("expected cleaned, got %s (%s)", j.State, j.Reason)
	}
	if exists(j.CompleteDir) || exists(j.NZBPath) {
		t.Fatal("nothing may be left behind")
	}
}

func TestUsenetBusyWaitsThenSucceeds(t *testing.T) {
	u := newURig(t)
	u.tb.mu.Lock()
	u.tb.busy = true
	u.tb.mu.Unlock()
	id := u.addNZB("Busy.Show.S01E02")
	u.tick(3)
	j, _ := u.st.Get(id)
	if j.State != job.Received || u.tb.creates != 0 {
		t.Fatalf("a busy TorBox must leave the job waiting, got %s (%s)", j.State, j.Reason)
	}
	u.tb.mu.Lock()
	u.tb.busy = false
	u.tb.mu.Unlock()
	u.tick(5) // the limiter backs off for two minutes
	j, _ = u.st.Get(id)
	if j.State == job.Received || j.State == job.Failed {
		t.Fatalf("job should have been submitted after the wait, got %s (%s)", j.State, j.Reason)
	}
}

func TestUsenetTorBoxErrorStateFailsThenArrDeleteCleans(t *testing.T) {
	u := newURig(t)
	id := u.addNZB("Bad.Show.S01E03")
	u.tick(2)
	for i := 0; i < 12; i++ { // every attempt TorBox reports a failure: re-submits twice, then fails
		u.tb.mu.Lock()
		for _, it := range u.tb.items {
			it["download_state"] = "failed"
		}
		u.tb.mu.Unlock()
		u.tick(1)
	}
	j, _ := u.st.Get(id)
	if j.State != job.Failed {
		t.Fatalf("expected failed, got %s (%s)", j.State, j.Reason)
	}
	if s := u.slot("history", id); s == nil || s["status"] != "Failed" {
		t.Fatalf("the arr must see the failure in history: %v", s)
	}
	u.sab("history", url.Values{"name": {"delete"}, "value": {id}})
	u.tick(3)
	j, _ = u.st.Get(id)
	if j.State != job.Cleaned {
		t.Fatalf("expected cleaned after the arr removed it, got %s", j.State)
	}
}
