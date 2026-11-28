package web

import (
	"context"
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

func (s *Server) requestStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"enabled": s.seerrPublic() != nil, "music": s.musicOn(), "books": s.booksOn()})
}

func (s *Server) requestSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(q) < 2 || len(q) > 200 {
		jsonErr(w, http.StatusBadRequest, "enter between 2 and 200 characters")
		return
	}
	c := s.seerrPublic()
	if c == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := c.Search(ctx, q)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"results": withPosters(res)})
}

func (s *Server) requestCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MediaID    int64  `json:"media_id"`
		MediaType  string `json:"media_type"`
		AllSeasons bool   `json:"all_seasons"`
		Seasons    []int  `json:"seasons"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.MediaID <= 0 || (in.MediaType != "movie" && in.MediaType != "tv") || (in.MediaType == "tv" && !in.AllSeasons && len(in.Seasons) == 0) || len(in.Seasons) > 100 {
		jsonErr(w, http.StatusBadRequest, "select a movie, or the seasons of a TV show")
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
	var seasons []int
	if in.MediaType == "tv" && !in.AllSeasons {
		for _, n := range in.Seasons {
			if n > 0 && n < 1000 {
				seasons = append(seasons, n)
			}
		}
		if len(seasons) == 0 {
			jsonErr(w, http.StatusBadRequest, "pick at least one season")
			return
		}
	}
	id, err := c.Request(ctx, in.MediaID, in.MediaType, seasons...)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.Cfg.Get().Requests.Auto(in.MediaType) {
		if a := s.seerrAdmin(); a != nil {
			// Seerr may already have approved it (the request user's own
			// permissions); either way it goes ahead.
			if err := a.Decide(ctx, id, "approve"); err != nil {
				if s.Log != nil && !strings.Contains(err.Error(), "no longer awaiting") {
					s.Log.Printf("requests: auto-approve of %d: %v", id, err)
				}
			}
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]int64{"id": id})
}

// requestsPending feeds the public "awaiting approval" count; decisions are made in Admin.
func (s *Server) requestsPending(w http.ResponseWriter, r *http.Request) {
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
	all := make([]any, 0, len(list))
	for _, p := range list {
		all = append(all, p)
	}
	if s.Auth != nil && s.signedIn(r) {
		waiting, _ := s.musicLists()
		for _, m := range waiting {
			all = append(all, map[string]any{"id": "music:" + m.ID, "source": "hyvemynd", "media_type": "music", "title": m.Name, "status": "pending", "created_at": m.RequestedAt})
		}
		books, _ := s.bookLists()
		for _, m := range books {
			all = append(all, map[string]any{"id": "book:" + m.ID, "source": "hyvemynd", "media_type": "book", "title": m.Title, "status": "pending", "created_at": m.RequestedAt})
		}
	}
	writeJSON(w, map[string]any{"requests": all, "errors": []string{}})
}

func (s *Server) requestsAdminList(w http.ResponseWriter, r *http.Request) {
	c := s.seerrAdmin()
	if c == nil {
		waiting, decided := s.musicLists()
		bw, bd := s.bookLists()
		writeJSON(w, map[string]any{"requests": []any{}, "auto": s.autoView(), "music": waiting, "musicDecided": decided, "musicOn": s.musicOn(),
			"books": bw, "booksDecided": bd, "booksOn": s.booksOn()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := c.PendingRequests(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	waiting, decided := s.musicLists()
	bw, bd := s.bookLists()
	writeJSON(w, map[string]any{"requests": list, "auto": s.autoView(), "music": waiting, "musicDecided": decided, "musicOn": s.musicOn(),
		"books": bw, "booksDecided": bd, "booksOn": s.booksOn()})
}

type autoApprove struct {
	Movies bool `json:"movies"`
	TV     bool `json:"tv"`
	Music  bool `json:"music"`
	Books  bool `json:"books"`
}

func (s *Server) autoView() autoApprove {
	r := s.Cfg.Get().Requests
	return autoApprove{r.AutoMovies, r.AutoTV, r.AutoMusic, r.AutoBooks}
}

// requestsAuto sets which kinds of request are approved by themselves; those
// already waiting of a kind just switched on are approved now.
func (s *Server) requestsAuto(w http.ResponseWriter, r *http.Request) {
	var in autoApprove
	if !readJSON(w, r, &in) {
		return
	}
	cur := s.Cfg.Get()
	was := cur.Requests
	cur.Requests.AutoMovies, cur.Requests.AutoTV, cur.Requests.AutoMusic, cur.Requests.AutoBooks = in.Movies, in.TV, in.Music, in.Books
	if err := s.Cfg.Set(cur); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	approved := 0
	if c := s.seerrAdmin(); c != nil && ((in.Movies && !was.AutoMovies) || (in.TV && !was.AutoTV)) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if list, err := c.PendingRequests(ctx); err == nil {
			for _, p := range list {
				if (p.MediaType == "movie" && in.Movies && !was.AutoMovies) || (p.MediaType == "tv" && in.TV && !was.AutoTV) {
					if c.Decide(ctx, p.SourceID, "approve") == nil {
						approved++
					}
				}
			}
		}
	}
	if in.Music && !was.AutoMusic {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		waiting, _ := s.musicLists()
		for _, m := range waiting {
			if _, err := s.decideMusic(ctx, m.ID, "approve"); err == nil {
				approved++
			}
		}
	}
	if in.Books && !was.AutoBooks {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		waiting, _ := s.bookLists()
		for _, m := range waiting {
			if _, err := s.decideBook(ctx, m.ID, "approve"); err == nil {
				approved++
			}
		}
	}
	writeJSON(w, map[string]any{"auto": s.autoView(), "approved": approved})
}

func (s *Server) requestsDecide(w http.ResponseWriter, r *http.Request) {
	idText := r.PathValue("id")
	if idText == "" {
		idText = r.PathValue("requestID")
	}
	id, err := strconv.ParseInt(idText, 10, 64)
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
		writeJSON(w, map[string]any{"results": withPosters(d)})
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
	writeJSON(w, map[string]any{"results": withPosters(res)})
}

const tmdbImage = "https://image.tmdb.org/t/p/"

// posterResult is a search or discover result with its poster as an address
// on this server (/img), so the page loads images only from here.
type posterResult struct {
	seerr.SearchResult
	Poster string `json:"poster,omitempty"`
}

func withPosters(list []seerr.SearchResult) []posterResult {
	out := make([]posterResult, 0, len(list))
	for _, x := range list {
		p := posterResult{SearchResult: x}
		if x.PosterPath != "" {
			p.Poster = imgURL(tmdbImage + "w342" + x.PosterPath)
		}
		out = append(out, p)
	}
	return out
}

// requestTitle is the request page of one title (?type=movie|tv&id=): details,
// cast, trailer and, for a series, where each season stands in Seerr.
func (s *Server) requestTitle(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("type")
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 || (kind != "movie" && kind != "tv") {
		jsonErr(w, http.StatusBadRequest, "invalid title")
		return
	}
	c := s.seerrPublic()
	if c == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media requests are not enabled")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	t, err := c.Title(ctx, kind, id)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if t.Poster != "" {
		t.Poster = imgURL(tmdbImage + "w342" + t.Poster)
	}
	if t.Backdrop != "" {
		t.Backdrop = imgURL(tmdbImage + "w1280" + t.Backdrop)
	}
	for i := range t.Cast {
		if t.Cast[i].Photo != "" {
			t.Cast[i].Photo = imgURL(tmdbImage + "w185" + t.Cast[i].Photo)
		}
	}
	writeJSON(w, t)
}
