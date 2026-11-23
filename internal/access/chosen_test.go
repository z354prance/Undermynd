package access

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	secretbox "github.com/z354prance/undermynd/internal/secret"
)

// fakeEmby keeps users in memory and answers the calls the access flow makes.
type fakeEmby struct {
	mu       sync.Mutex
	users    map[string]*embyUser
	password map[string]string
}

func newFakeEmby() *fakeEmby {
	f := &fakeEmby{users: map[string]*embyUser{}, password: map[string]string{}}
	f.users["tmpl"] = &embyUser{ID: "tmpl", Name: "template", Policy: map[string]any{"IsDisabled": true, "IsAdministrator": false, "EnableAllFolders": true, "EnableMediaPlayback": true}, Configuration: map[string]any{"PlayDefaultAudioTrack": true}}
	return f
}

func (f *fakeEmby) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	out := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == "GET" && r.URL.Path == "/Users":
		var l []embyUser
		for _, u := range f.users {
			l = append(l, *u)
		}
		out(l)
	case r.Method == "POST" && r.URL.Path == "/Users/New":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		t := f.users["tmpl"]
		u := &embyUser{ID: "u1", Name: in["Name"].(string), Policy: copySettings(t.Policy), Configuration: copySettings(t.Configuration)}
		f.users["u1"] = u
		out(u)
	case r.Method == "GET" && r.URL.Path == "/Connect/Pending":
		out([]any{})
	case len(parts) == 2 && parts[0] == "Users" && r.Method == "GET":
		if u := f.users[parts[1]]; u != nil {
			out(u)
			return
		}
		w.WriteHeader(404)
	case len(parts) == 3 && parts[0] == "Users" && r.Method == "POST":
		u := f.users[parts[1]]
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch parts[2] {
		case "Policy":
			u.Policy = in
		case "Configuration":
			u.Configuration = in
		case "Password":
			f.password[parts[1]], _ = in["NewPw"].(string)
		}
	case len(parts) == 4 && parts[2] == "Connect" && parts[3] == "Link":
		f.users[parts[1]].ConnectUserName = r.URL.Query().Get("ConnectUsername")
		out(map[string]any{"IsPending": true})
	default:
		w.WriteHeader(404)
	}
}

func chosenManager(t *testing.T, provider string, srv *httptest.Server, mails *[]string) *Manager {
	t.Helper()
	secretbox.SetKeyForTest(make([]byte, 32))
	s := Settings{Provider: provider, Enabled: true, PublicURL: "https://x.example.com", AdminEmail: "admin@example.com", From: "u@example.com", EmbyURL: srv.URL, EmbyKey: "k", TemplateID: "tmpl"}
	m, err := New(t.TempDir(), func() Settings { return s }, func(v Settings) error { s = v; return nil })
	if err != nil {
		t.Fatal(err)
	}
	m.send = func(_ Settings, to, subject string, _ letter) error {
		*mails = append(*mails, subject)
		return nil
	}
	return m
}

// Emby: the password chosen in the request is set at approval and deleted;
// the account switches on by itself once Emby's Connect link is confirmed.
func TestChosenPasswordEmby(t *testing.T) {
	f := newFakeEmby()
	srv := httptest.NewServer(f)
	defer srv.Close()
	var mails []string
	m := chosenManager(t, "emby", srv, &mails)
	if err := m.Submit(Request{Name: "Ann", Email: "ann@example.com", Username: "ann", Connect: "ann@example.com", Password: "short"}); err == nil {
		t.Fatal("a 5-character password was accepted")
	}
	if err := m.Submit(Request{Name: "Ann", Email: "ann@example.com", Username: "ann", Connect: "ann@example.com", Password: "hunter2222"}); err != nil {
		t.Fatal(err)
	}
	m.fmu.Lock()
	enc := m.recs[0].PasswordEnc
	m.fmu.Unlock()
	if enc == "" || strings.Contains(enc, "hunter2222") {
		t.Fatalf("password not stored encrypted: %q", enc)
	}
	if l, _ := m.List(); l[0].Password != "" {
		t.Fatal("the password is listed")
	}
	if err := m.Approve(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if f.password["u1"] != "hunter2222" {
		t.Fatalf("password on the server: %q", f.password["u1"])
	}
	l, _ := m.List()
	m.fmu.Lock()
	left := m.recs[0].PasswordEnc
	m.fmu.Unlock()
	if l[0].Status != "awaiting_connect" || left != "" {
		t.Fatalf("after approval: %s, stored %q", l[0].Status, left)
	}
	if mails[len(mails)-1] != "You're approved - one last step" {
		t.Fatalf("mails %v", mails)
	}
	// Not confirmed yet: stays off.
	m.FinishConnected(context.Background())
	if l, _ := m.List(); l[0].Status != "awaiting_connect" {
		t.Fatalf("finished before Connect was confirmed: %s", l[0].Status)
	}
	f.mu.Lock()
	f.users["u1"].ConnectLinkType = "LinkedUser"
	f.mu.Unlock()
	m.FinishConnected(context.Background())
	if l, _ := m.List(); l[0].Status != "active" {
		t.Fatalf("not switched on after confirmation: %+v", l[0])
	}
	if f.users["u1"].Policy["IsDisabled"] != false || f.password["u1"] != "hunter2222" {
		t.Fatalf("account %+v password %q", f.users["u1"].Policy, f.password["u1"])
	}
	if mails[len(mails)-1] != "You're ready to watch" {
		t.Fatalf("mails %v", mails)
	}
}

// Jellyfin: approval switches the account on at once with the chosen password.
func TestChosenPasswordJellyfin(t *testing.T) {
	f := newFakeEmby()
	srv := httptest.NewServer(f)
	defer srv.Close()
	var mails []string
	m := chosenManager(t, "jellyfin", srv, &mails)
	if err := m.Submit(Request{Name: "Bo", Email: "bo@example.com", Username: "bob", Password: "password8"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Approve(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if l, _ := m.List(); l[0].Status != "active" || f.password["u1"] != "password8" {
		t.Fatalf("jellyfin: %+v %q", l[0], f.password["u1"])
	}
}

// Declining deletes the stored password.
func TestDeclineDeletesPassword(t *testing.T) {
	f := newFakeEmby()
	srv := httptest.NewServer(f)
	defer srv.Close()
	var mails []string
	m := chosenManager(t, "emby", srv, &mails)
	_ = m.Submit(Request{Name: "Cy", Email: "cy@example.com", Username: "cyd", Connect: "cy@example.com", Password: "password8"})
	if err := m.Decline(1); err != nil {
		t.Fatal(err)
	}
	m.fmu.Lock()
	defer m.fmu.Unlock()
	if m.recs[0].PasswordEnc != "" {
		t.Fatal("declined request kept its password")
	}
}
