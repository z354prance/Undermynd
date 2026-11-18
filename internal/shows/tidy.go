package shows

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/library"
)

// Rename is one file to rename inside a show folder (paths inside the folder).
type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// TidyPlan is what it would take for a show folder to fit its own naming:
// files to rename, files whose names give no episode number (replace them),
// and episodes kept twice (keep one).
type TidyPlan struct {
	Folder     string        `json:"folder"`
	Name       string        `json:"name"`
	Style      library.Style `json:"style"`
	Renames    []Rename      `json:"renames"`
	Unreadable []string      `json:"unreadable"`
	Duplicates [][]string    `json:"duplicates"`
}

// Issues counts what the plan found.
func (p TidyPlan) Issues() int { return len(p.Renames) + len(p.Unreadable) + len(p.Duplicates) }

// sidecars are files that follow a video when it is renamed (same name, other extension).
var sidecars = []string{".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".nfo"}

// Tidy plans the renames that give a folder's files one name form: the
// folder's name as the title and the form most of its files already use. It
// changes nothing.
func (s *Service) Tidy(folder string) (TidyPlan, error) {
	if _, ok := s.rootOf(folder); !ok {
		return TidyPlan{}, errors.New("not a show folder in a library folder")
	}
	files, err := ScanFolder(folder)
	if err != nil {
		return TidyPlan{}, err
	}
	return planTidy(folder, files), nil
}

func planTidy(folder string, files []DiskFile) TidyPlan {
	name := filepath.Base(folder)
	st := library.FolderStyle(folder)
	p := TidyPlan{Folder: folder, Name: name, Style: st, Renames: []Rename{}, Unreadable: []string{}, Duplicates: [][]string{}}
	byEp := map[[2]int][]string{}
	taken := map[string]bool{}
	for _, f := range files {
		taken[strings.ToLower(f.Rel)] = true
	}
	for _, f := range files {
		if !f.OK {
			p.Unreadable = append(p.Unreadable, f.Rel)
			continue
		}
		k := [2]int{f.Season, f.Episode}
		byEp[k] = append(byEp[k], f.Rel)
	}
	var keys [][2]int
	for k := range byEp {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		list := byEp[k]
		if len(list) > 1 {
			p.Duplicates = append(p.Duplicates, list)
			continue // which copy to keep is the person's call
		}
		var f DiskFile
		for _, x := range files {
			if x.Rel == list[0] {
				f = x
			}
		}
		r := library.Release{Season: f.Season, Marked: f.Season != 1, Episode: f.Episode, Res: f.Res, Group: f.Group}
		want := library.FolderName(name, st, r, filepath.Ext(f.Rel))
		to := filepath.Join(filepath.Dir(f.Rel), want)
		if filepath.Dir(f.Rel) == "." {
			// Files at the top of the folder go into their season folder, as the importer places them.
			to = filepath.Join(fmt.Sprintf("Season %d", f.Season), want)
		}
		if to == f.Rel || taken[strings.ToLower(to)] {
			continue
		}
		taken[strings.ToLower(to)] = true
		p.Renames = append(p.Renames, Rename{From: f.Rel, To: to})
	}
	return p
}

// ApplyTidy renames the chosen files of a fresh plan; a rename the plan no
// longer holds, or whose target exists, is skipped, never forced. Subtitle and
// other sidecar files with the same name move with their video.
func (s *Service) ApplyTidy(folder string, chosen []Rename) (int, []string, error) {
	p, err := s.Tidy(folder)
	if err != nil {
		return 0, nil, err
	}
	n, skipped := applyPlan(s, folder, p, chosen)
	return n, skipped, nil
}

func applyPlan(s *Service, folder string, p TidyPlan, chosen []Rename) (int, []string) {
	ok := map[Rename]bool{}
	for _, r := range p.Renames {
		ok[r] = true
	}
	done, skipped := 0, []string{}
	for _, r := range chosen {
		if !ok[r] {
			skipped = append(skipped, r.From+": no longer in the plan")
			continue
		}
		from, to := filepath.Join(folder, r.From), filepath.Join(folder, r.To)
		if _, err := os.Stat(to); err == nil {
			skipped = append(skipped, r.From+": "+r.To+" already exists")
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o777); err != nil {
			skipped = append(skipped, r.From+": "+err.Error())
			continue
		}
		if err := os.Rename(from, to); err != nil {
			skipped = append(skipped, r.From+": "+err.Error())
			continue
		}
		_ = os.Chmod(filepath.Dir(to), 0o777)
		fs, ts := strings.TrimSuffix(from, filepath.Ext(from)), strings.TrimSuffix(to, filepath.Ext(to))
		for _, ext := range sidecars {
			for _, e := range []string{ext, strings.ToUpper(ext)} {
				if _, err := os.Stat(fs + e); err == nil {
					if _, err := os.Stat(ts + ext); err != nil {
						_ = os.Rename(fs+e, ts+ext)
					}
				}
			}
		}
		done++
		if s.Log != nil {
			s.Log.Printf("shows: renamed %s to %s in %s", r.From, r.To, filepath.Base(folder))
		}
	}
	return done, skipped
}

// TidySummary is one folder on the Naming page.
type TidySummary struct {
	Folder     string `json:"folder"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Renames    int    `json:"renames"`
	Unreadable int    `json:"unreadable"`
	Duplicates int    `json:"duplicates"`
	Files      int    `json:"files"`
}

// TidyList reads every show folder and lists those that do not fit their own
// naming, most work first. It reads names only.
func (s *Service) TidyList() ([]TidySummary, time.Time, error) {
	out := []TidySummary{}
	for _, root := range s.Roots() {
		ents, err := os.ReadDir(root.Dir)
		if err != nil {
			continue // the Anime page reports a missing library folder
		}
		for _, e := range ents {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(root.Dir, e.Name())
			files, err := ScanFolder(dir)
			if err != nil || len(files) == 0 {
				continue
			}
			p := planTidy(dir, files)
			if p.Issues() == 0 {
				continue
			}
			out = append(out, TidySummary{Folder: dir, Name: e.Name(), Category: root.Category, Renames: len(p.Renames), Unreadable: len(p.Unreadable), Duplicates: len(p.Duplicates), Files: len(files)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Unreadable+out[i].Duplicates, out[j].Unreadable+out[j].Duplicates
		if a != b {
			return a > b
		}
		return out[i].Renames > out[j].Renames
	})
	return out, time.Now(), nil
}
