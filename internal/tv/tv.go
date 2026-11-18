// Package tv runs native TV categories (roadmap F5): the library is read from
// Sonarr once, then HyveMynd keeps it: new episodes come from TMDB, recent
// releases on the indexers are checked every 15 minutes, monitored episodes
// that are missing or below their profile's cutoff are searched (a whole
// season at once when it has finished airing), the best release is grabbed
// into the pipeline, and finished files are imported under the naming
// settings (or, in a dry run, only described).
package tv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/parser"
	"github.com/z354prance/undermynd/internal/scheduler"
)

// Indexers is the part of the indexer service TV uses.
type Indexers interface {
	Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error)
	Grab(ctx context.Context, r indexers.Result, category string) (job.Job, error)
}

// Jobs reads a job's state.
type Jobs interface {
	Get(id string) (job.Job, bool)
}

// Service keeps the native TV library.
type Service struct {
	DB       *sql.DB
	Catalog  *catalog.Catalog
	TMDB     *metadata.TMDB // nil: no new episodes from TMDB
	Cfg      *config.Config
	Profiles *decision.Store
	Indexers Indexers
	Jobs     Jobs
	Arr      func(category string) *arr.Client
	Move     func(src, dst string) error
	IsVideo  func(path string) bool
	History  *history.Log
	Budget   *scheduler.Budget // the shared search budget and grace window; nil: none
	Log      *log.Logger
	Now      func() time.Time
}

// Series is a show of a native category with its counts.
type Series struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	TMDB      int64  `json:"tmdbId"`
	TVDB      int64  `json:"tvdbId"`
	IMDB      string `json:"imdbId"`
	Folder    string `json:"folder"`
	Monitored bool   `json:"monitored"`
	Category  string `json:"category"`
	Profile   string `json:"profile"`
	Runtime   int    `json:"runtime"`
	Type      string `json:"type"` // standard, daily, anime

	Episodes    int    `json:"episodes"` // aired, monitored
	Have        int    `json:"have"`
	Missing     int    `json:"missing"`
	Downloading int    `json:"downloading"`
	Size        int64  `json:"size"`
	NextAir     string `json:"nextAir,omitempty"`
	Poster      string `json:"poster"`
	Fanart      string `json:"fanart"`
	Overview    string `json:"overview"`
	Genres      string `json:"genres"`
	Network     string `json:"network"`
	Ended       bool   `json:"ended"`
	Seasons     int    `json:"seasons"`
}

// Episode is one episode with its file and state.
type Episode struct {
	ID        int64     `json:"id"`
	SeriesID  int64     `json:"seriesId"`
	Season    int       `json:"season"`
	Number    int       `json:"number"`
	Absolute  int       `json:"absolute"`
	Title     string    `json:"title"`
	AirDate   string    `json:"airDate"`
	Monitored bool      `json:"monitored"`
	File      string    `json:"file"`
	Quality   string    `json:"quality"`
	Score     int       `json:"score"`
	Version   int       `json:"version"`
	Size      int64     `json:"size"`
	JobID     string    `json:"jobId"`
	Searched  time.Time `json:"searched"`
	Note      string    `json:"note"`
	Status    string    `json:"status"` // have, missing, downloading, cutoff unmet, unaired, unmonitored
}

const seriesCols = `id, title, year, COALESCE(tmdb_id, 0), COALESCE(tvdb_id, 0), imdb_id, folder, monitored, category, profile, runtime, series_type,
	poster, fanart, overview, genres, network, ended`
const episodeCols = `id, series_id, season, number, COALESCE(absolute, 0), title, air_date, monitored, file_path, file_quality,
	file_score, file_version, file_size, job_id, searched, note`

func scanSeries(r interface{ Scan(...any) error }) (Series, error) {
	var s Series
	err := r.Scan(&s.ID, &s.Title, &s.Year, &s.TMDB, &s.TVDB, &s.IMDB, &s.Folder, &s.Monitored, &s.Category, &s.Profile, &s.Runtime, &s.Type,
		&s.Poster, &s.Fanart, &s.Overview, &s.Genres, &s.Network, &s.Ended)
	return s, err
}

func scanEpisode(r interface{ Scan(...any) error }) (Episode, error) {
	var e Episode
	var searched string
	err := r.Scan(&e.ID, &e.SeriesID, &e.Season, &e.Number, &e.Absolute, &e.Title, &e.AirDate, &e.Monitored, &e.File, &e.Quality,
		&e.Score, &e.Version, &e.Size, &e.JobID, &searched, &e.Note)
	if searched != "" {
		e.Searched, _ = time.Parse(db.TimeFormat, searched)
	}
	return e, err
}

func (s *Service) today() string { return s.Now().Format("2006-01-02") }

func (s *Service) status(e *Episode, seriesMonitored bool, p *decision.Profile) {
	switch {
	case e.JobID != "":
		e.Status = "downloading"
	case e.File != "":
		e.Status = "have"
		if p != nil && seriesMonitored && e.Monitored && !p.CutoffMet(e.Quality, e.Score) {
			e.Status = "cutoff unmet"
		}
	case e.AirDate == "" || e.AirDate > s.today():
		e.Status = "unaired"
	case !e.Monitored || !seriesMonitored:
		e.Status = "unmonitored"
	default:
		e.Status = "missing"
	}
}

func (s *Service) profiles() map[string]decision.Profile {
	out := map[string]decision.Profile{}
	ps, _ := s.Profiles.Profiles()
	for _, p := range ps {
		out[strings.ToLower(p.Name)] = p
	}
	return out
}

func (s *Service) profileOf(ps map[string]decision.Profile, name string) *decision.Profile {
	if p, ok := ps[strings.ToLower(name)]; ok {
		return &p
	}
	return nil
}

// List returns a category's series (every native-able category's when
// empty) with their episode counts.
func (s *Service) List(category string) ([]Series, error) {
	ps := s.profiles()
	q := `SELECT ` + seriesCols + ` FROM series WHERE category != '' AND kind = 'tv'`
	var args []any
	if category != "" {
		q += ` AND category = ?`
		args = append(args, category)
	}
	rows, err := s.DB.Query(q+` ORDER BY title COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	var out []Series
	for rows.Next() {
		x, err := scanSeries(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	rows.Close()
	all, err := s.allEpisodes()
	if err != nil {
		return nil, err
	}
	for i := range out {
		x := &out[i]
		seen := map[int]bool{}
		for _, e := range all[x.ID] {
			if e.Season > 0 && !seen[e.Season] {
				seen[e.Season] = true
				x.Seasons++
			}
			s.status(&e, x.Monitored, s.profileOf(ps, x.Profile))
			x.Size += e.Size
			switch e.Status {
			case "have", "cutoff unmet":
				x.Have++
				x.Episodes++
			case "missing":
				x.Missing++
				x.Episodes++
			case "downloading":
				x.Downloading++
				x.Episodes++
			case "unaired":
				if e.AirDate != "" && (x.NextAir == "" || e.AirDate < x.NextAir) {
					x.NextAir = e.AirDate
				}
			}
		}
	}
	return out, nil
}

func (s *Service) allEpisodes() (map[int64][]Episode, error) {
	rows, err := s.DB.Query(`SELECT ` + episodeCols + ` FROM episodes WHERE series_id IN (SELECT id FROM series WHERE category != '')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Episode{}
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out[e.SeriesID] = append(out[e.SeriesID], e)
	}
	return out, rows.Err()
}

// Get returns a series and its episodes, by season and number.
func (s *Service) Get(id int64) (Series, []Episode, error) {
	x, err := scanSeries(s.DB.QueryRow(`SELECT `+seriesCols+` FROM series WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Series{}, nil, catalog.ErrNotFound
	} else if err != nil {
		return Series{}, nil, err
	}
	p := s.profileOf(s.profiles(), x.Profile)
	rows, err := s.DB.Query(`SELECT `+episodeCols+` FROM episodes WHERE series_id = ? ORDER BY season, number`, id)
	if err != nil {
		return x, nil, err
	}
	defer rows.Close()
	var eps []Episode
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return x, nil, err
		}
		s.status(&e, x.Monitored, p)
		eps = append(eps, e)
	}
	return x, eps, rows.Err()
}

// EditSeries changes a series' monitoring, profile and type.
func (s *Service) EditSeries(id int64, monitored bool, profile, typ string) error {
	if profile != "" {
		if _, err := s.Profiles.ProfileByName(profile); err != nil {
			return fmt.Errorf("no profile named %q", profile)
		}
	}
	if typ != "" && typ != "standard" && typ != "daily" && typ != "anime" {
		return fmt.Errorf("type is standard, daily or anime")
	}
	_, err := s.DB.Exec(`UPDATE series SET monitored = ?, profile = CASE WHEN ? = '' THEN profile ELSE ? END,
		series_type = CASE WHEN ? = '' THEN series_type ELSE ? END, updated = ? WHERE id = ?`,
		monitored, profile, profile, typ, typ, db.Stamp(s.Now()), id)
	return err
}

// Monitor sets episodes monitored or not: one episode, or a whole season.
func (s *Service) Monitor(seriesID int64, season int, episodeID int64, on bool) error {
	if episodeID > 0 {
		_, err := s.DB.Exec(`UPDATE episodes SET monitored = ? WHERE id = ? AND series_id = ?`, on, episodeID, seriesID)
		return err
	}
	_, err := s.DB.Exec(`UPDATE episodes SET monitored = ? WHERE series_id = ? AND season = ?`, on, seriesID, season)
	return err
}

// Remove forgets a series. Its files stay on disk.
func (s *Service) Remove(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM series WHERE id = ?`, id)
	return err
}

func (s *Service) native(category string) bool {
	c, ok := s.Cfg.Get().Category(category)
	return ok && c.Native && c.Kind == "sonarr"
}

// ---------------------------------------------------------------- import

type sonarrSeries struct {
	ID               int64    `json:"id"`
	Title            string   `json:"title"`
	Year             int      `json:"year"`
	TVDB             int64    `json:"tvdbId"`
	TMDB             int64    `json:"tmdbId"`
	IMDB             string   `json:"imdbId"`
	Path             string   `json:"path"`
	Monitored        bool     `json:"monitored"`
	Runtime          int      `json:"runtime"`
	QualityProfileID int      `json:"qualityProfileId"`
	SeriesType       string   `json:"seriesType"`
	Overview         string   `json:"overview"`
	Genres           []string `json:"genres"`
	Network          string   `json:"network"`
	Status           string   `json:"status"`
	Images           []struct {
		CoverType string `json:"coverType"`
		RemoteURL string `json:"remoteUrl"`
	} `json:"images"`
}

// SetArt records a series' poster, backdrop, overview, genres, network and
// whether it has ended; empty values keep what is there.
func (s *Service) SetArt(id int64, poster, fanart, overview, genres, network string, ended bool) {
	s.DB.Exec(`UPDATE series SET poster = CASE WHEN ? != '' THEN ? ELSE poster END, fanart = CASE WHEN ? != '' THEN ? ELSE fanart END,
		overview = CASE WHEN ? != '' THEN ? ELSE overview END, genres = CASE WHEN ? != '' THEN ? ELSE genres END,
		network = CASE WHEN ? != '' THEN ? ELSE network END, ended = ? WHERE id = ?`,
		poster, poster, fanart, fanart, overview, overview, genres, genres, network, network, ended, id)
}

type sonarrEpisode struct {
	SeasonNumber          int    `json:"seasonNumber"`
	EpisodeNumber         int    `json:"episodeNumber"`
	AbsoluteEpisodeNumber int    `json:"absoluteEpisodeNumber"`
	Title                 string `json:"title"`
	AirDate               string `json:"airDate"`
	Monitored             bool   `json:"monitored"`
	EpisodeFileID         int64  `json:"episodeFileId"`
}

type sonarrFile struct {
	ID      int64  `json:"id"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Quality struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
		Revision struct {
			Version int `json:"version"`
		} `json:"revision"`
	} `json:"quality"`
	CustomFormatScore int `json:"customFormatScore"`
}

// ImportResult says what an import from Sonarr did.
type ImportResult struct {
	Added    int      `json:"added"`
	Updated  int      `json:"updated"`
	Episodes int      `json:"episodes"`
	Problems []string `json:"problems,omitempty"`
}

// Import reads a category's library from Sonarr: every series with its
// episodes (Sonarr's numbering), what is monitored, the profile by name and
// each episode's file. Paths under Sonarr's root folders are read as under the
// category's library folder. Importing again updates.
func (s *Service) Import(ctx context.Context, category string) (ImportResult, error) {
	var res ImportResult
	c, ok := s.Cfg.Get().Category(category)
	if !ok || c.Kind != "sonarr" || c.ArrURL == "" {
		return res, fmt.Errorf("%s is not a Sonarr category", category)
	}
	if c.LibraryDir == "" {
		return res, fmt.Errorf("set the TV library folder of %s first", category)
	}
	cl := s.Arr(category)
	var list []sonarrSeries
	if err := cl.Get(ctx, "/series", &list); err != nil {
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
	ps := s.profiles()
	now := db.Stamp(s.Now())
	for _, sr := range list {
		if sr.SeriesType == "anime" {
			res.Problems = append(res.Problems, sr.Title+": anime stays on the Anime page")
			continue
		}
		prof := names[sr.QualityProfileID]
		if _, ok := ps[strings.ToLower(prof)]; !ok {
			if _, ok := ps[strings.ToLower(prof+" ("+category+")")]; ok {
				prof += " (" + category + ")"
			}
		}
		var id int64
		err := s.DB.QueryRow(`SELECT id FROM series WHERE (tvdb_id = ? AND ? != 0) OR (tmdb_id = ? AND ? != 0) LIMIT 1`, sr.TVDB, sr.TVDB, sr.TMDB, sr.TMDB).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			x, err := s.Catalog.AddSeries(catalog.Series{Kind: "tv", Title: sr.Title, Year: sr.Year, TMDBID: sr.TMDB, TVDBID: sr.TVDB, IMDBID: sr.IMDB, Folder: local(sr.Path), Monitored: sr.Monitored})
			if err != nil {
				res.Problems = append(res.Problems, sr.Title+": "+err.Error())
				continue
			}
			id = x.ID
			res.Added++
		} else if err != nil {
			return res, err
		} else {
			res.Updated++
		}
		typ := sr.SeriesType
		if typ == "" {
			typ = "standard"
		}
		if _, err := s.DB.Exec(`UPDATE series SET title = ?, year = ?, imdb_id = ?, folder = ?, monitored = ?, category = ?, profile = ?,
			runtime = ?, series_type = ?, updated = ? WHERE id = ?`, sr.Title, sr.Year, sr.IMDB, local(sr.Path), sr.Monitored, category, prof,
			sr.Runtime, typ, now, id); err != nil {
			return res, err
		}
		var poster, fanart string
		for _, im := range sr.Images {
			u := strings.Replace(im.RemoteURL, "/t/p/original/", "/t/p/w500/", 1)
			switch im.CoverType {
			case "poster":
				poster = u
			case "fanart":
				fanart = strings.Replace(im.RemoteURL, "/t/p/original/", "/t/p/w1280/", 1)
			}
		}
		s.SetArt(id, poster, fanart, sr.Overview, strings.Join(sr.Genres, ", "), sr.Network, sr.Status == "ended")
		var eps []sonarrEpisode
		if err := cl.Get(ctx, "/episode?seriesId="+strconv.FormatInt(sr.ID, 10), &eps); err != nil {
			res.Problems = append(res.Problems, sr.Title+": episodes: "+err.Error())
			continue
		}
		var files []sonarrFile
		if err := cl.Get(ctx, "/episodefile?seriesId="+strconv.FormatInt(sr.ID, 10), &files); err != nil {
			res.Problems = append(res.Problems, sr.Title+": files: "+err.Error())
		}
		byID := map[int64]sonarrFile{}
		for _, f := range files {
			byID[f.ID] = f
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return res, err
		}
		for _, e := range eps {
			air := e.AirDate
			var path, quality string
			var size int64
			var score, version int
			if f, ok := byID[e.EpisodeFileID]; ok && e.EpisodeFileID != 0 {
				path, quality, size, score, version = local(f.Path), parser.Canonical(f.Quality.Quality.Name), f.Size, f.CustomFormatScore, f.Quality.Revision.Version
			}
			var abs any
			if e.AbsoluteEpisodeNumber > 0 {
				abs = e.AbsoluteEpisodeNumber
			}
			if _, err := tx.Exec(`INSERT INTO episodes (series_id, season, number, absolute, title, air_date, monitored, file_path, file_quality, file_score, file_version, file_size)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(series_id, season, number) DO UPDATE SET absolute = excluded.absolute, title = excluded.title, air_date = excluded.air_date,
				monitored = excluded.monitored, file_path = excluded.file_path, file_quality = excluded.file_quality, file_score = excluded.file_score,
				file_version = excluded.file_version, file_size = excluded.file_size`,
				id, e.SeasonNumber, e.EpisodeNumber, abs, e.Title, air, e.Monitored, path, quality, score, version, size); err != nil {
				tx.Rollback()
				return res, err
			}
			res.Episodes++
		}
		if err := tx.Commit(); err != nil {
			return res, err
		}
	}
	s.Log.Printf("tv: imported %d new and %d known series (%d episodes) from %s", res.Added, res.Updated, res.Episodes, category)
	return res, nil
}

// Add puts a series into a native category from TMDB (an approved request,
// or by hand), monitored from now on: episodes that already aired are not
// monitored unless all is set.
func (s *Service) Add(ctx context.Context, category string, tmdbID int64, profile string, all bool) (Series, error) {
	if !s.native(category) {
		return Series{}, fmt.Errorf("%s is not a native TV category", category)
	}
	if s.TMDB == nil {
		return Series{}, errors.New("set a TMDB key on the Metadata settings tab")
	}
	tv, err := s.TMDB.TV(ctx, tmdbID, true)
	if err != nil {
		return Series{}, err
	}
	if profile == "" {
		s.DB.QueryRow(`SELECT profile FROM series WHERE category = ? AND profile != '' GROUP BY profile ORDER BY COUNT(*) DESC LIMIT 1`, category).Scan(&profile)
	}
	if profile == "" {
		profile = "HD-1080p"
	}
	year, _ := strconv.Atoi(strings.SplitN(tv.FirstAirDate, "-", 2)[0])
	var id int64
	err = s.DB.QueryRow(`SELECT id FROM series WHERE tmdb_id = ?`, tmdbID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		x, err := s.Catalog.AddSeries(catalog.Series{Kind: "tv", Title: tv.Name, Year: year, TMDBID: tmdbID, TVDBID: tv.ExternalIDs.TVDB, IMDBID: tv.ExternalIDs.IMDB, Monitored: true})
		if err != nil {
			return Series{}, err
		}
		id = x.ID
	} else if err != nil {
		return Series{}, err
	}
	if _, err := s.DB.Exec(`UPDATE series SET category = ?, profile = ?, monitored = 1, updated = ? WHERE id = ?`, category, profile, db.Stamp(s.Now()), id); err != nil {
		return Series{}, err
	}
	if err := s.refresh(ctx, id, tmdbID); err != nil {
		return Series{}, err
	}
	if !all {
		s.DB.Exec(`UPDATE episodes SET monitored = 0 WHERE series_id = ? AND file_path = '' AND air_date != '' AND air_date < ?`, id, s.today())
	}
	s.DB.Exec(`UPDATE episodes SET monitored = 0 WHERE series_id = ? AND season = 0`, id) // specials, as Sonarr does
	x, _, err := s.Get(id)
	return x, err
}

// refresh adds new episodes from TMDB and updates titles and air dates.
// Numbers already known are kept as they are (they may follow Sonarr's order).
func (s *Service) refresh(ctx context.Context, seriesID, tmdbID int64) error {
	if s.TMDB == nil {
		return nil
	}
	tv, err := s.TMDB.TV(ctx, tmdbID, true)
	if err != nil {
		return err
	}
	var genres []string
	for _, g := range tv.Genres {
		genres = append(genres, g.Name)
	}
	var poster string
	s.DB.QueryRow(`SELECT poster FROM series WHERE id = ?`, seriesID).Scan(&poster)
	if poster == "" {
		s.SetArt(seriesID, metadata.Image(tv.PosterPath, "w500"), metadata.Image(tv.BackdropPath, "w1280"), tv.Overview, strings.Join(genres, ", "), "", tv.Status == "Ended" || tv.Status == "Canceled")
	}
	var known int
	s.DB.QueryRow(`SELECT COUNT(*) FROM episodes WHERE series_id = ?`, seriesID).Scan(&known)
	for _, sn := range tv.Seasons {
		eps, err := s.TMDB.Season(ctx, tmdbID, sn.Number, true)
		if err != nil {
			return err
		}
		for _, e := range eps {
			// A new episode is monitored when the series already had episodes
			// (it airs from now on); a new series' monitoring is set by Add.
			if _, err := s.DB.Exec(`INSERT INTO episodes (series_id, season, number, title, air_date, monitored) VALUES (?, ?, ?, ?, ?, 1)
				ON CONFLICT(series_id, season, number) DO UPDATE SET
				title = CASE WHEN excluded.title != '' THEN excluded.title ELSE episodes.title END,
				air_date = CASE WHEN excluded.air_date != '' THEN excluded.air_date ELSE episodes.air_date END`,
				seriesID, e.Season, e.Number, e.Name, e.AirDate); err != nil {
				return err
			}
		}
	}
	_, err = s.DB.Exec(`UPDATE series SET refreshed = ? WHERE id = ?`, db.Stamp(s.Now()), seriesID)
	return err
}

// RefreshDue refreshes, from TMDB, the native series not refreshed for a day.
func (s *Service) RefreshDue(ctx context.Context, limit int) int {
	rows, err := s.DB.Query(`SELECT id, COALESCE(tmdb_id, 0), COALESCE(tvdb_id, 0), category FROM series WHERE category != '' AND kind = 'tv' AND refreshed < ? ORDER BY refreshed LIMIT ?`,
		db.Stamp(s.Now().Add(-24*time.Hour)), limit)
	if err != nil {
		return 0
	}
	type due struct {
		id, tmdb, tvdb int64
		cat            string
	}
	var ds []due
	for rows.Next() {
		var d due
		rows.Scan(&d.id, &d.tmdb, &d.tvdb, &d.cat)
		ds = append(ds, d)
	}
	rows.Close()
	n := 0
	for _, d := range ds {
		if !s.native(d.cat) || s.TMDB == nil {
			continue
		}
		if d.tmdb == 0 && d.tvdb != 0 {
			if id, err := s.TMDB.FindByTVDB(ctx, d.tvdb); err == nil && id != 0 {
				d.tmdb = id
				s.DB.Exec(`UPDATE series SET tmdb_id = ? WHERE id = ? AND NOT EXISTS (SELECT 1 FROM series WHERE tmdb_id = ?)`, id, d.id, id)
			}
		}
		if d.tmdb == 0 {
			s.DB.Exec(`UPDATE series SET refreshed = ? WHERE id = ?`, db.Stamp(s.Now()), d.id)
			continue
		}
		if err := s.refresh(ctx, d.id, d.tmdb); err != nil {
			s.Log.Printf("tv: refreshing %d: %v", d.id, err)
			continue
		}
		n++
	}
	return n
}

// ---------------------------------------------------------------- searching

func (s *Service) perHour() int {
	n := s.Cfg.Get().TV.SearchPerHour
	if n <= 0 {
		n = 20
	}
	return n
}

// Run refreshes series, checks recent releases, searches for wanted episodes
// and follows downloads, every 15 minutes.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		if s.anyNative() {
			s.Follow()
			s.RefreshDue(ctx, 20)
			if n, err := s.RecentSync(ctx); err != nil {
				s.Log.Printf("tv: recent releases: %v", err)
			} else if n > 0 {
				s.Log.Printf("tv: grabbed %d release(s) from recent releases", n)
			}
			if n, err := s.SearchWanted(ctx); err != nil {
				s.Log.Printf("tv: %v", err)
			} else if n > 0 {
				s.Log.Printf("tv: ran %d search(es) for wanted episodes", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) anyNative() bool {
	for _, c := range s.Cfg.Get().Categories {
		if c.Native && c.Kind == "sonarr" {
			return true
		}
	}
	return false
}

// Follow clears downloads that ended: a failed one is blocklisted so the
// next search takes another release.
func (s *Service) Follow() {
	rows, err := s.DB.Query(`SELECT DISTINCT job_id FROM episodes WHERE job_id != ''`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		j, ok := s.Jobs.Get(id)
		switch {
		case !ok:
			s.DB.Exec(`UPDATE episodes SET job_id = '' WHERE job_id = ?`, id)
		case j.State == job.Failed:
			var sid int64
			s.DB.QueryRow(`SELECT series_id FROM episodes WHERE job_id = ? LIMIT 1`, id).Scan(&sid)
			s.block(sid, j.Name, "download failed: "+j.Reason)
			if x, _, err := s.Get(sid); err == nil {
				s.History.Add(history.Event{Kind: "episode", Item: x.Title, Event: history.Failed, Release: j.Name, Category: x.Category, Note: j.Reason})
			}
			s.DB.Exec(`UPDATE episodes SET job_id = '', searched = '', note = ? WHERE job_id = ?`, "download failed: "+j.Reason, id)
		case j.State == job.Cleaned || j.State == job.Imported:
			s.DB.Exec(`UPDATE episodes SET job_id = '' WHERE job_id = ?`, id)
		}
	}
}

func (s *Service) block(seriesID int64, title, reason string) {
	if title == "" || seriesID == 0 {
		return
	}
	s.DB.Exec(`INSERT INTO blocklist (series_id, title, norm, reason, at) VALUES (?, ?, ?, ?, ?)`,
		seriesID, title, library.Normalize(title), reason, db.Stamp(s.Now()))
}

func (s *Service) blocked(seriesID int64) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.Query(`SELECT norm FROM blocklist WHERE series_id = ?`, seriesID)
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

func (s *Service) titles(x Series) []string {
	out := []string{x.Title}
	rows, err := s.DB.Query(`SELECT title FROM aliases WHERE series_id = ?`, x.ID)
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

// Target is what one search is for: a whole season, or some episodes.
type Target struct {
	Series   Series
	Season   int
	Episodes []Episode // the wanted episodes; all of the season for a season search
	Pack     bool
}

func (t Target) label() string {
	if t.Pack {
		return fmt.Sprintf("%s season %d", t.Series.Title, t.Season)
	}
	if len(t.Episodes) == 1 {
		return fmt.Sprintf("%s S%02dE%02d", t.Series.Title, t.Season, t.Episodes[0].Number)
	}
	return fmt.Sprintf("%s season %d (%d episodes)", t.Series.Title, t.Season, len(t.Episodes))
}

// Wanted lists the monitored, aired episodes of native categories that are
// missing or below their profile's cutoff.
func (s *Service) Wanted() ([]Series, map[int64][]Episode, error) {
	list, err := s.List("")
	if err != nil {
		return nil, nil, err
	}
	out := map[int64][]Episode{}
	var keep []Series
	for _, x := range list {
		if !s.native(x.Category) || !x.Monitored {
			continue
		}
		_, eps, err := s.Get(x.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range eps {
			if e.Season > 0 && e.Monitored && (e.Status == "missing" && s.Budget.Aired(e.AirDate) || e.Status == "cutoff unmet") {
				out[x.ID] = append(out[x.ID], e)
			}
		}
		if len(out[x.ID]) > 0 {
			keep = append(keep, x)
		}
	}
	return keep, out, nil
}

// SearchWanted searches for wanted episodes within the hourly budget: a
// whole season at once when every episode of a finished season is missing,
// else episode by episode (missing ones again after 12 hours, upgrades after
// a day).
func (s *Service) SearchWanted(ctx context.Context) (int, error) {
	var used int
	s.DB.QueryRow(`SELECT COUNT(DISTINCT searched) FROM episodes WHERE searched >= ?`, db.Stamp(s.Now().Add(-time.Hour))).Scan(&used)
	left := s.perHour() - used
	if left <= 0 {
		return 0, nil
	}
	series, wanted, err := s.Wanted()
	if err != nil {
		return 0, err
	}
	now := s.Now()
	var targets []Target
	for _, x := range series {
		_, all, _ := s.Get(x.ID)
		bySeason := map[int][]Episode{}
		for _, e := range wanted[x.ID] {
			due := (e.Status == "missing" && now.Sub(e.Searched) > 12*time.Hour) || (e.Status == "cutoff unmet" && now.Sub(e.Searched) > 24*time.Hour)
			if due {
				bySeason[e.Season] = append(bySeason[e.Season], e)
			}
		}
		for sn, eps := range bySeason {
			total, aired, missing := 0, 0, 0
			for _, e := range all {
				if e.Season != sn {
					continue
				}
				total++
				if e.AirDate != "" && e.AirDate <= s.today() {
					aired++
				}
				if e.Status == "missing" {
					missing++
				}
			}
			if total > 1 && aired == total && missing == total && len(eps) == total {
				targets = append(targets, Target{Series: x, Season: sn, Episodes: eps, Pack: true})
				continue
			}
			for _, e := range eps {
				targets = append(targets, Target{Series: x, Season: sn, Episodes: []Episode{e}})
			}
		}
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].Episodes[0].Searched.Before(targets[j].Episodes[0].Searched) })
	n := 0
	for _, t := range targets {
		if n >= left || !s.Budget.Take("tv", t.label()) {
			break
		}
		n++
		if _, _, err := s.searchTarget(ctx, t, true); err != nil {
			s.Log.Printf("tv: %s: %v", t.label(), err)
		}
		// A season pack that found nothing acceptable falls back to the
		// episodes next time (they were searched now, so they wait).
	}
	return n, nil
}

// SearchEpisode searches for one episode (interactive when grab is false).
func (s *Service) SearchEpisode(ctx context.Context, episodeID int64, grab bool) ([]decision.Decision, []indexers.Result, error) {
	var sid int64
	if err := s.DB.QueryRow(`SELECT series_id FROM episodes WHERE id = ?`, episodeID).Scan(&sid); err != nil {
		return nil, nil, catalog.ErrNotFound
	}
	x, eps, err := s.Get(sid)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range eps {
		if e.ID == episodeID {
			return s.searchTarget(ctx, Target{Series: x, Season: e.Season, Episodes: []Episode{e}}, grab)
		}
	}
	return nil, nil, catalog.ErrNotFound
}

// SearchSeason searches for a whole season (interactive when grab is false).
func (s *Service) SearchSeason(ctx context.Context, seriesID int64, season int, grab bool) ([]decision.Decision, []indexers.Result, error) {
	x, eps, err := s.Get(seriesID)
	if err != nil {
		return nil, nil, err
	}
	var in []Episode
	for _, e := range eps {
		if e.Season == season {
			in = append(in, e)
		}
	}
	if len(in) == 0 {
		return nil, nil, fmt.Errorf("season %d has no episodes", season)
	}
	return s.searchTarget(ctx, Target{Series: x, Season: season, Episodes: in, Pack: true}, grab)
}

func (s *Service) want(t Target) decision.Want {
	w := decision.Want{Titles: s.titles(t.Series), Season: t.Season, Runtime: t.Series.Runtime}
	if !t.Pack {
		for _, e := range t.Episodes {
			w.Episodes = append(w.Episodes, e.Number)
			if e.Absolute > 0 {
				w.Absolute = append(w.Absolute, e.Absolute)
			}
		}
		if len(t.Episodes) == 1 && t.Episodes[0].File != "" {
			e := t.Episodes[0]
			w.HaveQuality, w.HaveScore, w.HaveVersion = e.Quality, e.Score, e.Version
		}
	}
	return w
}

func (s *Service) engine(x Series) (*decision.Engine, error) {
	p, err := s.Profiles.ProfileByName(x.Profile)
	if err != nil {
		return nil, fmt.Errorf("no profile named %q", x.Profile)
	}
	return s.Profiles.Engine(p)
}

func candidates(res []indexers.Result, blocked map[string]bool) []decision.Candidate {
	cs := make([]decision.Candidate, len(res))
	for i, x := range res {
		cs[i] = decision.Candidate{Title: x.Title, Size: x.Size, Indexer: x.Indexer, Priority: x.IndexerPrio, Protocol: x.Protocol,
			Seeders: x.Seeders, Published: x.Published, Blocked: blocked[library.Normalize(x.Title)]}
		if x.Freeleech {
			cs[i].Flags = []string{"freeleech"}
		}
	}
	return cs
}

func (s *Service) searchTarget(ctx context.Context, t Target, grab bool) ([]decision.Decision, []indexers.Result, error) {
	stamp := db.Stamp(s.Now())
	for _, e := range t.Episodes {
		s.DB.Exec(`UPDATE episodes SET searched = ? WHERE id = ?`, stamp, e.ID)
	}
	e, err := s.engine(t.Series)
	if err != nil {
		s.noteEps(t.Episodes, err.Error())
		return nil, nil, err
	}
	q := indexers.Query{Text: t.Series.Title, Type: "tv", TVDB: t.Series.TVDB, Season: t.Season}
	if !t.Pack && len(t.Episodes) == 1 {
		q.Episode = t.Episodes[0].Number
		if t.Series.Type == "daily" && t.Episodes[0].AirDate != "" {
			q.Season, q.Episode = 0, 0
			q.Text = t.Series.Title + " " + strings.ReplaceAll(t.Episodes[0].AirDate, "-", " ")
		}
	}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := s.Indexers.Search(sctx, q, "")
	if err != nil {
		return nil, nil, err
	}
	w := s.want(t)
	cs := candidates(res.Results, s.blocked(t.Series.ID))
	ds := e.Decide(w, cs)
	if t.Series.Type == "daily" && !t.Pack && len(t.Episodes) == 1 {
		// Dated releases are matched by air date instead of numbers.
		for i := range ds {
			if ds[i].Info.Daily && ds[i].Info.AirDate != t.Episodes[0].AirDate {
				ds[i].Rejected = append(ds[i].Rejected, "aired "+ds[i].Info.AirDate+", not "+t.Episodes[0].AirDate)
			}
		}
		sort.SliceStable(ds, func(i, j int) bool { return decision.Better(ds[i], ds[j], w) })
	}
	if !grab {
		return ds, res.Results, nil
	}
	if len(ds) == 0 || !ds[0].Accepted() {
		why := "nothing found"
		if len(ds) > 0 {
			why = "nothing acceptable: best was " + ds[0].Candidate.Title + " (" + strings.Join(ds[0].Rejected, "; ") + ")"
		}
		s.noteEps(t.Episodes, why)
		return ds, res.Results, nil
	}
	top := ds[0].Candidate
	for _, x := range res.Results {
		if x.Title == top.Title && x.Indexer == top.Indexer {
			_, err := s.GrabResult(ctx, t.Series.ID, x)
			return ds, res.Results, err
		}
	}
	return ds, res.Results, nil
}

func (s *Service) noteEps(eps []Episode, n string) {
	for _, e := range eps {
		s.DB.Exec(`UPDATE episodes SET note = ? WHERE id = ?`, n, e.ID)
	}
}

// Match finds the series' episodes a release covers.
func (s *Service) Match(seriesID int64, in parser.Info) []Episode {
	x, eps, err := s.Get(seriesID)
	if err != nil {
		return nil
	}
	var out []Episode
	for _, e := range eps {
		switch {
		case in.Daily:
			if e.AirDate == in.AirDate {
				out = append(out, e)
			}
		case in.FullSeason:
			if has(in.Seasons, e.Season) || (len(in.Seasons) == 0 && e.Season > 0) {
				out = append(out, e)
			}
		case len(in.Absolute) > 0:
			if has(in.Absolute, e.Absolute) && e.Absolute > 0 {
				out = append(out, e)
			}
		case len(in.Seasons) == 1:
			if e.Season == in.Seasons[0] && has(in.Episodes, e.Number) {
				out = append(out, e)
			}
		}
	}
	// An anime-style absolute number on a series without absolute numbers
	// reads as an episode of season 1.
	if len(out) == 0 && len(in.Absolute) > 0 && x.Type != "anime" {
		for _, e := range eps {
			if e.Season == 1 && has(in.Absolute, e.Number) {
				out = append(out, e)
			}
		}
	}
	return out
}

func has(v []int, n int) bool {
	for _, x := range v {
		if x == n {
			return true
		}
	}
	return false
}

// GrabResult grabs a release for a series; the episodes it covers are marked
// as downloading.
func (s *Service) GrabResult(ctx context.Context, seriesID int64, r indexers.Result) (job.Job, error) {
	x, _, err := s.Get(seriesID)
	if err != nil {
		return job.Job{}, err
	}
	if !s.native(x.Category) {
		return job.Job{}, fmt.Errorf("%s is not a native TV category", x.Category)
	}
	eps := s.Match(seriesID, parser.Parse(r.Title))
	if len(eps) == 0 {
		return job.Job{}, fmt.Errorf("%s matches none of the series' episodes", r.Title)
	}
	j, err := s.Indexers.Grab(ctx, r, x.Category)
	if err != nil {
		s.noteEps(eps, "grab failed: "+err.Error())
		return job.Job{}, err
	}
	var ids []int64
	for _, e := range eps {
		ids = append(ids, e.ID)
	}
	if err := s.Catalog.SetJobTarget(j.ID, catalog.Target{EpisodeIDs: ids}); err != nil {
		return j, err
	}
	for _, e := range eps {
		s.DB.Exec(`UPDATE episodes SET job_id = ?, note = ? WHERE id = ?`, j.ID, "grabbed "+r.Title, e.ID)
	}
	s.History.Add(history.Event{Kind: "episode", ItemID: eps[0].ID, Item: epItem(&x, eps), Event: history.Grabbed, Release: r.Title,
		Quality: parser.Parse(r.Title).Quality.Name(), Category: x.Category, Note: r.Indexer})
	s.Log.Printf("tv: grabbed %s for %s (%d episode(s))", r.Title, x.Title, len(eps))
	return j, nil
}

// RecentSync reads the newest TV releases from every indexer and grabs those
// that are wanted episodes of native series and that their profile accepts,
// as Sonarr's RSS sync does.
func (s *Service) RecentSync(ctx context.Context) (int, error) {
	series, wanted, err := s.Wanted()
	if err != nil || len(series) == 0 {
		return 0, err
	}
	if !s.Budget.Take("tv-recent", "") {
		return 0, nil
	}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := s.Indexers.Search(sctx, indexers.Query{Type: "tv", Limit: 100}, "")
	if err != nil {
		return 0, err
	}
	// Each release goes to the series whose title it carries.
	byNorm := map[string]Series{}
	for _, x := range series {
		for _, t := range s.titles(x) {
			byNorm[library.Normalize(t)] = x
		}
	}
	type key struct {
		series int64
		season int
		ep     int
	}
	groups := map[key][]indexers.Result{}
	for _, r := range res.Results {
		in := parser.Parse(r.Title)
		x, ok := byNorm[library.Normalize(in.Title)]
		if !ok && in.Year > 0 {
			x, ok = byNorm[library.Normalize(in.Title+" "+strconv.Itoa(in.Year))]
		}
		if !ok {
			continue
		}
		for _, e := range s.Match(x.ID, in) {
			for _, w := range wanted[x.ID] {
				if w.ID == e.ID && w.JobID == "" {
					k := key{x.ID, e.Season, e.Number}
					if in.FullSeason {
						k.ep = 0
					}
					groups[k] = append(groups[k], r)
				}
			}
		}
	}
	n := 0
	for k, rs := range groups {
		var x Series
		for _, y := range series {
			if y.ID == k.series {
				x = y
			}
		}
		e, err := s.engine(x)
		if err != nil {
			continue
		}
		t := Target{Series: x, Season: k.season, Pack: k.ep == 0}
		for _, w := range wanted[x.ID] {
			if w.Season == k.season && (t.Pack || w.Number == k.ep) {
				t.Episodes = append(t.Episodes, w)
			}
		}
		if len(t.Episodes) == 0 || (!t.Pack && t.Episodes[0].JobID != "") {
			continue
		}
		ds := e.Decide(s.want(t), candidates(rs, s.blocked(x.ID)))
		if len(ds) == 0 || !ds[0].Accepted() {
			continue
		}
		for _, r := range rs {
			if r.Title == ds[0].Candidate.Title && r.Indexer == ds[0].Candidate.Indexer {
				if _, err := s.GrabResult(ctx, x.ID, r); err == nil {
					n++
				}
				break
			}
		}
	}
	return n, nil
}

// ---------------------------------------------------------------- import

func pad(n int) string { return fmt.Sprintf("%02d", n) }

// Name builds the series folder, season folder and episode file names.
func (s *Service) Name(x Series, eps []Episode, in parser.Info, ext string) (folder, season, file string) {
	st := s.Cfg.Get().TV
	ff, sf, fl := st.Folder, st.Season, st.File
	if ff == "" {
		ff = "{Title} ({Year})"
	}
	if sf == "" {
		sf = "Season {season}"
	}
	if fl == "" {
		fl = "{Title} - S{season:00}E{episode:00} - {Episode} {Quality}"
	}
	q := in.Quality.Name()
	if in.Quality.Proper {
		q += " Proper"
	} else if in.Quality.Repack {
		q += " Repack"
	}
	first := eps[0]
	epNum := pad(first.Number)
	var titles []string
	for i, e := range eps {
		if i > 0 {
			epNum += "-E" + pad(e.Number)
		}
		if e.Title != "" && (len(titles) == 0 || titles[len(titles)-1] != e.Title) {
			titles = append(titles, e.Title)
		}
	}
	r := strings.NewReplacer("{Title}", safe(x.Title), "{Year}", strconv.Itoa(x.Year), "{season:00}", pad(first.Season), "{season}", strconv.Itoa(first.Season),
		"{episode:00}", epNum, "{episode}", strconv.Itoa(first.Number), "{Episode}", safe(strings.Join(titles, " + ")), "{Quality}", q,
		"{AirDate}", first.AirDate, "{Group}", in.Group, "{absolute}", strconv.Itoa(first.Absolute))
	clean := func(s string) string {
		s = strings.Join(strings.Fields(r.Replace(s)), " ")
		s = strings.ReplaceAll(strings.ReplaceAll(s, "()", ""), "[]", "")
		s = strings.ReplaceAll(s, " - - ", " - ")
		return strings.Trim(strings.TrimSpace(s), "- ")
	}
	return clean(ff), clean(sf), clean(fl) + ext
}

func safe(s string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", " -", "*", "", "?", "", "\"", "'", "<", "", ">", "", "|", "-").Replace(s)
}

// ImportJob is the pipeline's hook for finished jobs (pipeline.Engine.Native):
// each video file of a job HyveMynd grabbed for TV goes to its episode(s).
func (s *Service) ImportJob(ctx context.Context, j job.Job) (own bool, placed []string, left []string, err error) {
	t, err := s.Catalog.JobTarget(j.ID)
	if err != nil || len(t.EpisodeIDs) == 0 {
		return false, nil, nil, nil
	}
	var sid int64
	if s.DB.QueryRow(`SELECT series_id FROM episodes WHERE id = ?`, t.EpisodeIDs[0]).Scan(&sid) != nil {
		return false, nil, nil, nil
	}
	x, all, err := s.Get(sid)
	if err != nil {
		return false, nil, nil, nil
	}
	c, ok := s.Cfg.Get().Category(x.Category)
	if !ok || c.LibraryDir == "" {
		return true, nil, []string{"the category has no TV library folder"}, nil
	}
	targets := map[int64]bool{}
	for _, id := range t.EpisodeIDs {
		targets[id] = true
	}
	jobInfo := parser.Parse(j.Name)
	done := map[int64]bool{}
	for _, f := range j.Files {
		if !s.IsVideo(f) || strings.Contains(strings.ToLower(filepath.Base(f)), "sample") {
			continue
		}
		src := filepath.Join(j.CompleteDir, f)
		st, err := os.Stat(src)
		if err != nil {
			continue // placed on an earlier pass
		}
		in := parser.Parse(filepath.Base(f))
		var eps []Episode
		if in.IsTV() {
			eps = s.Match(sid, in)
		}
		if len(eps) == 0 && len(t.EpisodeIDs) == 1 {
			for _, e := range all {
				if e.ID == t.EpisodeIDs[0] {
					eps = []Episode{e} // a single-episode job with an unreadable file name
				}
			}
		}
		var mine []Episode
		for _, e := range eps {
			if targets[e.ID] {
				mine = append(mine, e)
			}
		}
		if len(mine) == 0 {
			left = append(left, filepath.Base(f)+": not one of the grabbed episodes")
			continue
		}
		if in.Quality.Source == parser.SourceUnknown && jobInfo.Quality.Source != parser.SourceUnknown {
			in.Quality = jobInfo.Quality
		}
		dst, msg, err := s.place(&x, c, mine, in, src, j.Name, st.Size())
		if err != nil {
			return true, placed, left, err
		}
		if strings.HasPrefix(msg, "dry run") {
			for _, m := range mine {
				done[m.ID] = true
			}
		}
		if msg != "" {
			left = append(left, msg)
			continue
		}
		for _, m := range mine {
			done[m.ID] = true
		}
		placed = append(placed, dst)
	}
	for id := range targets {
		if !done[id] {
			s.DB.Exec(`UPDATE episodes SET note = ? WHERE id = ? AND job_id = ?`, "the download had no file for it", id, j.ID)
		}
	}
	s.DB.Exec(`UPDATE episodes SET job_id = '' WHERE job_id = ?`, j.ID)
	if len(placed) == 0 && len(left) == 0 {
		left = append(left, "no video file in the download")
	}
	return true, placed, left, nil
}

func epItem(x *Series, eps []Episode) string {
	if len(eps) == 0 {
		return x.Title
	}
	t := fmt.Sprintf("%s S%02dE%02d", x.Title, eps[0].Season, eps[0].Number)
	if len(eps) > 1 {
		t += fmt.Sprintf("-E%02d", eps[len(eps)-1].Number)
	}
	return t
}

// place moves one file into the library for the episodes it holds, under the
// naming settings (or, in a dry run, only says where it would go). Replaced
// files are set aside, never deleted. msg is set when nothing was moved.
func (s *Service) place(x *Series, c config.Category, mine []Episode, in parser.Info, src, release string, size int64) (dst, msg string, err error) {
	folder, season, file := s.Name(*x, mine, in, strings.ToLower(filepath.Ext(src)))
	dir := x.Folder
	if dir == "" {
		dir = filepath.Join(c.LibraryDir, folder)
	}
	dst = filepath.Join(dir, season, file)
	item := epItem(x, mine)
	if s.Cfg.Get().TV.DryRun {
		msg := "dry run: would move " + filepath.Base(src) + " to " + dst
		s.History.Add(history.Event{Kind: "episode", ItemID: mine[0].ID, Item: item, Event: history.DryRun, Release: release, Quality: in.Quality.Name(), Category: x.Category, Note: msg})
		return "", msg, nil
	}
	if _, err := os.Stat(dst); err == nil && dst != mine[0].File {
		return "", filepath.Base(dst) + " already exists and is not the episode's file", nil
	}
	for _, m := range mine {
		if m.File == "" || m.File == dst {
			continue
		}
		if _, err := os.Stat(m.File); err == nil {
			aside := filepath.Join(c.LibraryDir, ".undermynd-replaced", s.Now().Format("2006-01-02"), filepath.Base(m.File))
			if err := s.Move(m.File, aside); err != nil {
				return "", "", fmt.Errorf("setting aside %s: %w", m.File, err)
			}
			s.History.Add(history.Event{Kind: "episode", ItemID: m.ID, Item: epItem(x, []Episode{m}), Event: history.Replaced, Release: filepath.Base(m.File), Quality: m.Quality, Category: x.Category, Note: "set aside in " + aside})
		}
	}
	if err := s.Move(src, dst); err != nil {
		return "", "", err
	}
	_ = os.Chmod(dst, 0o666)
	_ = os.Chmod(filepath.Dir(dst), 0o777)
	score := 0
	if p, err := s.Profiles.ProfileByName(x.Profile); err == nil {
		if eng, err := s.Profiles.Engine(p); err == nil {
			_, score = eng.Score(in, decision.Candidate{Title: release, Size: size})
		}
	}
	if x.Folder == "" {
		s.DB.Exec(`UPDATE series SET folder = ? WHERE id = ?`, dir, x.ID)
		x.Folder = dir
	}
	for _, m := range mine {
		s.DB.Exec(`UPDATE episodes SET file_path = ?, file_quality = ?, file_score = ?, file_version = ?, file_size = ?, job_id = '', note = ? WHERE id = ?`,
			dst, in.Quality.Name(), score, in.Quality.Version, size/int64(len(mine)), "imported "+filepath.Base(dst), m.ID)
	}
	s.History.Add(history.Event{Kind: "episode", ItemID: mine[0].ID, Item: item, Event: history.Imported, Release: release, Quality: in.Quality.Name(), Category: x.Category, Note: dst})
	return dst, "", nil
}

// ImportFile imports a file by hand (manual import) for some episodes of a series.
func (s *Service) ImportFile(seriesID int64, episodeIDs []int64, path string) (string, error) {
	x, all, err := s.Get(seriesID)
	if err != nil {
		return "", err
	}
	c, ok := s.Cfg.Get().Category(x.Category)
	if !ok || c.LibraryDir == "" {
		return "", fmt.Errorf("%s has no TV library folder", x.Category)
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || !s.IsVideo(path) {
		return "", fmt.Errorf("%s is not a video file", path)
	}
	want := map[int64]bool{}
	for _, id := range episodeIDs {
		want[id] = true
	}
	var mine []Episode
	for _, e := range all {
		if want[e.ID] {
			mine = append(mine, e)
		}
	}
	if len(mine) == 0 {
		return "", errors.New("pick the episodes the file holds")
	}
	in := parser.Parse(filepath.Base(path))
	dst, msg, err := s.place(&x, c, mine, in, path, filepath.Base(path), st.Size())
	if err != nil {
		return "", err
	}
	if msg != "" {
		return "", errors.New(msg)
	}
	return dst, nil
}
