// Package gpu watches how busy the server's graphics cards are, so the admin
// can see when the cards Tdarr encodes on are overloaded. It reads Intel
// cards (i915 and xe drivers) through the kernel's performance counters,
// AMD cards through amdgpu's sysfs files and NVIDIA cards through
// nvidia-smi. What a card does not offer is left out.
package gpu

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// GPU is one card as last read. A value below zero is unknown.
type GPU struct {
	ID       string  `json:"id"`     // card0, nvidia0
	Vendor   string  `json:"vendor"` // intel, amd, nvidia
	Name     string  `json:"name"`
	Slot     string  `json:"slot,omitempty"` // PCI address
	Driver   string  `json:"driver,omitempty"`
	Busy     float64 `json:"busy"`   // the busiest engine, %
	Video    float64 `json:"video"`  // video encode/decode, %
	Render   float64 `json:"render"` // 3D/compute, %
	Freq     float64 `json:"freq"`   // MHz
	FreqMax  float64 `json:"freqMax"`
	Power    float64 `json:"power"` // watts
	Temp     float64 `json:"temp"`  // °C
	MemUsed  float64 `json:"memUsed"`
	MemTotal float64 `json:"memTotal"` // bytes
	Source   string  `json:"source"`   // how the load is read
	Note     string  `json:"note,omitempty"`
	// Overloaded: the video engines (or the card) have been at or over the
	// limit for the whole window.
	Overloaded bool      `json:"overloaded"`
	Since      time.Time `json:"since,omitempty"` // over the limit since
	History    []Point   `json:"history"`         // one a minute, the last hour
}

// Point is one minute's average.
type Point struct {
	At    time.Time `json:"at"`
	Load  float64   `json:"load"` // the number Overloaded looks at
	Video float64   `json:"video"`
}

// Load is what counts for Tdarr: the video engines when known, else the
// card as a whole.
func (g GPU) Load() float64 {
	if g.Video >= 0 {
		return g.Video
	}
	return g.Busy
}

// reader reads one kind of card.
type reader interface {
	read(ctx context.Context, now time.Time) []GPU
	close()
}

// Monitor samples every card it finds.
type Monitor struct {
	Sys    string        // default /sys
	Every  time.Duration // default 5 seconds
	Limit  float64       // overloaded at or over this, default 90 (%)
	Window time.Duration // for this long, default 3 minutes
	Log    *log.Logger
	// NvidiaSMI is the nvidia-smi command ("" = look on the PATH).
	NvidiaSMI string
	open      opener // performance counters; nil = the kernel's

	mu       sync.RWMutex
	cards    map[string]*state
	readers  []reader
	found    bool
	prepared bool
}

// Prepare finds the cards and opens their counters now. Call it before the
// process gives up root: Intel's engine counters need CAP_PERFMON to open,
// and an open counter keeps working after the switch to PUID.
func (m *Monitor) Prepare() {
	m.discover()
	m.prepared = true
}

type state struct {
	last    GPU
	over    time.Time // first sample over the limit in this run; zero when under
	minute  time.Time
	sum     float64
	vsum    float64
	n       int
	history []Point
}

func (m *Monitor) sys() string {
	if m.Sys != "" {
		return m.Sys
	}
	return "/sys"
}

// Run samples until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	every := m.Every
	if every <= 0 {
		every = 5 * time.Second
	}
	if !m.prepared {
		m.discover()
	}
	defer func() {
		for _, r := range m.readers {
			r.close()
		}
	}()
	t := time.NewTicker(every)
	defer t.Stop()
	m.sample(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.sample(ctx, now)
		}
	}
}

func (m *Monitor) logf(f string, a ...any) {
	if m.Log != nil {
		m.Log.Printf("gpu: "+f, a...)
	}
}

// discover finds the cards and how to read each.
func (m *Monitor) discover() {
	open := m.open
	if open == nil {
		open = kernelCounters
	}
	var rs []reader
	cards, _ := filepath.Glob(filepath.Join(m.sys(), "class", "drm", "card*"))
	sort.Strings(cards)
	for _, c := range cards {
		base := filepath.Base(c)
		if strings.Contains(base, "-") {
			continue // card0-HDMI-A-1: a connector
		}
		dev := filepath.Join(c, "device")
		vendor := strings.TrimSpace(readStr(filepath.Join(dev, "vendor")))
		driver := filepath.Base(readLink(filepath.Join(dev, "driver")))
		slot := filepath.Base(readLink(dev))
		switch {
		case vendor == "0x8086" && (driver == "i915" || driver == "xe"):
			rs = append(rs, newIntel(m.sys(), base, dev, slot, driver, open, m.logf))
		case vendor == "0x1002" && driver == "amdgpu":
			rs = append(rs, &amd{id: base, dev: dev, slot: slot})
		}
	}
	if n := newNvidia(m.NvidiaSMI); n != nil {
		rs = append(rs, n)
	}
	m.mu.Lock()
	m.readers, m.found = rs, true
	m.mu.Unlock()
	if len(rs) == 0 {
		m.logf("no graphics card visible (pass /dev/dri, or the NVIDIA runtime, to this container to watch one)")
	}
}

func (m *Monitor) sample(ctx context.Context, now time.Time) {
	limit, window := m.Limit, m.Window
	if limit <= 0 {
		limit = 90
	}
	if window <= 0 {
		window = 3 * time.Minute
	}
	var got []GPU
	for _, r := range m.readers {
		got = append(got, r.read(ctx, now)...)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cards == nil {
		m.cards = map[string]*state{}
	}
	for _, g := range got {
		st := m.cards[g.ID]
		if st == nil {
			st = &state{}
			m.cards[g.ID] = st
		}
		load := g.Load()
		if load >= limit {
			if st.over.IsZero() {
				st.over = now
			}
		} else {
			st.over = time.Time{}
		}
		g.Overloaded = !st.over.IsZero() && now.Sub(st.over) >= window
		if !st.over.IsZero() {
			g.Since = st.over
		}
		// Minute averages for the history.
		min := now.Truncate(time.Minute)
		if !st.minute.Equal(min) {
			if st.n > 0 {
				st.history = append(st.history, Point{At: st.minute, Load: round1(st.sum / float64(st.n)), Video: round1(st.vsum / float64(st.n))})
				if len(st.history) > 60 {
					st.history = st.history[len(st.history)-60:]
				}
			}
			st.minute, st.sum, st.vsum, st.n = min, 0, 0, 0
		}
		if load >= 0 {
			st.sum += load
			st.vsum += max(g.Video, 0)
			st.n++
		}
		g.History = append([]Point(nil), st.history...)
		st.last = g
	}
}

// Cards is every card as last read, in order.
func (m *Monitor) Cards() []GPU {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]GPU, 0, len(m.cards))
	for _, st := range m.cards {
		out = append(out, st.last)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func readStr(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readLink(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return r
}
