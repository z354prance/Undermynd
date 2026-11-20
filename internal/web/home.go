package web

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/library"
)

// The public dashboard's "On the way" (design v2): shows and anime as cards
// with their episodes on the way and a season bar; movies and music as slim
// rows. Each item is at one of six stages, and the stage it is at is
// working, waiting, stalled or failed.

type onwayItem struct {
	Code     string    `json:"code,omitempty"` // S02E05, 1071, or empty for movies
	Title    string    `json:"title,omitempty"`
	Sub      string    `json:"sub,omitempty"`
	Kind     string    `json:"kind"`
	Stage    string    `json:"stage"` // requested, found, download, transfer, convert, ready
	State    string    `json:"state"` // working, waiting, stalled, failed, done
	Progress float64   `json:"progress"`
	Upgrade  bool      `json:"upgrade,omitempty"`
	Since    time.Time `json:"since"`
}

type onwaySeason struct {
	Season   int    `json:"season"`
	Total    int    `json:"total"`
	Imported int    `json:"imported"`
	Pixels   string `json:"pixels,omitempty"` // see progress.Season
}

type onwayShow struct {
	Title    string        `json:"title"`
	Kind     string        `json:"kind"`
	Bar      []onwaySeason `json:"bar,omitempty"`
	Imported int           `json:"imported"`
	Total    int           `json:"total"`
	Items    []onwayItem   `json:"items"`
}

var stageOf = map[job.State][2]string{
	job.Received: {"found", "waiting"}, job.Submitted: {"found", "working"},
	job.Downloading: {"download", "working"}, job.Pulling: {"transfer", "working"}, job.Downloaded: {"transfer", "waiting"},
	job.QueuedTranscode: {"convert", "waiting"}, job.Transcoding: {"convert", "working"}, job.Verifying: {"convert", "working"},
	job.Ready: {"ready", "waiting"}, job.Imported: {"ready", "done"},
}

func (s *Server) kindOf(category string) string {
	c, ok := s.Cfg.Get().Category(category)
	if !ok {
		return "tv"
	}
	switch c.Kind {
	case "radarr":
		return "movie"
	case "lidarr":
		return "music"
	case "sonarr":
		return "tv"
	}
	return "anime"
}

// onway builds the On the way lists from the jobs and the waiting requests.
func (s *Server) onway(w http.ResponseWriter, r *http.Request) {
	upgrade := map[string]bool{}
	if s.Progress != nil {
		for _, id := range s.Progress.Snapshot().Upgrades {
			upgrade[id] = true
		}
	}
	shows := map[string]*onwayShow{}
	var others []onwayItem
	for _, j := range s.Store.List() {
		if j.State == job.Cleaned || j.Source == "reencode" {
			continue
		}
		// Imported items show briefly as confirmation, then leave: what
		// arrived is under Recently added, and server-side cleanup that is
		// still retrying is not something the public page should wait on.
		if j.State == job.Imported && time.Since(importedAt(j)) > 5*time.Minute {
			continue
		}
		st, ok := stageOf[j.State]
		if j.State == job.Failed {
			st, ok = [2]string{"download", "failed"}, true
			for i := len(j.History) - 1; i >= 0; i-- {
				if prev, ok2 := stageOf[j.History[i].From]; ok2 {
					st[0] = prev[0]
					break
				}
			}
		}
		if !ok {
			continue
		}
		state := st[1]
		if j.Flag != "" && state != "failed" {
			state = "stalled"
		}
		kind := s.kindOf(j.Category)
		it := onwayItem{Kind: kind, Stage: st[0], State: state, Progress: job.Overall(j), Since: j.Updated, Upgrade: upgrade[j.ID]}
		rel, isEp := library.Parse(j.Name)
		if !isEp && kind != "movie" && kind != "music" {
			// A season pack ("Show S01 1080p ...", "Show Season 2") or a
			// release the simple parser cannot read still belongs to a show.
			title, season := seasonPack(j.Name)
			if season > 0 {
				it.Code = fmt.Sprintf("Season %d", season)
			} else {
				it.Code = "Release"
			}
			key := library.Normalize(title)
			sh := shows[key]
			if sh == nil {
				sh = &onwayShow{Title: title, Kind: kind}
				shows[key] = sh
			}
			sh.Items = append(sh.Items, it)
			continue
		}
		if kind == "movie" || kind == "music" || !isEp {
			it.Title, it.Sub = movieTitle(j.Name)
			if kind == "music" {
				it.Sub = ""
			}
			others = append(others, it)
			continue
		}
		title := rel.Title
		if title == "" {
			title = j.Name
		}
		switch {
		case rel.Marked:
			it.Code = fmt.Sprintf("S%02dE%02d", rel.Season, rel.Episode)
		default:
			it.Code = rel.EpisodeS
		}
		key := library.Normalize(title)
		sh := shows[key]
		if sh == nil {
			sh = &onwayShow{Title: title, Kind: kind}
			shows[key] = sh
		}
		sh.Items = append(sh.Items, it)
	}
	// Waiting requests are at the first stage.
	bars := map[string][]onwaySeason{}
	if s.Progress != nil {
		for _, x := range s.Progress.Snapshot().Seasons {
			k := library.Normalize(x.Show)
			if shows[k] == nil || hasSeason(bars[k], x.Season) {
				continue
			}
			bars[k] = append(bars[k], onwaySeason{Season: x.Season, Total: x.Total, Imported: x.Imported, Pixels: x.Pixels})
		}
	}
	out := []onwayShow{}
	for k, sh := range shows {
		sh.Bar = bars[k]
		sort.Slice(sh.Bar, func(i, j int) bool { return sh.Bar[i].Season < sh.Bar[j].Season })
		for _, b := range sh.Bar {
			sh.Total += b.Total
			sh.Imported += b.Imported
		}
		sort.SliceStable(sh.Items, func(i, j int) bool { return stageRank(sh.Items[i]) > stageRank(sh.Items[j]) })
		out = append(out, *sh)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Items[0].Since.After(out[j].Items[0].Since) })
	if others == nil {
		others = []onwayItem{}
	}
	sort.SliceStable(others, func(i, j int) bool { return stageRank(others[i]) > stageRank(others[j]) })
	writeJSON(w, map[string]any{"shows": out, "others": others})
}

func hasSeason(b []onwaySeason, n int) bool {
	for _, x := range b {
		if x.Season == n {
			return true
		}
	}
	return false
}

var stageOrder = map[string]int{"requested": 0, "found": 1, "download": 2, "transfer": 3, "convert": 4, "ready": 5}

func stageRank(it onwayItem) int { return stageOrder[it.Stage] }

// jobTargetUpgrade says whether a native job replaces a file already held.
var yearAt = regexp.MustCompile(`^(.+?)[ ._(\[]+((?:19|20)\d\d)(?:[ ._)\]]|$)`)

var packAt = regexp.MustCompile(`(?i)^(.+?)[ ._\-]+(?:S(\d{1,2})|Season[ ._]?(\d{1,2}))(?:[ ._\-\[(]|$)`)

// seasonPack reads a season pack name as the show's title and season number
// (0 when it names no season; the title is then the cleaned name).
func seasonPack(name string) (string, int) {
	clean := func(t string) string {
		return strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ").Replace(t)), " ")
	}
	if m := packAt.FindStringSubmatch(name); m != nil {
		n := m[2]
		if n == "" {
			n = m[3]
		}
		season, _ := strconv.Atoi(n)
		if t := clean(m[1]); t != "" {
			return t, season
		}
	}
	t, _ := movieTitle(name)
	return clean(t), 0
}

// movieTitle reads a movie or album release name as its title and year.
func movieTitle(name string) (string, string) {
	n := name
	if m := yearAt.FindStringSubmatch(n); m != nil {
		t := strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ").Replace(m[1])), " ")
		if t != "" {
			return t, m[2]
		}
	}
	return strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ").Replace(n)), " "), ""
}

// importedAt is when a job reached Imported (its last update if unknown).
func importedAt(j job.Job) time.Time {
	for i := len(j.History) - 1; i >= 0; i-- {
		if j.History[i].To == job.Imported {
			return j.History[i].At
		}
	}
	return j.Updated
}
