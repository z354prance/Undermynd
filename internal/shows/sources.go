package shows

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/library"
)

// Release is one item a nyaa search returned.
type Release struct {
	Title   string          `json:"title"`
	Link    string          `json:"-"`
	Size    int64           `json:"size"`
	Seeders int             `json:"seeders"`
	Pub     time.Time       `json:"published"`
	Parsed  library.Release `json:"-"`
	OK      bool            `json:"-"` // the title has an episode number
}

type nyaaItem struct {
	Title    string `xml:"title"`
	Link     string `xml:"link"`
	GUID     string `xml:"guid"`
	PubDate  string `xml:"pubDate"`
	Seeders  string `xml:"seeders"`
	Size     string `xml:"size"`
	InfoHash string `xml:"infoHash"`
}

// parseSize reads nyaa's sizes: "1.3 GiB", "412.5 MiB".
func parseSize(s string) int64 {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) != 2 {
		return 0
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	mult := map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}[f[1]]
	return int64(n * mult)
}

// ParseNyaa reads a nyaa RSS page.
func ParseNyaa(b []byte) ([]Release, error) {
	var doc struct {
		Items []nyaaItem `xml:"channel>item"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []Release
	for _, it := range doc.Items {
		r := Release{Title: strings.TrimSpace(it.Title), Link: strings.TrimSpace(it.Link), Size: parseSize(it.Size)}
		r.Seeders, _ = strconv.Atoi(strings.TrimSpace(it.Seeders))
		if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(it.PubDate)); err == nil {
			r.Pub = t
		} else if t, err := time.Parse(time.RFC1123, strings.TrimSpace(it.PubDate)); err == nil {
			r.Pub = t
		}
		if h := strings.TrimSpace(it.InfoHash); h != "" && r.Link == "" {
			r.Link = "magnet:?xt=urn:btih:" + h + "&dn=" + url.QueryEscape(r.Title)
		}
		name := r.Title
		if !videoExt[strings.ToLower(filepath.Ext(name))] {
			name += ".mkv"
		}
		r.Parsed, r.OK = library.Parse(name)
		out = append(out, r)
	}
	return out, nil
}

// Nyaa searches nyaa.si's English-translated anime by RSS.
type Nyaa struct {
	BaseURL string // default https://nyaa.si
	HTTP    *http.Client
}

// SearchURL is the RSS address for a query.
func (n Nyaa) SearchURL(q string) string {
	base := strings.TrimRight(n.BaseURL, "/")
	if base == "" {
		base = "https://nyaa.si"
	}
	return base + "/?page=rss&c=1_2&f=0&q=" + url.QueryEscape(q)
}

// Search runs a query.
func (n Nyaa) Search(ctx context.Context, q string) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.SearchURL(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("nyaa did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nyaa answered HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return ParseNyaa(b)
}

// AniList looks shows up on anilist.co, only to show their status.
type AniList struct {
	URL  string // default https://graphql.anilist.co
	HTTP *http.Client
}

const aniFields = `id title { romaji english } episodes status season seasonYear siteUrl nextAiringEpisode { episode airingAt }`

type aniMedia struct {
	ID    int64 `json:"id"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
	} `json:"title"`
	Episodes   int    `json:"episodes"`
	Status     string `json:"status"`
	Season     string `json:"season"`
	SeasonYear int    `json:"seasonYear"`
	SiteURL    string `json:"siteUrl"`
	Next       *struct {
		Episode  int   `json:"episode"`
		AiringAt int64 `json:"airingAt"`
	} `json:"nextAiringEpisode"`
}

func (m aniMedia) info() *AniInfo {
	a := &AniInfo{ID: m.ID, Romaji: m.Title.Romaji, English: m.Title.English, Episodes: m.Episodes, Status: m.Status, Season: m.Season, Year: m.SeasonYear, URL: m.SiteURL}
	if m.Next != nil {
		a.NextEpisode, a.NextAt = m.Next.Episode, m.Next.AiringAt
	}
	return a
}

func (a AniList) query(ctx context.Context, q string, vars map[string]any, out any) error {
	u := a.URL
	if u == "" {
		u = "https://graphql.anilist.co"
	}
	body, _ := json.Marshal(map[string]any{"query": q, "variables": vars})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return errors.New("AniList did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("AniList answered HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// Find looks a show up by title; nil when AniList has nothing.
func (a AniList) Find(ctx context.Context, title string) (*AniInfo, error) {
	var res struct {
		Data struct {
			Page struct {
				Media []aniMedia `json:"media"`
			} `json:"Page"`
		} `json:"data"`
	}
	q := `query ($s: String) { Page(perPage: 5) { media(search: $s, type: ANIME, sort: SEARCH_MATCH) { ` + aniFields + ` } } }`
	if err := a.query(ctx, q, map[string]any{"s": title}, &res); err != nil {
		return nil, err
	}
	media := res.Data.Page.Media
	if len(media) == 0 {
		return nil, nil
	}
	// A result whose title is the folder's beats the search order.
	want := squash(title)
	for _, m := range media {
		if squash(m.Title.Romaji) == want || squash(m.Title.English) == want {
			return m.info(), nil
		}
	}
	return media[0].info(), nil
}

// Get looks a show up by AniList id.
func (a AniList) Get(ctx context.Context, id int64) (*AniInfo, error) {
	var res struct {
		Data struct {
			Media *aniMedia `json:"Media"`
		} `json:"data"`
	}
	q := `query ($id: Int) { Media(id: $id, type: ANIME) { ` + aniFields + ` } }`
	if err := a.query(ctx, q, map[string]any{"id": id}, &res); err != nil {
		return nil, err
	}
	if res.Data.Media == nil {
		return nil, fmt.Errorf("AniList has no anime %d", id)
	}
	return res.Data.Media.info(), nil
}

// squash compares titles ignoring case, spacing and punctuation.
func squash(t string) string { return strings.ReplaceAll(library.Normalize(t), " ", "") }
