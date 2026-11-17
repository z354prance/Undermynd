// Package catalog is Undermynd's own list of what the library holds and should
// hold: series with their seasons and episodes, movies, the other titles each is
// known by, the files on disk, and which jobs are fetching what. It is the base
// the native anime library, requests and the movie and TV engine build on.
package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/library"
)

// ErrNotFound is returned when an item is not in the catalog.
var ErrNotFound = errors.New("not in the catalog")

// Numbering says how a series' episodes are laid out on disk.
const (
	BySeason   = "seasons"  // <Show>/Season N/...
	ByAbsolute = "absolute" // episodes counted across seasons, as SubsPlease names long runs
)

type Series struct {
	ID        int64
	Kind      string // tv | anime
	Title     string
	Year      int
	TMDBID    int64 // 0 when unknown
	TVDBID    int64
	IMDBID    string
	Numbering string // BySeason | ByAbsolute
	Folder    string
	Monitored bool
	// EpisodeOrder is "" for TMDB's own seasons, or the id of the TMDB episode
	// group whose seasons the releases and the library follow.
	EpisodeOrder string
	Added        time.Time
	Updated      time.Time
}

type Episode struct {
	ID       int64
	SeriesID int64
	Season   int
	Number   int
	Absolute int // 0 when unknown
	Title    string
	AirDate  string // YYYY-MM-DD, empty when unknown
}

type Movie struct {
	ID        int64
	Title     string
	Year      int
	TMDBID    int64
	IMDBID    string
	Folder    string
	Monitored bool
	Added     time.Time
	Updated   time.Time
}

type File struct {
	ID           int64
	Path         string
	Size         int64
	MovieID      int64 // 0 for episode files
	EpisodeIDs   []int64
	ReleaseGroup string
	Resolution   string
	Codec        string
	Added        time.Time
}

// Catalog reads and writes the catalog tables of a migrated database.
type Catalog struct {
	DB  *sql.DB
	Now func() time.Time
}

func New(h *sql.DB) *Catalog { return &Catalog{DB: h, Now: time.Now} }

func (c *Catalog) now() string { return db.Stamp(c.Now()) }

func nullID(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(db.TimeFormat, s)
	return t
}

// AddSeries stores a new series. Its own title is also recorded as an alias.
func (c *Catalog) AddSeries(s Series) (Series, error) {
	if s.Kind != "tv" && s.Kind != "anime" {
		return Series{}, fmt.Errorf("series kind must be tv or anime, not %q", s.Kind)
	}
	if strings.TrimSpace(s.Title) == "" {
		return Series{}, errors.New("a series needs a title")
	}
	if s.Numbering == "" {
		s.Numbering = BySeason
	}
	if s.Numbering != BySeason && s.Numbering != ByAbsolute {
		return Series{}, fmt.Errorf("numbering must be %q or %q", BySeason, ByAbsolute)
	}
	now := c.now()
	res, err := c.DB.Exec(`INSERT INTO series (kind, title, year, tmdb_id, tvdb_id, imdb_id, numbering, folder, monitored, episode_order, added, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Kind, s.Title, s.Year, nullID(s.TMDBID), nullID(s.TVDBID), s.IMDBID, s.Numbering, s.Folder, s.Monitored, s.EpisodeOrder, now, now)
	if err != nil {
		return Series{}, err
	}
	s.ID, _ = res.LastInsertId()
	if err := c.AddSeriesAlias(s.ID, s.Title, "title"); err != nil {
		return Series{}, err
	}
	return c.Series(s.ID)
}

const seriesCols = `id, kind, title, year, COALESCE(tmdb_id, 0), COALESCE(tvdb_id, 0), imdb_id, numbering, folder, monitored, episode_order, added, updated`

func scanSeries(r interface{ Scan(...any) error }) (Series, error) {
	var s Series
	var added, updated string
	err := r.Scan(&s.ID, &s.Kind, &s.Title, &s.Year, &s.TMDBID, &s.TVDBID, &s.IMDBID, &s.Numbering, &s.Folder, &s.Monitored, &s.EpisodeOrder, &added, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Series{}, ErrNotFound
	}
	s.Added, s.Updated = parseTime(added), parseTime(updated)
	return s, err
}

func (c *Catalog) Series(id int64) (Series, error) {
	return scanSeries(c.DB.QueryRow(`SELECT `+seriesCols+` FROM series WHERE id = ?`, id))
}

// SeriesByTMDB finds a series by its TMDB id.
func (c *Catalog) SeriesByTMDB(tmdbID int64) (Series, error) {
	return scanSeries(c.DB.QueryRow(`SELECT `+seriesCols+` FROM series WHERE tmdb_id = ?`, tmdbID))
}

// ListSeries returns every series, by title.
func (c *Catalog) ListSeries() ([]Series, error) {
	rows, err := c.DB.Query(`SELECT ` + seriesCols + ` FROM series ORDER BY title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Series
	for rows.Next() {
		s, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetNumbering records how a series is laid out on disk.
func (c *Catalog) SetNumbering(id int64, numbering string) error {
	if numbering != BySeason && numbering != ByAbsolute {
		return fmt.Errorf("numbering must be %q or %q", BySeason, ByAbsolute)
	}
	return c.exec1(`UPDATE series SET numbering = ?, updated = ? WHERE id = ?`, numbering, c.now(), id)
}

// ErrHasFiles is returned when a series' episodes cannot be renumbered because
// files are already recorded against them.
var ErrHasFiles = errors.New("files are already recorded for this series; its episode order can only change before that")

// ReplaceEpisodes swaps a series' whole episode list for eps and records the
// episode order they follow. It refuses once files are recorded, so no file is
// ever silently re-pointed to a different episode.
func (c *Catalog) ReplaceEpisodes(seriesID int64, order string, eps []Episode) error {
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM file_episodes fe JOIN episodes e ON e.id = fe.episode_id WHERE e.series_id = ?`, seriesID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrHasFiles
	}
	if _, err := tx.Exec(`DELETE FROM episodes WHERE series_id = ?`, seriesID); err != nil {
		return err
	}
	for _, e := range eps {
		if _, err := tx.Exec(`INSERT INTO episodes (series_id, season, number, absolute, title, air_date) VALUES (?, ?, ?, ?, ?, ?)`,
			seriesID, e.Season, e.Number, nullInt(e.Absolute), e.Title, e.AirDate); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`UPDATE series SET episode_order = ?, updated = ? WHERE id = ?`, order, c.now(), seriesID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SeriesDue lists up to limit series last updated before cutoff, oldest first.
func (c *Catalog) SeriesDue(cutoff time.Time, limit int) ([]Series, error) {
	rows, err := c.DB.Query(`SELECT `+seriesCols+` FROM series WHERE tmdb_id IS NOT NULL AND updated < ? ORDER BY updated LIMIT ?`, db.Stamp(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Series
	for rows.Next() {
		s, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetMonitored turns monitoring of a series on or off.
func (c *Catalog) SetMonitored(id int64, on bool) error {
	return c.exec1(`UPDATE series SET monitored = ?, updated = ? WHERE id = ?`, on, c.now(), id)
}

func (c *Catalog) exec1(q string, args ...any) error {
	res, err := c.DB.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddSeriesAlias records another title a series is known by. Titles that only
// differ in case or punctuation are the same alias.
func (c *Catalog) AddSeriesAlias(seriesID int64, title, source string) error {
	norm := library.Normalize(title)
	if norm == "" {
		return nil
	}
	_, err := c.DB.Exec(`INSERT INTO aliases (series_id, title, norm, source) VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, seriesID, strings.TrimSpace(title), norm, source)
	return err
}

// SeriesAliases lists the titles a series is known by.
func (c *Catalog) SeriesAliases(seriesID int64) ([]string, error) {
	rows, err := c.DB.Query(`SELECT title FROM aliases WHERE series_id = ? ORDER BY id`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindSeries returns the series any of the given titles names. It answers only
// when every title that matches points at the same one series; otherwise it
// returns the candidates, so nothing is ever guessed.
func (c *Catalog) FindSeries(titles ...string) (match Series, candidates []Series, err error) {
	ids := map[int64]bool{}
	for _, t := range titles {
		norm := library.Normalize(t)
		if norm == "" {
			continue
		}
		rows, err := c.DB.Query(`SELECT DISTINCT series_id FROM aliases WHERE norm = ? AND series_id IS NOT NULL`, norm)
		if err != nil {
			return Series{}, nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return Series{}, nil, err
			}
			ids[id] = true
		}
		rows.Close()
	}
	for id := range ids {
		s, err := c.Series(id)
		if err != nil {
			return Series{}, nil, err
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 1 {
		return candidates[0], nil, nil
	}
	if len(candidates) == 0 {
		return Series{}, nil, ErrNotFound
	}
	return Series{}, candidates, nil
}

// PutEpisodes adds or updates a series' episodes, keyed by season and number.
func (c *Catalog) PutEpisodes(seriesID int64, eps []Episode) error {
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	for _, e := range eps {
		if _, err := tx.Exec(`INSERT INTO episodes (series_id, season, number, absolute, title, air_date) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(series_id, season, number) DO UPDATE SET absolute = excluded.absolute, title = excluded.title, air_date = excluded.air_date`,
			seriesID, e.Season, e.Number, nullInt(e.Absolute), e.Title, e.AirDate); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE series SET updated = ? WHERE id = ?`, c.now(), seriesID); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

const episodeCols = `id, series_id, season, number, COALESCE(absolute, 0), title, air_date`

func scanEpisodes(rows *sql.Rows) ([]Episode, error) {
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		if err := rows.Scan(&e.ID, &e.SeriesID, &e.Season, &e.Number, &e.Absolute, &e.Title, &e.AirDate); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Episodes lists a series' episodes in order.
func (c *Catalog) Episodes(seriesID int64) ([]Episode, error) {
	rows, err := c.DB.Query(`SELECT `+episodeCols+` FROM episodes WHERE series_id = ? ORDER BY season, number`, seriesID)
	if err != nil {
		return nil, err
	}
	return scanEpisodes(rows)
}

// EpisodeBySeason finds an episode by season and number.
func (c *Catalog) EpisodeBySeason(seriesID int64, season, number int) (Episode, error) {
	rows, err := c.DB.Query(`SELECT `+episodeCols+` FROM episodes WHERE series_id = ? AND season = ? AND number = ?`, seriesID, season, number)
	if err != nil {
		return Episode{}, err
	}
	eps, err := scanEpisodes(rows)
	if err != nil {
		return Episode{}, err
	}
	if len(eps) == 0 {
		return Episode{}, ErrNotFound
	}
	return eps[0], nil
}

// EpisodeByAbsolute finds an episode by its absolute number. It refuses when the
// number is unknown or names more than one episode.
func (c *Catalog) EpisodeByAbsolute(seriesID int64, absolute int) (Episode, error) {
	rows, err := c.DB.Query(`SELECT `+episodeCols+` FROM episodes WHERE series_id = ? AND absolute = ?`, seriesID, absolute)
	if err != nil {
		return Episode{}, err
	}
	eps, err := scanEpisodes(rows)
	if err != nil {
		return Episode{}, err
	}
	switch len(eps) {
	case 0:
		return Episode{}, ErrNotFound
	case 1:
		return eps[0], nil
	}
	return Episode{}, fmt.Errorf("absolute episode %d matches %d episodes", absolute, len(eps))
}

// AddFile records a file on disk and the episodes (or movie) it holds. A path
// already recorded is updated in place.
func (c *Catalog) AddFile(f File) (File, error) {
	if (f.MovieID == 0) == (len(f.EpisodeIDs) == 0) {
		return File{}, errors.New("a file holds either episodes or a movie")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return File{}, err
	}
	if err := tx.QueryRow(`INSERT INTO files (path, size, movie_id, release_group, resolution, codec, added) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET size = excluded.size, movie_id = excluded.movie_id, release_group = excluded.release_group,
			resolution = excluded.resolution, codec = excluded.codec
		RETURNING id`, f.Path, f.Size, nullID(f.MovieID), f.ReleaseGroup, f.Resolution, f.Codec, c.now()).Scan(&f.ID); err != nil {
		tx.Rollback()
		return File{}, err
	}
	if _, err := tx.Exec(`DELETE FROM file_episodes WHERE file_id = ?`, f.ID); err != nil {
		tx.Rollback()
		return File{}, err
	}
	for _, id := range f.EpisodeIDs {
		if _, err := tx.Exec(`INSERT INTO file_episodes (file_id, episode_id) VALUES (?, ?)`, f.ID, id); err != nil {
			tx.Rollback()
			return File{}, err
		}
	}
	return f, tx.Commit()
}

// RemoveFile forgets a file (for example when it was deleted or replaced).
func (c *Catalog) RemoveFile(path string) error {
	return c.exec1(`DELETE FROM files WHERE path = ?`, path)
}

// EpisodeFiles returns the files that hold an episode.
func (c *Catalog) EpisodeFiles(episodeID int64) ([]File, error) {
	rows, err := c.DB.Query(`SELECT f.id, f.path, f.size, f.release_group, f.resolution, f.codec, f.added
		FROM files f JOIN file_episodes fe ON fe.file_id = f.id WHERE fe.episode_id = ? ORDER BY f.id`, episodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		var added string
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.ReleaseGroup, &f.Resolution, &f.Codec, &added); err != nil {
			return nil, err
		}
		f.Added = parseTime(added)
		f.EpisodeIDs = []int64{episodeID}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Missing lists a series' episodes that have aired by today and have no file.
// Episodes with no air date are not counted as missing: they may not exist yet.
func (c *Catalog) Missing(seriesID int64) ([]Episode, error) {
	today := c.Now().UTC().Format("2006-01-02")
	rows, err := c.DB.Query(`SELECT `+episodeCols+` FROM episodes e
		WHERE e.series_id = ? AND e.season > 0 AND e.air_date != '' AND e.air_date <= ?
		AND NOT EXISTS (SELECT 1 FROM file_episodes fe WHERE fe.episode_id = e.id)
		ORDER BY season, number`, seriesID, today)
	if err != nil {
		return nil, err
	}
	return scanEpisodes(rows)
}

// SeasonCount is how many episodes of a season exist and how many have files.
type SeasonCount struct {
	Season int `json:"season"`
	Have   int `json:"have"`
	Total  int `json:"total"`
}

// Seasons counts, per season, the episodes with files against the total.
// Specials (season 0) are left out.
func (c *Catalog) Seasons(seriesID int64) ([]SeasonCount, error) {
	rows, err := c.DB.Query(`SELECT e.season, COUNT(*),
		SUM(CASE WHEN EXISTS (SELECT 1 FROM file_episodes fe WHERE fe.episode_id = e.id) THEN 1 ELSE 0 END)
		FROM episodes e WHERE e.series_id = ? AND e.season > 0 GROUP BY e.season ORDER BY e.season`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeasonCount
	for rows.Next() {
		var s SeasonCount
		if err := rows.Scan(&s.Season, &s.Total, &s.Have); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AddMovie stores a new movie, with its title as an alias.
func (c *Catalog) AddMovie(m Movie) (Movie, error) {
	if strings.TrimSpace(m.Title) == "" {
		return Movie{}, errors.New("a movie needs a title")
	}
	now := c.now()
	res, err := c.DB.Exec(`INSERT INTO movies (title, year, tmdb_id, imdb_id, folder, monitored, added, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Title, m.Year, nullID(m.TMDBID), m.IMDBID, m.Folder, m.Monitored, now, now)
	if err != nil {
		return Movie{}, err
	}
	m.ID, _ = res.LastInsertId()
	if norm := library.Normalize(m.Title); norm != "" {
		if _, err := c.DB.Exec(`INSERT INTO aliases (movie_id, title, norm, source) VALUES (?, ?, ?, 'title') ON CONFLICT DO NOTHING`, m.ID, m.Title, norm); err != nil {
			return Movie{}, err
		}
	}
	return c.Movie(m.ID)
}

func (c *Catalog) Movie(id int64) (Movie, error) {
	var m Movie
	var added, updated string
	err := c.DB.QueryRow(`SELECT id, title, year, COALESCE(tmdb_id, 0), imdb_id, folder, monitored, added, updated FROM movies WHERE id = ?`, id).
		Scan(&m.ID, &m.Title, &m.Year, &m.TMDBID, &m.IMDBID, &m.Folder, &m.Monitored, &added, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Movie{}, ErrNotFound
	}
	m.Added, m.Updated = parseTime(added), parseTime(updated)
	return m, err
}

// Target is what a job is fetching: episodes of a series, or a movie.
type Target struct {
	EpisodeIDs []int64
	MovieID    int64
}

// SetJobTarget records what a job is fetching, replacing any earlier record.
// The job must already be stored; the record goes when the job is pruned.
func (c *Catalog) SetJobTarget(jobID string, t Target) error {
	if (t.MovieID == 0) == (len(t.EpisodeIDs) == 0) {
		return errors.New("a job fetches either episodes or a movie")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM job_targets WHERE job_id = ?`, jobID); err != nil {
		tx.Rollback()
		return err
	}
	if t.MovieID != 0 {
		if _, err := tx.Exec(`INSERT INTO job_targets (job_id, movie_id) VALUES (?, ?)`, jobID, t.MovieID); err != nil {
			tx.Rollback()
			return err
		}
	}
	for _, id := range t.EpisodeIDs {
		if _, err := tx.Exec(`INSERT INTO job_targets (job_id, episode_id) VALUES (?, ?)`, jobID, id); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// JobTarget returns what a job is fetching (empty when not recorded).
func (c *Catalog) JobTarget(jobID string) (Target, error) {
	rows, err := c.DB.Query(`SELECT COALESCE(episode_id, 0), COALESCE(movie_id, 0) FROM job_targets WHERE job_id = ? ORDER BY episode_id`, jobID)
	if err != nil {
		return Target{}, err
	}
	defer rows.Close()
	var t Target
	for rows.Next() {
		var ep, mv int64
		if err := rows.Scan(&ep, &mv); err != nil {
			return Target{}, err
		}
		if ep != 0 {
			t.EpisodeIDs = append(t.EpisodeIDs, ep)
		}
		if mv != 0 {
			t.MovieID = mv
		}
	}
	return t, rows.Err()
}

// SeriesByFolder finds the series kept in a folder.
func (c *Catalog) SeriesByFolder(folder string) (Series, error) {
	return scanSeries(c.DB.QueryRow(`SELECT `+seriesCols+` FROM series WHERE folder = ? AND folder != ''`, folder))
}

// RemoveSeries forgets a series with its episodes, titles and file records.
// Nothing on disk is touched.
func (c *Catalog) RemoveSeries(id int64) error {
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM files WHERE id IN (SELECT fe.file_id FROM file_episodes fe JOIN episodes e ON e.id = fe.episode_id WHERE e.series_id = ?)`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM series WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SeriesFile is a recorded file with the episode it holds.
type SeriesFile struct {
	File
	EpisodeID int64
}

// SeriesFiles lists every file recorded for a series' episodes.
func (c *Catalog) SeriesFiles(seriesID int64) ([]SeriesFile, error) {
	rows, err := c.DB.Query(`SELECT f.id, f.path, f.size, f.release_group, f.resolution, f.codec, f.added, fe.episode_id
		FROM files f JOIN file_episodes fe ON fe.file_id = f.id JOIN episodes e ON e.id = fe.episode_id
		WHERE e.series_id = ? ORDER BY f.id`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesFile
	for rows.Next() {
		var f SeriesFile
		var added string
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.ReleaseGroup, &f.Resolution, &f.Codec, &added, &f.EpisodeID); err != nil {
			return nil, err
		}
		f.Added = parseTime(added)
		f.EpisodeIDs = []int64{f.EpisodeID}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Place is one folder a series is kept in. The series' own folder starts at
// TMDB season 1 and uses the series' numbering; an extra folder holds the
// seasons from FirstSeason on, its own "Season 1" being that TMDB season.
type Place struct {
	Folder      string `json:"folder"`
	FirstSeason int    `json:"firstSeason"`
	Numbering   string `json:"numbering"`
	Extra       bool   `json:"extra"`
}

// AddSeriesFolder keeps a series in one more folder.
func (c *Catalog) AddSeriesFolder(seriesID int64, folder string, firstSeason int, numbering string) error {
	if firstSeason < 1 {
		return errors.New("an extra folder starts at season 1 or later")
	}
	if numbering != ByAbsolute {
		numbering = BySeason
	}
	_, err := c.DB.Exec(`INSERT INTO series_folders (folder, series_id, first_season, numbering) VALUES (?, ?, ?, ?)`, folder, seriesID, firstSeason, numbering)
	return err
}

// RemoveSeriesFolder forgets an extra folder and the files recorded in it.
func (c *Catalog) RemoveSeriesFolder(folder string) error {
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prefix := strings.TrimSuffix(folder, string(filepath.Separator)) + string(filepath.Separator)
	if _, err := tx.Exec(`DELETE FROM files WHERE substr(path, 1, ?) = ?`, len(prefix), prefix); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM series_folders WHERE folder = ?`, folder); err != nil {
		return err
	}
	return tx.Commit()
}

// Places lists the folders a series is kept in: its own first, then the extra
// ones by first season.
func (c *Catalog) Places(s Series) ([]Place, error) {
	var out []Place
	if s.Folder != "" {
		out = append(out, Place{Folder: s.Folder, FirstSeason: 1, Numbering: s.Numbering})
	}
	rows, err := c.DB.Query(`SELECT folder, first_season, numbering FROM series_folders WHERE series_id = ? ORDER BY first_season, folder`, s.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p := Place{Extra: true}
		if err := rows.Scan(&p.Folder, &p.FirstSeason, &p.Numbering); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SeriesByAnyFolder finds the series kept in a folder, its own or an extra one.
func (c *Catalog) SeriesByAnyFolder(folder string) (Series, Place, error) {
	if s, err := c.SeriesByFolder(folder); err == nil {
		return s, Place{Folder: folder, FirstSeason: 1, Numbering: s.Numbering}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Series{}, Place{}, err
	}
	p := Place{Folder: folder, Extra: true}
	var id int64
	err := c.DB.QueryRow(`SELECT series_id, first_season, numbering FROM series_folders WHERE folder = ?`, folder).Scan(&id, &p.FirstSeason, &p.Numbering)
	if errors.Is(err, sql.ErrNoRows) {
		return Series{}, Place{}, ErrNotFound
	}
	if err != nil {
		return Series{}, Place{}, err
	}
	s, err := c.Series(id)
	return s, p, err
}
