package web

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
)

// Leftover files (Settings › Leftover files): folders and files HyveMynd
// made for a download that no longer exists (cleaned up, failed and pruned,
// or from before a crash) and that are still taking room: staging, the
// built-in clients' folders, Tdarr's input and output folders, the complete
// folder, and the saved .nzb and .torrent files. Everything HyveMynd keeps
// there is named after its download's id (the complete folder by release
// name), so anything no current download points to is a leftover. Entries
// changed in the last six hours are never listed.

const leftoverQuiet = 6 * time.Hour

// jobIDPattern matches the names HyveMynd gives a download: a torrent's info
// hash, a SABnzbd-style id for an NZB, or a re-encode id.
var jobIDPattern = regexp.MustCompile(`^(?i:[0-9a-f]{40}|[0-9a-f]{64}|SABnzbd_nzo_[0-9a-f]{12}|reencode_[0-9a-f]{16})$`)

type leftover struct {
	Path    string    `json:"path"`
	Where   string    `json:"where"`
	Size    int64     `json:"size"`
	Changed time.Time `json:"changed"`
	Dir     bool      `json:"dir"`
}

type leftoverRoot struct {
	where, dir string
	ext        string // saved files: the id is the name without this
	complete   bool   // one level of category folders, matched by CompleteDir
}

func (s *Server) leftoverRoots() []leftoverRoot {
	env, cfg := s.Cfg.Env, s.Cfg.Get()
	w := env.Work()
	roots := []leftoverRoot{
		{where: "Staging", dir: filepath.Join(w, "staging")},
		{where: "Usenet downloads", dir: filepath.Join(w, "usenet")},
		{where: "Torrent downloads", dir: filepath.Join(w, "torrents")},
		{where: "Tdarr input", dir: cfg.Tdarr.InDir},
		{where: "Tdarr output", dir: cfg.Tdarr.OutDir},
		{where: "Complete", dir: env.Complete(), complete: true},
		{where: "Saved NZBs", dir: filepath.Join(env.ConfigDir, "nzbs"), ext: ".nzb"},
		{where: "Saved torrents", dir: filepath.Join(env.ConfigDir, "torrents"), ext: ".torrent"},
	}
	if cfg.QBit.SaveDir != "" {
		roots = append(roots, leftoverRoot{where: "Client folder", dir: cfg.QBit.SaveDir})
	}
	return roots
}

// findLeftovers lists what no download still points to.
func (s *Server) findLeftovers(now time.Time) []leftover {
	live := map[string]bool{}
	liveDirs := map[string]bool{}
	if s.Store != nil {
		for _, j := range s.Store.List() {
			if j.State == job.Cleaned {
				continue
			}
			live[j.ID] = true
			for _, p := range []string{j.StagingDir, j.CompleteDir, j.SavePath} {
				if p != "" {
					liveDirs[filepath.Clean(p)] = true
				}
			}
		}
	}
	cfg := s.Cfg.Get()
	// Only the folders of categories HyveMynd has are looked in, and not
	// plain-files categories whose downloads stay in the complete folder on
	// purpose.
	ownCat := map[string]bool{}
	for _, c := range cfg.Categories {
		if !(c.Plain() && c.LibraryDir == "") {
			ownCat[config.SafeName(c.Name)] = true
		}
	}
	var out []leftover
	seen := map[string]bool{}
	add := func(where, p string, info fs.FileInfo) {
		p = filepath.Clean(p)
		if seen[p] || now.Sub(newest(p, info)) < leftoverQuiet {
			return
		}
		seen[p] = true
		out = append(out, leftover{Path: p, Where: where, Size: treeSize(p, info), Changed: info.ModTime(), Dir: info.IsDir()})
	}
	for _, r := range s.leftoverRoots() {
		if r.dir == "" {
			continue
		}
		ents, err := os.ReadDir(r.dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			p := filepath.Join(r.dir, name)
			info, err := e.Info()
			if err != nil {
				continue
			}
			if r.complete {
				if !info.IsDir() || !ownCat[name] {
					continue
				}
				sub, err := os.ReadDir(p)
				if err != nil {
					continue
				}
				for _, se := range sub {
					if strings.HasPrefix(se.Name(), ".") {
						continue
					}
					sp := filepath.Join(p, se.Name())
					if liveDirs[filepath.Clean(sp)] || underLive(sp, liveDirs) {
						continue
					}
					if si, err := se.Info(); err == nil {
						add(r.where+" › "+name, sp, si)
					}
				}
				continue
			}
			id := name
			if r.ext != "" {
				if !strings.HasSuffix(name, r.ext) {
					continue
				}
				id = strings.TrimSuffix(name, r.ext)
			}
			if !jobIDPattern.MatchString(id) {
				continue // not something HyveMynd made
			}
			if live[id] || liveDirs[filepath.Clean(p)] || underLive(p, liveDirs) {
				continue
			}
			add(r.where, p, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

// underLive says whether p holds or sits inside a folder a download uses.
func underLive(p string, live map[string]bool) bool {
	p = filepath.Clean(p)
	for d := range live {
		if strings.HasPrefix(d, p+string(filepath.Separator)) || strings.HasPrefix(p, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// newest is the latest change inside a folder (the folder itself for a file),
// looking at most 2000 entries deep.
func newest(p string, info fs.FileInfo) time.Time {
	t := info.ModTime()
	if !info.IsDir() {
		return t
	}
	n := 0
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n++; n > 2000 {
			return fs.SkipAll
		}
		if i, err := d.Info(); err == nil && i.ModTime().After(t) {
			t = i.ModTime()
		}
		return nil
	})
	return t
}

func treeSize(p string, info fs.FileInfo) int64 {
	if !info.IsDir() {
		return info.Size()
	}
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if i, err := d.Info(); err == nil {
				n += i.Size()
			}
		}
		return nil
	})
	return n
}

func (s *Server) adminLeftovers(w http.ResponseWriter, r *http.Request) {
	list := s.findLeftovers(time.Now())
	var total int64
	for _, x := range list {
		total += x.Size
	}
	if list == nil {
		list = []leftover{}
	}
	writeJSON(w, map[string]any{"items": list, "total": total, "quietHours": int(leftoverQuiet.Hours())})
}

// adminLeftoversClear deletes the chosen leftovers, each only if it is still
// one when asked (so a download that started using it since is safe).
func (s *Server) adminLeftoversClear(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths []string `json:"paths"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	now := map[string]leftover{}
	for _, x := range s.findLeftovers(time.Now()) {
		now[x.Path] = x
	}
	var freed int64
	removed, skipped := 0, []string{}
	for _, p := range in.Paths {
		x, ok := now[filepath.Clean(p)]
		if !ok {
			skipped = append(skipped, p)
			continue
		}
		if err := os.RemoveAll(x.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.Log.Printf("leftovers: %v", err)
			skipped = append(skipped, p)
			continue
		}
		removed++
		freed += x.Size
		s.Log.Printf("leftovers: deleted %s (%d bytes)", x.Path, x.Size)
	}
	writeJSON(w, map[string]any{"removed": removed, "freed": freed, "skipped": skipped})
}
