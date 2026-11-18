package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/library"
)

// Default sources for the anime lists. Both are optional: HyveMynd works from
// TMDB alone when they cannot be fetched.
const (
	DefaultAnimeTitlesURL = "https://github.com/manami-project/anime-offline-database/releases/latest/download/anime-offline-database-minified.json"
	DefaultAnimeIDsURL    = "https://raw.githubusercontent.com/Kometa-Team/Anime-IDs/master/anime_ids.json"
)

// Lists keeps the anime-offline-database titles (other names for each anime) and
// the Kometa Anime-IDs mapping (AniDB to TVDB) in the database.
type Lists struct {
	DB        *sql.DB
	HTTP      *http.Client
	TitlesURL func() string
	IDsURL    func() string
	Now       func() time.Time
}

func NewLists(h *sql.DB, titlesURL, idsURL func() string) *Lists {
	return &Lists{DB: h, HTTP: &http.Client{Timeout: 10 * time.Minute}, TitlesURL: titlesURL, IDsURL: idsURL, Now: time.Now}
}

// ListsStatus is what the last refresh did.
type ListsStatus struct {
	Checked     time.Time `json:"checked"`
	Titles      int       `json:"titles"`
	TitlesError string    `json:"titlesError,omitempty"`
	TitlesDate  string    `json:"titlesDate,omitempty"` // lastUpdate inside the file
	IDs         int       `json:"ids"`
	IDsError    string    `json:"idsError,omitempty"`
}

const statusName = "anime-lists-status.json"

func (l *Lists) Status() ListsStatus {
	var s ListsStatus
	if b, ok, _ := (db.Blobs{DB: l.DB}).Load(statusName); ok {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Refresh downloads and imports both lists. A list that fails keeps its last
// imported copy; the error is recorded in the status.
func (l *Lists) Refresh(ctx context.Context) ListsStatus {
	s := l.Status()
	s.Checked = l.Now()
	if u := l.TitlesURL(); u != "" {
		n, date, err := l.fetch(ctx, u, func(r io.Reader) (int, string, error) { return l.ImportTitles(r) })
		if err != nil {
			s.TitlesError = err.Error()
		} else {
			s.Titles, s.TitlesDate, s.TitlesError = n, date, ""
		}
	}
	if u := l.IDsURL(); u != "" {
		n, _, err := l.fetch(ctx, u, func(r io.Reader) (int, string, error) { n, err := l.ImportIDs(r); return n, "", err })
		if err != nil {
			s.IDsError = err.Error()
		} else {
			s.IDs, s.IDsError = n, ""
		}
	}
	if b, err := json.Marshal(s); err == nil {
		_ = (db.Blobs{DB: l.DB}).Save(statusName, b)
	}
	return s
}

func (l *Lists) fetch(ctx context.Context, u string, imp func(io.Reader) (int, string, error)) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("download %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("download %s: HTTP %d", u, resp.StatusCode)
	}
	return imp(io.LimitReader(resp.Body, 512<<20))
}

// ImportTitles replaces the anime titles with those in an anime-offline-database
// JSON file, read as a stream. Only entries with an AniDB or AniList id are kept.
// It returns the number of entries kept and the file's lastUpdate.
func (l *Lists) ImportTitles(r io.Reader) (int, string, error) {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return 0, "", errors.New("anime titles: not a JSON object")
	}
	tx, err := l.DB.Begin()
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM anime_titles`); err != nil {
		return 0, "", err
	}
	stmt, err := tx.Prepare(`INSERT INTO anime_titles (anidb_id, anilist_id, title, norm) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, "", err
	}
	defer stmt.Close()
	n, date, sawData := 0, "", false
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return 0, "", err
		}
		key, _ := t.(string)
		switch key {
		case "lastUpdate":
			if err := dec.Decode(&date); err != nil {
				return 0, "", err
			}
		case "data":
			sawData = true
			if t, err := dec.Token(); err != nil || t != json.Delim('[') {
				return 0, "", errors.New("anime titles: data is not a list")
			}
			for dec.More() {
				var e struct {
					Sources  []string `json:"sources"`
					Title    string   `json:"title"`
					Synonyms []string `json:"synonyms"`
				}
				if err := dec.Decode(&e); err != nil {
					return 0, "", err
				}
				var anidb, anilist int64
				for _, s := range e.Sources {
					if i := strings.Index(s, "anidb.net/anime/"); i >= 0 {
						anidb, _ = strconv.ParseInt(strings.Trim(s[i+len("anidb.net/anime/"):], "/"), 10, 64)
					}
					if i := strings.Index(s, "anilist.co/anime/"); i >= 0 {
						anilist, _ = strconv.ParseInt(strings.Trim(s[i+len("anilist.co/anime/"):], "/"), 10, 64)
					}
				}
				if anidb == 0 && anilist == 0 {
					continue
				}
				seen := map[string]bool{}
				for _, title := range append([]string{e.Title}, e.Synonyms...) {
					norm := library.Normalize(title)
					if norm == "" || seen[norm] {
						continue
					}
					seen[norm] = true
					if _, err := stmt.Exec(anidb, anilist, title, norm); err != nil {
						return 0, "", err
					}
				}
				n++
			}
			if _, err := dec.Token(); err != nil {
				return 0, "", err
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return 0, "", err
			}
		}
	}
	if !sawData {
		return 0, "", errors.New("anime titles: no data list in the file")
	}
	return n, date, tx.Commit()
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// ImportIDs replaces the AniDB to TVDB mapping with a Kometa anime_ids.json file:
// an object keyed by AniDB id. A tvdb_season of -1 means absolute numbering.
func (l *Lists) ImportIDs(r io.Reader) (int, error) {
	var m map[string]map[string]any
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return 0, fmt.Errorf("anime ids: %w", err)
	}
	tx, err := l.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM anime_ids`); err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`INSERT INTO anime_ids (anidb_id, tvdb_id, tvdb_season, tvdb_epoffset, anilist_id, mal_id) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	n := 0
	for k, v := range m {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if _, err := stmt.Exec(id, toInt(v["tvdb_id"]), toInt(v["tvdb_season"]), toInt(v["tvdb_epoffset"]), toInt(v["anilist_id"]), toInt(v["mal_id"])); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}

// TVDBForTitle returns the TVDB ids of the anime whose title or synonym is title.
func (l *Lists) TVDBForTitle(ctx context.Context, title string) ([]int64, error) {
	norm := library.Normalize(title)
	if norm == "" {
		return nil, nil
	}
	rows, err := l.DB.QueryContext(ctx, `SELECT DISTINCT i.tvdb_id FROM anime_titles t JOIN anime_ids i ON i.anidb_id = t.anidb_id
		WHERE t.norm = ? AND t.anidb_id > 0 AND i.tvdb_id > 0 ORDER BY i.tvdb_id`, norm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TitlesForTVDB returns every title the lists know for the anime that map to a
// TVDB show (all its seasons; specials and movies, TVDB season 0, are left out).
func (l *Lists) TitlesForTVDB(ctx context.Context, tvdbID int64) ([]string, error) {
	if tvdbID == 0 {
		return nil, nil
	}
	rows, err := l.DB.QueryContext(ctx, `SELECT t.title FROM anime_ids i JOIN anime_titles t ON t.anidb_id = i.anidb_id
		WHERE i.tvdb_id = ? AND i.tvdb_season != 0 ORDER BY t.rowid`, tvdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// TitlesForUnlinked returns the titles of list entries that have no AniDB id (so
// no ID mapping reaches them; usually brand-new shows) but share at least one
// title with titles. That is how a new show's romaji spelling is found when
// TMDB lacks it.
func (l *Lists) TitlesForUnlinked(ctx context.Context, titles []string) ([]string, error) {
	var out []string
	seen := map[int64]bool{}
	for _, t := range titles {
		norm := library.Normalize(t)
		if norm == "" {
			continue
		}
		rows, err := l.DB.QueryContext(ctx, `SELECT DISTINCT anilist_id FROM anime_titles WHERE norm = ? AND anidb_id = 0 AND anilist_id > 0`, norm)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			r2, err := l.DB.QueryContext(ctx, `SELECT title FROM anime_titles WHERE anilist_id = ? AND anidb_id = 0 ORDER BY rowid`, id)
			if err != nil {
				return nil, err
			}
			for r2.Next() {
				var s string
				if err := r2.Scan(&s); err != nil {
					r2.Close()
					return nil, err
				}
				out = append(out, s)
			}
			r2.Close()
		}
	}
	return out, nil
}
