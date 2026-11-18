package web

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/parser"
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
	Season   int `json:"season"`
	Total    int `json:"total"`
	Imported int `json:"imported"`
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
	shows := map[string]*onwayShow{}
	var others []onwayItem
	for _, j := range s.Store.List() {
		if j.State == job.Cleaned || j.Source == "reencode" {
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
		in := parser.Parse(j.Name)
		title := in.Title
		if title == "" {
			title = j.Name
		}
		it := onwayItem{Kind: kind, Stage: st[0], State: state, Progress: job.Overall(j), Since: j.Updated}
		if t, err := s.jobTargetUpgrade(j.ID); err == nil {
			it.Upgrade = t
		}
		if kind == "movie" || kind == "music" || !in.IsTV() {
			it.Title = title
			if in.Year > 0 {
				it.Sub = fmt.Sprint(in.Year)
			}
			if kind == "music" {
				it.Sub = ""
			}
			others = append(others, it)
			continue
		}
		switch {
		case in.FullSeason && len(in.Seasons) > 0:
			it.Code = fmt.Sprintf("Season %d", in.Seasons[0])
		case len(in.Episodes) > 0 && len(in.Seasons) > 0:
			it.Code = fmt.Sprintf("S%02dE%02d", in.Seasons[0], in.Episodes[0])
			if len(in.Episodes) > 1 {
				it.Code += fmt.Sprintf("-E%02d", in.Episodes[len(in.Episodes)-1])
			}
		case len(in.Absolute) > 0:
			it.Code = fmt.Sprint(in.Absolute[0])
		case in.Daily:
			it.Code = in.AirDate
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
	if s.Requests != nil && s.nativeMode() {
		if list, err := s.Requests.List(""); err == nil {
			for _, rq := range list {
				if rq.Status != "pending" && rq.Status != "approved" {
					continue
				}
				if rq.Status == "approved" && time.Since(rq.Decided) > 2*time.Hour {
					continue // the library takes over once it is searching
				}
				it := onwayItem{Kind: rq.Kind, Stage: "requested", State: "waiting", Since: rq.Created}
				if rq.Status == "approved" {
					it.State = "working"
				}
				if rq.Kind == "movie" {
					it.Title, it.Sub = rq.Title, fmt.Sprint(rq.Year)
					others = append(others, it)
					continue
				}
				key := library.Normalize(rq.Title)
				sh := shows[key]
				if sh == nil {
					sh = &onwayShow{Title: rq.Title, Kind: rq.Kind}
					shows[key] = sh
				}
				it.Code = "Requested"
				sh.Items = append(sh.Items, it)
			}
		}
	}
	// Season bars: native TV first, then what the arrs report.
	bars := map[string][]onwaySeason{}
	if s.TV != nil {
		if list, err := s.TV.List(""); err == nil {
			for _, x := range list {
				if shows[library.Normalize(x.Title)] == nil {
					continue
				}
				if _, eps, err := s.TV.Get(x.ID); err == nil {
					by := map[int]*onwaySeason{}
					var order []int
					for _, e := range eps {
						if e.Season == 0 || e.Status == "unaired" {
							continue
						}
						if by[e.Season] == nil {
							by[e.Season] = &onwaySeason{Season: e.Season}
							order = append(order, e.Season)
						}
						by[e.Season].Total++
						if e.File != "" {
							by[e.Season].Imported++
						}
					}
					sort.Ints(order)
					for _, n := range order {
						bars[library.Normalize(x.Title)] = append(bars[library.Normalize(x.Title)], *by[n])
					}
				}
			}
		}
	}
	if s.Progress != nil {
		for _, x := range s.Progress.Snapshot().Seasons {
			k := library.Normalize(x.Show)
			if shows[k] == nil || hasSeason(bars[k], x.Season) {
				continue
			}
			bars[k] = append(bars[k], onwaySeason{Season: x.Season, Total: x.Total, Imported: x.Imported})
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
func (s *Server) jobTargetUpgrade(jobID string) (bool, error) {
	if s.Movies == nil || s.TV == nil {
		return false, nil
	}
	var n int
	err := s.Movies.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM movies WHERE job_id = ? AND file_path != '') +
		(SELECT COUNT(*) FROM episodes WHERE job_id = ? AND file_path != '')`, jobID, jobID).Scan(&n)
	return n > 0, err
}
