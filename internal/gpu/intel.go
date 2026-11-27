package gpu

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// counter is one opened performance counter.
type counter interface {
	read() (uint64, error)
	close()
}

// opener opens a counter of a kernel PMU (type, config) on a CPU.
type opener func(pmuType uint32, config uint64, cpu int) (counter, error)

type perfFD int

func (f perfFD) read() (uint64, error) {
	var b [8]byte
	if _, err := unix.Read(int(f), b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}
func (f perfFD) close() { unix.Close(int(f)) }

func kernelCounters(pmuType uint32, config uint64, cpu int) (counter, error) {
	attr := unix.PerfEventAttr{Type: pmuType, Config: config}
	attr.Size = uint32(unsafe.Sizeof(attr))
	fd, err := unix.PerfEventOpen(&attr, -1, cpu, -1, unix.PERF_FLAG_FD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return perfFD(fd), nil
}

// engine is one engine's counters: busy time (i915, nanoseconds) or active
// and total ticks (xe).
type engine struct {
	name         string // vcs0, rcs0
	kind         string // video, render, other
	busy, total  counter
	lastB, lastT uint64
	lastAt       time.Time
	have         bool
}

type intel struct {
	id, dev, slot, driver string
	engines               []*engine
	note                  string
	energy                uint64
	energyAt              time.Time
}

// pmuFor finds the card's PMU in /sys/bus/event_source/devices.
func pmuFor(sys, driver, slot string) string {
	base := filepath.Join(sys, "bus", "event_source", "devices")
	for _, n := range []string{driver + "_" + strings.ReplaceAll(slot, ":", "_"), driver} {
		if _, err := os.Stat(filepath.Join(base, n, "type")); err == nil {
			if n == driver && slot != "0000:00:02.0" && driver == "i915" {
				// The bare "i915" PMU is the integrated card's.
				continue
			}
			return filepath.Join(base, n)
		}
	}
	return ""
}

var fieldRe = regexp.MustCompile(`^config:(\d+)(?:-(\d+))?$`)

// eventConfig builds an event's config from its events/ file and the PMU's
// format/ fields, with params (field=value) added, as perf does.
func eventConfig(pmu, event string, params map[string]uint64) (uint64, error) {
	spec := readStr(filepath.Join(pmu, "events", event))
	if spec == "" {
		return 0, fmt.Errorf("no event %s", event)
	}
	vals := map[string]uint64{}
	for _, kv := range strings.Split(spec, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			v = "1"
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 64)
		if !strings.HasPrefix(v, "0x") {
			n, err = strconv.ParseUint(v, 10, 64)
		}
		if err != nil {
			return 0, fmt.Errorf("event %s: %q", event, kv)
		}
		vals[k] = n
	}
	for k, v := range params {
		vals[k] = v
	}
	var cfg uint64
	for k, v := range vals {
		if k == "config" {
			cfg |= v
			continue
		}
		m := fieldRe.FindStringSubmatch(readStr(filepath.Join(pmu, "format", k)))
		if m == nil {
			return 0, fmt.Errorf("unknown field %s", k)
		}
		lo, _ := strconv.Atoi(m[1])
		hi := lo
		if m[2] != "" {
			hi, _ = strconv.Atoi(m[2])
		}
		width := hi - lo + 1
		if width < 64 && v>>width != 0 {
			return 0, fmt.Errorf("%s=%d does not fit", k, v)
		}
		cfg |= v << lo
	}
	return cfg, nil
}

func newIntel(sys, id, dev, slot, driver string, open opener, logf func(string, ...any)) *intel {
	g := &intel{id: id, dev: dev, slot: slot, driver: driver}
	pmu := pmuFor(sys, driver, slot)
	if pmu == "" {
		g.note = "this kernel shows no performance counters for the card"
		return g
	}
	t, err := strconv.ParseUint(readStr(filepath.Join(pmu, "type")), 10, 32)
	if err != nil {
		g.note = "unreadable PMU type"
		return g
	}
	cpu := 0
	if c := readStr(filepath.Join(pmu, "cpumask")); c != "" {
		cpu, _ = strconv.Atoi(strings.FieldsFunc(c, func(r rune) bool { return r == ',' || r == '-' })[0])
	}
	var denied error
	try := func(cfg uint64) counter {
		c, err := open(uint32(t), cfg, cpu)
		if err != nil {
			if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
				denied = err
			}
			return nil
		}
		return c
	}
	kindOf := func(name string) string {
		switch {
		case strings.HasPrefix(name, "vcs"), strings.HasPrefix(name, "vecs"):
			return "video"
		case strings.HasPrefix(name, "rcs"), strings.HasPrefix(name, "ccs"):
			return "render"
		}
		return "other"
	}
	if driver == "i915" {
		evs, _ := filepath.Glob(filepath.Join(pmu, "events", "*-busy"))
		for _, e := range evs {
			name := strings.TrimSuffix(filepath.Base(e), "-busy")
			cfg, err := eventConfig(pmu, filepath.Base(e), nil)
			if err != nil {
				continue
			}
			if c := try(cfg); c != nil {
				g.engines = append(g.engines, &engine{name: name, kind: kindOf(name), busy: c})
			}
		}
	} else { // xe: engine-active-ticks / engine-total-ticks per class and instance
		classes := []struct {
			class uint64
			name  string
		}{{0, "rcs"}, {2, "vcs"}, {3, "vecs"}, {4, "ccs"}}
		for gt := uint64(0); gt < 2; gt++ {
			for _, cl := range classes {
				for inst := uint64(0); inst < 8; inst++ {
					p := map[string]uint64{"gt": gt, "engine_class": cl.class, "engine_instance": inst}
					a, err1 := eventConfig(pmu, "engine-active-ticks", p)
					tt, err2 := eventConfig(pmu, "engine-total-ticks", p)
					if err1 != nil || err2 != nil {
						break
					}
					ca := try(a)
					if ca == nil {
						break
					}
					ct := try(tt)
					if ct == nil {
						ca.close()
						break
					}
					name := fmt.Sprintf("%s%d", cl.name, inst)
					if gt > 0 {
						name += fmt.Sprintf("-gt%d", gt)
					}
					g.engines = append(g.engines, &engine{name: name, kind: kindOf(cl.name), busy: ca, total: ct})
				}
			}
		}
	}
	switch {
	case len(g.engines) == 0 && denied != nil:
		g.note = "load needs one more permission: add --cap-add=PERFMON to the HyveMynd container under Extra Parameters"
	case len(g.engines) == 0:
		g.note = "the card's engine counters could not be opened"
	default:
		logf("%s (%s, %s): watching %d engines", id, driver, slot, len(g.engines))
	}
	return g
}

func (g *intel) read(_ context.Context, now time.Time) []GPU {
	out := GPU{ID: g.id, Vendor: "intel", Name: intelName(g.dev), Slot: shortSlot(g.slot), Driver: g.driver, Busy: -1, Video: -1, Render: -1,
		Freq: -1, FreqMax: -1, Power: -1, Temp: -1, MemUsed: -1, MemTotal: -1, Source: "engine counters", Note: g.note}
	if len(g.engines) == 0 {
		out.Source = "frequency only"
	}
	for _, e := range g.engines {
		b, err := e.busy.read()
		if err != nil {
			continue
		}
		var tot uint64
		if e.total != nil {
			if tot, err = e.total.read(); err != nil {
				continue
			}
		}
		if e.have {
			var pct float64
			if e.total != nil {
				if dt := tot - e.lastT; dt > 0 {
					pct = float64(b-e.lastB) / float64(dt) * 100
				}
			} else if el := now.Sub(e.lastAt); el > 0 {
				pct = float64(b-e.lastB) / float64(el.Nanoseconds()) * 100
			}
			pct = clamp(pct)
			out.Busy = max(out.Busy, pct)
			switch e.kind {
			case "video":
				out.Video = max(out.Video, pct)
			case "render":
				out.Render = max(out.Render, pct)
			}
		}
		e.lastB, e.lastT, e.lastAt, e.have = b, tot, now, true
	}
	card := filepath.Dir(g.dev)
	// Frequency: i915 on the card, xe per GT.
	if f := num(filepath.Join(card, "gt_act_freq_mhz")); f >= 0 {
		out.Freq = f
		out.FreqMax = num(filepath.Join(card, "gt_RP0_freq_mhz"))
		if out.FreqMax < 0 {
			out.FreqMax = num(filepath.Join(card, "gt_max_freq_mhz"))
		}
	} else if fs, _ := filepath.Glob(filepath.Join(g.dev, "tile*", "gt*", "freq0", "act_freq")); len(fs) > 0 {
		for _, f := range fs {
			out.Freq = max(out.Freq, num(f))
			out.FreqMax = max(out.FreqMax, num(filepath.Join(filepath.Dir(f), "rp0_freq")))
		}
	}
	out.Power, out.Temp = g.hwmon(now)
	return []GPU{out}
}

// hwmon gives power (from the energy counter) and the hottest sensor.
func (g *intel) hwmon(now time.Time) (power, temp float64) {
	power, temp = -1, -1
	hs, _ := filepath.Glob(filepath.Join(g.dev, "hwmon", "hwmon*"))
	for _, h := range hs {
		if e := num(filepath.Join(h, "energy1_input")); e >= 0 {
			u := uint64(e)
			if !g.energyAt.IsZero() && u >= g.energy {
				if s := now.Sub(g.energyAt).Seconds(); s > 0 {
					power = round1(float64(u-g.energy) / 1e6 / s)
				}
			}
			g.energy, g.energyAt = u, now
		} else if p := num(filepath.Join(h, "power1_input")); p >= 0 {
			power = round1(p / 1e6)
		}
		ts, _ := filepath.Glob(filepath.Join(h, "temp*_input"))
		for _, t := range ts {
			if v := num(t); v > 0 {
				temp = max(temp, round1(v/1000))
			}
		}
	}
	return power, temp
}

func (g *intel) close() {
	for _, e := range g.engines {
		e.busy.close()
		if e.total != nil {
			e.total.close()
		}
	}
}

func intelName(dev string) string {
	id := strings.ToLower(strings.TrimPrefix(readStr(filepath.Join(dev, "device")), "0x"))
	if id == "" {
		return "Intel GPU"
	}
	return "Intel GPU (8086:" + id + ")"
}

func shortSlot(s string) string { return strings.TrimPrefix(s, "0000:") }

func clamp(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return round1(p)
}

func num(p string) float64 {
	s := readStr(p)
	if s == "" {
		return -1
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return -1
	}
	return f
}
