package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Limiter keeps TorBox calls of one kind (usenet or torrents, each with its
// own hourly create allowance at TorBox) inside their limits. Its counters
// are saved to disk so a restart does not reset the budget. It never fails a
// call: when a call cannot go out now it returns ErrBusy so the caller waits.
//
// A wait TorBox asks for on a create only holds back new creates: progress
// checks and downloads of what is already at TorBox keep going.
type Limiter struct {
	mu           sync.Mutex
	path         string
	Now          func() time.Time
	CreatePerHr  int           // budget for create calls
	LinkGap      time.Duration // minimum gap between download-link requests
	APIGap       time.Duration // minimum gap between other API calls
	creates      []time.Time
	backoffUntil time.Time // every call waits (TorBox limited a non-create call)
	createUntil  time.Time // only creates wait
	lastLink     time.Time
	lastAPI      time.Time
}

type limiterState struct {
	Creates      []time.Time `json:"creates"`
	BackoffUntil time.Time   `json:"backoffUntil"`
	CreateUntil  time.Time   `json:"createUntil"`
}

func NewLimiter(path string, createPerHour int, linkGap time.Duration) *Limiter {
	l := &Limiter{path: path, Now: time.Now, CreatePerHr: createPerHour, LinkGap: linkGap, APIGap: 300 * time.Millisecond}
	if b, err := os.ReadFile(path); err == nil {
		var st limiterState
		if json.Unmarshal(b, &st) == nil {
			l.creates, l.backoffUntil, l.createUntil = st.Creates, st.BackoffUntil, st.CreateUntil
		}
	}
	return l
}

func (l *Limiter) saveLocked() {
	if l.path == "" {
		return
	}
	b, _ := json.Marshal(limiterState{Creates: l.creates, BackoffUntil: l.backoffUntil, CreateUntil: l.createUntil})
	tmp := l.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, l.path)
	}
}

type Class int

const (
	API Class = iota
	Create
	Link
	// CreateCached adds something TorBox already has: TorBox does not count it
	// in the hourly allowance, so neither does the limiter.
	CreateCached
)

// Acquire reserves a slot for one call of the given class. A short pacing wait is
// done here; a limit or backoff returns an error wrapping ErrBusy.
func (l *Limiter) Acquire(ctx context.Context, c Class) error {
	l.mu.Lock()
	now := l.Now()
	if now.Before(l.backoffUntil) {
		d := l.backoffUntil.Sub(now)
		l.mu.Unlock()
		return fmt.Errorf("%w: TorBox asked us to wait %s", ErrBusy, d.Round(time.Second))
	}
	if (c == Create || c == CreateCached) && now.Before(l.createUntil) {
		d := l.createUntil.Sub(now)
		l.mu.Unlock()
		return fmt.Errorf("%w: TorBox asked us to wait %s before adding more", ErrBusy, d.Round(time.Second))
	}
	var wait time.Duration
	switch c {
	case Create:
		cut := now.Add(-time.Hour)
		kept := l.creates[:0]
		for _, t := range l.creates {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		l.creates = kept
		if l.CreatePerHr > 0 && len(l.creates) >= l.CreatePerHr {
			d := l.creates[0].Add(time.Hour).Sub(now)
			l.mu.Unlock()
			return fmt.Errorf("%w: %d creates used this hour, next slot in %s", ErrBusy, l.CreatePerHr, d.Round(time.Second))
		}
		l.creates = append(l.creates, now)
		l.saveLocked()
	case Link:
		if next := l.lastLink.Add(l.LinkGap); next.After(now) {
			wait = next.Sub(now)
		}
		l.lastLink = now.Add(wait)
	default:
		if next := l.lastAPI.Add(l.APIGap); next.After(now) {
			wait = next.Sub(now)
		}
		l.lastAPI = now.Add(wait)
	}
	l.mu.Unlock()
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Backoff records a server-requested wait (HTTP 429 or Retry-After).
func (l *Limiter) Backoff(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := l.Now().Add(d); until.After(l.backoffUntil) {
		l.backoffUntil = until
		l.saveLocked()
	}
}

// BackoffCreate records a wait that only holds back new creates.
func (l *Limiter) BackoffCreate(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := l.Now().Add(d); until.After(l.createUntil) {
		l.createUntil = until
		l.saveLocked()
	}
}

// Refund returns a create slot when the call never reached TorBox.
func (l *Limiter) Refund() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.creates); n > 0 {
		l.creates = l.creates[:n-1]
		l.saveLocked()
	}
}

// Used reports how many create calls are counted in the last hour.
func (l *Limiter) Used() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := l.Now().Add(-time.Hour)
	n := 0
	for _, t := range l.creates {
		if t.After(cut) {
			n++
		}
	}
	return n
}

// Status reports the create budget, for the admin System view: the later of
// the two waits.
func (l *Limiter) Status() (used, limit int, backoffUntil time.Time) {
	used = l.Used()
	l.mu.Lock()
	defer l.mu.Unlock()
	until := l.backoffUntil
	if l.createUntil.After(until) {
		until = l.createUntil
	}
	return used, l.CreatePerHr, until
}
