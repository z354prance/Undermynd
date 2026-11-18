// Package db opens HyveMynd's SQLite database, applies its schema migrations,
// keeps rolling backups, and stores named state blobs. It uses a pure-Go SQLite
// driver, so the binary stays static.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// TimeFormat is how times are stored: fixed width and UTC, so text order is time order.
const TimeFormat = "2006-01-02T15:04:05.000000000Z"

// Stamp formats t for storage.
func Stamp(t time.Time) string { return t.UTC().Format(TimeFormat) }

// Open opens (creating if needed) the database at path. One connection is used,
// so writes never contend; WAL keeps a crash from corrupting it.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"busy_timeout(10000)", "journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)", "temp_store(MEMORY)"} {
		q.Add("_pragma", p)
	}
	h, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	h.SetMaxOpenConns(1)
	if err := h.Ping(); err != nil {
		h.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	return h, nil
}

// migrations are applied in order, once each. Never edit one that has shipped;
// add a new one.
var migrations = []string{
	// 1: jobs and named state blobs.
	`CREATE TABLE jobs (
		id      TEXT PRIMARY KEY,
		state   TEXT NOT NULL,
		created TEXT NOT NULL,
		updated TEXT NOT NULL,
		data    TEXT NOT NULL
	);
	CREATE INDEX jobs_state_updated ON jobs(state, updated);
	CREATE TABLE kv (
		name    TEXT PRIMARY KEY,
		value   BLOB NOT NULL,
		updated TEXT NOT NULL
	);`,

	// 2: the title catalog (roadmap B2). An episode keeps both its season number
	// and, when known, its absolute number, whatever the folder layout on disk.
	`CREATE TABLE series (
		id        INTEGER PRIMARY KEY,
		kind      TEXT NOT NULL CHECK (kind IN ('tv', 'anime')),
		title     TEXT NOT NULL,
		year      INTEGER NOT NULL DEFAULT 0,
		tmdb_id   INTEGER UNIQUE,
		tvdb_id   INTEGER,
		imdb_id   TEXT NOT NULL DEFAULT '',
		numbering TEXT NOT NULL DEFAULT 'seasons' CHECK (numbering IN ('seasons', 'absolute')),
		folder    TEXT NOT NULL DEFAULT '',
		monitored INTEGER NOT NULL DEFAULT 0,
		added     TEXT NOT NULL,
		updated   TEXT NOT NULL
	);
	CREATE TABLE episodes (
		id        INTEGER PRIMARY KEY,
		series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
		season    INTEGER NOT NULL,
		number    INTEGER NOT NULL,
		absolute  INTEGER,
		title     TEXT NOT NULL DEFAULT '',
		air_date  TEXT NOT NULL DEFAULT '',
		UNIQUE (series_id, season, number)
	);
	CREATE INDEX episodes_absolute ON episodes(series_id, absolute);
	CREATE TABLE movies (
		id        INTEGER PRIMARY KEY,
		title     TEXT NOT NULL,
		year      INTEGER NOT NULL DEFAULT 0,
		tmdb_id   INTEGER UNIQUE,
		imdb_id   TEXT NOT NULL DEFAULT '',
		folder    TEXT NOT NULL DEFAULT '',
		monitored INTEGER NOT NULL DEFAULT 0,
		added     TEXT NOT NULL,
		updated   TEXT NOT NULL
	);
	CREATE TABLE aliases (
		id        INTEGER PRIMARY KEY,
		series_id INTEGER REFERENCES series(id) ON DELETE CASCADE,
		movie_id  INTEGER REFERENCES movies(id) ON DELETE CASCADE,
		title     TEXT NOT NULL,
		norm      TEXT NOT NULL,
		source    TEXT NOT NULL DEFAULT '',
		CHECK ((series_id IS NULL) <> (movie_id IS NULL))
	);
	CREATE UNIQUE INDEX aliases_series ON aliases(series_id, norm) WHERE series_id IS NOT NULL;
	CREATE UNIQUE INDEX aliases_movie ON aliases(movie_id, norm) WHERE movie_id IS NOT NULL;
	CREATE INDEX aliases_norm ON aliases(norm);
	CREATE TABLE files (
		id            INTEGER PRIMARY KEY,
		path          TEXT NOT NULL UNIQUE,
		size          INTEGER NOT NULL DEFAULT 0,
		movie_id      INTEGER REFERENCES movies(id) ON DELETE SET NULL,
		release_group TEXT NOT NULL DEFAULT '',
		resolution    TEXT NOT NULL DEFAULT '',
		codec         TEXT NOT NULL DEFAULT '',
		added         TEXT NOT NULL
	);
	CREATE TABLE file_episodes (
		file_id    INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
		episode_id INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
		PRIMARY KEY (file_id, episode_id)
	);
	CREATE INDEX file_episodes_episode ON file_episodes(episode_id);
	CREATE TABLE releases (
		id            INTEGER PRIMARY KEY,
		title         TEXT NOT NULL,
		release_group TEXT NOT NULL DEFAULT '',
		resolution    TEXT NOT NULL DEFAULT '',
		source        TEXT NOT NULL DEFAULT '',
		guid          TEXT NOT NULL DEFAULT '',
		job_id        TEXT NOT NULL DEFAULT '',
		outcome       TEXT NOT NULL DEFAULT '',
		reason        TEXT NOT NULL DEFAULT '',
		seen          TEXT NOT NULL
	);
	CREATE INDEX releases_job ON releases(job_id);
	CREATE TABLE job_targets (
		job_id     TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
		episode_id INTEGER REFERENCES episodes(id) ON DELETE CASCADE,
		movie_id   INTEGER REFERENCES movies(id) ON DELETE CASCADE,
		CHECK ((episode_id IS NULL) <> (movie_id IS NULL))
	);
	CREATE UNIQUE INDEX job_targets_episode ON job_targets(job_id, episode_id) WHERE episode_id IS NOT NULL;
	CREATE UNIQUE INDEX job_targets_movie ON job_targets(job_id, movie_id) WHERE movie_id IS NOT NULL;
	CREATE TABLE requests (
		id        INTEGER PRIMARY KEY,
		kind      TEXT NOT NULL CHECK (kind IN ('movie', 'tv', 'anime')),
		tmdb_id   INTEGER NOT NULL,
		title     TEXT NOT NULL,
		year      INTEGER NOT NULL DEFAULT 0,
		seasons   TEXT NOT NULL DEFAULT '',
		requester TEXT NOT NULL DEFAULT '',
		note      TEXT NOT NULL DEFAULT '',
		status    TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'declined', 'available')),
		reason    TEXT NOT NULL DEFAULT '',
		created   TEXT NOT NULL,
		decided   TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX requests_status ON requests(status, created);`,

	// 3: metadata (roadmap B4): each series' episode order, a TMDB response
	// cache, and the anime title and ID lists.
	`ALTER TABLE series ADD COLUMN episode_order TEXT NOT NULL DEFAULT '';
	CREATE TABLE meta_cache (
		key     TEXT PRIMARY KEY,
		body    BLOB NOT NULL,
		fetched TEXT NOT NULL
	);
	CREATE TABLE anime_titles (
		anidb_id   INTEGER NOT NULL DEFAULT 0,
		anilist_id INTEGER NOT NULL DEFAULT 0,
		title      TEXT NOT NULL,
		norm       TEXT NOT NULL
	);
	CREATE INDEX anime_titles_norm ON anime_titles(norm);
	CREATE INDEX anime_titles_anidb ON anime_titles(anidb_id);
	CREATE TABLE anime_ids (
		anidb_id      INTEGER PRIMARY KEY,
		tvdb_id       INTEGER NOT NULL DEFAULT 0,
		tvdb_season   INTEGER NOT NULL DEFAULT 0,
		tvdb_epoffset INTEGER NOT NULL DEFAULT 0,
		anilist_id    INTEGER NOT NULL DEFAULT 0,
		mal_id        INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX anime_ids_tvdb ON anime_ids(tvdb_id);`,

	// 4: a series kept in more than one folder, as when a sequel that TMDB
	// counts as a later season has its own folder (Bleach and Bleach
	// Thousand-Year Blood War). first_season is the TMDB season the folder's
	// season 1 is.
	`CREATE TABLE series_folders (
		folder       TEXT PRIMARY KEY,
		series_id    INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
		first_season INTEGER NOT NULL,
		numbering    TEXT NOT NULL
	);
	CREATE INDEX series_folders_series ON series_folders(series_id);`,

	// 5: anime stays with the simple folder-name importer (user decision,
	// Nov 17): forget the anime shows the catalog import recorded, and the
	// import and placement state. Nothing on disk is touched.
	`DELETE FROM files WHERE id IN (SELECT fe.file_id FROM file_episodes fe
		JOIN episodes e ON e.id = fe.episode_id JOIN series s ON s.id = e.series_id WHERE s.kind = 'anime');
	DELETE FROM series WHERE kind = 'anime';
	DELETE FROM kv WHERE name IN ('anime-import.json', 'placement-mode.json', 'placement-queue.json', 'placement-dryrun.json');`,

	// 6: followed shows (roadmap C3): settings per show folder of the simple
	// importer, and the releases nyaa has offered for followed shows.
	`CREATE TABLE shows (
		folder          TEXT PRIMARY KEY,
		followed        INTEGER NOT NULL DEFAULT 0,
		groups          TEXT NOT NULL DEFAULT '[]',
		quality         TEXT NOT NULL DEFAULT '1080p',
		titles          TEXT NOT NULL DEFAULT '[]',
		from_episode    INTEGER NOT NULL DEFAULT 0,
		anilist_id      INTEGER NOT NULL DEFAULT 0,
		anilist         TEXT NOT NULL DEFAULT '',
		anilist_checked TEXT NOT NULL DEFAULT '',
		checked         TEXT NOT NULL DEFAULT '',
		check_error     TEXT NOT NULL DEFAULT '',
		added           TEXT NOT NULL
	);
	CREATE TABLE show_releases (
		folder   TEXT NOT NULL REFERENCES shows(folder) ON DELETE CASCADE,
		title    TEXT NOT NULL,
		link     TEXT NOT NULL,
		grp      TEXT NOT NULL,
		res      TEXT NOT NULL,
		season   INTEGER NOT NULL,
		marked   INTEGER NOT NULL,
		episode  INTEGER NOT NULL,
		size     INTEGER NOT NULL DEFAULT 0,
		seeders  INTEGER NOT NULL DEFAULT 0,
		seen     TEXT NOT NULL,
		grabbed  TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (folder, title)
	);`,

	// 7: what each show folder holds, kept between restarts so the Anime page
	// opens at once; refreshed in the background.
	`CREATE TABLE show_folders (
		folder  TEXT PRIMARY KEY,
		summary TEXT NOT NULL,
		scanned TEXT NOT NULL
	);`,

	// 8: gap filling (roadmap C4): whether a followed show fills its gaps by
	// itself, and when it last looked.
	`ALTER TABLE shows ADD COLUMN fill_gaps INTEGER NOT NULL DEFAULT 1;
	ALTER TABLE shows ADD COLUMN gaps_checked TEXT NOT NULL DEFAULT '';`,

	// 9: indexers (roadmap D1): Torznab and Newznab indexers (direct, or
	// through Prowlarr while it still runs), their health and daily use.
	`CREATE TABLE indexers (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL UNIQUE,
		kind        TEXT NOT NULL,
		url         TEXT NOT NULL,
		api_key     TEXT NOT NULL DEFAULT '',
		categories  TEXT NOT NULL DEFAULT '',
		priority    INTEGER NOT NULL DEFAULT 25,
		enabled     INTEGER NOT NULL DEFAULT 1,
		query_limit INTEGER NOT NULL DEFAULT 0,
		grab_limit  INTEGER NOT NULL DEFAULT 0,
		via         TEXT NOT NULL DEFAULT '',
		added       TEXT NOT NULL
	);
	CREATE TABLE indexer_status (
		indexer_id     INTEGER PRIMARY KEY REFERENCES indexers(id) ON DELETE CASCADE,
		failures       INTEGER NOT NULL DEFAULT 0,
		disabled_until TEXT NOT NULL DEFAULT '',
		last_error     TEXT NOT NULL DEFAULT '',
		last_ok        TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE indexer_usage (
		indexer_id INTEGER NOT NULL REFERENCES indexers(id) ON DELETE CASCADE,
		day        TEXT NOT NULL,
		queries    INTEGER NOT NULL DEFAULT 0,
		grabs      INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (indexer_id, day)
	);`,
	// 10: quality profiles, custom formats and quality sizes (roadmap F2).
	// Each is kept as JSON; source is the category it was imported from.
	`CREATE TABLE profiles (
		id     INTEGER PRIMARY KEY,
		name   TEXT NOT NULL UNIQUE,
		source TEXT NOT NULL DEFAULT '',
		body   TEXT NOT NULL
	);
	CREATE TABLE custom_formats (
		id     INTEGER PRIMARY KEY,
		name   TEXT NOT NULL UNIQUE,
		source TEXT NOT NULL DEFAULT '',
		body   TEXT NOT NULL
	);
	CREATE TABLE quality_sizes (
		quality TEXT PRIMARY KEY,
		min     REAL NOT NULL DEFAULT 0,
		max     REAL NOT NULL DEFAULT 0
	);`,
	// 11: shadow mode (roadmap F3): each grab an arr made, and what
	// HyveMynd would have picked for the same thing.
	`CREATE TABLE shadow (
		id           INTEGER PRIMARY KEY,
		category     TEXT NOT NULL,
		grab_key     TEXT NOT NULL UNIQUE,
		at           TEXT NOT NULL,
		item         TEXT NOT NULL,
		profile      TEXT NOT NULL DEFAULT '',
		arr_title    TEXT NOT NULL,
		arr_quality  TEXT NOT NULL DEFAULT '',
		arr_indexer  TEXT NOT NULL DEFAULT '',
		our_title    TEXT NOT NULL DEFAULT '',
		our_quality  TEXT NOT NULL DEFAULT '',
		our_score    INTEGER NOT NULL DEFAULT 0,
		arr_score    INTEGER NOT NULL DEFAULT 0,
		verdict      TEXT NOT NULL,
		note         TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX shadow_at ON shadow(at);`,
	// 12: native movies (roadmap F4): which category and profile a movie
	// belongs to, the file on disk, the download under way, and releases not
	// to grab again.
	`ALTER TABLE movies ADD COLUMN category TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN profile TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN runtime INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE movies ADD COLUMN file_path TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN file_quality TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN file_score INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE movies ADD COLUMN file_version INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE movies ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE movies ADD COLUMN job_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN searched TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN note TEXT NOT NULL DEFAULT '';
	CREATE INDEX movies_category ON movies(category);
	CREATE TABLE blocklist (
		id       INTEGER PRIMARY KEY,
		movie_id INTEGER REFERENCES movies(id) ON DELETE CASCADE,
		title    TEXT NOT NULL,
		norm     TEXT NOT NULL,
		reason   TEXT NOT NULL DEFAULT '',
		at       TEXT NOT NULL
	);
	CREATE INDEX blocklist_movie ON blocklist(movie_id);`,
	// 13: native TV (roadmap F5): a series' category, profile and type, and
	// each episode's monitoring, file and download.
	`ALTER TABLE series ADD COLUMN category TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN profile TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN runtime INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE series ADD COLUMN series_type TEXT NOT NULL DEFAULT 'standard';
	ALTER TABLE series ADD COLUMN refreshed TEXT NOT NULL DEFAULT '';
	ALTER TABLE episodes ADD COLUMN monitored INTEGER NOT NULL DEFAULT 1;
	ALTER TABLE episodes ADD COLUMN file_path TEXT NOT NULL DEFAULT '';
	ALTER TABLE episodes ADD COLUMN file_quality TEXT NOT NULL DEFAULT '';
	ALTER TABLE episodes ADD COLUMN file_score INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE episodes ADD COLUMN file_version INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE episodes ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE episodes ADD COLUMN job_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE episodes ADD COLUMN searched TEXT NOT NULL DEFAULT '';
	ALTER TABLE episodes ADD COLUMN note TEXT NOT NULL DEFAULT '';
	ALTER TABLE blocklist ADD COLUMN series_id INTEGER REFERENCES series(id) ON DELETE CASCADE;
	CREATE INDEX series_category ON series(category);
	CREATE INDEX episodes_job ON episodes(job_id) WHERE job_id != '';
	CREATE INDEX blocklist_series ON blocklist(series_id);`,
	// 14: history of native movies and episodes (roadmap F6).
	`CREATE TABLE history (
		id       INTEGER PRIMARY KEY,
		at       TEXT NOT NULL,
		kind     TEXT NOT NULL,
		item_id  INTEGER NOT NULL,
		item     TEXT NOT NULL,
		event    TEXT NOT NULL,
		release  TEXT NOT NULL DEFAULT '',
		quality  TEXT NOT NULL DEFAULT '',
		category TEXT NOT NULL DEFAULT '',
		note     TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX history_at ON history(at);
	CREATE INDEX history_item ON history(kind, item_id);`,
	// 15: the scheduler (roadmap G-2): every automatic search, for the global
	// budget, and when each arr backlog item was last asked for.
	`CREATE TABLE search_log (
		id   INTEGER PRIMARY KEY,
		at   TEXT NOT NULL,
		kind TEXT NOT NULL,
		item TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX search_log_at ON search_log(at);
	CREATE TABLE backlog (
		category TEXT NOT NULL,
		kind     TEXT NOT NULL,
		item_id  INTEGER NOT NULL,
		label    TEXT NOT NULL DEFAULT '',
		at       TEXT NOT NULL,
		PRIMARY KEY (category, kind, item_id)
	);`,
	// 16: native music (roadmap H2): artists and their albums (MusicBrainz
	// release groups), the files of each album and the download under way.
	`CREATE TABLE artists (
		id        INTEGER PRIMARY KEY,
		mbid      TEXT NOT NULL UNIQUE,
		name      TEXT NOT NULL,
		sort_name TEXT NOT NULL DEFAULT '',
		folder    TEXT NOT NULL DEFAULT '',
		monitored INTEGER NOT NULL DEFAULT 1,
		category  TEXT NOT NULL DEFAULT '',
		added     TEXT NOT NULL,
		refreshed TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE albums (
		id           INTEGER PRIMARY KEY,
		artist_id    INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
		mbid         TEXT NOT NULL UNIQUE,
		title        TEXT NOT NULL,
		year         INTEGER NOT NULL DEFAULT 0,
		release_date TEXT NOT NULL DEFAULT '',
		type         TEXT NOT NULL DEFAULT '',
		secondary    TEXT NOT NULL DEFAULT '',
		monitored    INTEGER NOT NULL DEFAULT 0,
		tracks       INTEGER NOT NULL DEFAULT 0,
		folder       TEXT NOT NULL DEFAULT '',
		file_quality TEXT NOT NULL DEFAULT '',
		file_count   INTEGER NOT NULL DEFAULT 0,
		file_size    INTEGER NOT NULL DEFAULT 0,
		job_id       TEXT NOT NULL DEFAULT '',
		searched     TEXT NOT NULL DEFAULT '',
		note         TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX albums_artist ON albums(artist_id);
	ALTER TABLE blocklist ADD COLUMN album_id INTEGER REFERENCES albums(id) ON DELETE CASCADE;
	CREATE INDEX albums_job ON albums(job_id) WHERE job_id != '';`,
	// 17: API tokens (roadmap I1), kept as hashes.
	`CREATE TABLE api_tokens (
		id        INTEGER PRIMARY KEY,
		name      TEXT NOT NULL,
		hash      TEXT NOT NULL UNIQUE,
		hint      TEXT NOT NULL DEFAULT '',
		created   TEXT NOT NULL,
		last_used TEXT NOT NULL DEFAULT ''
	);`,
	// 18: posters and descriptions for the library pages (design v3).
	`ALTER TABLE movies ADD COLUMN poster TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN fanart TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN overview TEXT NOT NULL DEFAULT '';
	ALTER TABLE movies ADD COLUMN genres TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN poster TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN fanart TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN overview TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN genres TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN network TEXT NOT NULL DEFAULT '';
	ALTER TABLE series ADD COLUMN ended INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE artists ADD COLUMN poster TEXT NOT NULL DEFAULT '';
	ALTER TABLE artists ADD COLUMN fanart TEXT NOT NULL DEFAULT '';
	ALTER TABLE artists ADD COLUMN overview TEXT NOT NULL DEFAULT '';`,
}

// Version is the schema version this build expects.
func Version() int { return len(migrations) }

// Applied returns the schema version the database is at (0 for a new one).
func Applied(h *sql.DB) (int, error) {
	if _, err := h.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied TEXT NOT NULL)`); err != nil {
		return 0, err
	}
	var v sql.NullInt64
	if err := h.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Migrate brings the schema up to date, each step in its own transaction. A
// database newer than this build is refused rather than touched.
func Migrate(h *sql.DB) error {
	cur, err := Applied(h)
	if err != nil {
		return err
	}
	if cur > len(migrations) {
		return fmt.Errorf("the database is at schema version %d but this build only knows %d; run a newer image or restore a backup", cur, len(migrations))
	}
	for i := cur; i < len(migrations); i++ {
		tx, err := h.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied) VALUES (?, ?)`, i+1, Stamp(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const backupPrefix = "undermynd-"

// Backup writes a consistent copy of the database into dir and keeps only the
// newest keep copies. It returns the new file's path.
func Backup(h *sql.DB, dir string, keep int, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := filepath.Join(dir, backupPrefix+now.UTC().Format("20060102-150405")+".db")
	if _, err := os.Stat(name); err == nil {
		return name, nil
	}
	if _, err := h.Exec(`VACUUM INTO ?`, name); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	_ = os.Chmod(name, 0o600)
	list, err := Backups(dir)
	if err != nil {
		return name, err
	}
	for keep > 0 && len(list) > keep {
		_ = os.Remove(list[0])
		list = list[1:]
	}
	return name, nil
}

// Backups lists the backup files in dir, oldest first.
func Backups(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), backupPrefix) && strings.HasSuffix(e.Name(), ".db") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Blobs keeps named state blobs in the kv table. The first time a blob is asked
// for and the table has none, a legacy file of the same name in LegacyDir is
// imported and renamed to <name>.bak, so a downgrade can still find it.
type Blobs struct {
	DB        *sql.DB
	LegacyDir string
}

func (b Blobs) Load(name string) ([]byte, bool, error) {
	var v []byte
	err := b.DB.QueryRow(`SELECT value FROM kv WHERE name = ?`, name).Scan(&v)
	if err == nil {
		return v, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if b.LegacyDir == "" {
		return nil, false, nil
	}
	p := filepath.Join(b.LegacyDir, name)
	old, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := b.Save(name, old); err != nil {
		return nil, false, err
	}
	if err := RetireFile(p); err != nil {
		return nil, false, err
	}
	return old, true, nil
}

func (b Blobs) Save(name string, v []byte) error {
	_, err := b.DB.Exec(`INSERT INTO kv (name, value, updated) VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value, updated = excluded.updated`, name, v, Stamp(time.Now()))
	return err
}

// RetireFile renames an imported legacy file to <file>.bak, or <file>.bak.<time>
// when a .bak already exists, so nothing is ever overwritten or deleted.
func RetireFile(p string) error {
	dst := p + ".bak"
	if _, err := os.Stat(dst); err == nil {
		dst = p + ".bak." + time.Now().UTC().Format("20060102-150405")
	}
	return os.Rename(p, dst)
}
