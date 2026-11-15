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
	"sort"
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

func (s *Server) v1Recent(w http.ResponseWriter, r *http.Request) {
	c, ok := s.tracearr()
	if !ok {
		writeJSON(w, map[string]any{"items": []any{}, "errors": []string{"Recently added needs Tracearr; add it in Admin, Playback."}, "configured": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res := c.RecentlyAdded(ctx)
	for i := range res.Items {
		res.Items[i].PosterURL = signedPoster(res.Items[i].PosterURL)
	}
	// Tracearr's feed is movies and shows; music comes from the Lidarr imports.
	type entry struct {
		at time.Time
		v  any
	}
	all := []entry{}
	for _, it := range res.Items {
		all = append(all, entry{it.AddedAt, it})
	}
	if s.Progress != nil {
		for i, x := range s.Progress.Snapshot().Recent {
			if x.Kind == "movie" || x.Kind == "episode" {
				continue
			}
			all = append(all, entry{x.Date, map[string]any{"id": "recent-music:" + itoa(i) + ":" + x.Title, "title": x.Title,
				"kind": "album", "added_at": x.Date, "stage": "available"}})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	if len(all) > 6 {
		all = all[:6]
	}
	items := make([]any, 0, len(all))
	for _, e := range all {
		items = append(items, e.v)
	}
	writeJSON(w, map[string]any{"items": items, "errors": res.Errors, "configured": true})
}

func (s *Server) v1Poster(w http.ResponseWriter, r *http.Request) {
	path, sig := r.URL.Query().Get("path"), r.URL.Query().Get("signature")
	if !validPosterPath(path) || !hmac.Equal([]byte(sig), []byte(posterSignature(path))) {
		http.NotFound(w, r)
		return
	}
	base := strings.TrimRight(s.Cfg.Get().Playback.TracearrURL, "/")
	if base == "" {
		http.NotFound(w, r)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", base+path, nil)
	if err != nil {
		http.NotFound(w, r)
		return
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
