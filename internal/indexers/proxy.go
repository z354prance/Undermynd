package indexers

import (
	"context"
	"crypto/subtle"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Handler serves the indexers to the arrs as Torznab and Newznab endpoints:
// /indexers/<id>/api passes a search to that indexer (its own key stays in
// HyveMynd), and /indexers/all-torrent/api and /indexers/all-usenet/api search
// every usable indexer of that protocol at once. key is the key the arrs
// send (the download client password).
func (s *Service) Handler(key func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "indexers" || parts[2] != "api" {
			http.NotFound(w, r)
			return
		}
		k := key()
		if k == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("apikey")), []byte(k)) != 1 {
			writeError(w, 100, "Incorrect user credentials")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		v := r.URL.Query()
		switch parts[1] {
		case "all-torrent", "all-usenet":
			protocol := strings.TrimPrefix(parts[1], "all-")
			if v.Get("t") == "caps" {
				writeXML(w, aggregateCaps(protocol))
				return
			}
			if off, _ := strconv.Atoi(v.Get("offset")); off > 0 {
				writeXML(w, feed(protocol, nil)) // one page only
				return
			}
			res, err := s.Search(ctx, queryOf(v), protocol)
			if err != nil {
				writeError(w, 900, err.Error())
				return
			}
			writeXML(w, feed(protocol, res.Results))
		default:
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			b, err := s.pass(ctx, id, v)
			if err != nil {
				writeError(w, 900, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.Write(b)
		}
	})
}

// pass forwards a request to one indexer with its own key, counting and
// recording it like any search.
func (s *Service) pass(ctx context.Context, id int64, v url.Values) ([]byte, error) {
	i, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	caps := v.Get("t") == "caps"
	if !caps {
		if why := s.usable(i, ""); why != "" {
			return nil, fmt.Errorf("%s is %s", i.Name, why)
		}
		if err := s.wait(ctx, i.ID); err != nil {
			return nil, err
		}
		s.count(i.ID, "queries")
	}
	q := url.Values{}
	for k, vals := range v {
		if strings.EqualFold(k, "apikey") {
			continue
		}
		q[k] = vals
	}
	if i.APIKey != "" {
		q.Set("apikey", i.APIKey)
	}
	b, err := get(ctx, s.HTTP, apiURL(i.URL)+"?"+q.Encode(), 16<<20)
	if !caps {
		if err == nil {
			_, err = Parse(b, i.Protocol())
		}
		s.record(i.ID, err)
	}
	return b, err
}

func queryOf(v url.Values) Query {
	q := Query{Text: v.Get("q")}
	switch v.Get("t") {
	case "tvsearch":
		q.Type = "tv"
	case "movie":
		q.Type = "movie"
	case "music":
		q.Type = "music"
	default:
		q.Type = "search"
	}
	q.Season, _ = strconv.Atoi(v.Get("season"))
	q.Episode, _ = strconv.Atoi(v.Get("ep"))
	q.TVDB, _ = strconv.ParseInt(v.Get("tvdbid"), 10, 64)
	q.TMDB, _ = strconv.ParseInt(v.Get("tmdbid"), 10, 64)
	q.IMDB = v.Get("imdbid")
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	for _, c := range strings.Split(v.Get("cat"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
			q.Categories = append(q.Categories, n)
		}
	}
	return q
}

type xattr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xitem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Comments  string `xml:"comments,omitempty"`
	PubDate   string `xml:"pubDate,omitempty"`
	Size      int64  `xml:"size"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length int64  `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
	Attrs []xattr `xml:"torznab:attr"`
}

type xfeed struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	NS      string   `xml:"xmlns:torznab,attr"`
	NSNew   string   `xml:"xmlns:newznab,attr"`
	Channel struct {
		Title string  `xml:"title"`
		Items []xitem `xml:"item"`
	} `xml:"channel"`
}

// feed writes results as a Torznab (or Newznab) feed.
func feed(protocol string, results []Result) any {
	var f xfeed
	f.Version, f.NS, f.NSNew = "2.0", "http://torznab.com/schemas/2015/feed", "http://www.newznab.com/DTD/2010/feeds/attributes/"
	f.Channel.Title = "HyveMynd"
	for _, r := range results {
		it := xitem{Title: r.Title, GUID: r.GUID, Link: r.Link, Comments: r.Info, Size: r.Size}
		if it.GUID == "" {
			it.GUID = r.Link
		}
		if !r.Published.IsZero() {
			it.PubDate = r.Published.UTC().Format(time.RFC1123Z)
		}
		it.Enclosure.URL, it.Enclosure.Length = r.Link, r.Size
		it.Enclosure.Type = "application/x-bittorrent"
		if protocol == "usenet" {
			it.Enclosure.Type = "application/x-nzb"
		}
		add := func(n, v string) { it.Attrs = append(it.Attrs, xattr{n, v}) }
		for _, c := range r.Categories {
			add("category", strconv.Itoa(c))
		}
		add("size", strconv.FormatInt(r.Size, 10))
		if protocol == "torrent" {
			add("seeders", strconv.Itoa(r.Seeders))
			add("peers", strconv.Itoa(r.Peers))
			if r.InfoHash != "" {
				add("infohash", r.InfoHash)
			}
			if r.Freeleech {
				add("downloadvolumefactor", "0")
			}
		}
		if r.Grabs > 0 {
			add("grabs", strconv.Itoa(r.Grabs))
		}
		if r.TVDB > 0 {
			add("tvdbid", strconv.FormatInt(r.TVDB, 10))
		}
		if r.IMDB != "" {
			add("imdb", r.IMDB)
		}
		add("indexer", r.Indexer)
		f.Channel.Items = append(f.Channel.Items, it)
	}
	return f
}

type xcaps struct {
	XMLName xml.Name `xml:"caps"`
	Server  struct {
		Title string `xml:"title,attr"`
	} `xml:"server"`
	Limits struct {
		Max     int `xml:"max,attr"`
		Default int `xml:"default,attr"`
	} `xml:"limits"`
	Searching struct {
		Search mode `xml:"search"`
		TV     mode `xml:"tv-search"`
		Movie  mode `xml:"movie-search"`
		Music  mode `xml:"music-search"`
	} `xml:"searching"`
	Categories struct {
		Category []xcat `xml:"category"`
	} `xml:"categories"`
}

type mode struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type xcat struct {
	ID   int    `xml:"id,attr"`
	Name string `xml:"name,attr"`
	Subs []xcat `xml:"subcat"`
}

func aggregateCaps(protocol string) any {
	var c xcaps
	c.Server.Title = "HyveMynd (all " + protocol + " indexers)"
	c.Limits.Max, c.Limits.Default = 100, 100
	c.Searching.Search = mode{"yes", "q"}
	c.Searching.TV = mode{"yes", "q,season,ep,tvdbid"}
	c.Searching.Movie = mode{"yes", "q,imdbid,tmdbid"}
	c.Searching.Music = mode{"yes", "q"}
	c.Categories.Category = []xcat{
		{ID: 2000, Name: "Movies", Subs: []xcat{{ID: 2030, Name: "Movies/SD"}, {ID: 2040, Name: "Movies/HD"}, {ID: 2045, Name: "Movies/UHD"}}},
		{ID: 3000, Name: "Audio", Subs: []xcat{{ID: 3010, Name: "Audio/MP3"}, {ID: 3040, Name: "Audio/Lossless"}}},
		{ID: 5000, Name: "TV", Subs: []xcat{{ID: 5030, Name: "TV/SD"}, {ID: 5040, Name: "TV/HD"}, {ID: 5045, Name: "TV/UHD"}, {ID: 5070, Name: "TV/Anime"}}},
	}
	return c
}

func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, desc string) {
	writeXML(w, struct {
		XMLName     xml.Name `xml:"error"`
		Code        int      `xml:"code,attr"`
		Description string   `xml:"description,attr"`
	}{Code: code, Description: desc})
}
