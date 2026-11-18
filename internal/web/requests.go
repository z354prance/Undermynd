package web

import (
	"errors"
	"github.com/z354prance/undermynd/internal/metadata"

	"context"
	"github.com/z354prance/undermynd/internal/requests"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/seerr"
)

// Visitors may send 10 requests a minute from one address; the table is bounded.
type requestLimiter struct {
	mu      sync.Mutex
	clients map[string]struct {
		n     int
		until time.Time
	}
}

func (l *requestLimiter) allow(r *http.Request) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	type item = struct {
		n     int
		until time.Time
	}
	if l.clients == nil {
		l.clients = map[string]item{}
	}
	now := time.Now()
	for k, v := range l.clients {
		if now.After(v.until) {
			delete(l.clients, k)
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	v := l.clients[ip]
	if v.n >= 10 || (v.n == 0 && len(l.clients) >= 1024) {
		return false
	}
	if v.n == 0 {
		v.until = now.Add(time.Minute)
	}
	v.n++
	l.clients[ip] = v
	return true
}

var reqLimit requestLimiter

// seerrPublic is the client for visitors, or nil when requests are off or half set up (fail closed).
func (s *Server) seerrPublic() *seerr.Client {
	c := s.Cfg.Get().Requests
	if !c.Enabled || c.URL == "" || c.APIKey == "" || c.UserID <= 0 {
		return nil
	}
	return seerr.New(c.URL, string(c.APIKey), c.UserID)
}

func (s *Server) seerrAdmin() *seerr.Client {
	c := s.Cfg.Get().Requests
	if c.URL == "" || c.APIKey == "" {
		return nil
	}
	return seerr.New(c.URL, string(c.APIKey), 0)
}

// native says requests are kept by HyveMynd and can be taken now.
func (s *Server) native() bool {
	c := s.Cfg.Get()
	return c.Requests.Native() && c.Requests.Enabled && s.Requests != nil && c.Metadata.TMDBKey != ""
}

func (s *Server) nativeMode() bool { return s.Cfg.Get().Requests.Native() && s.Requests != nil }

func (s *Server) requestStatus(w http.ResponseWriter, r *http.Request) {
	if s.nativeMode() {
		writeJSON(w, map[string]bool{"enabled": s.native()})
		return
	}
	writeJSON(w, map[string]bool{"enabled": s.seerrPublic() != nil})
}

func (s *Server) requestSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(q) < 2 || len(q) > 200 {
		jsonErr(w, http.StatusBadRequest, "enter between 2 and 200 characters")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if s.nativeMode() {
		if !s.native() {
			jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
			return
		}
		res, err := s.Requests.Search(ctx, q)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, "the search did not work; try again shortly")
			return
		}
		writeJSON(w, map[string]any{"results": withPosters(res)})
		return
	}
	c := s.seerrPublic()
	if c == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
		return
	}
	res, err := c.Search(ctx, q)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"results": res})
}

func (s *Server) requestCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MediaID    int64  `json:"media_id"`
		MediaType  string `json:"media_type"`
		AllSeasons bool   `json:"all_seasons"`
		Seasons    []int  `json:"seasons"` // native requests: the seasons wanted
		Name       string `json:"name"`    // optional: who is asking (native requests)
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Seasons) > 100 {
		in.Seasons = in.Seasons[:100]
	}
	if in.MediaID <= 0 || (in.MediaType != "movie" && in.MediaType != "tv") || (in.MediaType == "tv" && !in.AllSeasons && (len(in.Seasons) == 0 || !s.nativeMode())) {
		jsonErr(w, http.StatusBadRequest, "select a movie or confirm all seasons of a TV show")
		return
	}
	if s.nativeMode() {
		if !s.native() {
			jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
			return
		}
		if !reqLimit.allow(r) {
			w.Header().Set("Retry-After", "60")
			jsonErr(w, http.StatusTooManyRequests, "too many requests; try again in a minute")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		req, err := s.Requests.Create(ctx, in.MediaType, in.MediaID, in.Name, in.Seasons...)
		if errors.Is(err, requests.ErrAlready) {
			jsonErr(w, http.StatusConflict, "already requested or in the library")
			return
		}
		if err != nil {
			jsonErr(w, http.StatusBadGateway, "the request could not be made; try again shortly")
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]int64{"id": req.ID})
		return
	}
	c := s.seerrPublic()
	if c == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
		return
	}
	if !reqLimit.allow(r) {
		w.Header().Set("Retry-After", "60")
		jsonErr(w, http.StatusTooManyRequests, "too many requests; try again in a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	id, err := c.Request(ctx, in.MediaID, in.MediaType)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]int64{"id": id})
}

// requestsPending feeds the public "awaiting approval" count; decisions are made in Admin.
func (s *Server) requestsPending(w http.ResponseWriter, r *http.Request) {
	if s.nativeMode() {
		list, err := s.Requests.Pending()
		if err != nil || !s.Cfg.Get().Requests.Enabled {
			writeJSON(w, map[string]any{"requests": []any{}, "errors": []string{}})
			return
		}
		writeJSON(w, map[string]any{"requests": list, "errors": []string{}})
		return
	}
	c := s.seerrAdmin()
	if c == nil || !s.Cfg.Get().Requests.Enabled {
		writeJSON(w, map[string]any{"requests": []any{}, "errors": []string{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := c.PendingRequests(ctx)
	if err != nil {
		writeJSON(w, map[string]any{"requests": []any{}, "errors": []string{"Seerr is unavailable"}})
		return
	}
	writeJSON(w, map[string]any{"requests": list, "errors": []string{}})
}

func (s *Server) requestsAdminList(w http.ResponseWriter, r *http.Request) {
	if s.nativeMode() {
		list, err := s.Requests.Pending()
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"requests": list})
		return
	}
	c := s.seerrAdmin()
	if c == nil {
		writeJSON(w, map[string]any{"requests": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := c.PendingRequests(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"requests": list})
}

func (s *Server) requestsDecide(w http.ResponseWriter, r *http.Request) {
	idText := r.PathValue("id")
	if idText == "" {
		idText = r.PathValue("requestID")
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if s.nativeMode() {
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid request decision")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err := s.Requests.Decide(ctx, id, r.PathValue("action"), r.URL.Query().Get("reason")); err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	c := s.seerrAdmin()
	if err != nil || c == nil {
		jsonErr(w, http.StatusBadRequest, "invalid request decision")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := c.Decide(ctx, id, r.PathValue("action")); err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// requestsTest checks the address and key (a blank key means the stored one) and the request user.
func (s *Server) requestsTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL    string `json:"url"`
		APIKey string `json:"apiKey"`
		UserID int64  `json:"userId"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.APIKey == "" {
		in.APIKey = string(s.Cfg.Get().Requests.APIKey)
	}
	u := strings.TrimSpace(in.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		jsonErr(w, http.StatusBadRequest, "the address must start with http:// or https://")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := seerr.New(u, in.APIKey, in.UserID).CheckUser(ctx); err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// Suggestions are the same for everyone, so Seerr is asked at most once every 10 minutes per list.
var discoverCache struct {
	sync.Mutex
	at   map[string]time.Time
	data map[string][]seerr.SearchResult
	key  string
}

func (s *Server) requestDiscover(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "trending"
	}
	if s.nativeMode() {
		if !s.native() {
			jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		res, err := s.Requests.Discover(ctx, kind)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, "suggestions are not available right now")
			return
		}
		writeJSON(w, map[string]any{"results": withPosters(res)})
		return
	}
	c := s.seerrPublic()
	if c == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
		return
	}
	cfg := s.Cfg.Get().Requests
	discoverCache.Lock()
	if discoverCache.key != cfg.URL+"|"+strconv.FormatInt(cfg.UserID, 10) || discoverCache.data == nil {
		discoverCache.key, discoverCache.at, discoverCache.data = cfg.URL+"|"+strconv.FormatInt(cfg.UserID, 10), map[string]time.Time{}, map[string][]seerr.SearchResult{}
	}
	if d, ok := discoverCache.data[kind]; ok && time.Since(discoverCache.at[kind]) < 10*time.Minute {
		discoverCache.Unlock()
		writeJSON(w, map[string]any{"results": d})
		return
	}
	discoverCache.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := c.Discover(ctx, kind)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	discoverCache.Lock()
	discoverCache.data[kind], discoverCache.at[kind] = res, time.Now()
	discoverCache.Unlock()
	writeJSON(w, map[string]any{"results": res})
}

type posterResult struct {
	seerr.SearchResult
	Poster string `json:"poster,omitempty"`
}

// withPosters adds this server's poster link to each result.
func withPosters(res []seerr.SearchResult) []posterResult {
	out := make([]posterResult, len(res))
	for i, x := range res {
		out[i] = posterResult{x, imgURL(metadata.Image(x.PosterPath, "w342"))}
	}
	return out
}

// requestTitle is a title's page in the request panel (native requests).
func (s *Server) requestTitle(w http.ResponseWriter, r *http.Request) {
	if !s.nativeMode() || !s.native() {
		jsonErr(w, http.StatusServiceUnavailable, "title pages need requests kept by HyveMynd")
		return
	}
	kind := r.URL.Query().Get("type")
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 || (kind != "movie" && kind != "tv") {
		jsonErr(w, http.StatusBadRequest, "type movie or tv, and an id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	t, err := s.Requests.Title(ctx, kind, id)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "that title is not available right now")
		return
	}
	t.Poster, t.Backdrop = imgURL(t.Poster), imgURL(t.Backdrop)
	for i := range t.Cast {
		t.Cast[i].Photo = imgURL(t.Cast[i].Photo)
	}
	writeJSON(w, t)
}
