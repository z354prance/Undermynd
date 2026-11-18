// Package musicbrainz reads artists, albums and track lists from MusicBrainz
// (roadmap H1), at most one request a second as its rules ask, with a cache in
// the database. Cover art comes from the Cover Art Archive.
package musicbrainz

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/db"
)

// DefaultURL is MusicBrainz's web service.
const DefaultURL = "https://musicbrainz.org/ws/2"

// Client talks to MusicBrainz.
type Client struct {
	DB      *sql.DB // nil: no cache
	HTTP    *http.Client
	BaseURL string
	Agent   string        // User-Agent; MusicBrainz asks for an application name and contact
	Gap     time.Duration // between requests; 0 means one second

	mu   sync.Mutex
	last time.Time
}

// ErrNotFound is MusicBrainz answering 404.
var ErrNotFound = errors.New("not found on MusicBrainz")

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	gap := c.Gap
	if gap == 0 {
		gap = time.Second
	}
	if d := gap - time.Since(c.last); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	c.last = time.Now()
	return nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, ttl time.Duration, out any) error {
	q.Set("fmt", "json")
	key := "mb:" + path + "?" + q.Encode()
	if c.DB != nil && ttl > 0 {
		var body []byte
		var fetched string
		if c.DB.QueryRowContext(ctx, `SELECT body, fetched FROM meta_cache WHERE key = ?`, key).Scan(&body, &fetched) == nil {
			if t, err := time.Parse(db.TimeFormat, fetched); err == nil && time.Since(t) < ttl {
				return json.Unmarshal(body, out)
			}
		}
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultURL
	}
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		agent := c.Agent
		if agent == "" {
			agent = "HyveMynd/1.0 ( https://github.com/z354prance/HyveMynd )"
		}
		req.Header.Set("User-Agent", agent)
		req.Header.Set("Accept", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("MusicBrainz is not reachable: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == http.StatusServiceUnavailable && attempt < 3:
			// Over the rate limit: wait and try again.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
			}
			continue
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("MusicBrainz answered HTTP %d", resp.StatusCode)
		}
		if err := json.Unmarshal(body, out); err != nil {
			return err
		}
		if c.DB != nil && ttl > 0 {
			c.DB.ExecContext(ctx, `INSERT INTO meta_cache (key, body, fetched) VALUES (?, ?, ?)
				ON CONFLICT(key) DO UPDATE SET body = excluded.body, fetched = excluded.fetched`, key, body, db.Stamp(time.Now()))
		}
		return nil
	}
}

// Artist is an artist.
type Artist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
	Type           string `json:"type"`
	Country        string `json:"country"`
	Score          int    `json:"score"`
}

// SearchArtists finds artists by name, best first.
func (c *Client) SearchArtists(ctx context.Context, name string) ([]Artist, error) {
	var r struct {
		Artists []Artist `json:"artists"`
	}
	err := c.get(ctx, "/artist", url.Values{"query": {"artist:\"" + strings.ReplaceAll(name, "\"", "") + "\""}, "limit": {"15"}}, 24*time.Hour, &r)
	return r.Artists, err
}

// Artist looks an artist up by MBID.
func (c *Client) Artist(ctx context.Context, id string) (Artist, error) {
	var a Artist
	err := c.get(ctx, "/artist/"+url.PathEscape(id), url.Values{}, 7*24*time.Hour, &a)
	return a, err
}

// Album is a release group: an album, EP or single as MusicBrainz groups
// its editions.
type Album struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
	FirstRelease   string   `json:"first-release-date"`
	Disambiguation string   `json:"disambiguation"`
}

// Year is the album's first release year, 0 when unknown.
func (a Album) Year() int {
	if len(a.FirstRelease) >= 4 {
		var y int
		fmt.Sscan(a.FirstRelease[:4], &y)
		return y
	}
	return 0
}

// Studio reports whether the album is a plain studio album, EP or single
// (no compilation, live, remix, soundtrack...).
func (a Album) Studio() bool { return len(a.SecondaryTypes) == 0 }

// Albums lists an artist's albums, EPs and singles, oldest first.
func (c *Client) Albums(ctx context.Context, artistID string) ([]Album, error) {
	var out []Album
	for offset := 0; offset < 1000; offset += 100 {
		var r struct {
			Count  int     `json:"release-group-count"`
			Groups []Album `json:"release-groups"`
		}
		q := url.Values{"artist": {artistID}, "type": {"album|ep|single"}, "limit": {"100"}, "offset": {fmt.Sprint(offset)}}
		if err := c.get(ctx, "/release-group", q, 24*time.Hour, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Groups...)
		if offset+100 >= r.Count {
			break
		}
	}
	return out, nil
}

// Track is one track of a release.
type Track struct {
	Disc   int    `json:"disc"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Length int    `json:"length"` // milliseconds
}

// Release is one edition of an album with its tracks.
type Release struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Date    string  `json:"date"`
	Country string  `json:"country"`
	Status  string  `json:"status"`
	Tracks  []Track `json:"tracks"`
	Discs   int     `json:"discs"`
}

// Releases lists an album's editions with their track lists; the usual
// edition (official, most common track count) first.
func (c *Client) Releases(ctx context.Context, albumID string) ([]Release, error) {
	var r struct {
		Releases []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Date    string `json:"date"`
			Country string `json:"country"`
			Status  string `json:"status"`
			Media   []struct {
				Position int `json:"position"`
				Tracks   []struct {
					Position int    `json:"position"`
					Title    string `json:"title"`
					Length   int    `json:"length"`
				} `json:"tracks"`
			} `json:"media"`
		} `json:"releases"`
	}
	if err := c.get(ctx, "/release", url.Values{"release-group": {albumID}, "inc": {"recordings+media"}, "limit": {"100"}}, 7*24*time.Hour, &r); err != nil {
		return nil, err
	}
	counts := map[int]int{}
	var out []Release
	for _, x := range r.Releases {
		rel := Release{ID: x.ID, Title: x.Title, Date: x.Date, Country: x.Country, Status: x.Status, Discs: len(x.Media)}
		for _, m := range x.Media {
			for _, t := range m.Tracks {
				rel.Tracks = append(rel.Tracks, Track{Disc: m.Position, Number: t.Position, Title: t.Title, Length: t.Length})
			}
		}
		counts[len(rel.Tracks)]++
		out = append(out, rel)
	}
	usual := func(r Release) int {
		n := counts[len(r.Tracks)] * 10
		if r.Status == "Official" {
			n += 5
		}
		return n
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && usual(out[j]) > usual(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// Cover is the Cover Art Archive's front cover of an album (250 px).
func Cover(albumID string) string {
	return "https://coverartarchive.org/release-group/" + url.PathEscape(albumID) + "/front-250"
}
