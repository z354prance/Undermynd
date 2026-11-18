// Package scheduler paces automatic searches (roadmap G-2, Houndarr parity):
// one budget of searches an hour shared by native movies and TV, anime gap
// filling and the arrs' backlog; a grace window after an episode airs; and a
// backlog search that asks Sonarr and Radarr to look for a few missing or
// cutoff-unmet items at a time, each resting for days afterwards.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
)

// Budget is the shared hourly budget of automatic searches.
type Budget struct {
	DB  *sql.DB
	Cfg *config.Config
	Now func() time.Time
	mu  sync.Mutex
}

func (b *Budget) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// PerHour is the budget.
func (b *Budget) PerHour() int {
	n := b.Cfg.Get().Scheduler.PerHour
	if n <= 0 {
		n = 30
	}
	return n
}

// Grace is how long after an air date an episode is first searched.
func (b *Budget) Grace() time.Duration {
	if b == nil {
		return 2 * time.Hour
	}
	h := b.Cfg.Get().Scheduler.GraceHours
	if h <= 0 {
		h = 2
	}
	return time.Duration(h) * time.Hour
}

// Used counts the searches of the last hour by kind.
func (b *Budget) Used() map[string]int {
	out := map[string]int{}
	rows, err := b.DB.Query(`SELECT kind, COUNT(*) FROM search_log WHERE at >= ? GROUP BY kind`, db.Stamp(b.now().Add(-time.Hour)))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		rows.Scan(&k, &n)
		out[k] = n
	}
	return out
}

// Left is how many searches the budget has for the rest of the hour.
func (b *Budget) Left() int {
	n := 0
	for _, v := range b.Used() {
		n += v
	}
	return b.PerHour() - n
}

// Take spends one search of the budget, or reports that none is left. A nil
// budget never runs out.
func (b *Budget) Take(kind, item string) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Left() <= 0 {
		return false
	}
	b.DB.Exec(`INSERT INTO search_log (at, kind, item) VALUES (?, ?, ?)`, db.Stamp(b.now()), kind, item)
	return true
}

// Prune drops the search log older than a week.
func (b *Budget) Prune() {
	b.DB.Exec(`DELETE FROM search_log WHERE at < ?`, db.Stamp(b.now().AddDate(0, 0, -7)))
}

// Aired reports whether an air date (YYYY-MM-DD) is past by the grace window.
func (b *Budget) Aired(airDate string) bool {
	d, err := time.ParseInLocation("2006-01-02", airDate, time.Local)
	if err != nil {
		return false
	}
	now := time.Now()
	if b != nil {
		now = b.now()
	}
	return !d.Add(b.Grace()).After(now)
}

// Backlog asks the arrs to search their missing and cutoff-unmet items.
type Backlog struct {
	DB     *sql.DB
	Cfg    *config.Config
	Budget *Budget
	Arr    func(category string) *arr.Client
	Log    *log.Logger
	Now    func() time.Time
}

// Item is one backlog item an arr was asked to search.
type Item struct {
	Category string    `json:"category"`
	Kind     string    `json:"kind"` // missing or cutoff
	ID       int64     `json:"id"`
	Label    string    `json:"label"`
	At       time.Time `json:"at"`
}

// Run searches a batch every hour while the backlog search is on.
func (b *Backlog) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		b.Budget.Prune()
		if b.Cfg.Get().Scheduler.Backlog {
			if n, err := b.Once(ctx); err != nil {
				b.Log.Printf("backlog: %v", err)
			} else if n > 0 {
				b.Log.Printf("backlog: asked the arrs to search %d item(s)", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type wanted struct {
	Records []struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Season     int    `json:"seasonNumber"`
		Episode    int    `json:"episodeNumber"`
		AirDateUTC string `json:"airDateUtc"`
		Monitored  bool   `json:"monitored"`
		Available  *bool  `json:"isAvailable"`
		Year       int    `json:"year"`
		Series     *struct {
			Title string `json:"title"`
		} `json:"series"`
	} `json:"records"`
}

func (b *Backlog) rested(cat, kind string, id int64, days int) bool {
	var at string
	if b.DB.QueryRow(`SELECT at FROM backlog WHERE category = ? AND kind = ? AND item_id = ?`, cat, kind, id).Scan(&at) != nil {
		return true
	}
	t, _ := time.Parse(db.TimeFormat, at)
	return b.Now().Sub(t) >= time.Duration(days)*24*time.Hour
}

// Once sends one batch for every arr category that is not native: missing
// items first, then cutoff-unmet ones, each resting for days afterwards.
func (b *Backlog) Once(ctx context.Context) (int, error) {
	st := b.Cfg.Get().Scheduler
	batch, missDays, cutDays := st.Batch, st.MissingDays, st.CutoffDays
	if batch <= 0 {
		batch = 5
	}
	if missDays <= 0 {
		missDays = 7
	}
	if cutDays <= 0 {
		cutDays = 14
	}
	total := 0
	for _, c := range b.Cfg.Get().Categories {
		if c.Native || c.ArrURL == "" || (c.Kind != "sonarr" && c.Kind != "radarr") {
			continue
		}
		cl := b.Arr(c.Name)
		sent := 0
		for _, kind := range []string{"missing", "cutoff"} {
			if sent >= batch {
				break
			}
			days := missDays
			if kind == "cutoff" {
				days = cutDays
			}
			q := "/wanted/" + kind + "?page=1&pageSize=200&monitored=true&sortDirection=descending"
			if c.Kind == "sonarr" {
				q += "&sortKey=airDateUtc&includeSeries=true"
			} else {
				q += "&sortKey=movieMetadata.sortTitle"
			}
			var w wanted
			if err := cl.Get(ctx, q, &w); err != nil {
				return total, fmt.Errorf("%s: %w", c.Name, err)
			}
			var ids []int64
			var labels []string
			for _, r := range w.Records {
				if len(ids) >= batch-sent {
					break
				}
				if c.Kind == "sonarr" {
					air, err := time.Parse(time.RFC3339, r.AirDateUTC)
					if err != nil || air.Add(b.Budget.Grace()).After(b.Now()) {
						continue // not aired, or inside the grace window
					}
				} else if r.Available != nil && !*r.Available {
					continue // not released yet
				}
				if !b.rested(c.Name, kind, r.ID, days) {
					continue
				}
				if !b.Budget.Take("backlog", c.Name) {
					break
				}
				label := r.Title
				if r.Series != nil {
					label = fmt.Sprintf("%s S%02dE%02d", r.Series.Title, r.Season, r.Episode)
				} else if r.Year > 0 {
					label = fmt.Sprintf("%s (%d)", r.Title, r.Year)
				}
				ids = append(ids, r.ID)
				labels = append(labels, label)
			}
			if len(ids) == 0 {
				continue
			}
			name, field := "MoviesSearch", "movieIds"
			if c.Kind == "sonarr" {
				name, field = "EpisodeSearch", "episodeIds"
			}
			if err := cl.Command(ctx, map[string]any{"name": name, field: ids}); err != nil {
				return total, fmt.Errorf("%s: %w", c.Name, err)
			}
			for i, id := range ids {
				b.DB.Exec(`INSERT INTO backlog (category, kind, item_id, label, at) VALUES (?, ?, ?, ?, ?)
					ON CONFLICT(category, kind, item_id) DO UPDATE SET label = excluded.label, at = excluded.at`,
					c.Name, kind, id, labels[i], db.Stamp(b.Now()))
			}
			sent += len(ids)
			total += len(ids)
		}
	}
	return total, nil
}

// Recent lists the backlog items asked for most recently.
func (b *Backlog) Recent(limit int) ([]Item, error) {
	rows, err := b.DB.Query(`SELECT category, kind, item_id, label, at FROM backlog ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var x Item
		var at string
		if err := rows.Scan(&x.Category, &x.Kind, &x.ID, &x.Label, &at); err != nil {
			return nil, err
		}
		x.At, _ = time.Parse(db.TimeFormat, at)
		out = append(out, x)
	}
	return out, rows.Err()
}
