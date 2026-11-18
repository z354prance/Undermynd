// Package music runs native music categories (roadmap H2, Lidarr parity):
// artists and their albums come from MusicBrainz (or once from Lidarr),
// monitored albums that are missing or below the wanted quality are searched
// on the indexers, the best release is grabbed into the pipeline, and the
// finished album is moved into <library>/<Artist>/<Album (Year)>/ as it came.
// Tags are not rewritten: the files keep the tags their release carries.
package music

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/history"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/musicbrainz"
	"github.com/z354prance/undermynd/internal/scheduler"
)

// Indexers is the part of the indexer service music uses.
type Indexers interface {
	Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error)
	Grab(ctx context.Context, r indexers.Result, category string) (job.Job, error)
}

// Jobs reads a job's state.
type Jobs interface {
	Get(id string) (job.Job, bool)
}

// Service keeps the native music library.
type Service struct {
	DB       *sql.DB
	MB       *musicbrainz.Client
	Cfg      *config.Config
	Indexers Indexers
	Jobs     Jobs
	Arr      func(category string) *arr.Client
	Move     func(src, dst string) error
	History  *history.Log
	Budget   *scheduler.Budget
	Log      *log.Logger
	Now      func() time.Time
}

// Qualities are music qualities, lowest first (Lidarr's names).
var Qualities = []string{"Unknown", "MP3-192", "MP3-VBR-V2", "MP3-256", "MP3-VBR-V0", "MP3-320", "AAC-320", "ALAC", "FLAC", "FLAC 24bit"}

func rank(q string) int {
	for i, x := range Qualities {
		if strings.EqualFold(x, q) {
			return i
		}
	}
	return 0
}

var (
	reFlac24 = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:24[ ._-]?bit|24[ ._-]?(?:44|48|88|96|176|192)|hi[ ._-]?res)(?:[^a-z0-9]|$)`)
	reFlac   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:flac|lossless)(?:[^a-z0-9]|$)`)
	reAlac   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])alac(?:[^a-z0-9]|$)`)
	reV0     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])v0(?:[^a-z0-9]|$)`)
	reV2     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])v2(?:[^a-z0-9]|$)`)
	re320    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])320(?:[ ._-]?kbps)?(?:[^a-z0-9]|$)`)
	re256    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])256(?:[ ._-]?kbps)?(?:[^a-z0-9]|$)`)
	re192    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])192(?:[ ._-]?kbps)?(?:[^a-z0-9]|$)`)
	reMP3    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])mp3(?:[^a-z0-9]|$)`)
	reAAC    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:aac|m4a)(?:[^a-z0-9]|$)`)
	reYear   = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d\d)(?:[^0-9]|$)`)
	reTags   = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
	reDisc   = regexp.MustCompile(`(?i)^(cd|disc|disk)\s*\d+$`)
	reDisco  = regexp.MustCompile(`(?i)discograph|collection|anthology|complete[ ._-]works|\bboxset\b|\bbox[ ._-]set\b`)
)

// Quality reads a music release's quality from its name.
func Quality(name string) string {
	switch {
	case reFlac.MatchString(name) && reFlac24.MatchString(name):
		return "FLAC 24bit"
	case reFlac.MatchString(name):
		return "FLAC"
	case reAlac.MatchString(name):
		return "ALAC"
	case reAAC.MatchString(name) && re320.MatchString(name):
		return "AAC-320"
	case reV0.MatchString(name):
		return "MP3-VBR-V0"
	case reV2.MatchString(name):
		return "MP3-VBR-V2"
	case re320.MatchString(name):
		return "MP3-320"
	case re256.MatchString(name):
		return "MP3-256"
	case re192.MatchString(name) || reMP3.MatchString(name):
		return "MP3-192"
	}
	return "Unknown"
}

// QualityOfFile reads a quality from an audio file's extension, as far as it can.
func QualityOfFile(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		return "FLAC"
	case ".m4a", ".alac":
		return "ALAC"
	case ".mp3":
		return "MP3-320"
	}
	return "Unknown"
}

var audioExt = map[string]bool{".flac": true, ".mp3": true, ".m4a": true, ".ogg": true, ".opus": true, ".wav": true,
	".aac": true, ".alac": true, ".ape": true, ".wv": true, ".aiff": true, ".dsf": true}

// IsAudio reports whether a path is an audio file.
func IsAudio(p string) bool { return audioExt[strings.ToLower(filepath.Ext(p))] }

func (s *Service) wanted() (allowed []string, cutoff string) {
	st := s.Cfg.Get().Music
	allowed = st.Qualities
	if len(allowed) == 0 {
		allowed = []string{"FLAC 24bit", "FLAC", "ALAC"}
	}
	cutoff = st.Cutoff
	if cutoff == "" {
		cutoff = allowed[len(allowed)-1]
	}
	return allowed, cutoff
}

func allowedQ(allowed []string, q string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, q) {
			return true
		}
	}
	return false
}

// Artist is an artist with album counts.
type Artist struct {
	ID        int64  `json:"id"`
	MBID      string `json:"mbid"`
	Name      string `json:"name"`
	SortName  string `json:"sortName"`
	Folder    string `json:"folder"`
	Monitored bool   `json:"monitored"`
	Category  string `json:"category"`
	Albums    int    `json:"albums"` // monitored, released
	Have      int    `json:"have"`
	Size      int64  `json:"size"`
	Poster    string `json:"poster"`
	Fanart    string `json:"fanart"`
	Overview  string `json:"overview"`
}

// Album is an album with its state.
type Album struct {
	ID          int64     `json:"id"`
	ArtistID    int64     `json:"artistId"`
	MBID        string    `json:"mbid"`
	Title       string    `json:"title"`
	Year        int       `json:"year"`
	ReleaseDate string    `json:"releaseDate"`
	Type        string    `json:"type"`
	Secondary   string    `json:"secondary"`
	Monitored   bool      `json:"monitored"`
	Tracks      int       `json:"tracks"`
	Folder      string    `json:"folder"`
	Quality     string    `json:"quality"`
	Files       int       `json:"files"`
	Size        int64     `json:"size"`
	JobID       string    `json:"jobId"`
	Searched    time.Time `json:"searched"`
	Note        string    `json:"note"`
	Cover       string    `json:"cover"`
	Status      string    `json:"status"` // have, partial, missing, downloading, cutoff unmet, unreleased, unmonitored
}

const albumCols = `id, artist_id, mbid, title, year, release_date, type, secondary, monitored, tracks, folder, file_quality, file_count, file_size, job_id, searched, note`

func scanAlbum(r interface{ Scan(...any) error }) (Album, error) {
	var a Album
	var searched string
	err := r.Scan(&a.ID, &a.ArtistID, &a.MBID, &a.Title, &a.Year, &a.ReleaseDate, &a.Type, &a.Secondary, &a.Monitored, &a.Tracks,
		&a.Folder, &a.Quality, &a.Files, &a.Size, &a.JobID, &searched, &a.Note)
	if searched != "" {
		a.Searched, _ = time.Parse(db.TimeFormat, searched)
	}
	a.Cover = musicbrainz.Cover(a.MBID)
	return a, err
}

func (s *Service) status(a *Album, artistMonitored bool, cutoff string) {
	today := s.Now().Format("2006-01-02")
	switch {
	case a.JobID != "":
		a.Status = "downloading"
	case a.Files > 0 && a.Tracks > 0 && a.Files < a.Tracks:
		a.Status = "partial"
	case a.Files > 0:
		a.Status = "have"
		if artistMonitored && a.Monitored && rank(a.Quality) < rank(cutoff) {
			a.Status = "cutoff unmet"
		}
	case a.ReleaseDate == "" || a.ReleaseDate > today:
		a.Status = "unreleased"
	case !a.Monitored || !artistMonitored:
		a.Status = "unmonitored"
	default:
		a.Status = "missing"
	}
}

// Artists lists a category's artists (all when empty) with their counts.
func (s *Service) Artists(category string) ([]Artist, error) {
	q := `SELECT id, mbid, name, sort_name, folder, monitored, category, poster, fanart, overview FROM artists`
	var args []any
	if category != "" {
		q += ` WHERE category = ?`
		args = append(args, category)
	}
	rows, err := s.DB.Query(q+` ORDER BY COALESCE(NULLIF(sort_name, ''), name) COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	var out []Artist
	for rows.Next() {
		var a Artist
		if err := rows.Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.Folder, &a.Monitored, &a.Category, &a.Poster, &a.Fanart, &a.Overview); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	rows.Close()
	_, cutoff := s.wanted()
	all, err := s.allAlbums()
	if err != nil {
		return nil, err
	}
	for i := range out {
		for _, al := range all[out[i].ID] {
			s.status(&al, out[i].Monitored, cutoff)
			out[i].Size += al.Size
			switch al.Status {
			case "have", "cutoff unmet":
				out[i].Have++
				out[i].Albums++
			case "missing", "partial", "downloading":
				out[i].Albums++
			}
		}
	}
	return out, nil
}

func (s *Service) allAlbums() (map[int64][]Album, error) {
	rows, err := s.DB.Query(`SELECT ` + albumCols + ` FROM albums`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Album{}
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, err
		}
		out[a.ArtistID] = append(out[a.ArtistID], a)
	}
	return out, rows.Err()
}

// Artist returns an artist and its albums, newest first.
func (s *Service) Artist(id int64) (Artist, []Album, error) {
	var a Artist
	err := s.DB.QueryRow(`SELECT id, mbid, name, sort_name, folder, monitored, category, poster, fanart, overview FROM artists WHERE id = ?`, id).
		Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.Folder, &a.Monitored, &a.Category, &a.Poster, &a.Fanart, &a.Overview)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil, errors.New("no such artist")
	} else if err != nil {
		return a, nil, err
	}
	_, cutoff := s.wanted()
	rows, err := s.DB.Query(`SELECT `+albumCols+` FROM albums WHERE artist_id = ? ORDER BY release_date DESC, title`, id)
	if err != nil {
		return a, nil, err
	}
	defer rows.Close()
	var out []Album
	for rows.Next() {
		al, err := scanAlbum(rows)
		if err != nil {
			return a, nil, err
		}
		s.status(&al, a.Monitored, cutoff)
		out = append(out, al)
	}
	return a, out, rows.Err()
}

func (s *Service) album(id int64) (Artist, Album, error) {
	var aid int64
	if err := s.DB.QueryRow(`SELECT artist_id FROM albums WHERE id = ?`, id).Scan(&aid); err != nil {
		return Artist{}, Album{}, errors.New("no such album")
	}
	ar, als, err := s.Artist(aid)
	if err != nil {
		return ar, Album{}, err
	}
	for _, al := range als {
		if al.ID == id {
			return ar, al, nil
		}
	}
	return ar, Album{}, errors.New("no such album")
}

// MonitorArtist turns an artist's monitoring on or off.
func (s *Service) MonitorArtist(id int64, on bool) error {
	_, err := s.DB.Exec(`UPDATE artists SET monitored = ? WHERE id = ?`, on, id)
	return err
}

// MonitorAlbum turns an album's monitoring on or off.
func (s *Service) MonitorAlbum(id int64, on bool) error {
	_, err := s.DB.Exec(`UPDATE albums SET monitored = ? WHERE id = ?`, on, id)
	return err
}

// RemoveArtist forgets an artist. The files stay on disk.
func (s *Service) RemoveArtist(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM artists WHERE id = ?`, id)
	return err
}

func (s *Service) native(category string) bool {
	c, ok := s.Cfg.Get().Category(category)
	return ok && c.Native && c.Kind == "lidarr"
}

// AddArtist adds an artist by MusicBrainz id to a native category, with its
// albums: studio albums monitored, EPs, singles and others not.
func (s *Service) AddArtist(ctx context.Context, category, mbid string) (Artist, error) {
	if !s.native(category) {
		return Artist{}, fmt.Errorf("%s is not a native music category", category)
	}
	a, err := s.MB.Artist(ctx, mbid)
	if err != nil {
		return Artist{}, err
	}
	var id int64
	err = s.DB.QueryRow(`SELECT id FROM artists WHERE mbid = ?`, mbid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		r, err := s.DB.Exec(`INSERT INTO artists (mbid, name, sort_name, monitored, category, added) VALUES (?, ?, ?, 1, ?, ?)`,
			mbid, a.Name, a.SortName, category, db.Stamp(s.Now()))
		if err != nil {
			return Artist{}, err
		}
		id, _ = r.LastInsertId()
	} else if err != nil {
		return Artist{}, err
	}
	if err := s.refresh(ctx, id, mbid, true); err != nil {
		return Artist{}, err
	}
	ar, _, err := s.Artist(id)
	return ar, err
}

// refresh adds the artist's albums MusicBrainz knows and HyveMynd not yet.
// first marks the studio albums monitored; later new albums are monitored
// when they are studio albums released after the artist was added.
func (s *Service) refresh(ctx context.Context, artistID int64, mbid string, first bool) error {
	als, err := s.MB.Albums(ctx, mbid)
	if err != nil {
		return err
	}
	var added string
	s.DB.QueryRow(`SELECT added FROM artists WHERE id = ?`, artistID).Scan(&added)
	for _, al := range als {
		studio := al.PrimaryType == "Album" && al.Studio()
		mon := studio && (first || al.FirstRelease >= strings.SplitN(added, "T", 2)[0])
		if _, err := s.DB.Exec(`INSERT INTO albums (artist_id, mbid, title, year, release_date, type, secondary, monitored) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(mbid) DO UPDATE SET title = excluded.title, year = excluded.year, release_date = excluded.release_date,
			type = excluded.type, secondary = excluded.secondary`,
			artistID, al.ID, al.Title, al.Year(), al.FirstRelease, al.PrimaryType, strings.Join(al.SecondaryTypes, ", "), mon); err != nil {
			return err
		}
	}
	_, err = s.DB.Exec(`UPDATE artists SET refreshed = ? WHERE id = ?`, db.Stamp(s.Now()), artistID)
	return err
}

// RefreshDue refreshes artists not refreshed for a week.
func (s *Service) RefreshDue(ctx context.Context, limit int) int {
	rows, err := s.DB.Query(`SELECT id, mbid, category FROM artists WHERE refreshed < ? ORDER BY refreshed LIMIT ?`, db.Stamp(s.Now().AddDate(0, 0, -7)), limit)
	if err != nil {
		return 0
	}
	type due struct {
		id        int64
		mbid, cat string
	}
	var ds []due
	for rows.Next() {
		var d due
		rows.Scan(&d.id, &d.mbid, &d.cat)
		ds = append(ds, d)
	}
	rows.Close()
	n := 0
	for _, d := range ds {
		if !s.native(d.cat) {
			continue
		}
		if err := s.refresh(ctx, d.id, d.mbid, false); err == nil {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- Lidarr

type lidarrArtist struct {
	ID        int64  `json:"id"`
	Name      string `json:"artistName"`
	SortName  string `json:"sortName"`
	MBID      string `json:"foreignArtistId"`
	Path      string `json:"path"`
	Monitored bool   `json:"monitored"`
	Overview  string `json:"overview"`
	Images    []struct {
		CoverType string `json:"coverType"`
		RemoteURL string `json:"remoteUrl"`
	} `json:"images"`
}

type lidarrAlbum struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	MBID           string `json:"foreignAlbumId"`
	ReleaseDate    string `json:"releaseDate"`
	AlbumType      string `json:"albumType"`
	SecondaryTypes []struct {
		Name string `json:"name"`
	} `json:"secondaryTypes"`
	Monitored  bool `json:"monitored"`
	Statistics struct {
		TrackFileCount int   `json:"trackFileCount"`
		TrackCount     int   `json:"trackCount"`
		TotalTracks    int   `json:"totalTrackCount"`
		Size           int64 `json:"sizeOnDisk"`
	} `json:"statistics"`
}

type lidarrFile struct {
	AlbumID int64  `json:"albumId"`
	Path    string `json:"path"`
	Quality struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
	} `json:"quality"`
}

// ImportResult says what an import from Lidarr did.
type ImportResult struct {
	Artists  int      `json:"artists"`
	Albums   int      `json:"albums"`
	Problems []string `json:"problems,omitempty"`
}

// Import reads a category's library from Lidarr: artists, their albums, what
// is monitored and which albums have files (with their folder and quality).
func (s *Service) Import(ctx context.Context, category string) (ImportResult, error) {
	var res ImportResult
	c, ok := s.Cfg.Get().Category(category)
	if !ok || c.Kind != "lidarr" || c.ArrURL == "" {
		return res, fmt.Errorf("%s is not a Lidarr category", category)
	}
	if c.LibraryDir == "" {
		return res, fmt.Errorf("set the music library folder of %s first", category)
	}
	cl := s.Arr(category)
	var artists []lidarrArtist
	if err := cl.Get(ctx, "/artist", &artists); err != nil {
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
	now := db.Stamp(s.Now())
	for _, la := range artists {
		if la.MBID == "" {
			continue
		}
		if _, err := s.DB.Exec(`INSERT INTO artists (mbid, name, sort_name, folder, monitored, category, added, refreshed) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(mbid) DO UPDATE SET name = excluded.name, sort_name = excluded.sort_name, folder = excluded.folder,
			monitored = excluded.monitored, category = excluded.category`,
			la.MBID, la.Name, la.SortName, local(la.Path), la.Monitored, category, now, now); err != nil {
			return res, err
		}
		var aid int64
		s.DB.QueryRow(`SELECT id FROM artists WHERE mbid = ?`, la.MBID).Scan(&aid)
		var poster, fanart string
		for _, im := range la.Images {
			switch im.CoverType {
			case "poster":
				poster = im.RemoteURL
			case "fanart":
				fanart = im.RemoteURL
			}
		}
		s.DB.Exec(`UPDATE artists SET poster = ?, fanart = ?, overview = ? WHERE id = ?`, poster, fanart, la.Overview, aid)
		res.Artists++
		var als []lidarrAlbum
		if err := cl.Get(ctx, "/album?artistId="+strconv.FormatInt(la.ID, 10), &als); err != nil {
			res.Problems = append(res.Problems, la.Name+": albums: "+err.Error())
			continue
		}
		var files []lidarrFile
		cl.Get(ctx, "/trackfile?artistId="+strconv.FormatInt(la.ID, 10), &files)
		byAlbum := map[int64][]lidarrFile{}
		for _, f := range files {
			byAlbum[f.AlbumID] = append(byAlbum[f.AlbumID], f)
		}
		for _, al := range als {
			if al.MBID == "" {
				continue
			}
			var sec []string
			for _, x := range al.SecondaryTypes {
				sec = append(sec, x.Name)
			}
			year := 0
			date := ""
			if len(al.ReleaseDate) >= 10 {
				date = al.ReleaseDate[:10]
				year, _ = strconv.Atoi(date[:4])
			}
			folder, quality := "", ""
			if fs := byAlbum[al.ID]; len(fs) > 0 {
				folder = local(filepath.Dir(fs[0].Path))
				quality = fs[0].Quality.Quality.Name
				for _, f := range fs {
					if rank(f.Quality.Quality.Name) < rank(quality) {
						quality = f.Quality.Quality.Name // the album is as good as its worst track
					}
				}
			}
			tracks := al.Statistics.TotalTracks
			if tracks == 0 {
				tracks = al.Statistics.TrackCount
			}
			if _, err := s.DB.Exec(`INSERT INTO albums (artist_id, mbid, title, year, release_date, type, secondary, monitored, tracks, folder, file_quality, file_count, file_size)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(mbid) DO UPDATE SET artist_id = excluded.artist_id, title = excluded.title, year = excluded.year, release_date = excluded.release_date,
				type = excluded.type, secondary = excluded.secondary, monitored = excluded.monitored, tracks = excluded.tracks, folder = excluded.folder,
				file_quality = excluded.file_quality, file_count = excluded.file_count, file_size = excluded.file_size`,
				aid, al.MBID, al.Title, year, date, al.AlbumType, strings.Join(sec, ", "), al.Monitored, tracks, folder, quality,
				al.Statistics.TrackFileCount, al.Statistics.Size); err != nil {
				return res, err
			}
			res.Albums++
		}
	}
	s.Log.Printf("music: imported %d artists and %d albums from %s", res.Artists, res.Albums, category)
	return res, nil
}

// ---------------------------------------------------------------- searching

func (s *Service) perHour() int {
	n := s.Cfg.Get().Music.SearchPerHour
	if n <= 0 {
		n = 10
	}
	return n
}

// Run follows downloads, refreshes artists and searches for wanted albums
// every half hour.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		if s.anyNative() {
			s.Follow()
			s.RefreshDue(ctx, 10)
			if n, err := s.SearchWanted(ctx); err != nil {
				s.Log.Printf("music: %v", err)
			} else if n > 0 {
				s.Log.Printf("music: searched %d wanted album(s)", n)
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
		if c.Native && c.Kind == "lidarr" {
			return true
		}
	}
	return false
}

// Follow clears downloads that ended; a failed one is blocklisted.
func (s *Service) Follow() {
	rows, err := s.DB.Query(`SELECT id, job_id FROM albums WHERE job_id != ''`)
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
			s.DB.Exec(`UPDATE albums SET job_id = '' WHERE id = ?`, p.id)
		case j.State == job.Failed:
			s.DB.Exec(`INSERT INTO blocklist (album_id, title, norm, reason, at) VALUES (?, ?, ?, ?, ?)`, p.id, j.Name, library.Normalize(j.Name), "download failed: "+j.Reason, db.Stamp(s.Now()))
			s.DB.Exec(`UPDATE albums SET job_id = '', searched = '', note = ? WHERE id = ?`, "download failed: "+j.Reason, p.id)
			if ar, al, err := s.album(p.id); err == nil {
				s.History.Add(history.Event{Kind: "album", ItemID: p.id, Item: ar.Name + " - " + al.Title, Event: history.Failed, Release: j.Name, Category: ar.Category, Note: j.Reason})
			}
		case j.State == job.Cleaned || j.State == job.Imported:
			s.DB.Exec(`UPDATE albums SET job_id = '' WHERE id = ? AND job_id = ?`, p.id, p.job)
		}
	}
}

func (s *Service) blocked(albumID int64) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.Query(`SELECT norm FROM blocklist WHERE album_id = ?`, albumID)
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

// Wanted lists monitored, released albums of native categories that are
// missing, partial or below the cutoff.
func (s *Service) Wanted() ([]Artist, []Album, error) {
	ars, err := s.Artists("")
	if err != nil {
		return nil, nil, err
	}
	var outA []Artist
	var out []Album
	for _, ar := range ars {
		if !s.native(ar.Category) || !ar.Monitored {
			continue
		}
		_, als, err := s.Artist(ar.ID)
		if err != nil {
			return nil, nil, err
		}
		n := 0
		for _, al := range als {
			if al.Status == "missing" || al.Status == "partial" || al.Status == "cutoff unmet" {
				out = append(out, al)
				n++
			}
		}
		if n > 0 {
			outA = append(outA, ar)
		}
	}
	return outA, out, nil
}

// Verdict is a decision on one release for an album.
type Verdict struct {
	Result   indexers.Result `json:"result"`
	Quality  string          `json:"quality"`
	Rejected []string        `json:"rejected"`
}

// Decide judges releases for an album, best first.
func (s *Service) Decide(ar Artist, al Album, rs []indexers.Result) []Verdict {
	allowed, _ := s.wanted()
	blocked := s.blocked(al.ID)
	artistN, albumN := library.Normalize(ar.Name), library.Normalize(al.Title)
	var out []Verdict
	for _, r := range rs {
		v := Verdict{Result: r, Quality: Quality(r.Title)}
		no := func(f string, a ...any) { v.Rejected = append(v.Rejected, fmt.Sprintf(f, a...)) }
		name := library.Normalize(reTags.ReplaceAllString(r.Title, " "))
		if !strings.Contains(" "+name+" ", " "+artistN+" ") {
			no("not by %s", ar.Name)
		}
		if !strings.Contains(" "+name+" ", " "+albumN+" ") {
			no("not %s", al.Title)
		}
		if reDisco.MatchString(r.Title) && !reDisco.MatchString(al.Title) {
			no("a discography or collection")
		}
		if m := reYear.FindAllStringSubmatch(r.Title, -1); al.Year > 0 && len(m) > 0 {
			ok := false
			for _, y := range m {
				if n, _ := strconv.Atoi(y[1]); n >= al.Year-1 && n <= al.Year+1 || strings.Contains(al.Title, y[1]) {
					ok = true
				}
			}
			if !ok {
				no("year %s, the album is %d", m[0][1], al.Year)
			}
		}
		if !allowedQ(allowed, v.Quality) {
			no("%s is not wanted", v.Quality)
		}
		if al.Files > 0 && al.Files >= al.Tracks && rank(v.Quality) <= rank(al.Quality) {
			no("not better than the %s on disk", al.Quality)
		}
		if r.Protocol == "torrent" && r.Seeders < 1 {
			no("no seeders")
		}
		if blocked[library.Normalize(r.Title)] {
			no("on the blocklist")
		}
		if r.Size > 0 && r.Size < 10<<20 {
			no("only %d MB", r.Size>>20)
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (len(a.Rejected) == 0) != (len(b.Rejected) == 0) {
			return len(a.Rejected) == 0
		}
		if rank(a.Quality) != rank(b.Quality) {
			return rank(a.Quality) > rank(b.Quality)
		}
		if a.Result.IndexerPrio != b.Result.IndexerPrio {
			return a.Result.IndexerPrio < b.Result.IndexerPrio
		}
		if a.Result.Seeders != b.Result.Seeders {
			return a.Result.Seeders > b.Result.Seeders
		}
		return a.Result.Size > b.Result.Size
	})
	return out
}

// Search looks for one album; with grab, the best accepted release is grabbed.
func (s *Service) Search(ctx context.Context, albumID int64, grab bool) ([]Verdict, error) {
	ar, al, err := s.album(albumID)
	if err != nil {
		return nil, err
	}
	s.DB.Exec(`UPDATE albums SET searched = ? WHERE id = ?`, db.Stamp(s.Now()), albumID)
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := s.Indexers.Search(sctx, indexers.Query{Type: "music", Text: ar.Name + " " + al.Title}, "")
	if err != nil {
		return nil, err
	}
	vs := s.Decide(ar, al, res.Results)
	if !grab {
		return vs, nil
	}
	if len(vs) == 0 || len(vs[0].Rejected) > 0 {
		why := "nothing found"
		if len(vs) > 0 {
			why = "nothing acceptable: best was " + vs[0].Result.Title + " (" + strings.Join(vs[0].Rejected, "; ") + ")"
		}
		s.DB.Exec(`UPDATE albums SET note = ? WHERE id = ?`, why, albumID)
		return vs, nil
	}
	_, err = s.GrabResult(ctx, albumID, vs[0].Result)
	return vs, err
}

// SearchWanted searches for wanted albums within the hourly limits: missing
// ones again after a day, upgrades after a week.
func (s *Service) SearchWanted(ctx context.Context) (int, error) {
	var used int
	s.DB.QueryRow(`SELECT COUNT(*) FROM albums WHERE searched >= ?`, db.Stamp(s.Now().Add(-time.Hour))).Scan(&used)
	left := s.perHour() - used
	if left <= 0 {
		return 0, nil
	}
	_, als, err := s.Wanted()
	if err != nil {
		return 0, err
	}
	now := s.Now()
	var due []Album
	for _, al := range als {
		wait := 24 * time.Hour
		if al.Status == "cutoff unmet" {
			wait = 7 * 24 * time.Hour
		}
		if now.Sub(al.Searched) > wait {
			due = append(due, al)
		}
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].Searched.Before(due[j].Searched) })
	n := 0
	for _, al := range due {
		if n >= left || !s.Budget.Take("music", al.Title) {
			break
		}
		n++
		if _, err := s.Search(ctx, al.ID, true); err != nil {
			s.Log.Printf("music: %s: %v", al.Title, err)
		}
	}
	return n, nil
}

// GrabResult grabs a release for an album.
func (s *Service) GrabResult(ctx context.Context, albumID int64, r indexers.Result) (job.Job, error) {
	ar, al, err := s.album(albumID)
	if err != nil {
		return job.Job{}, err
	}
	if !s.native(ar.Category) {
		return job.Job{}, fmt.Errorf("%s is not a native music category", ar.Category)
	}
	j, err := s.Indexers.Grab(ctx, r, ar.Category)
	if err != nil {
		s.DB.Exec(`UPDATE albums SET note = ? WHERE id = ?`, "grab failed: "+err.Error(), albumID)
		return job.Job{}, err
	}
	s.DB.Exec(`UPDATE albums SET job_id = ?, note = ? WHERE id = ?`, j.ID, "grabbed "+r.Title, albumID)
	s.History.Add(history.Event{Kind: "album", ItemID: albumID, Item: ar.Name + " - " + al.Title, Event: history.Grabbed, Release: r.Title, Quality: Quality(r.Title), Category: ar.Category, Note: r.Indexer})
	s.Log.Printf("music: grabbed %s for %s - %s", r.Title, ar.Name, al.Title)
	return j, nil
}

// ---------------------------------------------------------------- import

func safe(s string) string {
	return strings.TrimSpace(strings.NewReplacer("/", "-", "\\", "-", ":", " -", "*", "", "?", "", "\"", "'", "<", "", ">", "", "|", "-").Replace(s))
}

// Folder is the album's folder under the artist's.
func (s *Service) Folder(al Album) string {
	f := s.Cfg.Get().Music.Folder
	if f == "" {
		f = "{Album} ({Year})"
	}
	out := strings.NewReplacer("{Album}", safe(al.Title), "{Year}", strconv.Itoa(al.Year), "{Type}", al.Type).Replace(f)
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(out, "(0)", ""), "()", ""))
}

// ImportJob is the pipeline's hook for finished jobs: the audio files of an
// album HyveMynd grabbed go into the album's folder, as they came.
func (s *Service) ImportJob(ctx context.Context, j job.Job) (own bool, placed []string, left []string, err error) {
	var id int64
	if s.DB.QueryRow(`SELECT id FROM albums WHERE job_id = ?`, j.ID).Scan(&id) != nil {
		return false, nil, nil, nil
	}
	ar, al, err := s.album(id)
	if err != nil {
		return false, nil, nil, nil
	}
	c, ok := s.Cfg.Get().Category(ar.Category)
	if !ok || c.LibraryDir == "" {
		return true, nil, []string{"the category has no music library folder"}, nil
	}
	var audio, extras []string
	for _, f := range j.Files {
		switch {
		case IsAudio(f):
			audio = append(audio, f)
		case strings.EqualFold(filepath.Ext(f), ".jpg") || strings.EqualFold(filepath.Ext(f), ".png") || strings.EqualFold(filepath.Ext(f), ".cue") || strings.EqualFold(filepath.Ext(f), ".log"):
			extras = append(extras, f)
		}
	}
	if len(audio) == 0 {
		return true, nil, []string{"no audio files in the download"}, nil
	}
	if al.Tracks > 0 && len(audio)*2 < al.Tracks {
		return true, nil, []string{fmt.Sprintf("only %d of %d tracks", len(audio), al.Tracks)}, nil
	}
	artistDir := ar.Folder
	if artistDir == "" {
		artistDir = filepath.Join(c.LibraryDir, safe(ar.Name))
	}
	dir := filepath.Join(artistDir, s.Folder(al))
	quality := Quality(j.Name)
	if quality == "Unknown" {
		quality = QualityOfFile(audio[0])
	}
	item := ar.Name + " - " + al.Title
	if s.Cfg.Get().Music.DryRun {
		msg := fmt.Sprintf("dry run: would move %d track(s) to %s", len(audio), dir)
		if al.Folder != "" && al.Files > 0 {
			msg += " and set aside " + al.Folder
		}
		s.DB.Exec(`UPDATE albums SET job_id = '', note = ? WHERE id = ?`, msg, id)
		s.History.Add(history.Event{Kind: "album", ItemID: id, Item: item, Event: history.DryRun, Release: j.Name, Quality: quality, Category: ar.Category, Note: msg})
		return true, nil, []string{msg}, nil
	}
	// The album's old files are set aside, never deleted.
	if al.Folder != "" && al.Files > 0 {
		if _, err := os.Stat(al.Folder); err == nil {
			aside := filepath.Join(c.LibraryDir, ".undermynd-replaced", s.Now().Format("2006-01-02"), safe(ar.Name)+" - "+filepath.Base(al.Folder))
			if err := s.Move(al.Folder, aside); err != nil {
				return true, nil, nil, fmt.Errorf("setting aside %s: %w", al.Folder, err)
			}
			s.History.Add(history.Event{Kind: "album", ItemID: id, Item: item, Event: history.Replaced, Quality: al.Quality, Category: ar.Category, Note: "set aside in " + aside})
		}
	}
	// Disc folders are kept ("CD1/01 Track.flac"); the release's own top
	// folder is not.
	common := commonDir(append(append([]string{}, audio...), extras...))
	var size int64
	for _, f := range append(audio, extras...) {
		rel := strings.TrimPrefix(strings.TrimPrefix(f, common), "/")
		dst := filepath.Join(dir, rel)
		if err := s.Move(filepath.Join(j.CompleteDir, f), dst); err != nil {
			return true, placed, nil, err
		}
		_ = os.Chmod(dst, 0o666)
		if st, err := os.Stat(dst); err == nil && IsAudio(f) {
			size += st.Size()
		}
		placed = append(placed, dst)
	}
	_ = os.Chmod(dir, 0o777)
	if ar.Folder == "" {
		s.DB.Exec(`UPDATE artists SET folder = ? WHERE id = ?`, artistDir, ar.ID)
	}
	tracks := al.Tracks
	if tracks == 0 {
		tracks = len(audio)
	}
	s.DB.Exec(`UPDATE albums SET folder = ?, file_quality = ?, file_count = ?, file_size = ?, tracks = ?, job_id = '', note = ? WHERE id = ?`,
		dir, quality, len(audio), size, tracks, fmt.Sprintf("imported %d track(s)", len(audio)), id)
	s.History.Add(history.Event{Kind: "album", ItemID: id, Item: item, Event: history.Imported, Release: j.Name, Quality: quality, Category: ar.Category, Note: dir})
	s.Log.Printf("music: imported %s", dir)
	return true, []string{dir}, nil, nil
}

// commonDir is the folder every path starts with ("" when none).
func commonDir(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	dir := filepath.Dir(paths[0])
	for dir != "." && dir != "/" && dir != "" {
		all := true
		for _, p := range paths {
			if !strings.HasPrefix(p, dir+"/") {
				all = false
				break
			}
		}
		if all {
			// A disc folder ("CD1") is part of the album, not of the release.
			if reDisc.MatchString(filepath.Base(dir)) {
				if d := filepath.Dir(dir); d != "." {
					return d
				}
				return ""
			}
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
