// Package web serves the public pages and the admin pages on one listener.
// Admin routes sit behind a sign-in; the public side only ever reads the public view.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"github.com/z354prance/undermynd/internal/tdarr"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/store"
)

//go:embed static
var staticFS embed.FS

const (
	cookieName    = "um_session"
	requestHeader = "X-Undermynd-Request"
)

type Server struct {
	Store  *store.Store
	Engine *pipeline.Engine
	Cfg    *config.Config
	Auth   *auth.Auth
	Log    *log.Logger

	mu       sync.Mutex
	setupTry map[string]int
}

func New(s *store.Store, e *pipeline.Engine, c *config.Config, a *auth.Auth, l *log.Logger) *Server {
	return &Server{Store: s, Engine: e, Cfg: c, Auth: a, Log: l, setupTry: map[string]int{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))

	// ---- public: no sign-in, reads only the public view
	mux.HandleFunc("GET /{$}", s.page("public.html"))
	mux.HandleFunc("GET /api/public/jobs", s.publicJobs)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("GET /assets/", files)

	// ---- admin: everything under /admin
	mux.HandleFunc("GET /admin", s.page("admin.html"))
	mux.HandleFunc("GET /admin/", s.page("admin.html"))
	mux.HandleFunc("GET /admin/api/session", s.session)
	mux.HandleFunc("POST /admin/api/setup", s.guard(s.setup))
	mux.HandleFunc("POST /admin/api/setup/confirm", s.guard(s.setupConfirm))
	mux.HandleFunc("POST /admin/api/login", s.guard(s.login))
	mux.HandleFunc("POST /admin/api/logout", s.guard(s.logout))

	mux.HandleFunc("GET /admin/api/jobs", s.admin(s.jobs))
	mux.HandleFunc("GET /admin/api/jobs/{id}", s.admin(s.jobDetail))
	mux.HandleFunc("POST /admin/api/jobs/{id}/{action}", s.guard(s.admin(s.jobAction)))
	mux.HandleFunc("GET /admin/api/settings", s.admin(s.getSettings))
	mux.HandleFunc("PUT /admin/api/settings", s.guard(s.admin(s.putSettings)))
	mux.HandleFunc("POST /admin/api/tdarr/test", s.guard(s.admin(s.tdarrTest)))
	mux.HandleFunc("POST /admin/api/settings/client-password", s.guard(s.admin(s.newClientPassword)))
	return secure(mux)
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/admin") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
}

// ---------------------------------------------------------------- guards

func (s *Server) clientIP(r *http.Request) string {
	if s.Cfg.Env.TrustProxy {
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			return strings.TrimSpace(strings.Split(xf, ",")[0])
		}
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) host(r *http.Request) string {
	if s.Cfg.Env.TrustProxy {
		if xh := r.Header.Get("X-Forwarded-Host"); xh != "" {
			return xh
		}
	}
	return r.Host
}

// guard protects state-changing routes: a required custom header (which a
// cross-site form cannot send) and a same-origin check.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(requestHeader) != "1" {
			jsonErr(w, http.StatusForbidden, "missing request header")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !strings.EqualFold(u.Host, s.host(r)) {
				jsonErr(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		next(w, r)
	}
}

// admin requires a valid session before any handler runs.
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !s.Auth.Valid(c.Value) {
			jsonErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		next(w, r)
	}
}

func (s *Server) signedIn(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && s.Auth.Valid(c.Value)
}

// ---------------------------------------------------------------- public

type publicJob struct {
	Title    string  `json:"title"`
	Group    string  `json:"group"`
	Progress float64 `json:"progress"`
}

// publicJobs is the public view: title, state group and progress only. No
// paths, IDs, failure reasons, backend names or settings.
func (s *Server) publicJobs(w http.ResponseWriter, r *http.Request) {
	out := []publicJob{}
	for _, j := range s.Store.List() {
		if j.State == job.Cleaned {
			continue
		}
		out = append(out, publicJob{Title: j.Name, Group: job.Group(j.State), Progress: job.Overall(j)})
	}
	writeJSON(w, out)
}

// ----------------------------------------------------------------- admin

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"needsSetup": !s.Auth.HasAccount() && !s.Auth.Pending(),
		"pending":    s.Auth.Pending(),
		"signedIn":   s.signedIn(r),
	})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	s.mu.Lock()
	s.setupTry[ip]++
	n := s.setupTry[ip]
	s.mu.Unlock()
	if n > 10 {
		jsonErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var in struct{ Key, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	res, err := s.Auth.Setup(strings.TrimSpace(in.Key), in.Password)
	if err != nil {
		code := http.StatusBadRequest
		if err == auth.ErrBadSetupKey {
			code = http.StatusForbidden
		}
		jsonErr(w, code, err.Error())
		return
	}
	writeJSON(w, res)
}

func (s *Server) setupConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password, Code string }
	if !readJSON(w, r, &in) {
		return
	}
	codes, err := s.Auth.Confirm(in.Password, in.Code)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "that code or password is not right")
		return
	}
	writeJSON(w, map[string]any{"recovery": codes})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password, Code string }
	if !readJSON(w, r, &in) {
		return
	}
	tok, err := s.Auth.Login(s.clientIP(r), in.Password, in.Code)
	switch err {
	case nil:
	case auth.ErrThrottled:
		jsonErr(w, http.StatusTooManyRequests, err.Error())
		return
	default:
		time.Sleep(400 * time.Millisecond)
		jsonErr(w, http.StatusUnauthorized, "sign-in failed") // never says which part was wrong
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/admin", HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteStrictMode, MaxAge: int(s.Auth.SessionTTL.Seconds())})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Auth.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/admin", MaxAge: -1, HttpOnly: true})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	type row struct {
		ID       string    `json:"id"`
		Name     string    `json:"name"`
		Source   string    `json:"source"`
		Category string    `json:"category"`
		State    job.State `json:"state"`
		Reason   string    `json:"reason"`
		Flag     string    `json:"flag"`
		Progress float64   `json:"progress"`
		Backend  string    `json:"backend"`
		Created  time.Time `json:"created"`
		Changed  time.Time `json:"changed"`
	}
	out := []row{}
	for _, j := range s.Store.List() {
		out = append(out, row{j.ID, j.Name, j.Source, j.Category, j.State, j.Reason, j.Flag, job.Overall(j), j.Backend, j.Created, j.StateChanged})
	}
	writeJSON(w, out)
}

func (s *Server) jobDetail(w http.ResponseWriter, r *http.Request) {
	j, ok := s.Store.Get(r.PathValue("id"))
	if !ok {
		jsonErr(w, http.StatusNotFound, "no such job")
		return
	}
	j.Magnet = "" // magnets can carry tracker passkeys
	writeJSON(w, j)
}

func (s *Server) jobAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "retry":
		err = s.Engine.Retry(id)
	case "cancel":
		err = s.Engine.Cancel(id)
	case "imported":
		err = s.Engine.MarkImported(id)
	default:
		jsonErr(w, http.StatusNotFound, "unknown action")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Log.Printf("admin: %s on job %s", r.PathValue("action"), id[:min(8, len(id))])
	writeJSON(w, map[string]any{"ok": true})
}

// --------------------------------------------------------------- settings

type categoryView struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	ArrURL       string `json:"arrUrl"`
	ArrAPIKey    string `json:"arrApiKey,omitempty"` // write-only
	HasArrAPIKey bool   `json:"hasArrApiKey"`
	Transcode    bool   `json:"transcode"`
	OnFail       string `json:"onTranscodeFail"`
}

type settingsView struct {
	Categories []categoryView `json:"categories"`
	QBit       struct {
		URL         string `json:"url"`
		Username    string `json:"username"`
		Password    string `json:"password,omitempty"` // write-only
		HasPassword bool   `json:"hasPassword"`
		SaveDir     string `json:"saveDir"`
	} `json:"qbit"`
	Feeds []config.Feed `json:"feeds"`
	Rules []config.Rule `json:"rules"`
	Tdarr struct {
		URL            string `json:"url"`
		APIKey         string `json:"apiKey,omitempty"` // write-only
		HasAPIKey      bool   `json:"hasApiKey"`
		LibraryID      string `json:"libraryId"`
		MaxInFlight    int    `json:"maxInFlight"`
		StallMinutes   int    `json:"stallMinutes"`
		InDir          string `json:"inDir"`
		OutDir         string `json:"outDir"`
		TimeoutMinutes int    `json:"timeoutMinutes"`
	} `json:"tdarr"`
	NZBGet struct {
		URL         string `json:"url"`
		Username    string `json:"username"`
		Password    string `json:"password,omitempty"` // write-only
		HasPassword bool   `json:"hasPassword"`
		DestRoot    string `json:"destRoot"`
	} `json:"nzbget"`
	TorBox struct {
		Token          string `json:"token,omitempty"` // write-only
		HasToken       bool   `json:"hasToken"`
		CreatePerHour  int    `json:"createPerHour"`
		PullersAtOnce  int    `json:"pullersAtOnce"`
		LinkGapSeconds int    `json:"linkGapSeconds"`
	} `json:"torbox"`
	ClientUser       string `json:"clientUser"`
	StallMinutes     int    `json:"stallMinutes"`
	ReconcileSeconds int    `json:"reconcileSeconds"`
	FlagImportHours  int    `json:"flagImportHours"`
}

// getSettings never includes a secret value, only whether one is set.
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	c := s.Cfg.Get()
	var v settingsView
	for _, k := range c.Categories {
		v.Categories = append(v.Categories, categoryView{Name: k.Name, Kind: k.Kind, ArrURL: k.ArrURL,
			HasArrAPIKey: k.ArrAPIKey != "", Transcode: k.Transcode, OnFail: k.OnTranscodeFail})
	}
	v.QBit.URL, v.QBit.Username, v.QBit.HasPassword, v.QBit.SaveDir = c.QBit.URL, c.QBit.Username, c.QBit.Password != "", c.QBit.SaveDir
	v.Feeds, v.Rules = c.Feeds, c.Rules
	if v.Feeds == nil {
		v.Feeds = []config.Feed{}
	}
	if v.Rules == nil {
		v.Rules = []config.Rule{}
	}
	v.Tdarr.InDir, v.Tdarr.OutDir, v.Tdarr.TimeoutMinutes = c.Tdarr.InDir, c.Tdarr.OutDir, c.Tdarr.TimeoutMinutes
	v.Tdarr.URL, v.Tdarr.HasAPIKey, v.Tdarr.LibraryID = c.Tdarr.URL, c.Tdarr.APIKey != "", c.Tdarr.LibraryID
	v.Tdarr.MaxInFlight, v.Tdarr.StallMinutes = c.Tdarr.MaxInFlight, c.Tdarr.StallMinutes
	v.NZBGet.URL, v.NZBGet.Username, v.NZBGet.HasPassword, v.NZBGet.DestRoot = c.NZBGet.URL, c.NZBGet.Username, c.NZBGet.Password != "", c.NZBGet.DestRoot
	v.TorBox.HasToken, v.TorBox.CreatePerHour, v.TorBox.PullersAtOnce, v.TorBox.LinkGapSeconds = c.TorBox.Token != "", c.TorBox.CreatePerHour, c.TorBox.PullersAtOnce, c.TorBox.LinkGapSeconds
	v.ClientUser, v.StallMinutes, v.ReconcileSeconds, v.FlagImportHours = c.ClientUser, c.StallMinutes, c.ReconcileSeconds, c.FlagImportHours
	writeJSON(w, v)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsView
	if !readJSON(w, r, &in) {
		return
	}
	cur := s.Cfg.Get()
	next := cur
	next.Categories = nil
	for _, c := range in.Categories {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		nc := config.Category{Name: name, Kind: c.Kind, ArrURL: strings.TrimSpace(c.ArrURL), Transcode: c.Transcode, OnTranscodeFail: c.OnFail}
		if c.ArrAPIKey != "" {
			nc.ArrAPIKey = secret.String(c.ArrAPIKey)
		} else if old, ok := cur.Category(name); ok {
			nc.ArrAPIKey = old.ArrAPIKey // blank means unchanged
		}
		next.Categories = append(next.Categories, nc)
	}
	next.QBit.URL, next.QBit.Username = strings.TrimSpace(in.QBit.URL), in.QBit.Username
	if in.QBit.Password != "" {
		next.QBit.Password = secret.String(in.QBit.Password)
	}
	if in.QBit.SaveDir != "" {
		next.QBit.SaveDir = in.QBit.SaveDir
	}
	next.NZBGet.URL, next.NZBGet.Username, next.NZBGet.DestRoot = strings.TrimSpace(in.NZBGet.URL), in.NZBGet.Username, strings.TrimSpace(in.NZBGet.DestRoot)
	next.Feeds, next.Rules = nil, nil
	for _, f := range in.Feeds {
		f.Name, f.URL = strings.TrimSpace(f.Name), strings.TrimSpace(f.URL)
		if f.Name == "" || f.URL == "" {
			continue
		}
		if !strings.HasPrefix(f.URL, "http://") && !strings.HasPrefix(f.URL, "https://") {
			jsonErr(w, http.StatusBadRequest, "feed "+f.Name+": the address must start with http:// or https://")
			return
		}
		next.Feeds = append(next.Feeds, f)
	}
	for _, ru := range in.Rules {
		ru.Name = strings.TrimSpace(ru.Name)
		if ru.Name == "" {
			continue
		}
		for _, pat := range []string{ru.MustContain, ru.MustNotContain} {
			if _, err := regexp.Compile("(?i)" + pat); err != nil {
				jsonErr(w, http.StatusBadRequest, "rule "+ru.Name+": that pattern is not a valid regular expression")
				return
			}
		}
		next.Rules = append(next.Rules, ru)
	}
	next.Tdarr.URL, next.Tdarr.LibraryID = strings.TrimSpace(in.Tdarr.URL), strings.TrimSpace(in.Tdarr.LibraryID)
	if in.Tdarr.APIKey != "" {
		next.Tdarr.APIKey = secret.String(strings.TrimSpace(in.Tdarr.APIKey))
	}
	if in.Tdarr.MaxInFlight > 0 {
		next.Tdarr.MaxInFlight = in.Tdarr.MaxInFlight
	}
	if in.Tdarr.StallMinutes > 0 {
		next.Tdarr.StallMinutes = in.Tdarr.StallMinutes
	}
	if d := strings.TrimSpace(in.Tdarr.InDir); d != "" {
		next.Tdarr.InDir = d
	}
	if d := strings.TrimSpace(in.Tdarr.OutDir); d != "" {
		next.Tdarr.OutDir = d
	}
	if in.Tdarr.TimeoutMinutes > 0 {
		next.Tdarr.TimeoutMinutes = in.Tdarr.TimeoutMinutes
	}
	if in.NZBGet.Password != "" {
		next.NZBGet.Password = secret.String(in.NZBGet.Password)
	}
	if in.TorBox.Token != "" {
		next.TorBox.Token = secret.String(strings.TrimSpace(in.TorBox.Token))
	}
	if in.TorBox.CreatePerHour > 0 {
		next.TorBox.CreatePerHour = in.TorBox.CreatePerHour
	}
	if in.TorBox.PullersAtOnce > 0 {
		next.TorBox.PullersAtOnce = in.TorBox.PullersAtOnce
	}
	if in.TorBox.LinkGapSeconds > 0 {
		next.TorBox.LinkGapSeconds = in.TorBox.LinkGapSeconds
	}
	if in.ClientUser != "" {
		next.ClientUser = in.ClientUser
	}
	if in.StallMinutes > 0 {
		next.StallMinutes = in.StallMinutes
	}
	if in.ReconcileSeconds > 0 {
		next.ReconcileSeconds = in.ReconcileSeconds
	}
	if in.FlagImportHours > 0 {
		next.FlagImportHours = in.FlagImportHours
	}
	if err := s.Cfg.Set(next); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	s.getSettings(w, r)
}

// tdarrTest checks an address and key (a blank key means the stored one) and reports what it found.
func (s *Server) tdarrTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL    string `json:"url"`
		APIKey string `json:"apiKey"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	key := in.APIKey
	if key == "" {
		key = string(s.Cfg.Get().Tdarr.APIKey)
	}
	url := strings.TrimSpace(in.URL)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		jsonErr(w, http.StatusBadRequest, "the address must start with http:// or https://")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	nodes, busy, err := tdarr.New(url, key).Check(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]int{"nodes": nodes, "busyWorkers": busy})
}

// newClientPassword rotates the password the arrs use and shows it once; it is
// the only place a stored secret is ever returned, and it is a fresh value.
func (s *Server) newClientPassword(w http.ResponseWriter, r *http.Request) {
	cur := s.Cfg.Get()
	pw := config.RandomToken(24)
	cur.ClientPassword = secret.String(pw)
	if err := s.Cfg.Set(cur); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	writeJSON(w, map[string]string{"user": cur.ClientUser, "password": pw})
}

// ---------------------------------------------------------------- helpers

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
