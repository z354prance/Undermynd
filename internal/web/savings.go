package web

import (
	"net/http"
	"sort"
	"time"
)

type converted struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Category string    `json:"category"`
	Before   int64     `json:"before"`
	After    int64     `json:"after"`
	At       time.Time `json:"at"`
}

// tdarrSavings is the running total of what Tdarr saved, and the downloads
// still in the job list that it converted, newest first.
func (s *Server) tdarrSavings(w http.ResponseWriter, r *http.Request) {
	recent := []converted{}
	for _, j := range s.Store.List() {
		if j.SizeIn > 0 {
			recent = append(recent, converted{ID: j.ID, Name: j.Name, Category: j.Category, Before: j.SizeIn, After: j.SizeOut, At: j.StateChanged})
		}
	}
	sort.Slice(recent, func(i, k int) bool { return recent[i].At.After(recent[k].At) })
	if len(recent) > 200 {
		recent = recent[:200]
	}
	writeJSON(w, map[string]any{"total": s.Engine.TdarrSavings(), "recent": recent})
}
