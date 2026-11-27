package gpu

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// card makes /sys/class/drm/<id> pointing at a PCI device with a driver.
func card(t *testing.T, sys, id, slot, vendor, device, driver string) string {
	dev := filepath.Join(sys, "devices", "pci0000:00", slot)
	write(t, filepath.Join(dev, "vendor"), vendor)
	write(t, filepath.Join(dev, "device"), device)
	os.MkdirAll(filepath.Join(sys, "bus", "pci", "drivers", driver), 0o755)
	os.Symlink(filepath.Join(sys, "bus", "pci", "drivers", driver), filepath.Join(dev, "driver"))
	c := filepath.Join(sys, "class", "drm", id)
	os.MkdirAll(c, 0o755)
	os.Symlink(dev, filepath.Join(c, "device"))
	return c
}

// fakeCounters: each opened counter advances by a fixed step per read.
type fakeCounters struct {
	mu     sync.Mutex
	step   map[uint64]uint64 // by config
	opened map[uint64]bool
	deny   bool
}
type fakeC struct {
	f   *fakeCounters
	cfg uint64
	v   uint64
}

func (c *fakeC) read() (uint64, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.v += c.f.step[c.cfg]
	return c.v, nil
}
func (c *fakeC) close() {}
func (f *fakeCounters) open(_ uint32, cfg uint64, _ int) (counter, error) {
	if f.deny {
		return nil, syscall.EACCES
	}
	if _, ok := f.step[cfg]; !ok {
		return nil, syscall.EINVAL
	}
	f.mu.Lock()
	f.opened[cfg] = true
	f.mu.Unlock()
	return &fakeC{f: f, cfg: cfg}, nil
}

func TestEventConfigFromFormat(t *testing.T) {
	pmu := t.TempDir()
	write(t, filepath.Join(pmu, "events", "engine-active-ticks"), "event=0x02")
	write(t, filepath.Join(pmu, "format", "event"), "config:0-11")
	write(t, filepath.Join(pmu, "format", "engine_instance"), "config:12-19")
	write(t, filepath.Join(pmu, "format", "engine_class"), "config:20-27")
	write(t, filepath.Join(pmu, "format", "gt"), "config:60-63")
	cfg, err := eventConfig(pmu, "engine-active-ticks", map[string]uint64{"gt": 1, "engine_class": 2, "engine_instance": 1})
	if err != nil || cfg != 0x02|1<<12|2<<20|1<<60 {
		t.Fatalf("cfg %#x %v", cfg, err)
	}
	write(t, filepath.Join(pmu, "events", "vcs0-busy"), "config=0x200000")
	if c, err := eventConfig(pmu, "vcs0-busy", nil); err != nil || c != 0x200000 {
		t.Fatalf("i915 style %#x %v", c, err)
	}
}

func TestIntelI915(t *testing.T) {
	sys := t.TempDir()
	c := card(t, sys, "card1", "0000:03:00.0", "0x8086", "0x56b3", "i915")
	card(t, sys, "card1-DP-1", "0000:03:00.0", "0x8086", "0x56b3", "i915") // a connector: ignored
	pmu := filepath.Join(sys, "bus", "event_source", "devices", "i915_0000_03_00.0")
	write(t, filepath.Join(pmu, "type"), "21")
	write(t, filepath.Join(pmu, "cpumask"), "0")
	write(t, filepath.Join(pmu, "events", "rcs0-busy"), "config=0x0")
	write(t, filepath.Join(pmu, "events", "vcs0-busy"), "config=0x200000")
	write(t, filepath.Join(pmu, "events", "vcs1-busy"), "config=0x200100")
	write(t, filepath.Join(c, "gt_act_freq_mhz"), "2000")
	write(t, filepath.Join(c, "gt_RP0_freq_mhz"), "2400")
	hw := filepath.Join(c, "device", "hwmon", "hwmon3")
	write(t, filepath.Join(hw, "energy1_input"), "1000000")
	// vcs0 busy 95% of each 5s sample, rcs0 10%.
	fc := &fakeCounters{step: map[uint64]uint64{0x0: uint64(0.10 * 5e9), 0x200000: uint64(0.95 * 5e9), 0x200100: uint64(0.2 * 5e9)}, opened: map[uint64]bool{}}
	m := &Monitor{Sys: sys, open: fc.open, Window: 10 * time.Second}
	m.discover()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		write(t, filepath.Join(hw, "energy1_input"), itoa(1000000+i*150000000))
		m.sample(context.Background(), now.Add(time.Duration(i)*5*time.Second))
	}
	cs := m.Cards()
	if len(cs) != 1 {
		t.Fatalf("cards %+v", cs)
	}
	g := cs[0]
	if g.Video != 95 || g.Render != 10 || g.Busy != 95 || g.Freq != 2000 || g.FreqMax != 2400 || g.Power != 30 || g.Slot != "03:00.0" || !g.Overloaded {
		t.Fatalf("gpu %+v", g)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestIntelXeAndPermission(t *testing.T) {
	sys := t.TempDir()
	card(t, sys, "card0", "0000:03:00.0", "0x8086", "0x56a0", "xe")
	pmu := filepath.Join(sys, "bus", "event_source", "devices", "xe_0000_03_00.0")
	write(t, filepath.Join(pmu, "type"), "22")
	write(t, filepath.Join(pmu, "events", "engine-active-ticks"), "event=0x02")
	write(t, filepath.Join(pmu, "events", "engine-total-ticks"), "event=0x03")
	write(t, filepath.Join(pmu, "format", "event"), "config:0-11")
	write(t, filepath.Join(pmu, "format", "engine_instance"), "config:12-19")
	write(t, filepath.Join(pmu, "format", "engine_class"), "config:20-27")
	write(t, filepath.Join(pmu, "format", "gt"), "config:60-63")
	vcs := func(ev, inst uint64) uint64 { return ev | inst<<12 | 2<<20 }
	fc := &fakeCounters{step: map[uint64]uint64{vcs(2, 0): 60, vcs(3, 0): 100, vcs(2, 1): 30, vcs(3, 1): 100, 0x02: 5, 0x03: 100}, opened: map[uint64]bool{}}
	m := &Monitor{Sys: sys, open: fc.open}
	m.discover()
	now := time.Now()
	m.sample(context.Background(), now)
	m.sample(context.Background(), now.Add(5*time.Second))
	g := m.Cards()[0]
	if g.Video != 60 || g.Render != 5 || g.Driver != "xe" || g.Overloaded {
		t.Fatalf("xe %+v", g)
	}
	// Without the permission: a clear note, load unknown.
	fc.deny = true
	m2 := &Monitor{Sys: sys, open: fc.open}
	m2.discover()
	m2.sample(context.Background(), now)
	if g := m2.Cards()[0]; g.Busy != -1 || !strings.Contains(g.Note, "PERFMON") {
		t.Fatalf("denied %+v", g)
	}
}

func TestAMDAndNvidia(t *testing.T) {
	sys := t.TempDir()
	c := card(t, sys, "card2", "0000:0a:00.0", "0x1002", "0x744c", "amdgpu")
	d := filepath.Join(c, "device")
	write(t, filepath.Join(d, "gpu_busy_percent"), "42")
	write(t, filepath.Join(d, "pp_dpm_sclk"), "0: 500Mhz\n1: 1800Mhz *\n2: 2500Mhz")
	write(t, filepath.Join(d, "hwmon", "hwmon1", "power1_average"), "123000000")
	write(t, filepath.Join(d, "hwmon", "hwmon1", "temp1_input"), "61000")
	bin := filepath.Join(t.TempDir(), "nvidia-smi")
	write(t, bin, "#!/bin/sh\ncase \"$1\" in *encoder*) echo '0, NVIDIA RTX A2000, 00000000:0B:00.0, 37, 88, 12, 1800, 2100, 55.5, 64, 2048, 6144';; esac\n")
	os.Chmod(bin, 0o755)
	m := &Monitor{Sys: sys, NvidiaSMI: bin}
	m.discover()
	m.sample(context.Background(), time.Now())
	cs := m.Cards()
	if len(cs) != 2 {
		t.Fatalf("cards %+v", cs)
	}
	a, n := cs[0], cs[1]
	if a.Busy != 42 || a.Freq != 1800 || a.FreqMax != 2500 || a.Power != 123 || a.Temp != 61 || a.Video != -1 {
		t.Fatalf("amd %+v", a)
	}
	if n.Video != 88 || n.Busy != 88 || n.Render != 37 || n.Name != "NVIDIA RTX A2000" || n.Slot != "0b:00.0" || n.MemTotal != 6144*1048576 {
		t.Fatalf("nvidia %+v", n)
	}
}

func TestOverloadNeedsTheWholeWindow(t *testing.T) {
	sys := t.TempDir()
	c := card(t, sys, "card0", "0000:00:02.0", "0x1002", "0x1", "amdgpu")
	m := &Monitor{Sys: sys, Window: time.Minute}
	m.discover()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	set := func(p string) { write(t, filepath.Join(c, "device", "gpu_busy_percent"), p) }
	set("97")
	m.sample(context.Background(), now)
	m.sample(context.Background(), now.Add(30*time.Second))
	if m.Cards()[0].Overloaded {
		t.Fatal("overloaded too soon")
	}
	m.sample(context.Background(), now.Add(61*time.Second))
	if !m.Cards()[0].Overloaded {
		t.Fatal("not overloaded after the window")
	}
	set("40")
	m.sample(context.Background(), now.Add(70*time.Second))
	g := m.Cards()[0]
	if g.Overloaded || len(g.History) != 1 || g.History[0].Load != 97 {
		t.Fatalf("after %+v", g)
	}
}
