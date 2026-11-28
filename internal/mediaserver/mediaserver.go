// Package mediaserver talks to Emby and Jellyfin through one small interface:
// the same address and key, the same calls, the sign-in header each expects.
// Access requests, playback filtering and library refreshes all go through it.
package mediaserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Server is one Emby or Jellyfin server.
type Server struct {
	Kind string // "emby" (default) or "jellyfin"
	URL  string
	Key  string
	HTTP *http.Client // nil: a 15 second client that does not follow redirects
}

// Jellyfin says whether the server is Jellyfin.
func (s Server) Jellyfin() bool { return strings.EqualFold(s.Kind, "jellyfin") }

// Authorize sets the key the way the server expects it.
func Authorize(req *http.Request, kind, key string) {
	req.Header.Set("X-Emby-Token", key)
	if strings.EqualFold(kind, "jellyfin") {
		req.Header.Set("Authorization", `MediaBrowser Token="`+key+`"`)
	}
}

// StatusError is an HTTP status the server answered with.
type StatusError int

func (e StatusError) Error() string { return fmt.Sprintf("media server returned HTTP %d", int(e)) }

func (s Server) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Do sends one request; body and out are JSON, either may be nil.
func (s Server) Do(ctx context.Context, method, path string, body, out any) error {
	if strings.TrimSpace(s.URL) == "" {
		return errors.New("no media server address is set")
	}
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.URL, "/")+path, payload)
	if err != nil {
		return errors.New("invalid media server URL")
	}
	Authorize(req, s.Kind, s.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.client().Do(req)
	if err != nil {
		return errors.New("media server connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return StatusError(res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
}

// Check proves the address and key work and returns the server's name.
func (s Server) Check(ctx context.Context) (string, error) {
	var info struct {
		ServerName string `json:"ServerName"`
	}
	if err := s.Do(ctx, http.MethodGet, "/System/Info", nil, &info); err != nil {
		return "", err
	}
	return info.ServerName, nil
}

// Library is one library of the server.
type Library struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Kind  string   `json:"kind"` // movies, tvshows, music, ...
	Paths []string `json:"paths"`
}

// Libraries lists the server's libraries and their folders.
func (s Server) Libraries(ctx context.Context) ([]Library, error) {
	var raw []struct {
		Name           string   `json:"Name"`
		ItemID         string   `json:"ItemId"`
		CollectionType string   `json:"CollectionType"`
		Locations      []string `json:"Locations"`
	}
	if err := s.Do(ctx, http.MethodGet, "/Library/VirtualFolders", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Library, 0, len(raw))
	for _, l := range raw {
		out = append(out, Library{ID: l.ItemID, Name: l.Name, Kind: l.CollectionType, Paths: l.Locations})
	}
	return out, nil
}

// Updated tells the server files were added, so it scans just those folders
// rather than whole libraries. Paths are as the server sees them.
func (s Server) Updated(ctx context.Context, paths ...string) error {
	type update struct {
		Path       string `json:"Path"`
		UpdateType string `json:"UpdateType"`
	}
	var body struct {
		Updates []update `json:"Updates"`
	}
	for _, p := range paths {
		body.Updates = append(body.Updates, update{Path: p, UpdateType: "Created"})
	}
	if len(body.Updates) == 0 {
		return nil
	}
	return s.Do(ctx, http.MethodPost, "/Library/Media/Updated", body, nil)
}

// MapPath turns a path inside HyveMynd into the path the media server sees,
// replacing the prefix from with to ("" leaves it as it is).
func MapPath(p, from, to string) string {
	from, to = strings.TrimRight(from, "/"), strings.TrimRight(to, "/")
	if from == "" || (p != from && !strings.HasPrefix(p, from+"/")) {
		return p
	}
	return to + strings.TrimPrefix(p, from)
}

// ItemPath is the file or folder path of one library item, as the media
// server sees it.
func (s Server) ItemPath(ctx context.Context, id string) (string, error) {
	var r struct {
		Items []struct {
			Path string `json:"Path"`
		} `json:"Items"`
	}
	if err := s.Do(ctx, http.MethodGet, "/Items?Recursive=true&Fields=Path&Ids="+url.QueryEscape(id), nil, &r); err != nil {
		return "", err
	}
	if len(r.Items) == 0 {
		return "", nil
	}
	return r.Items[0].Path, nil
}

// Count is how many items of the given types (Movie, Series, Season, Episode,
// MusicAlbum, Audio, Book, AudioBook) a library holds, all levels down.
func (s Server) Count(ctx context.Context, libraryID, types string) (int, error) {
	var r struct {
		Total int `json:"TotalRecordCount"`
	}
	q := url.Values{"ParentId": {libraryID}, "Recursive": {"true"}, "IncludeItemTypes": {types}, "Limit": {"0"}}
	err := s.Do(ctx, http.MethodGet, "/Items?"+q.Encode(), nil, &r)
	return r.Total, err
}

// AlbumArtists is how many album artists a music library holds.
func (s Server) AlbumArtists(ctx context.Context, libraryID string) (int, error) {
	var r struct {
		Total int `json:"TotalRecordCount"`
	}
	q := url.Values{"ParentId": {libraryID}, "Limit": {"0"}}
	err := s.Do(ctx, http.MethodGet, "/Artists/AlbumArtists?"+q.Encode(), nil, &r)
	return r.Total, err
}
