package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/media"
	"github.com/z354prance/undermynd/internal/pipeline"
)

// What Tdarr could still save: a scan of the library for videos in older
// formats (H.264, MPEG-2, VC-1 and the like), with the space they would free
// at the size Tdarr has been reaching, and a way to send the biggest to Tdarr
// through Re-encode.

const potentialBlob = "tdarr-potential.json"

type potGroup struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

type potFile struct {
	Path  string `json:"path"`
	Rel   string `json:"rel"`
	Size  int64  `json:"size"`
	Codec string `json:"codec"`
}

type potSummary struct {
	At      time.Time  `json:"at"`
	Took    string     `json:"took"`
	Older   potGroup   `json:"older"` // the formats Tdarr shrinks most
	HEVC    potGroup   `json:"hevc"`
	Unknown potGroup   `json:"unknown"`
	ByCodec []potGroup `json:"byCodec"`
	ByRoot  []potGroup `json:"byLibrary"`
	Largest []potFile  `json:"largest"`
}

type potential struct {
	mu      sync.Mutex
	running bool
	sum     *potSummary
	loaded  bool
}

func olderCodec(c string) bool {
	switch c {
	case "av1", "hevc", "unknown", "":
		return false
	}
	return true
}

func (s *Server) potLoad() {
	if s.pot.loaded {
		return
	}
	s.pot.loaded = true
	if s.Blobs != nil {
		if b, ok, err := s.Blobs.Load(potentialBlob); err == nil && ok {
			var x potSummary
			if json.Unmarshal(b, &x) == nil {
				s.pot.sum = &x
			}
		}
	}
}

// scanPotential walks the library once and keeps the summary.
func (s *Server) scanPotential() {
	s.pot.mu.Lock()
	if s.pot.running || s.Engine == nil || len(s.Engine.LibraryRoots) == 0 {
		s.pot.mu.Unlock()
		return
	}
	s.pot.running = true
	s.pot.mu.Unlock()
	defer func() {
		s.pot.mu.Lock()
		s.pot.running = false
		s.pot.mu.Unlock()
	}()
	started := time.Now()
	roots := s.Engine.LibraryRoots
	files := media.Scan(roots, time.Time{})
	sum := potSummary{At: time.Now(), Older: potGroup{Name: "older"}, HEVC: potGroup{Name: "hevc"}, Unknown: potGroup{Name: "unknown"}}
	codecs, libs := map[string]*potGroup{}, map[string]*potGroup{}
	var older []potFile
	for _, f := range files {
		c := strings.ToLower(f.Codec)
		g := codecs[c]
		if g == nil {
			g = &potGroup{Name: c}
			codecs[c] = g
		}
		g.Files++
		g.Bytes += f.Size
		rel := f.Path
		lib := ""
		for _, root := range roots {
			if r, err := filepath.Rel(root, f.Path); err == nil && !strings.HasPrefix(r, "..") {
				rel = r
				lib = strings.SplitN(filepath.ToSlash(r), "/", 2)[0]
				break
			}
		}
		switch {
		case olderCodec(c):
			sum.Older.Files++
			sum.Older.Bytes += f.Size
			older = append(older, potFile{Path: f.Path, Rel: rel, Size: f.Size, Codec: c})
			if lib != "" {
				l := libs[lib]
				if l == nil {
					l = &potGroup{Name: lib}
					libs[lib] = l
				}
				l.Files++
				l.Bytes += f.Size
			}
		case c == "hevc":
			sum.HEVC.Files++
			sum.HEVC.Bytes += f.Size
		case c == "unknown" || c == "":
			sum.Unknown.Files++
			sum.Unknown.Bytes += f.Size
		}
	}
	for _, g := range codecs {
		sum.ByCodec = append(sum.ByCodec, *g)
	}
	for _, g := range libs {
		sum.ByRoot = append(sum.ByRoot, *g)
	}
	sort.Slice(sum.ByCodec, func(i, j int) bool { return sum.ByCodec[i].Bytes > sum.ByCodec[j].Bytes })
	sort.Slice(sum.ByRoot, func(i, j int) bool { return sum.ByRoot[i].Bytes > sum.ByRoot[j].Bytes })
	sort.Slice(older, func(i, j int) bool { return older[i].Size > older[j].Size })
	if len(older) > 300 {
		older = older[:300]
	}
	sum.Largest = older
	sum.Took = time.Since(started).Round(time.Second).String()
	s.pot.mu.Lock()
	s.pot.sum = &sum
	s.pot.mu.Unlock()
	if s.Blobs != nil {
		b, _ := json.Marshal(sum)
		_ = s.Blobs.Save(potentialBlob, b)
	}
	s.Log.Printf("tdarr potential: %d videos in older formats (%.1f GB), scanned in %s", sum.Older.Files, float64(sum.Older.Bytes)/1e9, sum.Took)
}

// RunPotential scans once a day, the first time ten minutes after start.
func (s *Server) RunPotential(ctx context.Context) {
	wait := 10 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		s.pot.mu.Lock()
		s.potLoad()
		fresh := s.pot.sum != nil && time.Since(s.pot.sum.At) < 20*time.Hour
		s.pot.mu.Unlock()
		if !fresh {
			s.scanPotential()
		}
		wait = time.Hour
	}
}

func (s *Server) adminPotential(w http.ResponseWriter, r *http.Request) {
	s.pot.mu.Lock()
	s.potLoad()
	sum, running := s.pot.sum, s.pot.running
	s.pot.mu.Unlock()
	out := map[string]any{"running": running, "library": s.Engine != nil && len(s.Engine.LibraryRoots) > 0}
	// Tdarr's own result so far: what share of the size it keeps.
	keep, measured := 0.0, 0
	if s.Engine != nil {
		sv := s.Engine.TdarrSavings()
		measured = sv.Files
		if sv.Before > 0 {
			keep = float64(sv.After) / float64(sv.Before)
		}
	}
	out["keep"], out["measured"] = keep, measured
	if sum != nil {
		largest := make([]map[string]any, 0, len(sum.Largest))
		for _, f := range sum.Largest {
			q := false
			if j, ok := s.Store.Get(pipeline.ReencodeID(f.Path)); ok && j.State != job.Cleaned && j.State != job.Failed {
				q = true
			}
			largest = append(largest, map[string]any{"path": f.Path, "rel": f.Rel, "size": f.Size, "codec": f.Codec, "queued": q})
		}
		out["summary"] = sum
		out["largest"] = largest
	}
	writeJSON(w, out)
}

func (s *Server) adminPotentialScan(w http.ResponseWriter, r *http.Request) {
	go s.scanPotential()
	writeJSON(w, map[string]bool{"ok": true})
}
