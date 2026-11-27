// Package web serves the public pages and the admin pages on one listener.
// Admin routes sit behind a sign-in; the public side only ever reads the public view.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/z354prance/undermynd/internal/attention"
	"github.com/z354prance/undermynd/internal/state"
	"github.com/z354prance/undermynd/internal/tdarr"
	"github.com/z354prance/undermynd/internal/usenet"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/access"
	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/logbuf"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/progress"
	"github.com/z354prance/undermynd/internal/rss"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/shows"
	"github.com/z354prance/undermynd/internal/storage"
	"github.com/z354prance/undermynd/internal/store"
)

//go:embed static
var staticFS embed.FS

const (
	cookieName    = "um_session"
	requestHeader = "X-HyveMynd-Request"
)

type Server struct {
	Store *store.Store
	// Blobs keeps small state (music requests); nil: in memory.
	Blobs    state.Blobs
	musicMem []musicRequest
	Engine   *pipeline.Engine
	Cfg      *config.Config
	Auth     *auth.Auth
	Log      *log.Logger
	Logs     *logbuf.Buffer // recent log lines for the admin Log tab
	Progress interface{ Snapshot() progress.Snapshot }
	Storage  *storage.Monitor
	Access   *access.Manager
	// Metadata is nil when state is kept in JSON files (no database).
	Metadata *metadata.Service
	// Shows follows show folders of the simple importer (the Anime page); nil
	// without the database.
	Shows *shows.Service
	// Attention moves much-watched titles to an upgrade profile.
	Attention *attention.Service
	// VPN is the PIA tunnel and HyveMynd's own torrent client.
	VPN *VPN
	// News is HyveMynd's own usenet downloader.
	News *usenet.Downloader
	RSS  interface {
		Status() []rss.FeedStatus
		Download(ctx context.Context, feed string, index int, title, category string) (string, error)
		CheckNow(context.Context)
	}

	mu       sync.Mutex
	setupTry map[string]int
}

func New(s *store.Store, e *pipeline.Engine, c *config.Config, a *auth.Auth, l *log.Logger) *Server {
	return &Server{Store: s, Engine: e, Cfg: c, Auth: a, Log: l, setupTry: map[string]int{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	// The container has no MIME table; fonts must say what they are.
	mime.AddExtensionType(".woff2", "font/woff2")
	files := http.FileServer(http.FS(sub))

	// ---- public: no sign-in, reads only the public view
	mux.HandleFunc("GET /{$}", s.page("home.html"))
	// The earlier public page, for a release while the new one settles in.
	mux.HandleFunc("GET /classic", s.page("public.html"))
	mux.Handle("GET /home/", files)
	mux.HandleFunc("GET /credits", s.page("credits.html"))
	mux.HandleFunc("GET /api/public/jobs", s.publicJobs)
	s.dashboardRoutes(mux, files)
	mux.HandleFunc("GET /admin/api/library", s.admin(s.adminLibrary))
	mux.HandleFunc("GET /admin/api/missing", s.admin(s.adminMissing))
	mux.HandleFunc("GET /admin/api/clients", s.admin(s.adminClients))
	mux.HandleFunc("POST /admin/api/clients/add", s.guard(s.admin(s.adminClientAdd)))
	mux.HandleFunc("PUT /admin/api/clients/limits", s.admin(s.adminClientLimits))
	mux.HandleFunc("POST /admin/api/clients/{id}/{action}", s.guard(s.admin(s.adminClientAction)))
	mux.HandleFunc("GET /admin/api/clients/{id}/log", s.admin(s.adminClientLog))
	mux.HandleFunc("GET /admin/api/usenet", s.admin(s.adminUsenet))
	mux.HandleFunc("PUT /admin/api/usenet", s.admin(s.adminUsenetSave))
	mux.HandleFunc("POST /admin/api/usenet/test", s.admin(s.adminUsenetTest))
	mux.HandleFunc("GET /admin/api/vpn", s.admin(s.adminVPN))
	mux.HandleFunc("PUT /admin/api/vpn", s.admin(s.adminVPNSave))
	mux.HandleFunc("GET /admin/api/vpn/regions", s.admin(s.adminVPNRegions))
	mux.HandleFunc("GET /admin/api/attention", s.admin(s.adminAttention))
	mux.HandleFunc("PUT /admin/api/attention", s.admin(s.adminAttentionSave))
	mux.HandleFunc("POST /admin/api/attention/run", s.admin(s.adminAttentionRun))
	mux.HandleFunc("POST /admin/api/attention/decide", s.admin(s.adminAttentionDecide))
	mux.HandleFunc("POST /admin/api/attention/undo", s.admin(s.adminAttentionUndo))
	mux.HandleFunc("GET /admin/api/missing/art", s.admin(s.adminMissingArt))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]string{"status": "ok"}) })
	mux.Handle("GET /assets/", files)
	mux.HandleFunc("GET /img", s.image)

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
	mux.HandleFunc("GET /admin/api/log", s.admin(s.logLines))
	mux.HandleFunc("GET /admin/api/reencode", s.admin(s.reencodeScan))
	mux.HandleFunc("POST /admin/api/reencode", s.guard(s.admin(s.reencodeQueue)))
	mux.HandleFunc("GET /admin/api/settings", s.admin(s.getSettings))
	mux.HandleFunc("PUT /admin/api/settings", s.guard(s.admin(s.putSettings)))
	mux.HandleFunc("GET /admin/api/events", s.admin(s.events))
	mux.HandleFunc("GET /admin/api/system", s.admin(s.system))
	mux.HandleFunc("POST /admin/api/intake", s.guard(s.admin(s.setIntake)))
	mux.HandleFunc("POST /admin/api/arrs/test", s.guard(s.admin(s.arrTest)))
	mux.HandleFunc("GET /admin/api/rss/status", s.admin(func(w http.ResponseWriter, r *http.Request) {
		if s.RSS == nil {
			writeJSON(w, []rss.FeedStatus{})
			return
		}
		writeJSON(w, s.RSS.Status())
	}))
	mux.HandleFunc("POST /admin/api/rss/check", s.guard(s.admin(func(w http.ResponseWriter, r *http.Request) {
		if s.RSS != nil {
			s.RSS.CheckNow(r.Context())
		}
		writeJSON(w, map[string]bool{"ok": true})
	})))
	mux.HandleFunc("POST /admin/api/rss/download", s.guard(s.admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Feed     string `json:"feed"`
			Index    int    `json:"index"`
			Title    string `json:"title"`
			Category string `json:"category"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if s.RSS == nil || json.NewDecoder(r.Body).Decode(&in) != nil {
			jsonErr(w, http.StatusBadRequest, "invalid request")
			return
		}
		out, err := s.RSS.Download(r.Context(), in.Feed, in.Index, in.Title, in.Category)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]string{"outcome": out})
	})))
	mux.HandleFunc("POST /admin/api/library/sweep", s.guard(s.admin(s.librarySweep)))
	mux.HandleFunc("GET /admin/api/playback/status", s.admin(s.playbackStatus))
	mux.HandleFunc("POST /admin/api/requests/test", s.guard(s.admin(s.requestsTest)))
	mux.HandleFunc("GET /admin/api/requests", s.admin(s.requestsAdminList))
	mux.HandleFunc("PUT /admin/api/requests/auto", s.guard(s.admin(s.requestsAuto)))
	mux.HandleFunc("POST /admin/api/music-requests/{id}/{action}", s.guard(s.admin(s.musicDecide)))
	mux.HandleFunc("POST /admin/api/requests/{id}/{action}", s.guard(s.admin(s.requestsDecide)))
	mux.HandleFunc("POST /admin/api/playback/test", s.guard(s.admin(s.playbackTest)))
	mux.HandleFunc("POST /admin/api/metadata/test", s.guard(s.admin(s.metadataTest)))
	mux.HandleFunc("GET /admin/api/metadata/status", s.admin(s.metadataStatus))
	mux.HandleFunc("POST /admin/api/metadata/lists/refresh", s.guard(s.admin(s.metadataListsRefresh)))
	mux.HandleFunc("GET /admin/api/shows", s.admin(s.showsList))
	mux.HandleFunc("GET /admin/api/shows/one", s.admin(s.showsOne))
	mux.HandleFunc("PUT /admin/api/shows/one", s.guard(s.admin(s.showsSave)))
	mux.HandleFunc("POST /admin/api/shows/{action}", s.guard(s.admin(s.showsAction)))
	mux.HandleFunc("GET /admin/api/shows/search", s.admin(s.showsSearch))
	mux.HandleFunc("GET /admin/api/shows/discover", s.admin(s.showsDiscover))
	mux.HandleFunc("GET /admin/api/shows/tidy", s.admin(s.showsTidy))
	mux.HandleFunc("POST /admin/api/shows/tidy", s.guard(s.admin(s.showsTidyApply)))
	mux.HandleFunc("GET /admin/api/shows/naming", s.admin(s.showsNaming))
	mux.HandleFunc("POST /admin/api/tdarr/test", s.guard(s.admin(s.tdarrTest)))
	mux.HandleFunc("POST /admin/api/settings/client-password", s.guard(s.admin(s.newClientPassword)))
	return secure(mux)
}

// frameAncestors lists the sites allowed to embed the pages in a frame, as Overmynd's
// were: the public dashboard and the admin pages. UNDERMYND_FRAME_ANCESTORS replaces
// the list; "'none'" turns embedding off.
func frameAncestors() string {
	if v := strings.TrimSpace(os.Getenv("UNDERMYND_FRAME_ANCESTORS")); v != "" {
		return v
	}
	return "'self' https://xivix.cc https://www.xivix.cc"
}

func secure(next http.Handler) http.Handler {
	embed := frameAncestors()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		admin := strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/assets/")
		if admin {
			// The admin pages may be framed only by the same sites as the public page.
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors "+embed+"; base-uri 'none'; form-action 'self'")
		} else {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://image.tmdb.org; style-src 'self' 'unsafe-inline'; frame-ancestors "+embed+"; base-uri 'none'; form-action 'self'")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		switch {
		case strings.HasPrefix(r.URL.Path, "/admin"), strings.HasPrefix(r.URL.Path, "/api/"):
			h.Set("Cache-Control", "no-store")
		case strings.HasPrefix(r.URL.Path, "/assets/"), strings.HasPrefix(r.URL.Path, "/dash/"), strings.HasPrefix(r.URL.Path, "/home/"), r.URL.Path == "/", r.URL.Path == "/classic":
			// The page scripts change with every release and carry no version in
			// their name, so a cached copy would show an old page.
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

type publicStep struct {
	Stage string    `json:"stage"`
	At    time.Time `json:"at"`
}

type publicJob struct {
	Title    string       `json:"title"`
	Group    string       `json:"group"`
	Progress float64      `json:"progress"`
	Stage    string       `json:"stage"`
	Via      string       `json:"via,omitempty"` // the app that asked for it, as a display name
	Size     int64        `json:"size,omitempty"`
	Done     int64        `json:"done,omitempty"`
	Steps    []publicStep `json:"steps"`
}

var viaName = map[string]string{"sonarr": "Sonarr", "radarr": "Radarr", "lidarr": "Lidarr", "rss": "RSS", "watch": "Watch folder", "reencode": "Re-encode"}

var stageName = map[job.State]string{
	job.Received: "Queued", job.Submitted: "Queued", job.Downloading: "Downloading", job.Pulling: "Copying",
	job.Downloaded: "Downloaded", job.QueuedTranscode: "Waiting for Tdarr", job.Transcoding: "Processing",
	job.Verifying: "Verifying", job.Ready: "Ready to import", job.Imported: "Imported", job.Failed: "Needs attention",
}

// publicJobs is the public view: title, state group and progress only. No
// paths, IDs, failure reasons, backend names or settings.
func (s *Server) publicJobs(w http.ResponseWriter, r *http.Request) {
	out := []publicJob{}
	for _, j := range s.Store.List() {
		if j.State == job.Cleaned || j.Source == "reencode" {
			continue
		}
		pj := publicJob{Title: j.Name, Group: job.Group(j.State), Progress: job.Overall(j), Stage: stageName[j.State],
			Via: viaName[j.Source], Size: j.Size, Steps: []publicStep{{Stage: "Received", At: j.Created}}}
		if j.State == job.Downloading {
			pj.Done = int64(j.Progress * float64(j.Size))
		}
		for _, h := range j.History {
			if n := stageName[h.To]; n != "" && n != pj.Steps[len(pj.Steps)-1].Stage {
				pj.Steps = append(pj.Steps, publicStep{Stage: n, At: h.At})
			}
		}
		out = append(out, pj)
	}
	writeJSON(w, out)
}

// adminLibrary is the same view with the errors the refresh hit, so a broken arr is visible.
func (s *Server) adminLibrary(w http.ResponseWriter, r *http.Request) {
	if s.Progress == nil {
		writeJSON(w, progress.Snapshot{Seasons: []progress.Season{}, Missing: []progress.Missing{}, Errors: []string{}})
		return
	}
	writeJSON(w, s.Progress.Snapshot())
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
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	c := &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: int(s.Auth.SessionTTL.Seconds())}
	if secure && frameAncestors() != "'none'" {
		// A page framed by another site only gets its cookie back if it is SameSite=None.
		// It is kept per embedding site (Partitioned), and every change still needs the
		// request header and a same-origin Origin, so this does not open it to forged requests.
		c.SameSite, c.Partitioned = http.SameSiteNoneMode, true
	}
	http.SetCookie(w, c)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Auth.Logout(c.Value)
	}
	gone := &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true}
	if (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") && frameAncestors() != "'none'" {
		gone.Secure, gone.SameSite, gone.Partitioned = true, http.SameSiteNoneMode, true // must match to clear it
	}
	http.SetCookie(w, gone)
	old := *gone
	old.Path = "/admin" // sessions started before the cookie covered the whole site
	http.SetCookie(w, &old)
	writeJSON(w, map[string]any{"ok": true})
}

type jobRow struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Source   string    `json:"source"`
	Category string    `json:"category"`
	State    job.State `json:"state"`
	Group    string    `json:"group"`
	Reason   string    `json:"reason"`
	Flag     string    `json:"flag"`
	Attn     bool      `json:"attention"`
	FailedAt string    `json:"failedAt,omitempty"`
	Progress float64   `json:"progress"`
	Backend  string    `json:"backend"`
	Node     string    `json:"transcodeNode,omitempty"`
	Worker   string    `json:"transcodeWorker,omitempty"`
	Created  time.Time `json:"created"`
	Changed  time.Time `json:"changed"`
}

func (s *Server) jobRows() []jobRow {
	out := []jobRow{}
	for _, j := range s.Store.List() {
		out = append(out, jobRow{ID: j.ID, Name: j.Name, Source: j.Source, Category: j.Category, State: j.State, Group: job.Group(j.State),
			Reason: j.Reason, Flag: j.Flag, Attn: j.State == job.Failed || j.Flag != "" || cleanupStuck(j), Progress: job.Overall(j), Backend: j.Backend,
			Node: j.TranscodeNode, Worker: j.TranscodeWorker, Created: j.Created, Changed: j.StateChanged, FailedAt: job.FailedStage(j)})
	}
	return out
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.jobRows()) }

// events streams the job list whenever it changes, so progress moves without refreshing.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	var last string
	send := func() bool {
		b, _ := json.Marshal(map[string]any{"jobs": s.jobRows(), "paused": s.Cfg.Get().IntakePaused})
		if string(b) != last {
			last = string(b)
			if _, err := fmt.Fprintf(w, "event: jobs\ndata: %s\n\n", b); err != nil {
				return false
			}
		} else if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send() {
		return
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			if !s.signedIn(r) { // the session ended while the stream was open
				return
			}
			if !send() {
				return
			}
		}
	}
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Engine.System()) }

func (s *Server) setIntake(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paused bool `json:"paused"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	cur := s.Cfg.Get()
	cur.IntakePaused = in.Paused
	if err := s.Cfg.Set(cur); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	s.Log.Printf("admin: intake paused=%v", in.Paused)
	writeJSON(w, map[string]bool{"paused": in.Paused})
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
	case "fail":
		err = s.Engine.FailJob(id)
	case "cleanup":
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(120 * time.Second))
		err = s.Engine.CleanupNow(id)
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
	LibraryDir   string `json:"libraryDir"`
	KeepNames    bool   `json:"keepNames"`
	// OrigName is the name the category had when the page loaded, so a rename
	// keeps the saved key instead of dropping it.
	OrigName string `json:"origName,omitempty"`
}

type settingsView struct {
	Categories []categoryView `json:"categories"`
	Feeds      []config.Feed  `json:"feeds"`
	Watches    []config.Watch `json:"watches"`
	Rules      []config.Rule  `json:"rules"`
	Tdarr      struct {
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
	Donations config.Donations `json:"donations"`
	Requests  struct {
		Enabled   bool   `json:"enabled"`
		URL       string `json:"url"`
		APIKey    string `json:"apiKey,omitempty"` // write-only
		HasAPIKey bool   `json:"hasApiKey"`
		UserID    int64  `json:"userId"`
	} `json:"requests"`
	Playback struct {
		TracearrURL       string `json:"tracearrUrl"`
		TracearrKey       string `json:"tracearrKey,omitempty"` // write-only
		HasTracearrKey    bool   `json:"hasTracearrKey"`
		EmbyURL           string `json:"embyUrl"`
		EmbyKey           string `json:"embyKey,omitempty"` // write-only
		HasEmbyKey        bool   `json:"hasEmbyKey"`
		HiddenLibraries   string `json:"hiddenLibraries"`
		RefreshAfterPlace bool   `json:"refreshAfterPlace"`
		PathFrom          string `json:"pathFrom"`
		PathTo            string `json:"pathTo"`
	} `json:"playback"`
	TorBox struct {
		Token          string `json:"token,omitempty"` // write-only
		HasToken       bool   `json:"hasToken"`
		CreatePerHour  int    `json:"createPerHour"`
		PullersAtOnce  int    `json:"pullersAtOnce"`
		LinkGapSeconds int    `json:"linkGapSeconds"`
	} `json:"torbox"`
	Metadata struct {
		TMDBKey        string `json:"tmdbKey,omitempty"` // write-only
		HasTMDBKey     bool   `json:"hasTmdbKey"`
		AnimeTitlesURL string `json:"animeTitlesUrl"`
		AnimeIDsURL    string `json:"animeIdsUrl"`
	} `json:"metadata"`
	ClientUser       string `json:"clientUser"`
	StallMinutes     int    `json:"stallMinutes"`
	ReconcileSeconds int    `json:"reconcileSeconds"`
	FlagImportHours  int    `json:"flagImportHours"`
	QualityCheck     *bool  `json:"qualityCheck,omitempty"`

	StorageScanMinutes int `json:"storageScanMinutes"`
}

// getSettings never includes a secret value, only whether one is set.
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	c := s.Cfg.Get()
	var v settingsView
	for _, k := range c.Categories {
		v.Categories = append(v.Categories, categoryView{Name: k.Name, Kind: k.Kind, ArrURL: k.ArrURL,
			HasArrAPIKey: k.ArrAPIKey != "", Transcode: k.Transcode, OnFail: k.OnTranscodeFail, LibraryDir: k.OwnLibrary(), KeepNames: k.KeepNames})
	}
	v.Feeds, v.Rules, v.Watches = c.Feeds, c.Rules, c.Watches
	if v.Watches == nil {
		v.Watches = []config.Watch{}
	}
	if v.Feeds == nil {
		v.Feeds = []config.Feed{}
	}
	if v.Rules == nil {
		v.Rules = []config.Rule{}
	}
	v.Tdarr.InDir, v.Tdarr.OutDir, v.Tdarr.TimeoutMinutes = c.Tdarr.InDir, c.Tdarr.OutDir, c.Tdarr.TimeoutMinutes
	v.Tdarr.URL, v.Tdarr.HasAPIKey, v.Tdarr.LibraryID = c.Tdarr.URL, c.Tdarr.APIKey != "", c.Tdarr.LibraryID
	v.Tdarr.MaxInFlight, v.Tdarr.StallMinutes = c.Tdarr.MaxInFlight, c.Tdarr.StallMinutes
	v.Donations = c.Donations
	v.Requests.Enabled, v.Requests.URL, v.Requests.HasAPIKey, v.Requests.UserID = c.Requests.Enabled, c.Requests.URL, c.Requests.APIKey != "", c.Requests.UserID
	v.Playback.TracearrURL, v.Playback.HasTracearrKey = c.Playback.TracearrURL, c.Playback.TracearrKey != ""
	v.Playback.EmbyURL, v.Playback.HasEmbyKey, v.Playback.HiddenLibraries = c.Playback.EmbyURL, c.Playback.EmbyKey != "", c.Playback.HiddenLibraries
	v.Playback.RefreshAfterPlace, v.Playback.PathFrom, v.Playback.PathTo = c.Playback.RefreshAfterPlace, c.Playback.PathFrom, c.Playback.PathTo
	v.TorBox.HasToken, v.TorBox.CreatePerHour, v.TorBox.PullersAtOnce, v.TorBox.LinkGapSeconds = c.TorBox.Token != "", c.TorBox.CreatePerHour, c.TorBox.PullersAtOnce, c.TorBox.LinkGapSeconds
	v.ClientUser, v.StallMinutes, v.ReconcileSeconds, v.FlagImportHours = c.ClientUser, c.StallMinutes, c.ReconcileSeconds, c.FlagImportHours
	v.StorageScanMinutes = c.StorageScanMinutes
	v.Metadata.HasTMDBKey, v.Metadata.AnimeTitlesURL, v.Metadata.AnimeIDsURL = c.Metadata.TMDBKey != "", c.Metadata.AnimeTitlesURL, c.Metadata.AnimeIDsURL
	qc := !c.SkipQualityCheck
	v.QualityCheck = &qc
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
		nc := config.Category{Name: name, Kind: c.Kind, ArrURL: strings.TrimSpace(c.ArrURL), Transcode: c.Transcode, OnTranscodeFail: c.OnFail, KeepNames: c.KeepNames}
		if lib := strings.TrimSpace(c.LibraryDir); lib != "" && !nc.IsArr() {
			lib = filepath.Clean(lib)
			if !filepath.IsAbs(lib) {
				jsonErr(w, http.StatusBadRequest, "category "+name+": the library folder must be a full path starting with /")
				return
			}
			for _, bad := range []string{s.Cfg.Env.Complete(), s.Cfg.Env.Work()} {
				if lib == bad || strings.HasPrefix(lib, bad+string(filepath.Separator)) {
					jsonErr(w, http.StatusBadRequest, "category "+name+": the library folder cannot be inside the Complete or work folders")
					return
				}
			}
			if st, err := os.Stat(lib); err != nil || !st.IsDir() {
				jsonErr(w, http.StatusBadRequest, "category "+name+": the library folder "+lib+" does not exist in the container")
				return
			}
			nc.LibraryDir = lib
		}
		if c.ArrAPIKey != "" {
			nc.ArrAPIKey = secret.String(c.ArrAPIKey)
		} else if old, ok := cur.Category(strings.TrimSpace(c.OrigName)); ok && c.OrigName != "" {
			nc.ArrAPIKey = old.ArrAPIKey // blank means unchanged, even if the category was renamed
		} else if old, ok := cur.Category(name); ok {
			nc.ArrAPIKey = old.ArrAPIKey
		}
		next.Categories = append(next.Categories, nc)
	}
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
	next.Watches = nil
	for _, wf := range in.Watches {
		wf.Dir, wf.Category = filepath.Clean(strings.TrimSpace(wf.Dir)), strings.TrimSpace(wf.Category)
		if strings.TrimSpace(wf.Dir) == "" || wf.Dir == "." {
			continue
		}
		if !filepath.IsAbs(wf.Dir) {
			jsonErr(w, http.StatusBadRequest, "watch folder "+wf.Dir+": use a full path starting with /")
			return
		}
		if wf.Category != "" {
			if _, ok := next.Category(wf.Category); !ok {
				jsonErr(w, http.StatusBadRequest, "watch folder "+wf.Dir+": that category does not exist")
				return
			}
		}
		for _, own := range []string{s.Cfg.Env.Complete(), s.Cfg.Env.Work()} {
			if wf.Dir == own || strings.HasPrefix(wf.Dir+"/", own+"/") {
				jsonErr(w, http.StatusBadRequest, "watch folder "+wf.Dir+": that is one of HyveMynd's own folders, so files would loop")
				return
			}
		}
		next.Watches = append(next.Watches, wf)
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
	if err := validDonations(&in.Donations); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next.Donations = in.Donations
	next.Requests.Enabled, next.Requests.URL, next.Requests.UserID = in.Requests.Enabled, strings.TrimSpace(in.Requests.URL), in.Requests.UserID
	if in.Requests.APIKey != "" {
		next.Requests.APIKey = secret.String(strings.TrimSpace(in.Requests.APIKey))
	}
	if next.Requests.Enabled && (next.Requests.URL == "" || next.Requests.APIKey == "" || next.Requests.UserID <= 0) {
		jsonErr(w, http.StatusBadRequest, "media requests need the Seerr address, its API key and the user ID requests are made as")
		return
	}
	next.Playback.TracearrURL, next.Playback.EmbyURL = strings.TrimSpace(in.Playback.TracearrURL), strings.TrimSpace(in.Playback.EmbyURL)
	if in.Playback.TracearrKey != "" {
		next.Playback.TracearrKey = secret.String(strings.TrimSpace(in.Playback.TracearrKey))
	}
	if in.Playback.EmbyKey != "" {
		next.Playback.EmbyKey = secret.String(strings.TrimSpace(in.Playback.EmbyKey))
	}
	next.Playback.HiddenLibraries = strings.TrimSpace(in.Playback.HiddenLibraries)
	next.Playback.RefreshAfterPlace = in.Playback.RefreshAfterPlace
	next.Playback.PathFrom, next.Playback.PathTo = strings.TrimSpace(in.Playback.PathFrom), strings.TrimSpace(in.Playback.PathTo)
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
	if in.Metadata.TMDBKey != "" {
		next.Metadata.TMDBKey = secret.String(strings.TrimSpace(in.Metadata.TMDBKey))
	}
	for _, u := range []string{in.Metadata.AnimeTitlesURL, in.Metadata.AnimeIDsURL} {
		if u = strings.TrimSpace(u); u != "" && u != "off" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			jsonErr(w, http.StatusBadRequest, "anime list addresses must start with https:// (or be empty for the default, or off)")
			return
		}
	}
	next.Metadata.AnimeTitlesURL, next.Metadata.AnimeIDsURL = strings.TrimSpace(in.Metadata.AnimeTitlesURL), strings.TrimSpace(in.Metadata.AnimeIDsURL)
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
	if in.StorageScanMinutes > 0 {
		next.StorageScanMinutes = in.StorageScanMinutes
	}
	if in.QualityCheck != nil {
		next.SkipQualityCheck = !*in.QualityCheck
	}
	if err := s.Cfg.Set(next); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	s.getSettings(w, r)
}

// tdarrTest checks an address and key (a blank key means the stored one) and reports what it found.
// arrTest checks one arr connection. A blank key means the one already saved for that category.
func (s *Server) arrTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		OrigName string `json:"origName"`
		Kind     string `json:"kind"`
		URL      string `json:"url"`
		APIKey   string `json:"apiKey"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	key := in.APIKey
	if key == "" {
		for _, c := range s.Cfg.Get().Categories {
			if c.Name == in.Name || (in.OrigName != "" && c.Name == in.OrigName) {
				key = string(c.ArrAPIKey)
			}
		}
	}
	addr := strings.TrimSpace(in.URL)
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		jsonErr(w, http.StatusBadRequest, "the address must start with http:// or https://")
		return
	}
	if key == "" {
		jsonErr(w, http.StatusBadRequest, "no API key is set for this category")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	app, ver, err := arr.New(in.Kind, addr, key).Check(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]string{"app": app, "version": ver})
}

func (s *Server) librarySweep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	msg, err := s.Engine.SweepLibrary(in.Name)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"result": msg})
}

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

// cleanupStuck says whether an imported job's cleanup (deleting the leftover
// copies and the download client's item) has kept failing for a while; its
// reason says why.
func cleanupStuck(j job.Job) bool {
	return j.State == job.Imported && strings.HasPrefix(j.Reason, "cleanup:") && time.Since(importedAt(j)) > 15*time.Minute
}
