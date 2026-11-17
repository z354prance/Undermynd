// Package store keeps jobs in memory and persists every change, to SQLite or to a
// JSON file, so a restart resumes from the last stored state.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/job"
)

var ErrNotFound = errors.New("job not found")

// Store keeps every job in memory and writes each change through to its backend:
// a SQLite table (OpenDB) or a JSON file (Open).
type Store struct {
	mu   sync.Mutex
	path string  // JSON backend
	db   *sql.DB // SQLite backend
	jobs map[string]*job.Job
	Now  func() time.Time
}

// keepCleaned is how long a cleaned job stays before it is dropped.
const keepCleaned = 7 * 24 * time.Hour

// Open uses a JSON file, rewritten atomically after every change.
func Open(path string) (*Store, error) {
	s := &Store{path: path, jobs: map[string]*job.Job{}, Now: time.Now}
	list, err := readJSON(path)
	if err != nil {
		return nil, err
	}
	for _, j := range list {
		s.jobs[j.ID] = j
	}
	return s, nil
}

func readJSON(path string) ([]*job.Job, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*job.Job
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// OpenDB uses the jobs table of a migrated database. When the table is empty and
// a legacy JSON job file exists at legacyPath, its jobs are imported in one
// transaction and the file is renamed to .bak.
func OpenDB(h *sql.DB, legacyPath string) (*Store, error) {
	s := &Store{db: h, jobs: map[string]*job.Job{}, Now: time.Now}
	var n int
	if err := h.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 && legacyPath != "" {
		list, err := readJSON(legacyPath)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", legacyPath, err)
		}
		if len(list) > 0 {
			tx, err := h.Begin()
			if err != nil {
				return nil, err
			}
			for _, j := range list {
				if err := upsert(tx, j); err != nil {
					tx.Rollback()
					return nil, fmt.Errorf("import %s: %w", legacyPath, err)
				}
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
		}
		if _, err := os.Stat(legacyPath); err == nil {
			if err := db.RetireFile(legacyPath); err != nil {
				return nil, err
			}
		}
	}
	rows, err := h.Query(`SELECT data FROM jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var j job.Job
		if err := json.Unmarshal([]byte(data), &j); err != nil {
			return nil, fmt.Errorf("a stored job is unreadable: %w", err)
		}
		s.jobs[j.ID] = &j
	}
	return s, rows.Err()
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func upsert(x execer, j *job.Job) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = x.Exec(`INSERT INTO jobs (id, state, created, updated, data) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated = excluded.updated, data = excluded.data`,
		j.ID, string(j.State), db.Stamp(j.Created), db.Stamp(j.Updated), string(b))
	return err
}

// writeLocked finishes a change: the JSON backend rewrites its file; the SQLite
// backend (whose row the caller already wrote) drops long-cleaned jobs.
func (s *Store) writeLocked() error {
	if s.db == nil {
		return s.saveLocked()
	}
	cutoff := s.Now().Add(-keepCleaned)
	if _, err := s.db.Exec(`DELETE FROM jobs WHERE state = ? AND updated < ?`, string(job.Cleaned), db.Stamp(cutoff)); err != nil {
		return err
	}
	for id, x := range s.jobs {
		if x.State == job.Cleaned && x.Updated.Before(cutoff) {
			delete(s.jobs, id)
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	// Drop long-finished jobs so the file stays small.
	cutoff := s.Now().Add(-keepCleaned)
	list := make([]*job.Job, 0, len(s.jobs))
	for id, j := range s.jobs {
		if j.State == job.Cleaned && j.Updated.Before(cutoff) {
			delete(s.jobs, id)
			continue
		}
		list = append(list, j)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Created.Before(list[b].Created) })
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Get(id string) (job.Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return job.Job{}, false
	}
	return j.Clone(), true
}

func (s *Store) List() []job.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]job.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.Clone())
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Created.After(out[b].Created) })
	return out
}

// Put inserts or replaces a job.
func (s *Store) Put(j job.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := j.Clone()
	if s.db != nil {
		// Write first, so a failed write leaves memory and disk agreeing.
		if err := upsert(s.db, &c); err != nil {
			return err
		}
	}
	s.jobs[j.ID] = &c
	return s.writeLocked()
}

// Update applies fn to the stored job and persists the result. If fn returns an
// error nothing is saved.
func (s *Store) Update(id string, fn func(*job.Job) error) (job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.jobs[id]
	if !ok {
		return job.Job{}, ErrNotFound
	}
	c := cur.Clone()
	if err := fn(&c); err != nil {
		return job.Job{}, err
	}
	if s.db != nil {
		if err := upsert(s.db, &c); err != nil {
			return job.Job{}, err
		}
	}
	*cur = c
	if err := s.writeLocked(); err != nil {
		return job.Job{}, err
	}
	return c.Clone(), nil
}
