package usenet

import (
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Per-provider numbers for the report card: kept by day (40 days) in the
// state blob, so the last 30 days survive restarts.

const provStatsBlob = "usenet-provider-stats.json"

// ProvDay is one provider's day.
type ProvDay struct {
	Articles int64   `json:"articles"`          // fetched from it
	Bytes    int64   `json:"bytes"`             // fetched from it
	Missing  int64   `json:"missing"`           // it said it does not have
	Rescued  int64   `json:"rescued"`           // fetched from it after another provider lacked them
	Errors   int64   `json:"errors"`            // broken connections and refusals
	Seconds  float64 `json:"seconds"`           // time spent fetching from it
	Checked  int64   `json:"checked,omitempty"` // dead-release STATs it answered
}

type provStats struct {
	mu     sync.Mutex
	days   map[string]map[string]*ProvDay // day -> provider -> numbers
	loaded bool
	dirty  bool
}

func (d *Downloader) stat(provider string, f func(*ProvDay)) {
	ps := &d.pstats
	ps.mu.Lock()
	defer ps.mu.Unlock()
	d.loadProvStats()
	k := time.Now().Format("2006-01-02")
	day := ps.days[k]
	if day == nil {
		day = map[string]*ProvDay{}
		ps.days[k] = day
	}
	p := day[provider]
	if p == nil {
		p = &ProvDay{}
		day[provider] = p
	}
	f(p)
	ps.dirty = true
}

func (d *Downloader) loadProvStats() {
	ps := &d.pstats
	if ps.loaded {
		return
	}
	ps.loaded = true
	ps.days = map[string]map[string]*ProvDay{}
	if d.State != nil {
		if b, ok, _ := d.State.Load(provStatsBlob); ok {
			_ = json.Unmarshal(b, &ps.days)
		}
	}
	if ps.days == nil {
		ps.days = map[string]map[string]*ProvDay{}
	}
}

func (d *Downloader) saveProvStats() {
	ps := &d.pstats
	ps.mu.Lock()
	if !ps.dirty || d.State == nil {
		ps.mu.Unlock()
		return
	}
	cut := time.Now().AddDate(0, 0, -40).Format("2006-01-02")
	for k := range ps.days {
		if k < cut {
			delete(ps.days, k)
		}
	}
	b, _ := json.Marshal(ps.days)
	ps.dirty = false
	ps.mu.Unlock()
	_ = d.State.Save(provStatsBlob, b)
}

// ProviderReport is one provider's last days, added up.
type ProviderReport struct {
	Name     string `json:"name"`
	Priority int    `json:"priority"` // 0 main, 1.. backups
	On       bool   `json:"on"`
	Conns    int    `json:"conns"`
	ProvDay
	Share    float64 `json:"share"`    // of all bytes fetched
	Complete float64 `json:"complete"` // articles asked of it that it had
	Speed    float64 `json:"speed"`    // bytes a second per connection while fetching
}

// ProviderReports adds up the last days for each configured provider (and
// any no longer configured that served something).
func (d *Downloader) ProviderReports(days int) []ProviderReport {
	ps := &d.pstats
	ps.mu.Lock()
	d.loadProvStats()
	cut := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
	sum := map[string]*ProvDay{}
	for k, day := range ps.days {
		if k < cut {
			continue
		}
		for name, p := range day {
			t := sum[name]
			if t == nil {
				t = &ProvDay{}
				sum[name] = t
			}
			t.Articles += p.Articles
			t.Bytes += p.Bytes
			t.Missing += p.Missing
			t.Rescued += p.Rescued
			t.Errors += p.Errors
			t.Seconds += p.Seconds
			t.Checked += p.Checked
		}
	}
	ps.mu.Unlock()
	var out []ProviderReport
	seen := map[string]bool{}
	var total int64
	for _, p := range sum {
		total += p.Bytes
	}
	add := func(name string, prio, conns int, on bool) {
		r := ProviderReport{Name: name, Priority: prio, On: on, Conns: conns}
		if p := sum[name]; p != nil {
			r.ProvDay = *p
		}
		if total > 0 {
			r.Share = float64(r.Bytes) / float64(total)
		}
		if asked := r.Articles + r.Missing; asked > 0 {
			r.Complete = float64(r.Articles) / float64(asked)
		}
		if r.Seconds > 0 {
			r.Speed = float64(r.Bytes) / r.Seconds
		}
		out = append(out, r)
		seen[name] = true
	}
	if d.Cfg != nil {
		for _, s := range d.Cfg().Servers {
			if s.Host != "" && !seen[s.Label()] {
				add(s.Label(), s.Priority, s.Conns, s.On)
			}
		}
	}
	for name := range sum {
		if !seen[name] {
			add(name, -1, 0, false)
		}
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].Bytes > out[k].Bytes })
	return out
}
