package gpu

import (
	"bufio"
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// amd reads amdgpu's sysfs files (no extra permission needed).
type amd struct{ id, dev, slot string }

func (a *amd) read(_ context.Context, now time.Time) []GPU {
	g := GPU{ID: a.id, Vendor: "amd", Name: "AMD GPU (1002:" + strings.TrimPrefix(readStr(filepath.Join(a.dev, "device")), "0x") + ")", Slot: shortSlot(a.slot), Driver: "amdgpu",
		Busy: num(filepath.Join(a.dev, "gpu_busy_percent")), Video: -1, Render: -1, Freq: -1, FreqMax: -1, Power: -1, Temp: -1,
		MemUsed: num(filepath.Join(a.dev, "mem_info_vram_used")), MemTotal: num(filepath.Join(a.dev, "mem_info_vram_total")), Source: "amdgpu"}
	g.Render = g.Busy
	if g.Busy < 0 {
		g.Note = "this card does not report its load"
	}
	// pp_dpm_sclk: "0: 500Mhz\n1: 2100Mhz *"
	for _, l := range strings.Split(readStr(filepath.Join(a.dev, "pp_dpm_sclk")), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		mhz, err := strconv.ParseFloat(strings.TrimSuffix(strings.ToLower(f[1]), "mhz"), 64)
		if err != nil {
			continue
		}
		g.FreqMax = max(g.FreqMax, mhz)
		if strings.HasSuffix(l, "*") {
			g.Freq = mhz
		}
	}
	hs, _ := filepath.Glob(filepath.Join(a.dev, "hwmon", "hwmon*"))
	for _, h := range hs {
		for _, n := range []string{"power1_average", "power1_input"} {
			if p := num(filepath.Join(h, n)); p >= 0 {
				g.Power = round1(p / 1e6)
				break
			}
		}
		ts, _ := filepath.Glob(filepath.Join(h, "temp*_input"))
		for _, t := range ts {
			if v := num(t); v > 0 {
				g.Temp = max(g.Temp, round1(v/1000))
			}
		}
	}
	return []GPU{g}
}
func (a *amd) close() {}

// nvidia asks nvidia-smi, which the NVIDIA container runtime puts in the
// container.
type nvidia struct {
	bin    string
	fields []string
}

var nvFull = []string{"index", "name", "pci.bus_id", "utilization.gpu", "utilization.encoder", "utilization.decoder", "clocks.gr", "clocks.max.gr", "power.draw", "temperature.gpu", "memory.used", "memory.total"}

func newNvidia(bin string) *nvidia {
	if bin == "" {
		p, err := exec.LookPath("nvidia-smi")
		if err != nil {
			return nil
		}
		bin = p
	}
	return &nvidia{bin: bin, fields: nvFull}
}

func (n *nvidia) query(ctx context.Context, fields []string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, n.bin, "--query-gpu="+strings.Join(fields, ","), "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	var rows [][]string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			parts := strings.Split(l, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			rows = append(rows, parts)
		}
	}
	return rows, nil
}

func (n *nvidia) read(ctx context.Context, _ time.Time) []GPU {
	rows, err := n.query(ctx, n.fields)
	if err != nil && len(n.fields) == len(nvFull) {
		// Older drivers lack the encoder and decoder fields.
		n.fields = []string{"index", "name", "pci.bus_id", "utilization.gpu", "clocks.gr", "clocks.max.gr", "power.draw", "temperature.gpu", "memory.used", "memory.total"}
		rows, err = n.query(ctx, n.fields)
	}
	if err != nil {
		return nil
	}
	var out []GPU
	for _, r := range rows {
		v := map[string]string{}
		for i, f := range n.fields {
			if i < len(r) {
				v[f] = r[i]
			}
		}
		fl := func(k string) float64 {
			f, err := strconv.ParseFloat(v[k], 64)
			if err != nil {
				return -1
			}
			return f
		}
		g := GPU{ID: "nvidia" + v["index"], Vendor: "nvidia", Name: v["name"], Slot: strings.ToLower(strings.TrimPrefix(v["pci.bus_id"], "00000000:")), Driver: "nvidia",
			Busy: fl("utilization.gpu"), Video: -1, Render: fl("utilization.gpu"), Freq: fl("clocks.gr"), FreqMax: fl("clocks.max.gr"), Power: fl("power.draw"), Temp: fl("temperature.gpu"), Source: "nvidia-smi"}
		if e, d := fl("utilization.encoder"), fl("utilization.decoder"); e >= 0 || d >= 0 {
			g.Video = max(e, d)
			g.Busy = max(g.Busy, g.Video)
		}
		if m := fl("memory.used"); m >= 0 {
			g.MemUsed = m * 1048576
		} else {
			g.MemUsed = -1
		}
		if m := fl("memory.total"); m >= 0 {
			g.MemTotal = m * 1048576
		} else {
			g.MemTotal = -1
		}
		out = append(out, g)
	}
	return out
}
func (n *nvidia) close() {}
