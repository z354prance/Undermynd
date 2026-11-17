// Package animelib keeps the anime library in the catalog: it imports existing
// show folders (matching each to a TMDB show, with a person confirming anything
// unsure), works out how each folder numbers its episodes, and records which
// episodes are on disk. It never moves, renames or deletes anything on disk.
package animelib

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/library"
)

// DiskFile is a video file found in a show folder.
type DiskFile struct {
	Path    string
	Rel     string // path inside the show folder
	Size    int64
	OK      bool // the name has an episode number
	Title   string
	Season  int // from the name, else the season folder, else 1
	Episode int
	Res     string
}

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".ts": true, ".webm": true, ".wmv": true, ".mov": true}

var seasonDir = regexp.MustCompile(`(?i)^(?:season|series|s)\s*0*(\d{1,3})$`)

// folderSeason reads a season folder's name: "Season 2" is 2, "Specials" is 0.
func folderSeason(name string) (int, bool) {
	if strings.EqualFold(strings.TrimSpace(name), "specials") {
		return 0, true
	}
	if m := seasonDir.FindStringSubmatch(strings.TrimSpace(name)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

// ScanFolder lists the video files in a show folder and what their names say.
func ScanFolder(dir string) ([]DiskFile, error) {
	var out []DiskFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil // an unreadable subfolder is skipped, not fatal
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !videoExt[strings.ToLower(filepath.Ext(p))] || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		f := DiskFile{Path: p}
		f.Rel, _ = filepath.Rel(dir, p)
		if info, err := d.Info(); err == nil {
			f.Size = info.Size()
		}
		if r, ok := library.Parse(d.Name()); ok {
			f.OK, f.Title, f.Episode, f.Res = true, r.Title, r.Episode, r.Res
			f.Season = 1
			if r.Marked {
				f.Season = r.Season
			} else if n, ok := folderSeason(filepath.Base(filepath.Dir(p))); ok && filepath.Dir(p) != dir {
				f.Season = n
			}
		}
		out = append(out, f)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, err
}

// MapFiles points each file at an episode, by season and number or by absolute
// number. The result maps a file's index to an episode's index; distinct is how
// many different episodes are covered.
func MapFiles(files []DiskFile, eps []catalog.Episode, numbering string) (m map[int]int, distinct int) {
	bySeason := map[[2]int]int{}
	byAbs := map[int]int{}
	absDup := map[int]bool{}
	for i, e := range eps {
		bySeason[[2]int{e.Season, e.Number}] = i
		if e.Absolute > 0 {
			if _, ok := byAbs[e.Absolute]; ok {
				absDup[e.Absolute] = true
			}
			byAbs[e.Absolute] = i
		}
	}
	m = map[int]int{}
	seen := map[int]bool{}
	for i, f := range files {
		if !f.OK {
			continue
		}
		var (
			j  int
			ok bool
		)
		if numbering == catalog.ByAbsolute {
			if f.Season == 0 { // specials keep their season numbers
				j, ok = bySeason[[2]int{0, f.Episode}]
			} else if !absDup[f.Episode] {
				j, ok = byAbs[f.Episode]
			}
		} else {
			j, ok = bySeason[[2]int{f.Season, f.Episode}]
		}
		if ok {
			m[i] = j
			if !seen[j] {
				seen[j] = true
				distinct++
			}
		}
	}
	return m, distinct
}

// Fit is how well a folder's files fit one episode list in one numbering.
type Fit struct {
	Numbering string
	Mapped    int
	Distinct  int
	m         map[int]int
}

// Fits tries both numberings against an episode list. When both place the files
// on exactly the same episodes only one is returned: absolute when the numbers
// run on past the start of a later season (as One Piece's do), else seasons.
func Fits(files []DiskFile, eps []catalog.Episode) []Fit {
	sm, sd := MapFiles(files, eps, catalog.BySeason)
	am, ad := MapFiles(files, eps, catalog.ByAbsolute)
	s := Fit{catalog.BySeason, len(sm), sd, sm}
	a := Fit{catalog.ByAbsolute, len(am), ad, am}
	if same(sm, am) {
		perSeason := map[int]int{}
		for _, e := range eps {
			perSeason[e.Season]++
		}
		for _, f := range files {
			if f.OK && f.Season > 1 && f.Episode > perSeason[f.Season] {
				return []Fit{a}
			}
		}
		return []Fit{s}
	}
	out := []Fit{s, a}
	if better(a, s) {
		out = []Fit{a, s}
	}
	return out
}

func better(a, b Fit) bool {
	if a.Mapped != b.Mapped {
		return a.Mapped > b.Mapped
	}
	return a.Distinct > b.Distinct
}

func same(a, b map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
