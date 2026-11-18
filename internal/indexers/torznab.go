// Package indexers searches Torznab and Newznab indexers (the Prowlarr part of
// the arr stack): one list of indexers with their keys, priorities and limits,
// a health record that backs off a failing indexer, one search across all of
// them, and grabbing a result into the pipeline.
package indexers

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Kinds of indexer.
const (
	Torznab = "torznab" // torrents
	Newznab = "newznab" // usenet
)

// Result is one release an indexer offered.
type Result struct {
	Indexer     string    `json:"indexer"`
	IndexerID   int64     `json:"indexerId"`
	Protocol    string    `json:"protocol"` // torrent or usenet
	Title       string    `json:"title"`
	GUID        string    `json:"guid"`
	Link        string    `json:"link"` // download address (may carry the key)
	Info        string    `json:"info,omitempty"`
	Size        int64     `json:"size"`
	Seeders     int       `json:"seeders"`
	Peers       int       `json:"peers"`
	Grabs       int       `json:"grabs"`
	Published   time.Time `json:"published"`
	Categories  []int     `json:"categories"`
	InfoHash    string    `json:"infoHash,omitempty"`
	TVDB        int64     `json:"tvdbId,omitempty"`
	IMDB        string    `json:"imdbId,omitempty"`
	Freeleech   bool      `json:"freeleech,omitempty"`
	IndexerPrio int       `json:"priority"`
}

type rssItem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Comments  string `xml:"comments"`
	PubDate   string `xml:"pubDate"`
	Size      string `xml:"size"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
	} `xml:"enclosure"`
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

// apiError is the error document Torznab and Newznab answer with.
type apiError struct {
	XMLName     xml.Name `xml:"error"`
	Code        string   `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

func pubTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, f := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", time.RFC3339} {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Parse reads a Torznab or Newznab search answer.
func Parse(b []byte, protocol string) ([]Result, error) {
	var e apiError
	if xml.Unmarshal(b, &e) == nil && e.XMLName.Local == "error" {
		return nil, fmt.Errorf("the indexer said: %s (code %s)", e.Description, e.Code)
	}
	var doc struct {
		Items []rssItem `xml:"channel>item"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, errors.New("the indexer's answer is not a feed")
	}
	out := make([]Result, 0, len(doc.Items))
	for _, it := range doc.Items {
		r := Result{Protocol: protocol, Title: strings.TrimSpace(it.Title), GUID: strings.TrimSpace(it.GUID), Info: strings.TrimSpace(it.Comments), Published: pubTime(it.PubDate)}
		r.Link = strings.TrimSpace(it.Enclosure.URL)
		if r.Link == "" {
			r.Link = strings.TrimSpace(it.Link)
		}
		r.Size, _ = strconv.ParseInt(strings.TrimSpace(it.Size), 10, 64)
		if r.Size == 0 {
			r.Size, _ = strconv.ParseInt(strings.TrimSpace(it.Enclosure.Length), 10, 64)
		}
		for _, a := range it.Attrs {
			v := strings.TrimSpace(a.Value)
			switch strings.ToLower(a.Name) {
			case "seeders":
				r.Seeders, _ = strconv.Atoi(v)
			case "peers":
				r.Peers, _ = strconv.Atoi(v)
			case "grabs":
				r.Grabs, _ = strconv.Atoi(v)
			case "size":
				if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
					r.Size = n
				}
			case "category":
				if n, err := strconv.Atoi(v); err == nil {
					r.Categories = append(r.Categories, n)
				}
			case "infohash":
				r.InfoHash = strings.ToLower(v)
			case "magneturl":
				if r.Link == "" {
					r.Link = v
				}
			case "tvdbid":
				r.TVDB, _ = strconv.ParseInt(v, 10, 64)
			case "imdb", "imdbid":
				r.IMDB = strings.TrimPrefix(v, "tt")
			case "downloadvolumefactor":
				r.Freeleech = v == "0"
			}
		}
		if r.Peers < r.Seeders {
			r.Peers = r.Seeders
		}
		if r.Categories == nil {
			r.Categories = []int{}
		}
		out = append(out, r)
	}
	return out, nil
}

// Caps is what an indexer says it can search.
type Caps struct {
	Server      string `json:"server"`
	Search      bool   `json:"search"`
	TVSearch    bool   `json:"tvSearch"`
	MovieSearch bool   `json:"movieSearch"`
	MusicSearch bool   `json:"musicSearch"`
	// TVParams and MovieParams are the ids and fields the searches accept.
	TVParams    string `json:"tvParams"`
	MovieParams string `json:"movieParams"`
}

// ParseCaps reads a t=caps answer.
func ParseCaps(b []byte) (Caps, error) {
	var e apiError
	if xml.Unmarshal(b, &e) == nil && e.XMLName.Local == "error" {
		return Caps{}, fmt.Errorf("the indexer said: %s (code %s)", e.Description, e.Code)
	}
	type mode struct {
		Available       string `xml:"available,attr"`
		SupportedParams string `xml:"supportedParams,attr"`
	}
	var doc struct {
		XMLName xml.Name `xml:"caps"`
		Server  struct {
			Title string `xml:"title,attr"`
		} `xml:"server"`
		Searching struct {
			Search mode `xml:"search"`
			TV     mode `xml:"tv-search"`
			Movie  mode `xml:"movie-search"`
			Music  mode `xml:"music-search"`
			Audio  mode `xml:"audio-search"`
		} `xml:"searching"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil || doc.XMLName.Local != "caps" {
		return Caps{}, errors.New("the address does not answer like a Torznab or Newznab indexer")
	}
	yes := func(m mode) bool { return strings.EqualFold(m.Available, "yes") }
	return Caps{Server: doc.Server.Title, Search: yes(doc.Searching.Search), TVSearch: yes(doc.Searching.TV), MovieSearch: yes(doc.Searching.Movie),
		MusicSearch: yes(doc.Searching.Music) || yes(doc.Searching.Audio), TVParams: doc.Searching.TV.SupportedParams, MovieParams: doc.Searching.Movie.SupportedParams}, nil
}

// Query is one search.
type Query struct {
	Text       string `json:"q"`
	Type       string `json:"type"` // search, tv, movie, music
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	TVDB       int64  `json:"tvdbId"`
	IMDB       string `json:"imdbId"`
	TMDB       int64  `json:"tmdbId"`
	Categories []int  `json:"categories"`
	Limit      int    `json:"limit"`
}

// DefaultCategories are the Newznab categories a type of search asks for.
func DefaultCategories(typ string) []int {
	switch typ {
	case "tv":
		return []int{5000}
	case "movie":
		return []int{2000}
	case "music":
		return []int{3000}
	}
	return nil
}

// apiURL is an indexer's API address: a base like https://x.example (or one
// already ending in /api) gives https://x.example/api.
func apiURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(strings.ToLower(base), "/api") {
		return base
	}
	return base + "/api"
}

// searchURL builds the request for a query.
func searchURL(base, key string, q Query, cats []int) string {
	v := url.Values{}
	switch q.Type {
	case "tv":
		v.Set("t", "tvsearch")
		if q.Season > 0 {
			v.Set("season", strconv.Itoa(q.Season))
		}
		if q.Episode > 0 {
			v.Set("ep", strconv.Itoa(q.Episode))
		}
		if q.TVDB > 0 {
			v.Set("tvdbid", strconv.FormatInt(q.TVDB, 10))
		}
	case "movie":
		v.Set("t", "movie")
		if q.IMDB != "" {
			v.Set("imdbid", strings.TrimPrefix(q.IMDB, "tt"))
		}
		if q.TMDB > 0 {
			v.Set("tmdbid", strconv.FormatInt(q.TMDB, 10))
		}
	case "music":
		v.Set("t", "music")
	default:
		v.Set("t", "search")
	}
	if strings.TrimSpace(q.Text) != "" {
		v.Set("q", strings.TrimSpace(q.Text))
	}
	if len(cats) > 0 {
		s := make([]string, len(cats))
		for i, c := range cats {
			s[i] = strconv.Itoa(c)
		}
		v.Set("cat", strings.Join(s, ","))
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	v.Set("limit", strconv.Itoa(limit))
	v.Set("extended", "1")
	if key != "" {
		v.Set("apikey", key)
	}
	return apiURL(base) + "?" + v.Encode()
}

func capsURL(base, key string) string {
	v := url.Values{"t": {"caps"}}
	if key != "" {
		v.Set("apikey", key)
	}
	return apiURL(base) + "?" + v.Encode()
}

// get fetches an indexer address. The key is never part of an error message.
func get(ctx context.Context, hc *http.Client, u string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("invalid indexer address")
	}
	req.Header.Set("User-Agent", "HyveMynd")
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("the indexer took too long")
		}
		return nil, errors.New("the indexer did not answer")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("the indexer says too many requests")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, errors.New("the indexer refused the API key")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("the indexer answered HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}
