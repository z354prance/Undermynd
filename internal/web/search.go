package web

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/job"
)

// One search across everything (the box in the admin's header): downloads in
// the pipeline, finished and failed downloads of the last 40 days, music and
// book requests, anime show folders and what the feeds last saw. Settings
// pages are matched in the browser.

// searchKey folds a name for matching: lower case, with the dots, dashes and
// underscores of release names read as spaces.
func searchKey(s string) string {
	s = strings.ToLower(s)
	return strings.Map(func(r rune) rune {
		switch r {
		case '.', '_', '-', '[', ']', '(', ')', '{', '}':
			return ' '
		}
		return r
	}, s)
}

func splitWords(q string) []string { return strings.Fields(searchKey(q)) }

// matchAll reports whether every word of the query is in the text.
func matchAll(words []string, text string) bool {
	k := searchKey(text)
	for _, w := range words {
		if !strings.Contains(k, w) {
			return false
		}
	}
	return true
}

type searchHit struct {
	Kind  string    `json:"kind"` // download | history | request | show | feed
	Title string    `json:"title"`
	Sub   string    `json:"sub,omitempty"`
	State string    `json:"state,omitempty"`
	Tone  string    `json:"tone,omitempty"` // ok | bad | warn | dim
	At    time.Time `json:"at,omitempty"`
	ID    string    `json:"id,omitempty"`   // job id
	Open  string    `json:"open,omitempty"` // admin page to open
	Arg   string    `json:"arg,omitempty"`  // show folder
}

const searchMax = 12

func (s *Server) adminSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	words := splitWords(q)
	out := map[string][]searchHit{"downloads": {}, "history": {}, "requests": {}, "shows": {}, "feeds": {}}
	if len(words) == 0 || len(q) < 2 {
		writeJSON(w, out)
		return
	}
	inStore := map[string]bool{}
	if s.Store != nil {
		jobs := s.Store.List()
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].StateChanged.After(jobs[j].StateChanged) })
		for _, j := range jobs {
			inStore[j.ID] = true
			if len(out["downloads"]) >= searchMax || !matchAll(words, j.Name+" "+j.Category+" "+j.ID) {
				continue
			}
			tone := "dim"
			switch j.State {
			case job.Failed:
				tone = "bad"
			case job.Imported, job.Cleaned:
				tone = "ok"
			}
			sub := j.Category
			if j.Reason != "" && j.State == job.Failed {
				sub += " · " + j.Reason
			}
			out["downloads"] = append(out["downloads"], searchHit{Kind: "download", Title: j.Name, Sub: sub, State: string(j.State), Tone: tone, At: j.StateChanged, ID: j.ID, Open: "jobs"})
		}
	}
	s.outs.mu.Lock()
	s.outLoad()
	for i := len(s.outs.list) - 1; i >= 0 && len(out["history"]) < searchMax; i-- {
		x := s.outs.list[i]
		if inStore[x.ID] || !matchAll(words, x.Name+" "+x.Category+" "+x.Indexer+" "+x.Group) {
			continue
		}
		h := searchHit{Kind: "history", Title: x.Name, Sub: x.Category, At: x.At, Open: "reports", State: "done", Tone: "ok"}
		if !x.OK {
			h.State, h.Tone = "failed", "bad"
			if x.Reason != "" {
				h.Sub += " · " + x.Reason
			}
		}
		out["history"] = append(out["history"], h)
	}
	s.outs.mu.Unlock()
	mw, md := s.musicLists()
	for _, m := range append(mw, md...) {
		if len(out["requests"]) < searchMax && matchAll(words, m.Name+" "+m.Detail) {
			out["requests"] = append(out["requests"], searchHit{Kind: "request", Title: m.Name, Sub: "Music · whole artist", State: m.Status, Tone: reqTone(m.Status), At: m.RequestedAt, Open: "requests"})
		}
	}
	bw, bd := s.bookLists()
	for _, b := range append(bw, bd...) {
		if len(out["requests"]) < searchMax && matchAll(words, b.Title+" "+strings.Join(b.Authors, " ")) {
			sub := "Book · " + b.Kind
			if len(b.Authors) > 0 {
				sub += " · " + b.Authors[0]
			}
			out["requests"] = append(out["requests"], searchHit{Kind: "request", Title: b.Title, Sub: sub, State: b.Status, Tone: reqTone(b.Status), At: b.RequestedAt, Open: "requests"})
		}
	}
	if s.Shows != nil {
		if list, err := s.Shows.List(); err == nil || len(list) > 0 {
			for _, sh := range list {
				if len(out["shows"]) >= searchMax || !matchAll(words, sh.Name+" "+sh.Folder) {
					continue
				}
				sub := sh.Category
				if sh.Followed {
					sub += " · followed"
				}
				out["shows"] = append(out["shows"], searchHit{Kind: "show", Title: sh.Name, Sub: sub, Open: "anime", Arg: sh.Folder})
			}
		}
	}
	if s.RSS != nil {
		for _, f := range s.RSS.Status() {
			for _, it := range f.Items {
				if len(out["feeds"]) >= searchMax {
					break
				}
				if matchAll(words, it.Title) {
					out["feeds"] = append(out["feeds"], searchHit{Kind: "feed", Title: it.Title, Sub: f.Name + " · " + it.Outcome, At: f.CheckedAt, Open: "rss"})
				}
			}
		}
	}
	writeJSON(w, out)
}

func reqTone(st string) string {
	switch st {
	case "approved":
		return "ok"
	case "declined", "failed":
		return "bad"
	case "pending":
		return "warn"
	}
	return "dim"
}
