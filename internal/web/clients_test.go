package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func (e *env) upload(fields map[string]string, header bool) (int, []byte) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+"/admin/api/clients/add", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if header {
		req.Header.Set("X-HyveMynd-Request", "1")
	}
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestClientsAddByHandAndList(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	mag := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Some.App.v2"
	if code, _ := e.upload(map[string]string{"magnet": mag, "category": "radarr"}, false); code != http.StatusForbidden {
		t.Fatalf("no request header: %d", code)
	}
	if code, b := e.upload(map[string]string{"magnet": mag, "category": "nope"}, true); code != 400 || !strings.Contains(string(b), "category") {
		t.Fatalf("bad category: %d %s", code, b)
	}
	if code, b := e.upload(map[string]string{"magnet": "http://x", "category": "radarr"}, true); code != 400 {
		t.Fatalf("not a magnet: %d %s", code, b)
	}
	if code, b := e.upload(map[string]string{"magnet": mag, "category": "radarr", "prefer": "builtin"}, true); code != 200 {
		t.Fatalf("add: %d %s", code, b)
	}
	j, ok := e.st.Get("0123456789abcdef0123456789abcdef01234567")
	if !ok || j.Source != "manual" || j.Prefer != "builtin" || j.Category != "radarr" {
		t.Fatalf("job %+v", j)
	}
	code, b := e.do("GET", "/admin/api/clients", nil, false)
	if code != 200 {
		t.Fatalf("list %d", code)
	}
	var out struct {
		Items []struct {
			Name string   `json:"name"`
			Can  []string `json:"can"`
		} `json:"items"`
	}
	_ = json.Unmarshal(b, &out)
	if len(out.Items) != 1 || out.Items[0].Name != "Some.App.v2" {
		t.Fatalf("items %s", b)
	}
	if code, _ := e.do("POST", "/admin/api/clients/"+j.ID+"/remove", nil, true); code != 200 {
		t.Fatalf("remove %d", code)
	}
	if code, _ := e.do("PUT", "/admin/api/clients/limits", map[string]int{"torrentDownKb": 500, "usenetKb": -3}, true); code != 200 {
		t.Fatal("limits")
	}
	if sp := e.cfg.Get().Speed; sp.TorrentDownKB != 500 || sp.UsenetKB != 0 {
		t.Fatalf("speed %+v", sp)
	}
}
