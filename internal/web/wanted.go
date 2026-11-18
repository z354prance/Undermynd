package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/movies"
	"github.com/z354prance/undermynd/internal/parser"
	"github.com/z354prance/undermynd/internal/tv"
)

type wantedEpisode struct {
	Series   string     `json:"series"`
	SeriesID int64      `json:"seriesId"`
	Category string     `json:"category"`
	Episode  tv.Episode `json:"episode"`
}

// wanted lists what native categories are missing (?kind=missing) or have
// below the cutoff (?kind=cutoff).
func (s *Server) wanted(w http.ResponseWriter, r *http.Request) {
	status := "missing"
	if r.URL.Query().Get("kind") == "cutoff" {
		status = "cutoff unmet"
	}
	var ms []movies.Movie
	if s.Movies != nil {
		all, err := s.Movies.List("")
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, m := range all {
			if m.Status == status && m.Monitored {
				ms = append(ms, m)
			}
		}
	}
	var eps []wantedEpisode
	if s.TV != nil {
		series, wanted, err := s.TV.Wanted()
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, x := range series {
			for _, e := range wanted[x.ID] {
				if e.Status == status {
					eps = append(eps, wantedEpisode{x.Title, x.ID, x.Category, e})
				}
			}
		}
		sort.SliceStable(eps, func(i, j int) bool { return eps[i].Episode.AirDate > eps[j].Episode.AirDate })
	}
	writeJSON(w, map[string]any{"movies": ms, "episodes": eps})
}

// calendar lists native episodes airing from a week ago to ?days= ahead.
func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	if !s.tvReady(w) {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 14
	}
	from := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	to := time.Now().AddDate(0, 0, days).Format("2006-01-02")
	list, err := s.TV.List("")
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var out []wantedEpisode
	for _, x := range list {
		_, eps, err := s.TV.Get(x.ID)
		if err != nil {
			continue
		}
		for _, e := range eps {
			if e.AirDate >= from && e.AirDate <= to {
				out = append(out, wantedEpisode{x.Title, x.ID, x.Category, e})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Episode.AirDate < out[j].Episode.AirDate })
	writeJSON(w, map[string]any{"from": from, "to": to, "episodes": out})
}

func (s *Server) historyList(w http.ResponseWriter, r *http.Request) {
	if s.History == nil {
		jsonErr(w, http.StatusConflict, "history needs the database")
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("item"), 10, 64)
	list, err := s.History.List(r.URL.Query().Get("kind"), id, 500)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"events": list})
}

// importRoots are the folders manual import may read: the data and complete
// folders and every category's library folder.
func (s *Server) importRoots() []string {
	roots := []string{s.Cfg.Env.DataDir, s.Cfg.Env.Complete()}
	for _, c := range s.Cfg.Get().Categories {
		if c.LibraryDir != "" {
			roots = append(roots, c.LibraryDir)
		}
	}
	return roots
}

func (s *Server) underRoots(p string) bool {
	p = filepath.Clean(p)
	for _, r := range s.importRoots() {
		r = filepath.Clean(r)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

type importFile struct {
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	Quality  string  `json:"quality"`
	Title    string  `json:"title"`
	Year     int     `json:"year"`
	Seasons  []int   `json:"seasons"`
	Episodes []int   `json:"episodes"`
	MovieID  int64   `json:"movieId,omitempty"`
	Movie    string  `json:"movie,omitempty"`
	SeriesID int64   `json:"seriesId,omitempty"`
	Series   string  `json:"series,omitempty"`
	EpIDs    []int64 `json:"episodeIds,omitempty"`
	EpLabel  string  `json:"episodeLabel,omitempty"`
}

// manualImportList lists the video files in a folder with what HyveMynd
// reads from each name and the movie or episodes it would go to.
func (s *Server) manualImportList(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if !filepath.IsAbs(dir) || !s.underRoots(dir) {
		jsonErr(w, http.StatusBadRequest, "give a folder under the data, complete or a library folder: "+strings.Join(s.importRoots(), ", "))
		return
	}
	var ms []movies.Movie
	if s.Movies != nil {
		ms, _ = s.Movies.List("")
	}
	var series []tv.Series
	if s.TV != nil {
		series, _ = s.TV.List("")
	}
	var out []importFile
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(out) >= 500 {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if rel, _ := filepath.Rel(dir, p); strings.Count(rel, string(filepath.Separator)) >= 3 || strings.HasPrefix(d.Name(), ".") && p != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !isVideoName(p) || strings.Contains(strings.ToLower(d.Name()), "sample") {
			return nil
		}
		info, _ := d.Info()
		in := parser.Parse(d.Name())
		if in.Title == "" || (!in.IsTV() && in.Year == 0) {
			if f := parser.Parse(filepath.Base(filepath.Dir(p))); f.Title != "" {
				q := in.Quality
				in = f
				if in.Quality.Source == parser.SourceUnknown {
					in.Quality = q
				}
			}
		}
		f := importFile{Path: p, Quality: in.Quality.Name(), Title: in.Title, Year: in.Year, Seasons: in.Seasons, Episodes: in.Episodes}
		if info != nil {
			f.Size = info.Size()
		}
		norm := library.Normalize(in.Title)
		if in.IsTV() {
			for _, x := range series {
				if library.Normalize(x.Title) == norm && s.TV != nil {
					f.SeriesID, f.Series = x.ID, x.Title
					for _, e := range s.TV.Match(x.ID, in) {
						f.EpIDs = append(f.EpIDs, e.ID)
						if f.EpLabel == "" {
							f.EpLabel = "S" + strconv.Itoa(e.Season) + "E" + strconv.Itoa(e.Number)
						} else {
							f.EpLabel += ", E" + strconv.Itoa(e.Number)
						}
					}
					break
				}
			}
		} else {
			for _, m := range ms {
				if library.Normalize(m.Title) == norm && (in.Year == 0 || m.Year == 0 || abs(in.Year-m.Year) <= 1) {
					f.MovieID, f.Movie = m.ID, m.Title+" ("+strconv.Itoa(m.Year)+")"
					break
				}
			}
		}
		out = append(out, f)
		return nil
	})
	writeJSON(w, map[string]any{"files": out, "roots": s.importRoots()})
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func isVideoName(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".wmv", ".mov", ".m2ts", ".webm", ".mpg", ".mpeg":
		return true
	}
	return false
}

// manualImport imports one file as a movie's or some episodes' file.
func (s *Server) manualImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path       string  `json:"path"`
		MovieID    int64   `json:"movieId"`
		SeriesID   int64   `json:"seriesId"`
		EpisodeIDs []int64 `json:"episodeIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !filepath.IsAbs(in.Path) || !s.underRoots(in.Path) {
		jsonErr(w, http.StatusBadRequest, "give a file under the data, complete or a library folder")
		return
	}
	var dst string
	var err error
	switch {
	case in.MovieID > 0 && s.Movies != nil:
		dst, err = s.Movies.ImportFile(in.MovieID, in.Path)
	case in.SeriesID > 0 && s.TV != nil:
		dst, err = s.TV.ImportFile(in.SeriesID, in.EpisodeIDs, in.Path)
	default:
		err = errBad("pick a movie, or a series and its episodes")
	}
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"path": dst})
}

type errBad string

func (e errBad) Error() string { return string(e) }
