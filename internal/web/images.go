package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Posters, backdrops and covers are fetched through this server: the pages
// load images only from it, the visitor's address never reaches the image
// hosts, and a copy is kept on disk for 30 days.

var imageHosts = []string{"image.tmdb.org", "artworks.thetvdb.com", "thetvdb.com", "assets.fanart.tv", "coverartarchive.org",
	"archive.org", "theaudiodb.com", "lastfm.freetls.fastly.net", "s4.anilist.co"}

func imageHostOK(h string) bool {
	h = strings.ToLower(h)
	for _, x := range imageHosts {
		if h == x || strings.HasSuffix(h, "."+x) {
			return true
		}
	}
	return false
}

func imageSignature(u string) string {
	m := hmac.New(sha256.New, posterKey)
	m.Write([]byte("img\n" + u))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// imgURL turns an image address into this server's signed link to it; ""
// for an empty or foreign address.
func imgURL(remote string) string {
	u, err := url.Parse(remote)
	if remote == "" || err != nil || u.Scheme != "https" && u.Scheme != "http" || !imageHostOK(u.Hostname()) {
		return ""
	}
	return "/img?" + url.Values{"u": {remote}, "s": {imageSignature(remote)}}.Encode()
}

var errNotImage = errors.New("not an image")

func (s *Server) imageCacheDir() string { return filepath.Join(s.Cfg.Env.Work(), "image-cache") }

// image serves a signed image link from the disk cache or the image host.
func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	remote, sig := r.URL.Query().Get("u"), r.URL.Query().Get("s")
	if !hmac.Equal([]byte(sig), []byte(imageSignature(remote))) {
		http.Error(w, "bad link", http.StatusForbidden)
		return
	}
	sum := sha256.Sum256([]byte(remote))
	name := hex.EncodeToString(sum[:])
	dir := s.imageCacheDir()
	path := filepath.Join(dir, name[:2], name)
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < 30*24*time.Hour {
		w.Header().Set("Cache-Control", "private, max-age=604800")
		ct := "image/jpeg"
		if b, err := os.ReadFile(path + ".type"); err == nil {
			ct = string(b)
		}
		w.Header().Set("Content-Type", ct)
		http.ServeFile(w, r, path)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	data, ct, err := fetchImage(ctx, remote)
	if err != nil {
		http.Error(w, "image not available", http.StatusBadGateway)
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		if os.WriteFile(path+".tmp", data, 0o644) == nil {
			os.Rename(path+".tmp", path)
			os.WriteFile(path+".type", []byte(ct), 0o644)
		}
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Write(data)
}

var imageClient = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !imageHostOK(req.URL.Hostname()) {
			return errors.New("redirect to a host that is not an image host")
		}
		return nil
	},
}

func fetchImage(ctx context.Context, remote string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "HyveMynd")
	resp, err := imageClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") || strings.Contains(ct, "svg") {
		return nil, "", errNotImage
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > 8<<20 {
		return nil, "", errors.New("too large")
	}
	return data, ct, nil
}
