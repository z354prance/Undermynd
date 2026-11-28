package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/attention"
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
	// What to do with it: "redownload" (an encode, sent to Upgrades) or
	// "reencode" (Tdarr converts the file itself), and why.
	Way    string `json:"way"`
	Why    string `json:"why,omitempty"`
	Source string `json:"source,omitempty"` // encode | source | ""
	Kind   string `json:"kind,omitempty"`   // movie | show
	TMDB   int64  `json:"tmdb,omitempty"`
	TVDB   int64  `json:"tvdb,omitempty"`
	Title  string `json:"title,omitempty"`
	Year   int    `json:"year,omitempty"`
	InArr  bool   `json:"inArr,omitempty"`
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
	// Better downloaded again (encodes the arrs or the media server can
	// name) and fine to re-encode.
	Redownload potGroup     `json:"redownload"`
	Reencode   potGroup     `json:"reencode"`
	Titles     []potTitle   `json:"titles"` // the re-downloads by movie or series, largest first
	Groups     []potReGroup `json:"groups"` // the re-encodes by movie or show folder, largest first
	Notes      []string     `json:"notes,omitempty"`
}

// potReGroup is a movie or show folder with files fine to re-encode.
type potReGroup struct {
	Key   string   `json:"key"`  // library folder / title folder
	Name  string   `json:"name"` // the title folder
	Lib   string   `json:"lib"`
	Files int      `json:"files"`
	Bytes int64    `json:"bytes"`
	Why   string   `json:"why"`
	Kind  string   `json:"kind,omitempty"`
	TMDB  int64    `json:"tmdb,omitempty"`
	TVDB  int64    `json:"tvdb,omitempty"`
	Title string   `json:"title,omitempty"`
	Year  int      `json:"year,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// potTitle is a movie or series with files better downloaded again.
type potTitle struct {
	Kind  string `json:"kind"`
	TMDB  int64  `json:"tmdb,omitempty"`
	TVDB  int64  `json:"tvdb,omitempty"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	InArr bool   `json:"inArr"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	Why   string `json:"why"`
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
	ix := s.buildIDIndex(context.Background(), s.Cfg.Get())
	sum.Notes = ix.notes
	titles := map[string]*potTitle{}
	for i := range older {
		f := &older[i]
		ix.classify(f)
		g := &sum.Reencode
		if f.Way == "redownload" {
			g = &sum.Redownload
			k := fmt.Sprintf("%s:%d:%d", f.Kind, f.TMDB, f.TVDB)
			t := titles[k]
			if t == nil {
				t = &potTitle{Kind: f.Kind, TMDB: f.TMDB, TVDB: f.TVDB, Title: f.Title, Year: f.Year, InArr: f.InArr, Why: f.Why}
				titles[k] = t
			}
			t.Files++
			t.Bytes += f.Size
		}
		g.Files++
		g.Bytes += f.Size
	}
	for _, t := range titles {
		sum.Titles = append(sum.Titles, *t)
	}
	sort.Slice(sum.Titles, func(i, j int) bool { return sum.Titles[i].Bytes > sum.Titles[j].Bytes })
	groups := map[string]*potReGroup{}
	whys := map[string]map[string]int{}
	for _, f := range older {
		if f.Way == "redownload" {
			continue
		}
		key := splitRel(f.Rel)
		segs := strings.Split(key, "/")
		name, lib := segs[len(segs)-1], ""
		if len(segs) == 2 {
			lib = segs[0]
		}
		g := groups[key]
		if g == nil {
			g = &potReGroup{Key: key, Name: name, Lib: lib}
			groups[key] = g
			whys[key] = map[string]int{}
		}
		g.Files++
		g.Bytes += f.Size
		g.Paths = append(g.Paths, f.Path)
		whys[key][f.Why]++
		if g.Kind == "" && f.Kind != "" {
			g.Kind, g.TMDB, g.TVDB, g.Title, g.Year = f.Kind, f.TMDB, f.TVDB, f.Title, f.Year
		}
	}
	for k, g := range groups {
		best := 0
		for w, n := range whys[k] {
			if n > best || (n == best && w < g.Why) {
				g.Why, best = w, n
			}
		}
		sum.Groups = append(sum.Groups, *g)
	}
	sort.Slice(sum.Groups, func(i, j int) bool { return sum.Groups[i].Bytes > sum.Groups[j].Bytes })
	older = nil
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
		view := *sum
		view.Largest = nil
		view.Groups = make([]potReGroup, 0, len(sum.Groups))
		queuedFiles := map[string]int{}
		for _, g := range sum.Groups {
			q := 0
			for _, p := range g.Paths {
				if j, ok := s.Store.Get(pipeline.ReencodeID(p)); ok && j.State != job.Cleaned && j.State != job.Failed {
					q++
				}
			}
			queuedFiles[g.Key] = q
			g.Paths = nil
			view.Groups = append(view.Groups, g)
		}
		out["queuedFiles"] = queuedFiles
		out["summary"] = view
		queued := []string{}
		if s.Attention != nil {
			for _, q := range s.Attention.Snapshot().Queue {
				queued = append(queued, fmt.Sprintf("%s:%d:%d", q.Kind, q.TMDB, q.TVDB))
			}
		}
		out["upgradeQueue"] = queued
	}
	at := s.Cfg.Get().Attention
	perDay := at.MoviePerDay
	if perDay <= 0 {
		perDay = 3
	}
	out["upgrades"] = map[string]any{"on": at.On && s.Attention != nil, "movies": at.On && strings.TrimSpace(at.MovieProfile) != "", "shows": at.On && strings.TrimSpace(at.TVProfile) != "", "moviePerDay": perDay}
	writeJSON(w, out)
}

// adminPotentialReencode queues every file of the chosen folders for Tdarr
// through Re-encode (files already queued are left as they are).
func (s *Server) adminPotentialReencode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys []string `json:"keys"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	s.pot.mu.Lock()
	s.potLoad()
	sum := s.pot.sum
	s.pot.mu.Unlock()
	if sum == nil {
		jsonErr(w, http.StatusConflict, "scan the library first")
		return
	}
	want := map[string]bool{}
	for _, k := range in.Keys {
		want[k] = true
	}
	queued, already, failed := 0, 0, []string{}
	for _, g := range sum.Groups {
		if !want[g.Key] {
			continue
		}
		for _, p := range g.Paths {
			if j, ok := s.Store.Get(pipeline.ReencodeID(p)); ok && j.State != job.Cleaned && j.State != job.Failed {
				already++
				continue
			}
			if queued >= 2000 {
				failed = append(failed, "more than 2000 files at once: send the rest after these")
				break
			}
			if _, err := s.Engine.SubmitReencode(p); err != nil {
				failed = append(failed, filepath.Base(p)+": "+err.Error())
				continue
			}
			queued++
		}
	}
	s.Log.Printf("tdarr potential: queued %d file(s) from %d folder(s) for re-encoding", queued, len(in.Keys))
	if len(failed) > 5 {
		failed = append(failed[:5], fmt.Sprintf("and %d more", len(failed)-5))
	}
	writeJSON(w, map[string]any{"queued": queued, "already": already, "skipped": failed})
}

// adminPotentialUpgrade sends titles to Upgrades to be downloaded again from a
// better source.
func (s *Server) adminPotentialUpgrade(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Titles []struct {
			Kind  string `json:"kind"`
			TMDB  int64  `json:"tmdb"`
			TVDB  int64  `json:"tvdb"`
			Title string `json:"title"`
			Year  int    `json:"year"`
			Why   string `json:"why"`
		} `json:"titles"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.Attention == nil {
		jsonErr(w, http.StatusServiceUnavailable, "Upgrades is not running")
		return
	}
	var reqs []attention.Request
	for _, t := range in.Titles {
		reason := "older encode: downloading it again from a better source for Tdarr"
		if t.Why != "" {
			reason += " (" + t.Why + ")"
		}
		reqs = append(reqs, attention.Request{Kind: t.Kind, TMDB: t.TMDB, TVDB: t.TVDB, Title: t.Title, Year: t.Year, Reason: reason})
	}
	added, waiting, err := s.Attention.Enqueue(reqs)
	if err != nil {
		jsonErr(w, http.StatusConflict, "Upgrades is switched off: switch it on and choose the upgrade profiles under Library › Upgrades first")
		return
	}
	s.Log.Printf("tdarr potential: %d titles sent to Upgrades to download again (%d already waiting)", added, waiting)
	writeJSON(w, map[string]int{"added": added, "waiting": waiting, "skipped": len(reqs) - added - waiting})
}

func (s *Server) adminPotentialScan(w http.ResponseWriter, r *http.Request) {
	go s.scanPotential()
	writeJSON(w, map[string]bool{"ok": true})
}

// splitRel is the movie or show folder a library file belongs to: its
// library folder and the folder under it ("TV/Suits"), or the path itself for
// a file directly in a library folder.
func splitRel(rel string) string {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	if len(segs) >= 3 {
		return segs[0] + "/" + segs[1]
	}
	return filepath.ToSlash(rel)
}
