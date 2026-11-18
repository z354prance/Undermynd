// Package movies runs native movie categories (roadmap F4): the library is
// read from Radarr once, then Undermynd keeps it: it searches its own indexers
// for monitored movies that are missing or below their profile's cutoff, grabs
// the best release into the pipeline, and imports the finished file under its
// own naming (or, in a dry run, only says where it would go).
package movies

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/history"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/parser"
)

// Indexers is the part of the indexer service movies use.
type Indexers interface {
	Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error)
	Grab(ctx context.Context, r indexers.Result, category string) (job.Job, error)
}

// Jobs reads a job's state.
type Jobs interface {
	Get(id string) (job.Job, bool)
}

// Service keeps the native movie library.
type Service struct {
	DB       *sql.DB
	Catalog  *catalog.Catalog
	Cfg      *config.Config
	Profiles *decision.Store
	Indexers Indexers
	Jobs     Jobs
	Arr      func(category string) *arr.Client
	Move     func(src, dst string) error
	IsVideo  func(path string) bool
	History  *history.Log
	Log      *log.Logger
	Now      func() time.Time
}

// Movie is a movie of a native category with its state.
type Movie struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	TMDB      int64     `json:"tmdbId"`
	IMDB      string    `json:"imdbId"`
	Folder    string    `json:"folder"`
	Monitored bool      `json:"monitored"`
	Category  string    `json:"category"`
	Profile   string    `json:"profile"`
	Runtime   int       `json:"runtime"`
	File      string    `json:"file"`
	Quality   string    `json:"quality"`
	Score     int       `json:"score"`
	Version   int       `json:"version"`
	Size      int64     `json:"size"`
	JobID     string    `json:"jobId"`
	Searched  time.Time `json:"searched"`
	Note      string    `json:"note"`
	Status    string    `json:"status"` // missing, downloading, cutoff unmet, have, unmonitored
}

const cols = `id, title, year, COALESCE(tmdb_id, 0), imdb_id, folder, monitored, category, profile, runtime,
	file_path, file_quality, file_score, file_version, file_size, job_id, searched, note`

func scan(r interface{ Scan(...any) error }) (Movie, error) {
	var m Movie
	var searched string
	err := r.Scan(&m.ID, &m.Title, &m.Year, &m.TMDB, &m.IMDB, &m.Folder, &m.Monitored, &m.Category, &m.Profile, &m.Runtime,
		&m.File, &m.Quality, &m.Score, &m.Version, &m.Size, &m.JobID, &searched, &m.Note)
	if searched != "" {
		m.Searched, _ = time.Parse(db.TimeFormat, searched)
	}
	return m, err
}

func (s *Service) status(m *Movie, profiles map[string]decision.Profile) {
	switch {
	case m.JobID != "":
		m.Status = "downloading"
	case m.File == "" && !m.Monitored:
		m.Status = "unmonitored"
	case m.File == "":
		m.Status = "missing"
	default:
		m.Status = "have"
		if p, ok := profiles[strings.ToLower(m.Profile)]; ok && m.Monitored && !p.CutoffMet(m.Quality, m.Score) {
			m.Status = "cutoff unmet"
		}
	}
}

func (s *Service) profileMap() map[string]decision.Profile {
	out := map[string]decision.Profile{}
	ps, _ := s.Profiles.Profiles()
	for _, p := range ps {
		out[strings.ToLower(p.Name)] = p
	}
	return out
}

// List returns a category's movies (every category's when empty), by title.
func (s *Service) List(category string) ([]Movie, error) {
	q := `SELECT ` + cols + ` FROM movies WHERE category != ''`
	var args []any
	if category != "" {
		q += ` AND category = ?`
		args = append(args, category)
	}
	ps := s.profileMap() // before the query: the database has one connection
	rows, err := s.DB.Query(q+` ORDER BY title COLLATE NOCASE, year`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Movie
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		s.status(&m, ps)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one movie.
func (s *Service) Get(id int64) (Movie, error) {
	m, err := scan(s.DB.QueryRow(`SELECT `+cols+` FROM movies WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Movie{}, catalog.ErrNotFound
	}
	if err == nil {
		s.status(&m, s.profileMap())
	}
	return m, err
}

// Edit changes whether a movie is monitored and its profile.
func (s *Service) Edit(id int64, monitored bool, profile string) error {
	if profile != "" {
		if _, err := s.Profiles.ProfileByName(profile); err != nil {
			return fmt.Errorf("no profile named %q", profile)
		}
	}
	_, err := s.DB.Exec(`UPDATE movies SET monitored = ?, profile = CASE WHEN ? = '' THEN profile ELSE ? END, updated = ? WHERE id = ?`,
		monitored, profile, profile, db.Stamp(s.Now()), id)
	return err
}

// Remove forgets a movie. Its files stay on disk.
func (s *Service) Remove(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM movies WHERE id = ?`, id)
	return err
}

// ImportResult says what an import from Radarr did.
type ImportResult struct {
	Added    int      `json:"added"`
	Updated  int      `json:"updated"`
	Problems []string `json:"problems,omitempty"`
}

type radarrMovie struct {
	Title            string `json:"title"`
	Year             int    `json:"year"`
	TMDB             int64  `json:"tmdbId"`
	IMDB             string `json:"imdbId"`
	Path             string `json:"path"`
	Monitored        bool   `json:"monitored"`
	Runtime          int    `json:"runtime"`
	QualityProfileID int    `json:"qualityProfileId"`
	MovieFile        *struct {
		Path         string `json:"path"`
		RelativePath string `json:"relativePath"`
		Size         int64  `json:"size"`
		Quality      struct {
			Quality struct {
				Name string `json:"name"`
			} `json:"quality"`
			Revision struct {
				Version int `json:"version"`
			} `json:"revision"`
		} `json:"quality"`
		CustomFormatScore int `json:"customFormatScore"`
	} `json:"movieFile"`
}

// Import reads a category's library from Radarr: every movie, whether it is
// monitored, its profile (by name) and the file it has. Paths are moved from
// Radarr's root folder to the category's library folder. Importing again
// updates what changed.
func (s *Service) Import(ctx context.Context, category string) (ImportResult, error) {
	var res ImportResult
	c, ok := s.Cfg.Get().Category(category)
	if !ok || c.Kind != "radarr" || c.ArrURL == "" {
		return res, fmt.Errorf("%s is not a Radarr category", category)
	}
	if c.LibraryDir == "" {
		return res, fmt.Errorf("set the movie library folder of %s first", category)
	}
	cl := s.Arr(category)
	var ms []radarrMovie
	if err := cl.Get(ctx, "/movie", &ms); err != nil {
		return res, err
	}
	names, err := cl.ProfileNames(ctx)
	if err != nil {
		return res, err
	}
	roots, _ := cl.RootFolders(ctx)
	local := func(p string) string {
		if p == "" {
			return ""
		}
		for _, r := range roots {
			rp := strings.TrimRight(r.Path, "/")
			if p == rp || strings.HasPrefix(p, rp+"/") {
				return filepath.Join(c.LibraryDir, strings.TrimPrefix(p, rp))
			}
		}
		return filepath.Join(c.LibraryDir, filepath.Base(p))
	}
	ps := s.profileMap()
	now := db.Stamp(s.Now())
	for _, m := range ms {
		if m.TMDB == 0 {
			res.Problems = append(res.Problems, m.Title+": no TMDB id")
			continue
		}
		prof := names[m.QualityProfileID]
		if _, ok := ps[strings.ToLower(prof)]; !ok {
			if _, ok := ps[strings.ToLower(prof+" ("+category+")")]; ok {
				prof += " (" + category + ")"
			}
		}
		folder := local(m.Path)
		var file, quality string
		var size int64
		var score, version int
		if f := m.MovieFile; f != nil {
			file = f.Path
			if file == "" {
				file = filepath.Join(m.Path, f.RelativePath)
			}
			file = local(file)
			quality, size, score, version = parser.Canonical(f.Quality.Quality.Name), f.Size, f.CustomFormatScore, f.Quality.Revision.Version
		}
		var id int64
		err := s.DB.QueryRow(`SELECT id FROM movies WHERE tmdb_id = ?`, m.TMDB).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			mv, err := s.Catalog.AddMovie(catalog.Movie{Title: m.Title, Year: m.Year, TMDBID: m.TMDB, IMDBID: m.IMDB, Folder: folder, Monitored: m.Monitored})
			if err != nil {
				res.Problems = append(res.Problems, m.Title+": "+err.Error())
				continue
			}
			id = mv.ID
			res.Added++
		} else if err != nil {
			return res, err
		} else {
			res.Updated++
		}
		// A download under way is kept; the file is Radarr's view until
		// Undermynd imports one itself.
		if _, err := s.DB.Exec(`UPDATE movies SET title = ?, year = ?, imdb_id = ?, folder = ?, monitored = ?, category = ?, profile = ?,
			runtime = ?, file_path = ?, file_quality = ?, file_score = ?, file_version = ?, file_size = ?, updated = ? WHERE id = ?`,
			m.Title, m.Year, m.IMDB, folder, m.Monitored, category, prof, m.Runtime, file, quality, score, version, size, now, id); err != nil {
			return res, err
		}
	}
	s.Log.Printf("movies: imported %d new and %d known movies from %s", res.Added, res.Updated, category)
	return res, nil
}

// Add puts a movie into a native category (an approved request, or by hand),
// monitored, with the profile given or the category's most used one.
func (s *Service) Add(ctx context.Context, category string, m catalog.Movie, runtime int, profile string) (Movie, error) {
	c, ok := s.Cfg.Get().Category(category)
	if !ok || !c.Native {
		return Movie{}, fmt.Errorf("%s is not a native movie category", category)
	}
	if profile == "" {
		s.DB.QueryRow(`SELECT profile FROM movies WHERE category = ? AND profile != '' GROUP BY profile ORDER BY COUNT(*) DESC LIMIT 1`, category).Scan(&profile)
	}
	if profile == "" {
		profile = "HD-1080p"
	}
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM movies WHERE tmdb_id = ?`, m.TMDBID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		m.Monitored = true
		mv, err := s.Catalog.AddMovie(m)
		if err != nil {
			return Movie{}, err
		}
		id = mv.ID
	} else if err != nil {
		return Movie{}, err
	}
	if _, err := s.DB.Exec(`UPDATE movies SET category = ?, profile = ?, runtime = ?, monitored = 1, searched = '', updated = ? WHERE id = ?`,
		category, profile, runtime, db.Stamp(s.Now()), id); err != nil {
		return Movie{}, err
	}
	return s.Get(id)
}

func (s *Service) native(category string) bool {
	c, ok := s.Cfg.Get().Category(category)
	return ok && c.Native && c.Kind == "radarr"
}

func (s *Service) perHour() int {
	n := s.Cfg.Get().Movies.SearchPerHour
	if n <= 0 {
		n = 10
	}
	return n
}

// Run searches for wanted movies every 15 minutes and follows downloads.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		s.Follow()
		if n, err := s.SearchWanted(ctx); err != nil {
			s.Log.Printf("movies: %v", err)
		} else if n > 0 {
			s.Log.Printf("movies: searched %d wanted movie(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Follow clears downloads that ended: a failed one is blocklisted so the
// next search takes another release.
func (s *Service) Follow() {
	rows, err := s.DB.Query(`SELECT id, job_id FROM movies WHERE job_id != ''`)
	if err != nil {
		return
	}
	type pair struct {
		id  int64
		job string
	}
	var ps []pair
	for rows.Next() {
		var p pair
		rows.Scan(&p.id, &p.job)
		ps = append(ps, p)
	}
	rows.Close()
	for _, p := range ps {
		j, ok := s.Jobs.Get(p.job)
		switch {
		case !ok:
			s.DB.Exec(`UPDATE movies SET job_id = '' WHERE id = ?`, p.id)
		case j.State == job.Failed:
			s.block(p.id, j.Name, "download failed: "+j.Reason)
			if m, err := s.Get(p.id); err == nil {
				s.History.Add(history.Event{Kind: "movie", ItemID: p.id, Item: fmt.Sprintf("%s (%d)", m.Title, m.Year), Event: history.Failed, Release: j.Name, Category: m.Category, Note: j.Reason})
			}
			s.DB.Exec(`UPDATE movies SET job_id = '', searched = '', note = ? WHERE id = ?`, "download failed: "+j.Reason, p.id)
		case j.State == job.Cleaned || j.State == job.Imported:
			s.DB.Exec(`UPDATE movies SET job_id = '' WHERE id = ? AND job_id = ?`, p.id, p.job)
		}
	}
}

func (s *Service) block(movieID int64, title, reason string) {
	if title == "" {
		return
	}
	s.DB.Exec(`INSERT INTO blocklist (movie_id, title, norm, reason, at) VALUES (?, ?, ?, ?, ?)`,
		movieID, title, library.Normalize(title), reason, db.Stamp(s.Now()))
}

func (s *Service) blocked(movieID int64) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.Query(`SELECT norm FROM blocklist WHERE movie_id = ?`, movieID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out[n] = true
	}
	return out
}

// SearchWanted searches for monitored movies of native categories that are
// missing (again after 12 hours) or below the cutoff (again after a day),
// within the hourly budget, and grabs what the profile accepts.
func (s *Service) SearchWanted(ctx context.Context) (int, error) {
	var used int
	s.DB.QueryRow(`SELECT COUNT(*) FROM movies WHERE searched >= ?`, db.Stamp(s.Now().Add(-time.Hour))).Scan(&used)
	left := s.perHour() - used
	if left <= 0 {
		return 0, nil
	}
	all, err := s.List("")
	if err != nil {
		return 0, err
	}
	var want []Movie
	now := s.Now()
	for _, m := range all {
		if !s.native(m.Category) || !m.Monitored || m.JobID != "" {
			continue
		}
		switch {
		case m.Status == "missing" && now.Sub(m.Searched) > 12*time.Hour:
		case m.Status == "cutoff unmet" && now.Sub(m.Searched) > 24*time.Hour:
		default:
			continue
		}
		want = append(want, m)
	}
	sort.SliceStable(want, func(i, j int) bool { return want[i].Searched.Before(want[j].Searched) })
	n := 0
	for _, m := range want {
		if n >= left {
			break
		}
		n++
		if _, _, err := s.Search(ctx, m.ID, true); err != nil {
			s.Log.Printf("movies: %s: %v", m.Title, err)
		}
	}
	return n, nil
}

// Search looks for one movie on every indexer and decides with its profile.
// With grab, the best accepted release is grabbed.
func (s *Service) Search(ctx context.Context, id int64, grab bool) ([]decision.Decision, []indexers.Result, error) {
	m, err := s.Get(id)
	if err != nil {
		return nil, nil, err
	}
	s.DB.Exec(`UPDATE movies SET searched = ? WHERE id = ?`, db.Stamp(s.Now()), id)
	p, err := s.Profiles.ProfileByName(m.Profile)
	if err != nil {
		s.note(id, "no profile named "+m.Profile)
		return nil, nil, fmt.Errorf("no profile named %q", m.Profile)
	}
	e, err := s.Profiles.Engine(p)
	if err != nil {
		return nil, nil, err
	}
	q := indexers.Query{Text: m.Title, Type: "movie", TMDB: m.TMDB, IMDB: m.IMDB}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := s.Indexers.Search(sctx, q, "")
	if err != nil {
		return nil, nil, err
	}
	blocked := s.blocked(id)
	w := decision.Want{Movie: true, Titles: s.titles(m), Year: m.Year, Runtime: m.Runtime,
		HaveQuality: m.Quality, HaveScore: m.Score, HaveVersion: m.Version}
	cs := make([]decision.Candidate, len(res.Results))
	byTitle := map[string]indexers.Result{}
	for i, x := range res.Results {
		cs[i] = decision.Candidate{Title: x.Title, Size: x.Size, Indexer: x.Indexer, Priority: x.IndexerPrio, Protocol: x.Protocol,
			Seeders: x.Seeders, Published: x.Published, Blocked: blocked[library.Normalize(x.Title)]}
		if x.Freeleech {
			cs[i].Flags = []string{"freeleech"}
		}
		byTitle[x.GUID+"|"+x.Title] = x
	}
	ds := e.Decide(w, cs)
	if !grab {
		return ds, res.Results, nil
	}
	if len(ds) == 0 || !ds[0].Accepted() {
		why := "nothing found"
		if len(ds) > 0 {
			why = "nothing acceptable: best was " + ds[0].Candidate.Title + " (" + strings.Join(ds[0].Rejected, "; ") + ")"
		}
		s.note(id, why)
		return ds, res.Results, nil
	}
	top := ds[0].Candidate
	for _, x := range res.Results {
		if x.Title == top.Title && x.Indexer == top.Indexer {
			_, err := s.GrabResult(ctx, id, x)
			return ds, res.Results, err
		}
	}
	return ds, res.Results, nil
}

func (s *Service) titles(m Movie) []string {
	out := []string{m.Title}
	rows, err := s.DB.Query(`SELECT title FROM aliases WHERE movie_id = ?`, m.ID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		rows.Scan(&t)
		out = append(out, t)
	}
	return out
}

func (s *Service) note(id int64, n string) {
	s.DB.Exec(`UPDATE movies SET note = ? WHERE id = ?`, n, id)
}

// GrabResult grabs one release for a movie into its category.
func (s *Service) GrabResult(ctx context.Context, id int64, r indexers.Result) (job.Job, error) {
	m, err := s.Get(id)
	if err != nil {
		return job.Job{}, err
	}
	if !s.native(m.Category) {
		return job.Job{}, fmt.Errorf("%s is not a native movie category", m.Category)
	}
	j, err := s.Indexers.Grab(ctx, r, m.Category)
	if err != nil {
		s.note(id, "grab failed: "+err.Error())
		return job.Job{}, err
	}
	if err := s.Catalog.SetJobTarget(j.ID, catalog.Target{MovieID: id}); err != nil {
		return j, err
	}
	s.DB.Exec(`UPDATE movies SET job_id = ?, note = ? WHERE id = ?`, j.ID, "grabbed "+r.Title, id)
	s.History.Add(history.Event{Kind: "movie", ItemID: id, Item: fmt.Sprintf("%s (%d)", m.Title, m.Year), Event: history.Grabbed, Release: r.Title,
		Quality: parser.Parse(r.Title).Quality.Name(), Category: m.Category, Note: r.Indexer})
	s.Log.Printf("movies: grabbed %s for %s", r.Title, m.Title)
	return j, nil
}

// Name builds the movie's folder and file name from the naming settings.
func (s *Service) Name(m Movie, in parser.Info, ext string) (folder, file string) {
	st := s.Cfg.Get().Movies
	ff, fl := st.Folder, st.File
	if ff == "" {
		ff = "{Title} ({Year})"
	}
	if fl == "" {
		fl = "{Title} ({Year}) {Quality}"
	}
	q := in.Quality.Name()
	if in.Quality.Proper {
		q += " Proper"
	} else if in.Quality.Repack {
		q += " Repack"
	}
	r := strings.NewReplacer("{Title}", safe(m.Title), "{Year}", fmt.Sprint(m.Year), "{Quality}", q,
		"{Edition}", in.Edition, "{Group}", in.Group, "{IMDb}", m.IMDB, "{TMDb}", fmt.Sprint(m.TMDB))
	clean := func(s string) string {
		s = strings.Join(strings.Fields(r.Replace(s)), " ")
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "()", ""), "[]", ""))
	}
	return clean(ff), clean(fl) + ext
}

func safe(s string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", " -", "*", "", "?", "", "\"", "'", "<", "", ">", "", "|", "-").Replace(s)
}

// Import is the pipeline's hook for finished jobs (pipeline.Engine.Native).
func (s *Service) ImportJob(ctx context.Context, j job.Job) (own bool, placed []string, left []string, err error) {
	t, err := s.Catalog.JobTarget(j.ID)
	if err != nil || t.MovieID == 0 {
		return false, nil, nil, nil
	}
	m, err := s.Get(t.MovieID)
	if err != nil {
		return false, nil, nil, nil
	}
	c, ok := s.Cfg.Get().Category(m.Category)
	if !ok || c.LibraryDir == "" {
		return true, nil, []string{"the category has no movie library folder"}, nil
	}
	// The movie is the largest video file of the download.
	var src string
	var size int64
	for _, f := range j.Files {
		if !s.IsVideo(f) || strings.Contains(strings.ToLower(f), "sample") {
			continue
		}
		p := filepath.Join(j.CompleteDir, f)
		if st, err := os.Stat(p); err == nil && st.Size() > size {
			src, size = p, st.Size()
		}
	}
	if src == "" {
		return true, nil, []string{"no video file in the download"}, nil
	}
	dst, msg, err := s.place(m, c, src, j.Name, size)
	if err != nil {
		return true, nil, nil, err
	}
	if msg != "" {
		return true, nil, []string{msg}, nil
	}
	return true, []string{dst}, nil, nil
}

// place moves a movie's file into the library under the naming settings (or,
// in a dry run, only says where it would go). The file it replaces is set
// aside, never deleted. msg is set when nothing was moved.
func (s *Service) place(m Movie, c config.Category, src, release string, size int64) (dst, msg string, err error) {
	in := parser.Parse(release)
	if in.Title == "" || in.Quality.Source == parser.SourceUnknown {
		if f := parser.Parse(filepath.Base(src)); f.Quality.Source != parser.SourceUnknown || in.Title == "" {
			in = f
		}
	}
	folder, file := s.Name(m, in, strings.ToLower(filepath.Ext(src)))
	dir := m.Folder
	if dir == "" {
		dir = filepath.Join(c.LibraryDir, folder)
	}
	dst = filepath.Join(dir, file)
	item := m.Title
	if m.Year > 0 {
		item += fmt.Sprintf(" (%d)", m.Year)
	}
	if s.Cfg.Get().Movies.DryRun {
		msg := "dry run: would move " + filepath.Base(src) + " to " + dst
		if m.File != "" && m.File != dst {
			msg += " and set aside " + m.File
		}
		s.DB.Exec(`UPDATE movies SET job_id = '', note = ? WHERE id = ?`, msg, m.ID)
		s.History.Add(history.Event{Kind: "movie", ItemID: m.ID, Item: item, Event: history.DryRun, Release: release, Quality: in.Quality.Name(), Category: m.Category, Note: msg})
		return "", msg, nil
	}
	if _, err := os.Stat(dst); err == nil && dst != m.File {
		return "", filepath.Base(dst) + " already exists and is not the movie's file", nil
	}
	if m.File != "" && m.File != dst {
		if _, err := os.Stat(m.File); err == nil {
			aside := filepath.Join(c.LibraryDir, ".undermynd-replaced", s.Now().Format("2006-01-02"), filepath.Base(m.File))
			if err := s.Move(m.File, aside); err != nil {
				return "", "", fmt.Errorf("setting aside %s: %w", m.File, err)
			}
			s.History.Add(history.Event{Kind: "movie", ItemID: m.ID, Item: item, Event: history.Replaced, Release: filepath.Base(m.File), Quality: m.Quality, Category: m.Category, Note: "set aside in " + aside})
		}
	}
	if err := s.Move(src, dst); err != nil {
		return "", "", err
	}
	_ = os.Chmod(dst, 0o666)
	_ = os.Chmod(filepath.Dir(dst), 0o777)
	score := 0
	if p, err := s.Profiles.ProfileByName(m.Profile); err == nil {
		if e, err := s.Profiles.Engine(p); err == nil {
			_, score = e.Score(in, decision.Candidate{Title: release, Size: size})
		}
	}
	s.DB.Exec(`UPDATE movies SET folder = ?, file_path = ?, file_quality = ?, file_score = ?, file_version = ?, file_size = ?,
		job_id = '', note = ?, updated = ? WHERE id = ?`, dir, dst, in.Quality.Name(), score, in.Quality.Version, size,
		"imported "+filepath.Base(dst), db.Stamp(s.Now()), m.ID)
	s.History.Add(history.Event{Kind: "movie", ItemID: m.ID, Item: item, Event: history.Imported, Release: release, Quality: in.Quality.Name(), Category: m.Category, Note: dst})
	s.Log.Printf("movies: imported %s", dst)
	return dst, "", nil
}

// ImportFile imports a file by hand (manual import) as a movie's file.
func (s *Service) ImportFile(id int64, path string) (string, error) {
	m, err := s.Get(id)
	if err != nil {
		return "", err
	}
	c, ok := s.Cfg.Get().Category(m.Category)
	if !ok || c.LibraryDir == "" {
		return "", fmt.Errorf("%s has no movie library folder", m.Category)
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || !s.IsVideo(path) {
		return "", fmt.Errorf("%s is not a video file", path)
	}
	dst, msg, err := s.place(m, c, path, filepath.Base(path), st.Size())
	if err != nil {
		return "", err
	}
	if msg != "" {
		return "", errors.New(msg)
	}
	return dst, nil
}
