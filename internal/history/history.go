// Package history records what happened to native movies and episodes:
// grabbed, imported, failed, dry runs (roadmap F6).
package history

import (
	"database/sql"
	"time"

	"github.com/z354prance/undermynd/internal/db"
)

// Event is one thing that happened.
type Event struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"` // movie or episode
	ItemID   int64     `json:"itemId"`
	Item     string    `json:"item"` // "Heat (1995)" or "Severance S02E03"
	Event    string    `json:"event"`
	Release  string    `json:"release"`
	Quality  string    `json:"quality"`
	Category string    `json:"category"`
	Note     string    `json:"note"`
}

// Events.
const (
	Grabbed  = "grabbed"
	Imported = "imported"
	Failed   = "failed"
	DryRun   = "dry run"
	Replaced = "replaced"
)

// Log keeps events in the database.
type Log struct {
	DB  *sql.DB
	Now func() time.Time
}

// Add records an event. A nil log records nothing.
func (l *Log) Add(e Event) {
	if l == nil || l.DB == nil {
		return
	}
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	l.DB.Exec(`INSERT INTO history (at, kind, item_id, item, event, release, quality, category, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		db.Stamp(now), e.Kind, e.ItemID, e.Item, e.Event, e.Release, e.Quality, e.Category, e.Note)
}

// List returns events newest first, optionally of one item.
func (l *Log) List(kind string, itemID int64, limit int) ([]Event, error) {
	q := `SELECT id, at, kind, item_id, item, event, release, quality, category, note FROM history`
	var args []any
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
		if itemID > 0 {
			q += ` AND item_id = ?`
			args = append(args, itemID)
		}
	}
	q += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.ItemID, &e.Item, &e.Event, &e.Release, &e.Quality, &e.Category, &e.Note); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(db.TimeFormat, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune drops events older than a year.
func (l *Log) Prune() {
	l.DB.Exec(`DELETE FROM history WHERE at < ?`, db.Stamp(time.Now().AddDate(-1, 0, 0)))
}
