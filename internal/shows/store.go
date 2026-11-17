package shows

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/db"
)

// Settings are what a person sets for one show folder.
type Settings struct {
	Followed bool     `json:"followed"`
	Groups   []string `json:"groups"`  // release groups, first choice first
	Quality  string   `json:"quality"` // "1080p", "720p", "480p" or "any"
	Titles   []string `json:"titles"`  // other titles releases may use; the folder name always counts
	// FromEpisode: new episodes above this number are grabbed by themselves.
	// Following a show sets it to the newest episode on disk; zero grabs every
	// episode the feeds offer.
	FromEpisode int   `json:"fromEpisode"`
	AniListID   int64 `json:"anilistId"` // 0: found by the folder name
}

// AniInfo is what AniList says about a show. It is shown, never used to place.
type AniInfo struct {
	ID          int64  `json:"id"`
	Romaji      string `json:"romaji"`
	English     string `json:"english"`
	Episodes    int    `json:"episodes"` // 0 when not known yet
	Status      string `json:"status"`   // FINISHED, RELEASING, NOT_YET_RELEASED, CANCELLED, HIATUS
	NextEpisode int    `json:"nextEpisode"`
	NextAt      int64  `json:"nextAt"` // unix seconds
	Season      string `json:"season"`
	Year        int    `json:"year"`
	URL         string `json:"url"`
}

// Record is a show folder's stored row.
type Record struct {
	Folder string `json:"folder"`
	Settings
	AniList        *AniInfo  `json:"anilist,omitempty"`
	AniListChecked time.Time `json:"anilistChecked"`
	Checked        time.Time `json:"checked"`    // last nyaa check
	CheckError     string    `json:"checkError"` // why the last check failed
	Added          time.Time `json:"added"`
}

// Store keeps the show rows.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

const recordCols = `folder, followed, groups, quality, titles, from_episode, anilist_id, anilist, anilist_checked, checked, check_error, added`

func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var followed int
	var groups, titles, ani, aniChecked, checked, added string
	err := row.Scan(&r.Folder, &followed, &groups, &r.Quality, &titles, &r.FromEpisode, &r.AniListID, &ani, &aniChecked, &checked, &r.CheckError, &added)
	if err != nil {
		return r, err
	}
	r.Followed = followed != 0
	_ = json.Unmarshal([]byte(groups), &r.Groups)
	_ = json.Unmarshal([]byte(titles), &r.Titles)
	if ani != "" {
		var a AniInfo
		if json.Unmarshal([]byte(ani), &a) == nil {
			r.AniList = &a
		}
	}
	r.AniListChecked = parseTime(aniChecked)
	r.Checked = parseTime(checked)
	r.Added = parseTime(added)
	if r.Groups == nil {
		r.Groups = []string{}
	}
	if r.Titles == nil {
		r.Titles = []string{}
	}
	return r, nil
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(db.TimeFormat, s)
	return t
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return db.Stamp(t)
}

// ErrNoRecord: the folder has no settings yet (it is not followed).
var ErrNoRecord = errors.New("no settings for this folder")

// Get returns a folder's row.
func (s *Store) Get(folder string) (Record, error) {
	r, err := scanRecord(s.DB.QueryRow(`SELECT `+recordCols+` FROM shows WHERE folder = ?`, folder))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{Folder: folder}, ErrNoRecord
	}
	return r, err
}

// All returns every row, by folder.
func (s *Store) All() (map[string]Record, error) {
	rows, err := s.DB.Query(`SELECT ` + recordCols + ` FROM shows`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out[r.Folder] = r
	}
	return out, rows.Err()
}

func clean(list []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range list {
		x = strings.TrimSpace(x)
		if x == "" || seen[strings.ToLower(x)] {
			continue
		}
		seen[strings.ToLower(x)] = true
		out = append(out, x)
	}
	return out
}

// Put saves a folder's settings, creating its row when needed.
func (s *Store) Put(folder string, st Settings) error {
	st.Groups, st.Titles = clean(st.Groups), clean(st.Titles)
	switch st.Quality {
	case "1080p", "720p", "480p", "2160p", "any":
	default:
		st.Quality = "1080p"
	}
	groups, _ := json.Marshal(st.Groups)
	titles, _ := json.Marshal(st.Titles)
	followed := 0
	if st.Followed {
		followed = 1
	}
	_, err := s.DB.Exec(`INSERT INTO shows (folder, followed, groups, quality, titles, from_episode, anilist_id, added)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder) DO UPDATE SET followed = excluded.followed, groups = excluded.groups, quality = excluded.quality,
			titles = excluded.titles, from_episode = excluded.from_episode,
			anilist = CASE WHEN anilist_id = excluded.anilist_id THEN anilist ELSE '' END,
			anilist_checked = CASE WHEN anilist_id = excluded.anilist_id THEN anilist_checked ELSE '' END,
			anilist_id = excluded.anilist_id`,
		folder, followed, string(groups), st.Quality, string(titles), st.FromEpisode, st.AniListID, stamp(s.Now()))
	return err
}

// SetAniList stores what AniList said, and when.
func (s *Store) SetAniList(folder string, a *AniInfo) error {
	b := ""
	var id int64
	if a != nil {
		j, _ := json.Marshal(a)
		b, id = string(j), a.ID
	}
	res, err := s.DB.Exec(`UPDATE shows SET anilist = ?, anilist_checked = ?, anilist_id = CASE WHEN anilist_id = 0 THEN ? ELSE anilist_id END WHERE folder = ?`, b, stamp(s.Now()), id, folder)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if err := s.Put(folder, Settings{Quality: "1080p"}); err != nil {
			return err
		}
		_, err = s.DB.Exec(`UPDATE shows SET anilist = ?, anilist_checked = ?, anilist_id = ? WHERE folder = ?`, b, stamp(s.Now()), id, folder)
	}
	return err
}

// SetChecked records a nyaa check.
func (s *Store) SetChecked(folder string, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_, e := s.DB.Exec(`UPDATE shows SET checked = ?, check_error = ? WHERE folder = ?`, stamp(s.Now()), msg, folder)
	return e
}

// Seen is a release nyaa offered for a followed show.
type Seen struct {
	Title   string    `json:"title"`
	Link    string    `json:"-"`
	Group   string    `json:"group"`
	Res     string    `json:"res"`
	Season  int       `json:"season"`
	Marked  bool      `json:"marked"`
	Episode int       `json:"episode"`
	Size    int64     `json:"size"`
	Seeders int       `json:"seeders"`
	First   time.Time `json:"seen"`
	Grabbed time.Time `json:"grabbed"`
}

// AddSeen records releases, keeping when each was first seen.
func (s *Store) AddSeen(folder string, list []Seen) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range list {
		marked := 0
		if r.Marked {
			marked = 1
		}
		if _, err := tx.Exec(`INSERT INTO show_releases (folder, title, link, grp, res, season, marked, episode, size, seeders, seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(folder, title) DO UPDATE SET seeders = excluded.seeders, link = excluded.link`,
			folder, r.Title, r.Link, r.Group, r.Res, r.Season, marked, r.Episode, r.Size, r.Seeders, stamp(s.Now())); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SeenFor lists the releases recorded for a folder, newest first.
func (s *Store) SeenFor(folder string) ([]Seen, error) {
	rows, err := s.DB.Query(`SELECT title, link, grp, res, season, marked, episode, size, seeders, seen, grabbed FROM show_releases WHERE folder = ? ORDER BY seen DESC, episode DESC`, folder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Seen
	for rows.Next() {
		var r Seen
		var marked int
		var seen, grabbed string
		if err := rows.Scan(&r.Title, &r.Link, &r.Group, &r.Res, &r.Season, &marked, &r.Episode, &r.Size, &r.Seeders, &seen, &grabbed); err != nil {
			return nil, err
		}
		r.Marked, r.First, r.Grabbed = marked != 0, parseTime(seen), parseTime(grabbed)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkGrabbed records that a release was sent to download.
func (s *Store) MarkGrabbed(folder, title string) error {
	_, err := s.DB.Exec(`UPDATE show_releases SET grabbed = ? WHERE folder = ? AND title = ?`, stamp(s.Now()), folder, title)
	return err
}
