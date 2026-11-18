package indexers

import (
	"strings"
	"testing"
	"time"
)

const torznabFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
<item><title>Show.S01E02.1080p.WEB.H264-GRP</title><guid>https://t/1</guid><link>https://t/dl/1.torrent?apikey=K</link>
<comments>https://t/1</comments><pubDate>Sun, 16 Nov 2026 15:31:02 +0000</pubDate><size>1500000000</size>
<torznab:attr name="seeders" value="42"/><torznab:attr name="peers" value="50"/><torznab:attr name="category" value="5040"/>
<torznab:attr name="infohash" value="ABCDEF"/><torznab:attr name="tvdbid" value="81797"/><torznab:attr name="downloadvolumefactor" value="0"/></item>
<item><title>Magnet only</title><guid>m</guid><torznab:attr name="magneturl" value="magnet:?xt=urn:btih:abc"/><torznab:attr name="seeders" value="3"/></item>
</channel></rss>`

func TestParse(t *testing.T) {
	r, err := Parse([]byte(torznabFeed), "torrent")
	if err != nil || len(r) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	a := r[0]
	if a.Seeders != 42 || a.Peers != 50 || a.Size != 1500000000 || a.InfoHash != "abcdef" || a.TVDB != 81797 || !a.Freeleech || a.Categories[0] != 5040 || a.Published.IsZero() || !strings.HasSuffix(a.Link, "apikey=K") {
		t.Fatalf("%+v", a)
	}
	if r[1].Link != "magnet:?xt=urn:btih:abc" {
		t.Fatalf("magnet: %+v", r[1])
	}
	if _, err := Parse([]byte(`<error code="100" description="Incorrect user credentials"/>`), "torrent"); err == nil || !strings.Contains(err.Error(), "Incorrect user credentials") {
		t.Fatalf("error doc: %v", err)
	}
}

func TestCaps(t *testing.T) {
	c, err := ParseCaps([]byte(`<caps><server title="Tracker"/><searching><search available="yes" supportedParams="q"/><tv-search available="yes" supportedParams="q,season,ep,tvdbid"/><movie-search available="no"/></searching></caps>`))
	if err != nil || c.Server != "Tracker" || !c.TVSearch || c.MovieSearch || c.TVParams != "q,season,ep,tvdbid" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseCaps([]byte(`<html>nope</html>`)); err == nil {
		t.Fatal("html is not caps")
	}
}

func TestSearchURL(t *testing.T) {
	u := searchURL("https://idx.example/", "K", Query{Type: "tv", Text: "One Piece", Season: 1, Episode: 2, TVDB: 81797}, []int{5000, 5070})
	for _, want := range []string{"https://idx.example/api?", "t=tvsearch", "season=1", "ep=2", "tvdbid=81797", "cat=5000%2C5070", "q=One+Piece", "apikey=K"} {
		if !strings.Contains(u, want) {
			t.Errorf("%s lacks %s", u, want)
		}
	}
	if u := searchURL("http://prowlarr:9696/3/api", "", Query{Type: "movie", IMDB: "tt0133093"}, nil); !strings.HasPrefix(u, "http://prowlarr:9696/3/api?") || !strings.Contains(u, "imdbid=0133093") {
		t.Fatal(u)
	}
}

func TestBackoff(t *testing.T) {
	if backoff(1) != 5*time.Minute || backoff(2) != 10*time.Minute || backoff(20) != 24*time.Hour {
		t.Fatal(backoff(1), backoff(2), backoff(20))
	}
}
