// Package db opens Undermynd's SQLite database, applies its schema migrations,
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
