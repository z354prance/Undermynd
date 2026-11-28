package web

import (
	"net/http"
	"strconv"
	"time"
)

// Trend charts on Home: the last two weeks (or month) day by day: downloads
// finished and failed, data downloaded, space Tdarr saved, and what was
// added to and watched in the library.

type trendDay struct {
	Date      string  `json:"date"`
	Done      int     `json:"done"`
	Failed    int     `json:"failed"`
	Bytes     int64   `json:"bytes"`
	Saved     int64   `json:"saved"`
	Converted int     `json:"converted"`
	Added     int     `json:"added"`
	Watched   float64 `json:"watchedHours"`
}

func (s *Server) adminTrends(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if n != 30 {
		n = 14
	}
	now := time.Now()
	days := make([]trendDay, n)
	idx := map[string]int{}
	for i := 0; i < n; i++ {
		k := dayKey(now.AddDate(0, 0, -(n - 1 - i)))
		days[i].Date = k
		idx[k] = i
	}
	s.outs.mu.Lock()
	s.outLoad()
	for _, x := range s.outs.list {
		i, ok := idx[dayKey(x.At)]
		if !ok {
			continue
		}
		if x.OK {
			days[i].Done++
			days[i].Bytes += x.Size
		} else {
			days[i].Failed++
		}
	}
	s.outs.mu.Unlock()
	if s.Engine != nil {
		for k, d := range s.Engine.TdarrSavings().Days {
			if i, ok := idx[k]; ok && d != nil {
				days[i].Converted += d.Files
				if d.Before > d.After {
					days[i].Saved += d.Before - d.After
				}
			}
		}
	}
	in := &s.ins
	in.mu.Lock()
	s.insLoad()
	for k, d := range in.st.Days {
		i, ok := idx[k]
		if !ok || d == nil {
			continue
		}
		for _, v := range d.Added {
			days[i].Added += v
		}
		for _, v := range d.Watch {
			days[i].Watched += v / 3600
		}
	}
	slow := in.slow
	in.mu.Unlock()
	// The media server's and Tracearr's own numbers, where read, replace
	// HyveMynd's counts for the days they cover.
	for k, m := range slow.Added {
		if i, ok := idx[k]; ok {
			days[i].Added = 0
			for _, v := range m {
				days[i].Added += v
			}
		}
	}
	for k, m := range slow.Watch {
		if i, ok := idx[k]; ok {
			days[i].Watched = 0
			for _, v := range m {
				days[i].Watched += v
			}
		}
	}
	writeJSON(w, map[string]any{"days": days})
}
