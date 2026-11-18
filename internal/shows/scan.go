// Package shows follows shows kept as folders by the simple importer (anime):
// it reads the episodes in each folder, keeps a few settings per folder (follow,
// group order, quality, other titles), checks nyaa for new episodes of the shows
// that are followed, and shows what AniList says about each one. It never looks
// shows up to place files: a show is its folder.
package shows

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

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
	Group   string
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
		r, ok := library.Parse(d.Name())
		if !ok {
			r, ok = looseParse(d.Name())
		}
		if ok {
			f.OK, f.Title, f.Episode, f.Res, f.Group = true, r.Title, r.Episode, r.Res, r.Group
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

var (
	looseTags  = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|\{[^}]*\}`)
	looseRes   = regexp.MustCompile(`(?i)\b\d{3,4}p\b`)
	looseSxE   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,2})\s?e(\d{1,4})(?:v\d)?(?:[^0-9]|$)`)
	looseNxM   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^0-9]|$)`)
	looseEp    = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:episode|ep|e)\.?\s*(\d{1,4})(?:v\d)?(?:[^0-9]|$)`)
	looseTail  = regexp.MustCompile(`(?:^|[\s._-])(\d{1,4})(?:v\d)?\s*$`)
	looseYear  = regexp.MustCompile(`^(?:19|20)\d\d$`)
	looseSpace = regexp.MustCompile(`[\s._]+`)
)

// looseParse reads an episode number from names the release parser does not
// accept, as found in older parts of a library: "S01E01 - Title", "Episode 1",
// "Show 01", or a bare "01". The show is known from the folder, so no title is
// needed. Anything wrong is caught later when the files are matched to episodes.
func looseParse(name string) (library.Release, bool) {
	s := strings.TrimSuffix(name, filepath.Ext(name))
	r := library.Release{Season: 1}
	if m := looseRes.FindString(s); m != "" {
		r.Res = strings.ToLower(m)
	}
	s = looseTags.ReplaceAllString(s, " ")
	s = looseRes.ReplaceAllString(s, " ")
	if m := looseSxE.FindStringSubmatch(s); m != nil {
		r.Season, _ = strconv.Atoi(m[1])
		r.Episode, _ = strconv.Atoi(m[2])
		r.Marked = true
		return r, true
	}
	if m := looseNxM.FindStringSubmatch(s); m != nil {
		r.Season, _ = strconv.Atoi(m[1])
		r.Episode, _ = strconv.Atoi(m[2])
		r.Marked = true
		return r, true
	}
	if m := looseEp.FindStringSubmatch(s); m != nil {
		r.Episode, _ = strconv.Atoi(m[1])
		return r, r.Episode > 0
	}
	s = strings.TrimSpace(looseSpace.ReplaceAllString(s, " "))
	if m := looseTail.FindStringSubmatch(s); m != nil && !looseYear.MatchString(m[1]) {
		r.Episode, _ = strconv.Atoi(m[1])
		return r, r.Episode > 0
	}
	return library.Release{}, false
}

// SeasonSummary is what one season folder (or the folder itself) holds.
type SeasonSummary struct {
	Season int   `json:"season"`
	Count  int   `json:"count"`
	First  int   `json:"first"`
	Last   int   `json:"last"`
	Gaps   []int `json:"gaps"`
}

// Summary is what a show folder holds, read from its file names.
type Summary struct {
	Files   int             `json:"files"`   // video files with an episode number
	Other   int             `json:"other"`   // video files without one
	Size    int64           `json:"size"`    // bytes, all video files
	Latest  int             `json:"latest"`  // the highest episode number in the newest season
	Season  int             `json:"season"`  // the newest season with files
	Gaps    int             `json:"gaps"`    // numbers missing between the first and last of each season
	Seasons []SeasonSummary `json:"seasons"` // oldest first
}

// maxGaps caps how many missing numbers a season reports; more than this is a
// folder kept in pieces on purpose, not holes.
const maxGaps = 30

// Summarize reads a folder's files: per season, how many episodes, the first and
// last numbers and the numbers missing in between. Season 1 counts from 1.
func Summarize(files []DiskFile) Summary {
	var s Summary
	by := map[int]map[int]bool{}
	for _, f := range files {
		s.Size += f.Size
		if !f.OK {
			s.Other++
			continue
		}
		s.Files++
		if by[f.Season] == nil {
			by[f.Season] = map[int]bool{}
		}
		by[f.Season][f.Episode] = true
	}
	var seasons []int
	for n := range by {
		seasons = append(seasons, n)
	}
	sort.Ints(seasons)
	for _, n := range seasons {
		eps := by[n]
		ss := SeasonSummary{Season: n, Count: len(eps), First: 1 << 30}
		for e := range eps {
			if e < ss.First {
				ss.First = e
			}
			if e > ss.Last {
				ss.Last = e
			}
		}
		from := ss.First
		if n == 1 && from > 1 && from <= maxGaps {
			from = 1
		}
		if n > 0 {
			for e := from; e < ss.Last; e++ {
				if !eps[e] {
					ss.Gaps = append(ss.Gaps, e)
				}
			}
			if len(ss.Gaps) > maxGaps {
				ss.Gaps = nil
			}
		}
		if ss.Gaps == nil {
			ss.Gaps = []int{}
		}
		s.Gaps += len(ss.Gaps)
		s.Seasons = append(s.Seasons, ss)
		if n > 0 && n >= s.Season {
			s.Season, s.Latest = n, ss.Last
		}
	}
	if s.Seasons == nil {
		s.Seasons = []SeasonSummary{}
	}
	return s
}

// Has says whether the folder holds an episode: by season and number when the
// release names its season, else by number in any season.
func Has(files []DiskFile, season int, marked bool, episode int) bool {
	for _, f := range files {
		if f.OK && f.Episode == episode && (!marked || f.Season == season) {
			return true
		}
	}
	return false
}
