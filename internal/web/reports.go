package web

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/usenet"
)

// Reports (Downloads › Reports): how each usenet provider, indexer and
// download client has done, and why downloads failed. Every download that
// ends is noted once (blob "outcomes.json", 40 days), with where it came
// from, which client did it and why it failed.

const outcomesBlob = "outcomes.json"

type outcome struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Name     string    `json:"name,omitempty"`
	Category string    `json:"cat,omitempty"`
	Kind     string    `json:"kind,omitempty"` // usenet | torrent
	Backend  string    `json:"backend,omitempty"`
	Rescued  bool      `json:"rescued,omitempty"` // TorBox failed it and HyveMynd's own client took over
	Indexer  string    `json:"indexer,omitempty"`
	Group    string    `json:"group,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Seconds  float64   `json:"secs,omitempty"` // time downloading
	OK       bool      `json:"ok"`
	Why      string    `json:"why,omitempty"` // failure class
	Reason   string    `json:"reason,omitempty"`
}

type outcomeLog struct {
	mu     sync.Mutex
	list   []outcome
	seen   map[string]bool
	loaded bool
	dirty  bool
}

func (s *Server) outLoad() {
	o := &s.outs
	if o.loaded {
		return
	}
	o.loaded = true
	o.seen = map[string]bool{}
	if s.Blobs != nil {
		if b, ok, err := s.Blobs.Load(outcomesBlob); err == nil && ok {
			_ = json.Unmarshal(b, &o.list)
		}
	}
	for _, x := range o.list {
		o.seen[x.ID] = true
	}
}

func (s *Server) outSave() {
	o := &s.outs
	o.mu.Lock()
	if !o.dirty || s.Blobs == nil {
		o.mu.Unlock()
		return
	}
	cut := time.Now().AddDate(0, 0, -40)
	keep := o.list[:0]
	for _, x := range o.list {
		if x.At.After(cut) {
			keep = append(keep, x)
		}
	}
	if len(keep) > 30000 {
		keep = keep[len(keep)-30000:]
	}
	o.list = keep
	b, _ := json.Marshal(o.list)
	o.dirty = false
	o.mu.Unlock()
	if err := s.Blobs.Save(outcomesBlob, b); err != nil {
		s.Log.Printf("reports: %v", err)
	}
}

var groupRe = regexp.MustCompile(`-([A-Za-z0-9]+)(?:\[[^\]]*\])?$`)

// failClass sorts a failure reason into a few plain kinds.
func failClass(reason string) string {
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "gone from your providers"), strings.Contains(r, "missing or damaged"), strings.Contains(r, "too much of this release"), strings.Contains(r, "no par2"), strings.Contains(r, "articles"):
		return "Missing articles"
	case strings.Contains(r, "blocked"):
		return "Blocked file"
	case strings.Contains(r, "resolution"), strings.Contains(r, "quality"), strings.Contains(r, "fake"):
		return "Fake or wrong quality"
	case strings.Contains(r, "no progress"), strings.Contains(r, "stall"), strings.Contains(r, "inactive"), strings.Contains(r, "did not finish"), strings.Contains(r, "timed out"):
		return "Stalled"
	case strings.Contains(r, "password"), strings.Contains(r, "unpack"), strings.Contains(r, "archive"):
		return "Unpack failed"
	case strings.Contains(r, "tdarr"), strings.Contains(r, "transcode"):
		return "Tdarr failed"
	case strings.Contains(r, "cancel"):
		return "Cancelled"
	case strings.Contains(r, "torbox"), strings.Contains(r, "refused"), strings.Contains(r, "client"):
		return "Client refused"
	}
	return "Other"
}

// recordOutcomes notes each download that ended since the last look.
func (s *Server) recordOutcomes(ctx context.Context) {
	if s.Store == nil {
		return
	}
	cfg := s.Cfg.Get()
	horizon := time.Now().AddDate(0, 0, -30)
	var fresh []job.Job
	s.outs.mu.Lock()
	s.outLoad()
	for _, j := range s.Store.List() {
		if s.outs.seen[j.ID] {
			continue
		}
		if j.State != job.Imported && j.State != job.Cleaned && j.State != job.Failed {
			continue
		}
		if j.StateChanged.Before(horizon) {
			continue
		}
		fresh = append(fresh, j)
	}
	s.outs.mu.Unlock()
	clients := map[string]*arr.Client{}
	for _, j := range fresh {
		c, _ := cfg.Category(j.Category)
		x := outcome{ID: j.ID, Name: j.Name, At: j.StateChanged, Category: j.Category, Kind: j.Kind, Backend: j.Backend, Size: j.Size, OK: j.State != job.Failed}
		var started, ended time.Time
		for _, h := range j.History {
			switch h.To {
			case job.Submitted:
				started = h.At
			case job.Downloaded:
				if ended.IsZero() {
					ended = h.At
				}
			case job.Failed:
				x.Reason = h.Reason
			}
			if strings.Contains(h.Reason, "TorBox could not finish it") {
				x.Rescued = true
			}
		}
		if !started.IsZero() && ended.After(started) {
			x.Seconds = ended.Sub(started).Seconds()
		}
		if !x.OK {
			if x.Reason == "" {
				x.Reason = j.Reason
			}
			x.Why = failClass(x.Reason)
		}
		if m := groupRe.FindStringSubmatch(strings.TrimSuffix(strings.TrimSuffix(j.Name, ".nzb"), ".torrent")); m != nil {
			x.Group = m[1]
		}
		if c.IsArr() && c.ArrURL != "" && c.ArrAPIKey != "" {
			a := clients[c.Name]
			if a == nil {
				a = arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey))
				clients[c.Name] = a
			}
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if g, err := a.GrabDetail(cctx, j.DownloadID()); err == nil {
				x.Indexer = g.Indexer
				if g.Group != "" {
					x.Group = g.Group
				}
			}
			cancel()
		}
		s.outs.mu.Lock()
		if !s.outs.seen[x.ID] {
			s.outs.seen[x.ID] = true
			s.outs.list = append(s.outs.list, x)
			s.outs.dirty = true
		}
		s.outs.mu.Unlock()
	}
}

type tally struct {
	Name     string  `json:"name"`
	Total    int     `json:"total"`
	OK       int     `json:"ok"`
	Failed   int     `json:"failed"`
	Rate     float64 `json:"rate"`            // share that ended in the library
	Speed    float64 `json:"speed,omitempty"` // bytes a second, downloads that finished
	Bytes    int64   `json:"bytes,omitempty"`
	Rescued  int     `json:"rescued,omitempty"`
	TopFails []kv    `json:"topFails,omitempty"`
	bytesOK  int64
	secs     float64
	fails    map[string]int
}

type kv struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (t *tally) add(x outcome) {
	t.Total++
	t.Bytes += x.Size
	if x.OK {
		t.OK++
		if x.Seconds > 0 && x.Size > 0 {
			t.bytesOK += x.Size
			t.secs += x.Seconds
		}
	} else {
		t.Failed++
		if t.fails == nil {
			t.fails = map[string]int{}
		}
		t.fails[x.Why]++
	}
	if x.Rescued {
		t.Rescued++
	}
}

func (t *tally) done() {
	if t.Total > 0 {
		t.Rate = float64(t.OK) / float64(t.Total)
	}
	if t.secs > 0 {
		t.Speed = float64(t.bytesOK) / t.secs
	}
	t.TopFails = topOf(t.fails, 3)
}

func topOf(m map[string]int, n int) []kv {
	var out []kv
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func tallies(list []outcome, key func(outcome) string) []tally {
	m := map[string]*tally{}
	for _, x := range list {
		k := key(x)
		if k == "" {
			continue
		}
		t := m[k]
		if t == nil {
			t = &tally{Name: k}
			m[k] = t
		}
		t.add(x)
	}
	var out []tally
	for _, t := range m {
		t.done()
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return out
}

var backendName = map[string]string{"torbox": "TorBox", "usenet": "Your usenet providers", "local": "Your torrent client (VPN)", "nzbget": "NZBGet", "qbit": "qBittorrent"}

// adminReports gives the report cards for the last days (7, 30).
func (s *Server) adminReports(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days != 7 {
		days = 30
	}
	s.recordOutcomes(r.Context())
	since := time.Now().AddDate(0, 0, -days)
	s.outs.mu.Lock()
	var list []outcome
	for _, x := range s.outs.list {
		if x.At.After(since) {
			list = append(list, x)
		}
	}
	s.outs.mu.Unlock()
	var provs []usenet.ProviderReport
	if s.News != nil {
		provs = s.News.ProviderReports(days)
	}
	if provs == nil {
		provs = []usenet.ProviderReport{}
	}
	why := map[string]int{}
	failedByIndexer, failedByGroup := map[string]int{}, map[string]int{}
	var failed []outcome
	for _, x := range list {
		if x.OK {
			continue
		}
		why[x.Why]++
		if x.Indexer != "" {
			failedByIndexer[x.Indexer]++
		}
		if x.Group != "" {
			failedByGroup[x.Group]++
		}
		failed = append(failed, x)
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].At.After(failed[j].At) })
	if len(failed) > 50 {
		failed = failed[:50]
	}
	clients := tallies(list, func(x outcome) string {
		if x.Backend == "" {
			return ""
		}
		if n := backendName[x.Backend]; n != "" {
			return n
		}
		return x.Backend
	})
	idx := tallies(list, func(x outcome) string { return x.Indexer })
	total := len(list)
	ok := 0
	for _, x := range list {
		if x.OK {
			ok++
		}
	}
	rescued := 0
	for _, x := range list {
		if x.Rescued && x.OK {
			rescued++
		}
	}
	if failed == nil {
		failed = []outcome{}
	}
	writeJSON(w, map[string]any{
		"days": days, "total": total, "ok": ok, "rescued": rescued,
		"providers": provs, "clients": nonNilT(clients), "indexers": nonNilT(idx),
		"why": topOf(why, 10), "failIndexers": topOf(failedByIndexer, 5), "failGroups": topOf(failedByGroup, 5), "recentFails": failed,
	})
}

func nonNilT(t []tally) []tally {
	if t == nil {
		return []tally{}
	}
	return t
}
