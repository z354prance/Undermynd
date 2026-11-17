package shows

import (
	"testing"
	"time"
)

func disk(eps ...[2]int) []DiskFile { // season, episode
	var out []DiskFile
	for _, e := range eps {
		out = append(out, DiskFile{OK: true, Season: e[0], Episode: e[1]})
	}
	return out
}

func rel(group string, ep int, res string, age time.Duration, now time.Time) Seen {
	return Seen{Title: group + " ep", Group: group, Episode: ep, Season: 1, Res: res, First: now.Add(-age)}
}

func TestSummarize(t *testing.T) {
	s := Summarize(disk([2]int{1, 1}, [2]int{1, 2}, [2]int{1, 3}, [2]int{1, 5}, [2]int{1, 6}))
	if s.Files != 5 || s.Latest != 6 || s.Gaps != 1 || s.Seasons[0].Gaps[0] != 4 {
		t.Fatalf("%+v", s)
	}
	// Season 1 starting at 3 misses 1 and 2; a later season starting at 26 (absolute) misses nothing.
	s = Summarize(disk([2]int{1, 3}, [2]int{1, 4}, [2]int{2, 26}, [2]int{2, 27}))
	if s.Gaps != 2 || s.Season != 2 || s.Latest != 27 {
		t.Fatalf("%+v", s)
	}
}

func TestPick(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	st := Settings{Followed: true, Groups: []string{"Judas", "SubsPlease"}, Quality: "1080p", FromEpisode: 6}
	files := disk([2]int{1, 1}, [2]int{1, 2}, [2]int{1, 3}, [2]int{1, 5}, [2]int{1, 6})
	seen := []Seen{
		rel("SubsPlease", 7, "1080p", 2*time.Hour, now), // Judas has not released 7 yet: wait
		rel("SubsPlease", 8, "1080p", time.Hour, now),
		rel("Judas", 8, "1080p", time.Hour, now),         // Judas released 8, so it skipped 7
		rel("Judas", 6, "1080p", 72*time.Hour, now),      // on disk
		rel("SubsPlease", 4, "1080p", 72*time.Hour, now), // a gap: below FromEpisode, left to gap search
		rel("Judas", 9, "720p", time.Hour, now),          // wrong quality
		rel("Erai-raws", 9, "1080p", time.Hour, now),     // not in the order
	}
	got := Pick(st, files, seen, now, 24*time.Hour)
	if len(got) != 2 || got[0].Episode != 7 || got[0].Group != "SubsPlease" || got[1].Episode != 8 || got[1].Group != "Judas" {
		t.Fatalf("got %+v", got)
	}
	// Without Judas moving past 7, SubsPlease's 7 waits for the grace period.
	got = Pick(st, files, seen[:1], now, 24*time.Hour)
	if len(got) != 0 {
		t.Fatalf("should wait: %+v", got)
	}
	got = Pick(st, files, []Seen{rel("SubsPlease", 7, "1080p", 25*time.Hour, now)}, now, 24*time.Hour)
	if len(got) != 1 {
		t.Fatalf("after the grace period: %+v", got)
	}
	// Something already grabbed for an episode is not grabbed again.
	g := rel("Judas", 7, "1080p", time.Hour, now)
	g.Grabbed = now
	if got := Pick(st, files, []Seen{g, rel("SubsPlease", 7, "1080p", 30*time.Hour, now)}, now, 24*time.Hour); len(got) != 0 {
		t.Fatalf("grabbed already: %+v", got)
	}
}

const nyaaRSS = `<?xml version="1.0" encoding="utf-8"?>
<rss xmlns:atom="http://www.w3.org/2005/Atom" xmlns:nyaa="https://nyaa.si/xmlns/nyaa" version="2.0"><channel>
<item><title>[SubsPlease] Gorilla no Kami - 08 (1080p) [ABCD1234].mkv</title><link>https://nyaa.si/download/1.torrent</link><guid isPermaLink="true">https://nyaa.si/view/1</guid>
<pubDate>Sun, 16 Nov 2026 15:31:02 -0000</pubDate><nyaa:seeders>412</nyaa:seeders><nyaa:infoHash>aaaa</nyaa:infoHash><nyaa:size>1.3 GiB</nyaa:size></item>
<item><title>Gorilla no Kami Vol.1 Raws</title><link>https://nyaa.si/download/2.torrent</link><pubDate>Sun, 16 Nov 2026 15:31:02 -0000</pubDate><nyaa:seeders>3</nyaa:seeders><nyaa:size>412.5 MiB</nyaa:size></item>
</channel></rss>`

func TestParseNyaa(t *testing.T) {
	r, err := ParseNyaa([]byte(nyaaRSS))
	if err != nil || len(r) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	a := r[0]
	if !a.OK || a.Parsed.Group != "SubsPlease" || a.Parsed.Episode != 8 || a.Parsed.Res != "1080p" || a.Seeders != 412 || a.Size < 1300<<20 || a.Pub.IsZero() || a.Link == "" {
		t.Fatalf("%+v", a)
	}
	if r[1].Size != int64(412.5*(1<<20)) {
		t.Fatalf("size %d", r[1].Size)
	}
}
