// Package requests keeps media requests in Undermynd (roadmap E1): the public
// page searches TMDB, a request waits in the admin's approval queue, and an
// approved request is handed on: movies to Radarr, shows to Sonarr, anime to
// the Anime page. Its answers have the same shapes as the Seerr ones, so the
// public page and the admin queue work the same in either mode.
package requests

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/movies"
	"github.com/z354prance/undermynd/internal/seerr"
	"github.com/z354prance/undermynd/internal/shows"
)

// Seerr's media statuses, kept for the public page.
const (
	statusPending    = 2
	statusProcessing = 3
	statusPartial    = 4
	statusAvailable  = 5
)

// Service keeps native requests.
type Service struct {
	DB    *sql.DB
	TMDB  *metadata.TMDB
	Meta  *metadata.Service
	Cfg   *config.Config
	Shows *shows.Service
	Log   *log.Logger
	Now   func() time.Time
	// Arr returns a client for a category's arr; nil when it has none.
	Arr func(category string) *arr.Client
	// Movies keeps native movie categories (roadmap F4); approved movies for
	// a native category go there instead of Radarr.
	Movies *movies.Service

	mu      sync.Mutex
	lib     map[string]libCache // by arr kind
	discov  map[string][]seerr.SearchResult
	discovT map[string]time.Time
}

type libCache struct {
	at    time.Time
	byTMD map[int64]arr.Have
}

func (s *Service) category(kind string) string {
	r := s.Cfg.Get().Requests
	want := r.MovieCategory
	arrKind := "radarr"
	if kind == "tv" {
		want, arrKind = r.TVCategory, "sonarr"
	}
	for _, c := range s.Cfg.Get().Categories {
		if c.Kind == arrKind && (want == "" || c.Name == want) && c.ArrURL != "" {
			return c.Name
		}
	}
	return ""
}

func (s *Service) nativeMovies(cat string) bool {
	c, ok := s.Cfg.Get().Category(cat)
	return ok && c.Native
}

func (s *Service) client(kind string) *arr.Client {
	cat := s.category(kind)
	if cat == "" || s.Arr == nil {
		return nil
	}
	return s.Arr(cat)
}

// library is the arr's library by TMDB id, read at most every 10 minutes.
func (s *Service) library(ctx context.Context, kind string) map[int64]arr.Have {
	if cat := s.category(kind); kind == "movie" && s.Movies != nil && s.nativeMovies(cat) {
		ms, err := s.Movies.List(cat)
		if err != nil {
			return nil
		}
		by := map[int64]arr.Have{}
		for _, m := range ms {
			by[m.TMDB] = arr.Have{TMDBID: m.TMDB, Monitored: m.Monitored, HasFile: m.File != ""}
		}
		return by
	}
	c := s.client(kind)
	if c == nil {
		return nil
	}
	s.mu.Lock()
	if s.lib == nil {
		s.lib = map[string]libCache{}
	}
	lc, ok := s.lib[kind]
	s.mu.Unlock()
	if ok && s.Now().Sub(lc.at) < 10*time.Minute {
		return lc.byTMD
	}
	list, err := c.Library(ctx)
	if err != nil {
		s.Log.Printf("requests: %s library: %v", c.Kind, err)
		return lc.byTMD
	}
	by := map[int64]arr.Have{}
	for _, h := range list {
		if h.TMDBID > 0 {
			by[h.TMDBID] = h
		}
	}
	s.mu.Lock()
	s.lib[kind] = libCache{at: s.Now(), byTMD: by}
	s.mu.Unlock()
	return by
}

func (s *Service) forgetLibrary(kind string) {
	s.mu.Lock()
	delete(s.lib, kind)
	s.mu.Unlock()
}

// status says where a movie or show stands: requested, in the library
// (partly), or nothing (0).
func (s *Service) status(ctx context.Context, kind string, tmdbID int64) int {
	var st string
	err := s.DB.QueryRow(`SELECT status FROM requests WHERE tmdb_id = ? AND kind IN (?, 'anime') AND status IN ('pending', 'approved', 'available') ORDER BY id DESC LIMIT 1`, tmdbID, kind).Scan(&st)
	if h, ok := s.library(ctx, kind)[tmdbID]; ok {
		switch {
		case kind == "movie" && h.HasFile:
			return statusAvailable
		case kind == "tv" && h.Stats.EpisodeCount > 0 && h.Stats.EpisodeFileCount >= h.Stats.EpisodeCount:
			return statusAvailable
		case kind == "tv" && h.Stats.EpisodeFileCount > 0:
			return statusPartial
		}
		return statusProcessing
	}
	switch {
	case err != nil:
		return 0
	case st == "available":
		return statusAvailable
	case st == "approved":
		return statusProcessing
	}
	return statusPending
}

func (s *Service) result(ctx context.Context, m metadata.MediaResult) seerr.SearchResult {
	r := seerr.SearchResult{ID: m.ID, MediaType: m.MediaType, Title: m.Title, Name: m.Name, ReleaseDate: m.ReleaseDate, FirstAirDate: m.FirstAirDate, Overview: m.Overview}
	if strings.HasPrefix(m.PosterPath, "/") && !strings.Contains(m.PosterPath, "..") {
		r.PosterPath = m.PosterPath
	}
	if st := s.status(ctx, m.MediaType, m.ID); st > 0 {
		r.MediaInfo = &struct {
			Status int `json:"status"`
		}{st}
	}
	return r
}

// Search looks movies and shows up on TMDB.
func (s *Service) Search(ctx context.Context, q string) ([]seerr.SearchResult, error) {
	list, err := s.TMDB.SearchMulti(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]seerr.SearchResult, 0, len(list))
	for i, m := range list {
		if i >= 20 {
			break
		}
		out = append(out, s.result(ctx, m))
	}
	return out, nil
}

// Discover lists trending movies and shows: kind is trending, movies or tv.
func (s *Service) Discover(ctx context.Context, kind string) ([]seerr.SearchResult, error) {
	tk := map[string]string{"trending": "all", "movies": "movie", "tv": "tv"}[kind]
	if tk == "" {
		return nil, errors.New("unknown list")
	}
	list, err := s.TMDB.Trending(ctx, tk)
	if err != nil {
		return nil, err
	}
	out := make([]seerr.SearchResult, 0, 20)
	for _, m := range list {
		if m.MediaType != "movie" && m.MediaType != "tv" {
			continue
		}
		out = append(out, s.result(ctx, m))
		if len(out) == 20 {
			break
		}
	}
	return out, nil
}

// Request is one native request.
type Request struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"` // movie, tv or anime
	TMDBID    int64     `json:"tmdbId"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	Requester string    `json:"requester"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	Created   time.Time `json:"created"`
	Decided   time.Time `json:"decided"`
}

func yearOf(d string) int {
	if len(d) >= 4 {
		y, _ := strconv.Atoi(d[:4])
		return y
	}
	return 0
}

// ErrAlready says the title is already requested or in the library.
var ErrAlready = errors.New("already requested or in the library")

// Create records a request (a movie, or every season of a show). Anime is
// recognised from TMDB (Japanese animation) and handed to the Anime page on
// approval. With auto-approve on it is approved at once.
func (s *Service) Create(ctx context.Context, mediaType string, tmdbID int64, requester string) (Request, error) {
	if mediaType != "movie" && mediaType != "tv" {
		return Request{}, errors.New("select a movie or a show")
	}
	if st := s.status(ctx, mediaType, tmdbID); st > 0 {
		return Request{}, ErrAlready
	}
	r := Request{Kind: mediaType, TMDBID: tmdbID, Requester: strings.TrimSpace(requester), Status: "pending", Created: s.Now()}
	if len(r.Requester) > 80 {
		r.Requester = r.Requester[:80]
	}
	if mediaType == "movie" {
		m, err := s.TMDB.Movie(ctx, tmdbID)
		if err != nil {
			return Request{}, err
		}
		r.Title, r.Year = m.Title, yearOf(m.ReleaseDate)
	} else {
		tv, err := s.TMDB.TV(ctx, tmdbID, false)
		if err != nil {
			return Request{}, err
		}
		r.Title, r.Year = tv.Name, yearOf(tv.FirstAirDate)
		if tv.Anime() {
			r.Kind = "anime"
		}
	}
	res, err := s.DB.Exec(`INSERT INTO requests (kind, tmdb_id, title, year, requester, status, created) VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		r.Kind, r.TMDBID, r.Title, r.Year, r.Requester, db.Stamp(r.Created))
	if err != nil {
		return Request{}, err
	}
	r.ID, _ = res.LastInsertId()
	s.Log.Printf("requests: %s %q requested%s", r.Kind, r.Title, map[bool]string{true: " by " + r.Requester, false: ""}[r.Requester != ""])
	if s.Cfg.Get().Requests.AutoApprove {
		if err := s.Decide(ctx, r.ID, "approve", ""); err != nil {
			s.Log.Printf("requests: auto-approving %q: %v", r.Title, err)
		}
		return s.Get(r.ID)
	}
	return r, nil
}

func scanReq(row interface{ Scan(...any) error }) (Request, error) {
	var r Request
	var created, decided string
	err := row.Scan(&r.ID, &r.Kind, &r.TMDBID, &r.Title, &r.Year, &r.Requester, &r.Status, &r.Reason, &created, &decided)
	if err != nil {
		return r, err
	}
	r.Created, _ = time.Parse(db.TimeFormat, created)
	if decided != "" {
		r.Decided, _ = time.Parse(db.TimeFormat, decided)
	}
	return r, nil
}

const reqCols = `id, kind, tmdb_id, title, year, requester, status, reason, created, decided`

// Get returns one request.
func (s *Service) Get(id int64) (Request, error) {
	return scanReq(s.DB.QueryRow(`SELECT `+reqCols+` FROM requests WHERE id = ?`, id))
}

// List returns requests in a status ("" for all), newest first.
func (s *Service) List(status string) ([]Request, error) {
	q, args := `SELECT `+reqCols+` FROM requests`, []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	rows, err := s.DB.Query(q+` ORDER BY id DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanReq(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Pending lists pending requests in Seerr's shape, for the queue and the public count.
func (s *Service) Pending() ([]seerr.Pending, error) {
	list, err := s.List("pending")
	if err != nil {
		return nil, err
	}
	out := make([]seerr.Pending, 0, len(list))
	for _, r := range list {
		mt := r.Kind
		if mt == "anime" {
			mt = "tv"
		}
		title := r.Title
		if r.Kind == "anime" {
			title += " (anime)"
		}
		out = append(out, seerr.Pending{ID: strconv.FormatInt(r.ID, 10), Source: "undermynd", SourceID: r.ID, MediaType: mt, Title: title, TMDBID: r.TMDBID,
			Status: "pending", RequestedBy: r.Requester, CreatedAt: r.Created})
	}
	return out, nil
}

func (s *Service) setStatus(id int64, status, reason string) error {
	_, err := s.DB.Exec(`UPDATE requests SET status = ?, reason = ?, decided = ? WHERE id = ?`, status, reason, db.Stamp(s.Now()), id)
	return err
}

// Decide approves or declines a pending request. Approving hands it on; if
// that fails the request stays pending with the reason, to try again.
func (s *Service) Decide(ctx context.Context, id int64, action, reason string) error {
	r, err := s.Get(id)
	if err != nil {
		return errors.New("no such request")
	}
	if r.Status != "pending" {
		return fmt.Errorf("the request is %s already", r.Status)
	}
	switch action {
	case "decline":
		return s.setStatus(id, "declined", strings.TrimSpace(reason))
	case "approve":
	default:
		return errors.New("approve or decline")
	}
	note, err := s.handOn(ctx, r)
	if err != nil {
		_, _ = s.DB.Exec(`UPDATE requests SET reason = ? WHERE id = ?`, "approving failed: "+err.Error(), id)
		return err
	}
	s.Log.Printf("requests: approved %s %q: %s", r.Kind, r.Title, note)
	return s.setStatus(id, "approved", note)
}

// handOn adds an approved request where it belongs.
func (s *Service) handOn(ctx context.Context, r Request) (string, error) {
	switch r.Kind {
	case "movie":
		if cat := s.category("movie"); s.Movies != nil && s.nativeMovies(cat) {
			info, err := s.TMDB.Movie(ctx, r.TMDBID)
			if err != nil {
				return "", err
			}
			m, err := s.Movies.Add(ctx, cat, catalog.Movie{Title: info.Title, Year: r.Year, TMDBID: r.TMDBID, IMDBID: info.IMDB}, info.Runtime, "")
			if err != nil {
				return "", err
			}
			go s.Movies.Search(context.WithoutCancel(ctx), m.ID, true)
			return "added to " + cat + " (native), searching", nil
		}
		c := s.client("movie")
		if c == nil {
			return "", errors.New("no Radarr category with an address is set")
		}
		if _, err := c.AddMovie(ctx, r.TMDBID, arr.Choice{}); err != nil {
			return "", err
		}
		s.forgetLibrary("movie")
		return "added to Radarr, searching", nil
	case "tv":
		c := s.client("tv")
		if c == nil {
			return "", errors.New("no Sonarr category with an address is set")
		}
		tv, err := s.TMDB.TV(ctx, r.TMDBID, false)
		if err != nil {
			return "", err
		}
		if tv.ExternalIDs.TVDB == 0 {
			return "", errors.New("TMDB has no TVDB id for this show, which Sonarr needs")
		}
		if _, err := c.AddSeries(ctx, tv.ExternalIDs.TVDB, arr.Choice{}); err != nil {
			return "", err
		}
		s.forgetLibrary("tv")
		return "added to Sonarr, searching", nil
	case "anime":
		return s.toAnime(ctx, r)
	}
	return "", errors.New("unknown kind of request")
}

// toAnime adds an anime request on the Anime page: nyaa is searched under the
// show's titles, and when exactly one release title is found the folder is made
// and followed from the first episode. Otherwise the request is approved with
// a note to add it by hand.
func (s *Service) toAnime(ctx context.Context, r Request) (string, error) {
	if s.Shows == nil {
		return "approved: add it on the Anime page", nil
	}
	tv, err := s.TMDB.TV(ctx, r.TMDBID, false)
	if err != nil {
		return "", err
	}
	titles := []string{tv.OriginalName, tv.Name}
	for _, a := range tv.AlternativeTitles.Results {
		titles = append(titles, a.Title)
	}
	roots := s.Shows.Roots()
	if len(roots) == 0 {
		return "approved: add it on the Anime page (no anime library folder is set)", nil
	}
	for _, t := range titles {
		if strings.TrimSpace(t) == "" || !isLatin(t) {
			continue
		}
		found, err := s.Shows.Discover(ctx, t)
		if err != nil || len(found) != 1 {
			continue
		}
		d := found[0]
		if d.Folder != "" {
			return "already on the Anime page: " + d.Folder, nil
		}
		sh, started, err := s.Shows.Add(ctx, roots[0].Dir, d.Title, shows.Settings{Groups: []string{"SubsPlease"}, Quality: "1080p", FillGaps: true}, true)
		if err != nil && sh.Folder == "" {
			return "", err
		}
		return fmt.Sprintf("added on the Anime page as %s; %d episode(s) started", sh.Name, len(started)), nil
	}
	return "approved: add it on the Anime page (nyaa had no single match)", nil
}

func isLatin(s string) bool {
	for _, r := range s {
		if r > 0x24F && !(r >= 0x2000 && r <= 0x206F) {
			return false
		}
	}
	return true
}

// Refresh marks approved movies and shows that arrived as available.
func (s *Service) Refresh(ctx context.Context) {
	list, err := s.List("approved")
	if err != nil {
		return
	}
	for _, r := range list {
		if r.Kind == "anime" {
			continue
		}
		if s.status(ctx, r.Kind, r.TMDBID) == statusAvailable {
			_ = s.setStatus(r.ID, "available", "in the library")
		}
	}
}
