package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/playback"
	"github.com/z354prance/undermynd/internal/tracearr"
)

// Signed poster links expire with the process, as in Overmynd.
var posterKey = func() []byte { b := make([]byte, 32); rand.Read(b); return b }()

func posterSignature(path string) string {
	h := hmac.New(sha256.New, posterKey)
	h.Write([]byte(path))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func validPosterPath(path string) bool {
	u, err := url.Parse(path)
	return err == nil && !u.IsAbs() && u.Host == "" && u.User == nil && u.Fragment == "" &&
		u.Path == "/api/v1/images/proxy" && u.RawPath == "" && len(path) < 4096
}

func signedPoster(path string) string {
	if path == "" || !validPosterPath(path) {
		return ""
	}
	return "/api/v1/playback/poster?" + url.Values{"path": {path}, "signature": {posterSignature(path)}}.Encode()
}

var coverPath = regexp.MustCompile(`^/MediaCover/([1-9][0-9]*/poster|Albums/[1-9][0-9]*/cover)\.jpg$`)

func coverSignature(arr, path string) string {
	h := hmac.New(sha256.New, posterKey)
	h.Write([]byte("arr\n" + arr + "\n" + path))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// signedCover links an arr's own poster through this server, so the arr key never leaves it.
func signedCover(arr, path string) string {
	if arr == "" || !coverPath.MatchString(path) {
		return ""
	}
	return "/api/v1/playback/poster?" + url.Values{"arr": {arr}, "path": {path}, "signature": {coverSignature(arr, path)}}.Encode()
}

func (s *Server) tracearr() (*tracearr.Client, bool) {
	p := s.Cfg.Get().Playback
	if p.TracearrURL == "" || p.TracearrKey == "" {
		return nil, false
	}
	return tracearr.New(p.TracearrURL, string(p.TracearrKey)), true
}

func (s *Server) v1Playback(w http.ResponseWriter, r *http.Request) {
	c, ok := s.tracearr()
	if !ok {
		writeJSON(w, map[string]any{"sessions": []any{}, "errors": []string{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	sessions, err := c.Streams(ctx)
	if err != nil {
		writeJSON(w, map[string]any{"sessions": []any{}, "errors": []string{"Now playing is unavailable from Tracearr."}})
		return
	}
	p := s.Cfg.Get().Playback
	sessions = playback.Filter(ctx, playback.Emby{URL: p.EmbyURL, Key: string(p.EmbyKey)}, sessions, p.HiddenLibraries)
	for i := range sessions {
		sessions[i].PosterURL = signedPoster(sessions[i].PosterURL)
	}
	if sessions == nil {
		sessions = []tracearr.Session{}
	}
	writeJSON(w, map[string]any{"sessions": sessions, "errors": []string{}})
}

// v1Recent lists what Sonarr, Radarr and Lidarr imported lately, music included.
func (s *Server) v1Recent(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	if s.Progress != nil {
		for i, x := range s.Progress.Snapshot().Recent {
			it := map[string]any{"id": "recent:" + itoa(i) + ":" + x.Title, "title": x.Title, "added_at": x.Date, "stage": "available"}
			if x.Poster != "" {
				if u := signedCover(x.Arr, x.Poster); u != "" {
					it["poster_url"] = u
				}
			}
			switch x.Kind {
			case "episode":
				it["kind"], it["show_title"], it["episodes"] = "series", x.Title, x.Episodes
			case "movie":
				it["kind"] = "movie"
				if x.Year != 0 {
					it["year"] = x.Year
				}
			default:
				it["kind"] = x.Kind
			}
			items = append(items, it)
		}
	}
	if len(items) > 6 {
		items = items[:6]
	}
	writeJSON(w, map[string]any{"items": items, "errors": []string{}, "configured": true})
}

func (s *Server) v1Poster(w http.ResponseWriter, r *http.Request) {
	path, sig := r.URL.Query().Get("path"), r.URL.Query().Get("signature")
	if arrName := r.URL.Query().Get("arr"); arrName != "" {
		if !coverPath.MatchString(path) || !hmac.Equal([]byte(sig), []byte(coverSignature(arrName, path))) {
			http.NotFound(w, r)
			return
		}
		for _, c := range s.Cfg.Get().Categories {
			if c.Name == arrName && c.ArrURL != "" && (c.Kind == "sonarr" || c.Kind == "radarr" || c.Kind == "lidarr") {
				s.serveImage(w, r, strings.TrimRight(c.ArrURL, "/")+path, string(c.ArrAPIKey))
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	if !validPosterPath(path) || !hmac.Equal([]byte(sig), []byte(posterSignature(path))) {
		http.NotFound(w, r)
		return
	}
	base := strings.TrimRight(s.Cfg.Get().Playback.TracearrURL, "/")
	if base == "" {
		http.NotFound(w, r)
		return
	}
	s.serveImage(w, r, base+path, "")
}

func (s *Server) serveImage(w http.ResponseWriter, r *http.Request, target, apiKey string) {
	req, err := http.NewRequestWithContext(r.Context(), "GET", target, nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	// Tracearr's image proxy is public; never send the API key to an image URL.
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		http.NotFound(w, r)
		return
	}
	defer resp.Body.Close()
	const maxImage = 5 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImage+1))
	if err != nil || len(body) > maxImage {
		http.NotFound(w, r)
		return
	}
	ct := http.DetectContentType(body)
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Write(body)
}

// playbackTest checks Tracearr and, if given, Emby. Blank keys mean the stored ones.
func (s *Server) playbackTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TracearrURL, TracearrKey, EmbyURL, EmbyKey string
	}
	if !readJSON(w, r, &in) {
		return
	}
	cur := s.Cfg.Get().Playback
	if in.TracearrKey == "" {
		in.TracearrKey = string(cur.TracearrKey)
	}
	if in.EmbyKey == "" {
		in.EmbyKey = string(cur.EmbyKey)
	}
	okURL := func(u string) bool { return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") }
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out := map[string]string{}
	if u := strings.TrimSpace(in.TracearrURL); u != "" {
		if !okURL(u) {
			jsonErr(w, http.StatusBadRequest, "the Tracearr address must start with http:// or https://")
			return
		}
		if err := tracearr.New(u, in.TracearrKey).Check(ctx); err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		out["tracearr"] = "ok"
	}
	if u := strings.TrimSpace(in.EmbyURL); u != "" {
		if !okURL(u) {
			jsonErr(w, http.StatusBadRequest, "the Emby address must start with http:// or https://")
			return
		}
		name, err := playback.Emby{URL: u, Key: in.EmbyKey}.Check(ctx)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, "Emby: "+err.Error())
			return
		}
		out["emby"] = name
	}
	writeJSON(w, out)
}
