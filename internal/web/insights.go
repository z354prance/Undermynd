package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/mediaserver"
	"github.com/z354prance/undermynd/internal/tracearr"
)

// The public storage panel (pinned v5): what came in and what was watched over
// the last days, Tdarr's savings, the library's counts, a pick for tonight and
// how far upgrades have come. Day totals are kept in a blob, because jobs are
// pruned after a week and Tracearr's history carries no play time.

const (
	insightsBlob = "insights.json"
	insightDays  = 10
	keepDays     = 40
)

type insDay struct {
	Added     map[string]int     `json:"added,omitempty"`
	Watch     map[string]float64 `json:"watch,omitempty"` // seconds per kind
	Direct    float64            `json:"direct,omitempty"`
	Transcode float64            `json:"transcode,omitempty"`
}

type insState struct {
	Days    map[string]*insDay `json:"days"`
	Counted map[string]string  `json:"counted"` // job id -> day it was counted on
}

// libCounts is one library's counts; zero fields are left out.
type libCounts struct {
	Movies   int `json:"movies,omitempty"`
	Shows    int `json:"shows,omitempty"`
	Seasons  int `json:"seasons,omitempty"`
	Episodes int `json:"episodes,omitempty"`
	Artists  int `json:"artists,omitempty"`
	Albums   int `json:"albums,omitempty"`
	Tracks   int `json:"tracks,omitempty"`
	Ebooks   int `json:"ebooks,omitempty"`
	Audio    int `json:"audiobooks,omitempty"`
}

type pick struct {
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	Kind      string `json:"kind"`
	Poster    string `json:"poster,omitempty"`
	Unwatched bool   `json:"unwatched,omitempty"`
}

type insSlow struct {
	Library  map[string]libCounts `json:"library"`   // by kind
	ByName   map[string]libCounts `json:"libraries"` // by media-server library name, lower case
	Upgrades struct {
		Episodes int     `json:"episodes"`
		Movies   int     `json:"movies"`
		Share    float64 `json:"share"` // part of the library at the better quality; -1 unknown
	} `json:"upgrades"`
	Picks []pick `json:"picks"`
	At    time.Time
}

type insights struct {
	mu       sync.Mutex
	st       insState
	loaded   bool
	dirty    bool
	lastSeen time.Time
	slow     insSlow
	watched  watchedSet
}

type watchedSet struct {
	movies map[int64]bool
	shows  map[string]bool
	ok     bool
	at     time.Time
}

// kindOf is the library kind of a category: tv, movie, anime, music or book.
func kindOf(c config.Category) string {
	n := strings.ToLower(c.Name)
	switch {
	case strings.Contains(n, "anime") || c.Kind == "rss":
		return "anime"
	case c.Kind == "sonarr":
		return "tv"
	case c.Kind == "radarr":
		return "movie"
	case c.Kind == "lidarr":
		return "music"
	case strings.Contains(n, "book"):
		return "book"
	}
	return ""
}

var videoFile = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".m2ts": true, ".wmv": true, ".webm": true}

// addedCount is how many library items a finished download brought: its
// episodes for shows, one otherwise (a movie, an album, a book).
func addedCount(kind string, files []string) int {
	if kind != "tv" && kind != "anime" {
		return 1
	}
	n := 0
	for _, f := range files {
		if videoFile[strings.ToLower(filepath.Ext(f))] {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

func dayKey(t time.Time) string { return t.Local().Format("2006-01-02") }

func (s *Server) insLoad() {
	in := &s.ins
	if in.loaded {
		return
	}
	in.loaded = true
	in.st = insState{Days: map[string]*insDay{}, Counted: map[string]string{}}
	if s.Blobs != nil {
		if b, ok, err := s.Blobs.Load(insightsBlob); err == nil && ok {
			_ = json.Unmarshal(b, &in.st)
		}
	}
	if in.st.Days == nil {
		in.st.Days = map[string]*insDay{}
	}
	if in.st.Counted == nil {
		in.st.Counted = map[string]string{}
	}
}

func (s *Server) insDayLocked(t time.Time) *insDay {
	k := dayKey(t)
	d := s.ins.st.Days[k]
	if d == nil {
		d = &insDay{Added: map[string]int{}, Watch: map[string]float64{}}
		s.ins.st.Days[k] = d
	}
	if d.Added == nil {
		d.Added = map[string]int{}
	}
	if d.Watch == nil {
		d.Watch = map[string]float64{}
	}
	return d
}

// countImports adds each download that reached the library to the day it got
// there, once.
func (s *Server) countImports(now time.Time) {
	if s.Store == nil {
		return
	}
	cfg := s.Cfg.Get()
	horizon := now.AddDate(0, 0, -keepDays)
	in := &s.ins
	in.mu.Lock()
	defer in.mu.Unlock()
	s.insLoad()
	for _, j := range s.Store.List() {
		if j.State != job.Imported && j.State != job.Cleaned {
			continue
		}
		if _, done := in.st.Counted[j.ID]; done {
			continue
		}
		var at time.Time
		for i := len(j.History) - 1; i >= 0; i-- {
			if j.History[i].To == job.Imported {
				at = j.History[i].At
				break
			}
		}
		if at.IsZero() || at.Before(horizon) {
			continue
		}
		c, _ := cfg.Category(j.Category)
		k := kindOf(c)
		in.st.Counted[j.ID] = dayKey(at)
		in.dirty = true
		if k == "" {
			continue
		}
		s.insDayLocked(at).Added[k] += addedCount(k, j.Files)
	}
}

// sampleWatching adds the time since the last look to each stream playing now.
func (s *Server) sampleWatching(ctx context.Context, now time.Time) {
	c, ok := s.tracearr()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sessions, err := c.Streams(ctx)
	if err != nil {
		return
	}
	anime := s.animeNames()
	in := &s.ins
	in.mu.Lock()
	defer in.mu.Unlock()
	s.insLoad()
	dt := now.Sub(in.lastSeen).Seconds()
	in.lastSeen = now
	if dt <= 0 || dt > 150 {
		dt = 60
	}
	for _, x := range sessions {
		if strings.EqualFold(x.State, "paused") || strings.EqualFold(x.State, "stopped") {
			continue
		}
		k := streamKind(x, anime)
		d := s.insDayLocked(now)
		d.Watch[k] += dt
		if x.IsTranscode {
			d.Transcode += dt
		} else {
			d.Direct += dt
		}
		in.dirty = true
	}
}

func streamKind(x tracearr.Session, anime map[string]bool) string {
	switch strings.ToLower(x.MediaType) {
	case "movie":
		return "movie"
	case "track", "music", "album", "artist":
		return "music"
	}
	for _, g := range x.Genres {
		if strings.EqualFold(g, "anime") {
			return "anime"
		}
	}
	if anime[strings.ToLower(x.ShowTitle)] {
		return "anime"
	}
	return "tv"
}

// animeNames is the set of followed-anime folder names, lower case.
func (s *Server) animeNames() map[string]bool {
	out := map[string]bool{}
	if s.Shows == nil {
		return out
	}
	list, err := s.Shows.List()
	if err != nil {
		return out
	}
	for _, sh := range list {
		out[strings.ToLower(sh.Name)] = true
	}
	return out
}

func (s *Server) insSave() {
	in := &s.ins
	in.mu.Lock()
	if !in.dirty || s.Blobs == nil {
		in.mu.Unlock()
		return
	}
	cut := dayKey(time.Now().AddDate(0, 0, -keepDays))
	for k := range in.st.Days {
		if k < cut {
			delete(in.st.Days, k)
		}
	}
	for id, k := range in.st.Counted {
		if k < cut {
			delete(in.st.Counted, id)
		}
	}
	b, _ := json.Marshal(in.st)
	in.dirty = false
	in.mu.Unlock()
	if err := s.Blobs.Save(insightsBlob, b); err != nil {
		s.Log.Printf("insights: %v", err)
	}
}

// RunInsights keeps the panel's numbers: streams every minute, imports every
// minute, the library and upgrades every half hour.
func (s *Server) RunInsights(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	s.refreshSlow(ctx)
	n := 0
	for {
		now := time.Now()
		s.countImports(now)
		s.sampleWatching(ctx, now)
		if n%5 == 0 {
			s.insSave()
		}
		if n > 0 && n%30 == 0 {
			s.refreshSlow(ctx)
		}
		n++
		select {
		case <-ctx.Done():
			s.insSave()
			return
		case <-tick.C:
		}
	}
}

// arrOf is the first category of an arr kind with an address and key.
func (s *Server) arrOf(kind string) (config.Category, *arr.Client, bool) {
	for _, c := range s.Cfg.Get().Categories {
		if c.Kind == kind && c.ArrURL != "" && c.ArrAPIKey != "" && kindOf(c) != "anime" {
			return c, arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey)), true
		}
	}
	return config.Category{}, nil, false
}

type arrSeries struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	SeriesType string `json:"seriesType"`
	Statistics struct {
		EpisodeFileCount int `json:"episodeFileCount"`
	} `json:"statistics"`
	Seasons []struct {
		SeasonNumber int `json:"seasonNumber"`
		Statistics   struct {
			EpisodeFileCount int `json:"episodeFileCount"`
		} `json:"statistics"`
	} `json:"seasons"`
}

type arrMovie struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Year    int    `json:"year"`
	TMDB    int64  `json:"tmdbId"`
	HasFile bool   `json:"hasFile"`
}

func (s *Server) refreshSlow(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var out insSlow
	out.Library = map[string]libCounts{}
	out.Upgrades.Share = -1
	since := time.Now().AddDate(0, 0, -insightDays)
	watched := s.watchedTitles(ctx)
	var pool []pick
	var withFiles, belowCutoff int
	cutoffKnown := true

	if c, a, ok := s.arrOf("radarr"); ok {
		var movies []arrMovie
		if err := a.Get(ctx, "/movie", &movies); err == nil {
			lc := libCounts{}
			for _, m := range movies {
				if !m.HasFile {
					continue
				}
				lc.Movies++
				pool = append(pool, pick{Title: m.Title, Year: m.Year, Kind: "movie",
					Poster: signedCover(c.Name, fmt.Sprintf("/MediaCover/%d/poster.jpg", m.ID)), Unwatched: watched.ok && !watched.movies[m.TMDB]})
			}
			out.Library["movie"] = lc
			withFiles += lc.Movies
		}
		out.Upgrades.Movies = countUpgrades(ctx, a, "movieFileDeleted", "6", since)
		if n, err := a.Total(ctx, "/wanted/cutoff"); err == nil {
			belowCutoff += n
		} else {
			cutoffKnown = false
		}
	}
	if c, a, ok := s.arrOf("sonarr"); ok {
		var series []arrSeries
		if err := a.Get(ctx, "/series", &series); err == nil {
			lc := libCounts{}
			for _, x := range series {
				if strings.EqualFold(x.SeriesType, "anime") || x.Statistics.EpisodeFileCount == 0 {
					continue
				}
				lc.Shows++
				lc.Episodes += x.Statistics.EpisodeFileCount
				for _, se := range x.Seasons {
					if se.SeasonNumber > 0 && se.Statistics.EpisodeFileCount > 0 {
						lc.Seasons++
					}
				}
				pool = append(pool, pick{Title: x.Title, Year: x.Year, Kind: "tv",
					Poster: signedCover(c.Name, fmt.Sprintf("/MediaCover/%d/poster.jpg", x.ID)), Unwatched: watched.ok && !watched.shows[strings.ToLower(x.Title)]})
			}
			out.Library["tv"] = lc
			withFiles += lc.Episodes
		}
		out.Upgrades.Episodes = countUpgrades(ctx, a, "episodeFileDeleted", "5", since)
		if n, err := a.Total(ctx, "/wanted/cutoff"); err == nil {
			belowCutoff += n
		} else {
			cutoffKnown = false
		}
	}
	if _, a, ok := s.arrOf("lidarr"); ok {
		var artists []struct {
			Statistics struct {
				TrackFileCount int `json:"trackFileCount"`
			} `json:"statistics"`
		}
		var albums []struct {
			Statistics struct {
				TrackFileCount int `json:"trackFileCount"`
			} `json:"statistics"`
		}
		if err := a.Get(ctx, "/artist", &artists); err == nil {
			lc := libCounts{}
			for _, x := range artists {
				if x.Statistics.TrackFileCount > 0 {
					lc.Artists++
					lc.Tracks += x.Statistics.TrackFileCount
				}
			}
			if err := a.Get(ctx, "/album", &albums); err == nil {
				for _, x := range albums {
					if x.Statistics.TrackFileCount > 0 {
						lc.Albums++
					}
				}
			}
			out.Library["music"] = lc
		}
	}
	if s.Shows != nil {
		if list, err := s.Shows.List(); err == nil {
			lc := libCounts{}
			for _, sh := range list {
				if sh.Summary.Files == 0 {
					continue
				}
				lc.Shows++
				lc.Episodes += sh.Summary.Files
				lc.Seasons += max(1, len(sh.Summary.Seasons))
			}
			if lc.Shows > 0 {
				out.Library["anime"] = lc
			}
		}
	}
	// The media server's own counts, when it is connected, take the place of
	// the arrs' and the anime folders': it holds everything, not only what the
	// arrs manage.
	if lib, byName, ok := s.embyCounts(ctx); ok {
		out.Library, out.ByName = lib, byName
	}
	if cutoffKnown && withFiles > 0 {
		out.Upgrades.Share = 1 - float64(min(belowCutoff, withFiles))/float64(withFiles)
	}
	// Tonight's pick: unwatched titles first, a handful to shuffle through.
	rand.Shuffle(len(pool), func(i, k int) { pool[i], pool[k] = pool[k], pool[i] })
	sort.SliceStable(pool, func(i, k int) bool { return pool[i].Unwatched && !pool[k].Unwatched })
	if len(pool) > 20 {
		pool = pool[:20]
	}
	out.Picks = pool
	if out.Picks == nil {
		out.Picks = []pick{}
	}
	out.At = time.Now()
	s.ins.mu.Lock()
	s.ins.slow = out
	s.ins.mu.Unlock()
}

// countUpgrades counts the files an arr replaced with a better copy since a
// time: its "file deleted" history entries with the reason Upgrade.
func countUpgrades(ctx context.Context, a *arr.Client, event, code string, since time.Time) int {
	var recs []struct {
		EventType string `json:"eventType"`
		Data      struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	q := url.Values{"date": {since.UTC().Format(time.RFC3339)}, "eventType": {code}}
	if err := a.Get(ctx, "/history/since?"+q.Encode(), &recs); err != nil {
		return 0
	}
	n := 0
	for _, r := range recs {
		if strings.EqualFold(r.EventType, event) && strings.EqualFold(r.Data.Reason, "Upgrade") {
			n++
		}
	}
	return n
}

// watchedTitles is what Tracearr has seen played in the last year, kept six hours.
func (s *Server) watchedTitles(ctx context.Context) watchedSet {
	s.ins.mu.Lock()
	w := s.ins.watched
	s.ins.mu.Unlock()
	if w.ok && time.Since(w.at) < 6*time.Hour {
		return w
	}
	c, ok := s.tracearr()
	if !ok {
		return watchedSet{}
	}
	since := time.Now().AddDate(-1, 0, 0)
	w = watchedSet{movies: map[int64]bool{}, shows: map[string]bool{}, at: time.Now()}
	m, err1 := c.History(ctx, "movie", since)
	e, err2 := c.History(ctx, "episode", since)
	for _, p := range m {
		w.movies[p.TMDB] = true
	}
	for _, p := range e {
		w.shows[strings.ToLower(p.ShowTitle)] = true
	}
	w.ok = err1 == nil && err2 == nil
	s.ins.mu.Lock()
	s.ins.watched = w
	s.ins.mu.Unlock()
	return w
}

type insDayOut struct {
	Date  string             `json:"date"`
	Added map[string]int     `json:"added"`
	Watch map[string]float64 `json:"watchHours"`
}

// v1Insights is the public storage panel's data.
func (s *Server) v1Insights(w http.ResponseWriter, r *http.Request) {
	s.countImports(time.Now())
	in := &s.ins
	in.mu.Lock()
	s.insLoad()
	days := make([]insDayOut, 0, insightDays)
	var direct, trans float64
	now := time.Now()
	for i := insightDays - 1; i >= 0; i-- {
		t := now.AddDate(0, 0, -i)
		o := insDayOut{Date: dayKey(t), Added: map[string]int{}, Watch: map[string]float64{}}
		if d := in.st.Days[o.Date]; d != nil {
			for k, v := range d.Added {
				o.Added[k] = v
			}
			for k, v := range d.Watch {
				o.Watch[k] = v / 3600
			}
			direct += d.Direct
			trans += d.Transcode
		}
		days = append(days, o)
	}
	slow := in.slow
	in.mu.Unlock()
	play := map[string]float64{"direct": 0, "transcode": 0}
	if t := direct + trans; t > 0 {
		play["direct"], play["transcode"] = direct/t, trans/t
	}
	out := map[string]any{
		"days": days, "play": play,
		"library": slow.Library, "libraries": slow.ByName, "upgrades": slow.Upgrades, "picks": slow.Picks,
		"watchKnown": s.Cfg.Get().Playback.TracearrURL != "",
	}
	if s.Engine != nil {
		sv := s.Engine.TdarrSavings()
		out["tdarr"] = map[string]any{"files": sv.Files, "before": sv.Before, "after": sv.After}
	}
	if out["library"] == nil {
		out["library"] = map[string]libCounts{}
	}
	if out["libraries"] == nil {
		out["libraries"] = map[string]libCounts{}
	}
	if out["picks"] == nil {
		out["picks"] = []pick{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

// embyCounts reads each media-server library's counts: by kind (summed) and by
// library name.
func (s *Server) embyCounts(ctx context.Context) (map[string]libCounts, map[string]libCounts, bool) {
	set := s.Cfg.Get()
	if set.Playback.EmbyURL == "" || set.Playback.EmbyKey == "" {
		return nil, nil, false
	}
	ms := mediaserver.Server{Kind: set.Access.Provider, URL: set.Playback.EmbyURL, Key: string(set.Playback.EmbyKey)}
	libs, err := ms.Libraries(ctx)
	if err != nil {
		s.Log.Printf("insights: media server libraries: %v", err)
		return nil, nil, false
	}
	byKind, byName := map[string]libCounts{}, map[string]libCounts{}
	n := func(types string, id string) int {
		v, err := ms.Count(ctx, id, types)
		if err != nil {
			return 0
		}
		return v
	}
	for _, l := range libs {
		k := embyKind(l)
		var c libCounts
		switch k {
		case "movie":
			c.Movies = n("Movie", l.ID)
		case "tv", "anime":
			c.Shows, c.Seasons, c.Episodes = n("Series", l.ID), n("Season", l.ID), n("Episode", l.ID)
		case "music":
			c.Artists, _ = ms.AlbumArtists(ctx, l.ID)
			c.Albums, c.Tracks = n("MusicAlbum", l.ID), n("Audio", l.ID)
		case "book":
			c.Ebooks, c.Audio = n("Book", l.ID), n("AudioBook", l.ID)
		default:
			continue
		}
		byName[strings.ToLower(l.Name)] = c
		t := byKind[k]
		t.Movies += c.Movies
		t.Shows += c.Shows
		t.Seasons += c.Seasons
		t.Episodes += c.Episodes
		t.Artists += c.Artists
		t.Albums += c.Albums
		t.Tracks += c.Tracks
		t.Ebooks += c.Ebooks
		t.Audio += c.Audio
		byKind[k] = t
	}
	return byKind, byName, len(byName) > 0
}

// embyKind is a media-server library's kind: anime by its name, otherwise by
// its collection type.
func embyKind(l mediaserver.Library) string {
	n := strings.ToLower(l.Name)
	if strings.Contains(n, "anime") {
		return "anime"
	}
	switch strings.ToLower(l.Kind) {
	case "movies":
		return "movie"
	case "tvshows":
		return "tv"
	case "music":
		return "music"
	case "books", "audiobooks":
		return "book"
	}
	return ""
}
