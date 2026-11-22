// Package attention moves what people watch a lot onto an upgrade profile in
// Sonarr or Radarr and starts the upgrade search.
//
// From Tracearr's watch history: a movie that two or more different people
// watched (finished, or got at least halfway) in the last 30 days, or a show
// one person watched three or more episodes of in the last 14 days. A show
// gets a search of the season that person is on and the next one; once
// nothing for the series is left in Sonarr's queue, Sonarr is asked to search
// the whole series and finishes the rest itself. Something already on the
// upgrade profile is left alone. At most a few switches a day.
package attention

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/state"
	"github.com/z354prance/undermynd/internal/tracearr"
)

const (
	movieDays    = 30
	movieViewers = 2
	showDays     = 14
	showEpisodes = 3
	minPercent   = 50
	stateName    = "attention.json"
	// handoffAfter is the least time between the season searches and the
	// whole-series search, so the first grabs can show up in the queue.
	handoffAfter = 2 * time.Hour
)

// History is where plays come from (Tracearr).
type History interface {
	History(ctx context.Context, mediaType string, since time.Time) ([]tracearr.Play, error)
	ShowIDs(ctx context.Context, showMediaID string) (tvdb, tmdb int64, title string, err error)
}

// Arr is what this needs from Sonarr or Radarr.
type Arr interface {
	ProfileByName(ctx context.Context, name string) (arr.Profile, error)
	SeriesByTVDB(ctx context.Context, tvdb int64) (arr.Item, bool, error)
	MovieByTMDB(ctx context.Context, tmdb int64) (arr.Item, bool, error)
	SetProfile(ctx context.Context, id, profileID int64) error
	SearchSeason(ctx context.Context, seriesID int64, season int) error
	SearchSeries(ctx context.Context, seriesID int64) error
	SearchMovie(ctx context.Context, movieID int64) error
	QueuedFor(ctx context.Context, seriesID int64) (int, error)
}

// Action is one switch to the upgrade profile, kept for the admin to see.
type Action struct {
	Kind     string     `json:"kind"` // show | movie
	Category string     `json:"category"`
	ArrID    int64      `json:"arrId"`
	Title    string     `json:"title"`
	Reason   string     `json:"reason"`
	At       time.Time  `json:"at"`
	Seasons  []int      `json:"seasons,omitempty"`  // seasons searched first
	Handoff  *time.Time `json:"handoff,omitempty"`  // whole-series search sent
	Error    string     `json:"error,omitempty"`    // last problem with it
	Profile  string     `json:"profile"`            // the profile it was moved to
	Previous int64      `json:"previous,omitempty"` // profile id before the move
}

type saved struct {
	Actions []Action `json:"actions"`
	// Seen marks items found already on the upgrade profile, so they are not
	// looked up again every hour ("sonarr:tv:123").
	Seen      map[string]time.Time `json:"seen,omitempty"`
	LastRun   time.Time            `json:"lastRun"`
	LastError string               `json:"lastError,omitempty"`
}

// Service runs the check.
type Service struct {
	Cfg     *config.Config
	Log     *log.Logger
	State   state.Blobs
	Now     func() time.Time
	History func(config.Playback) History // nil: Tracearr from the settings
	Arr     func(config.Category) Arr     // nil: arr.New from the category

	mu    sync.Mutex
	st    saved
	ready bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) load() {
	if s.ready {
		return
	}
	s.ready = true
	if s.State == nil {
		return
	}
	if b, ok, _ := s.State.Load(stateName); ok {
		_ = json.Unmarshal(b, &s.st)
	}
}

func (s *Service) save() {
	if s.State == nil {
		return
	}
	if len(s.st.Actions) > 200 {
		s.st.Actions = s.st.Actions[len(s.st.Actions)-200:]
	}
	if b, err := json.Marshal(s.st); err == nil {
		_ = s.State.Save(stateName, b)
	}
}

// Status is what the admin shows.
type Status struct {
	Actions   []Action  `json:"actions"`
	LastRun   time.Time `json:"lastRun"`
	LastError string    `json:"lastError,omitempty"`
}

// Snapshot returns the recent actions, newest first.
func (s *Service) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	out := Status{LastRun: s.st.LastRun, LastError: s.st.LastError}
	for i := len(s.st.Actions) - 1; i >= 0 && len(out.Actions) < 50; i-- {
		out.Actions = append(out.Actions, s.st.Actions[i])
	}
	return out
}

// Run checks once an hour (the first time five minutes after start).
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.Check(ctx)
		t.Reset(time.Hour)
	}
}

func (s *Service) history(pb config.Playback) History {
	if s.History != nil {
		return s.History(pb)
	}
	return tracearr.New(pb.TracearrURL, string(pb.TracearrKey))
}

func (s *Service) arrFor(c config.Category) Arr {
	if s.Arr != nil {
		return s.Arr(c)
	}
	return arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey))
}

// candidate is something that got enough attention.
type candidate struct {
	kind   string // show | movie
	tvdb   int64
	tmdb   int64
	title  string
	season int // the season the watcher is on (shows)
	reason string
	when   time.Time // the latest qualifying play
}

func counts(p tracearr.Play) bool { return p.Watched || p.Percent >= minPercent }

// Check runs one pass: hand-offs for earlier switches, then new switches.
func (s *Service) Check(ctx context.Context) {
	set := s.Cfg.Get()
	at := set.Attention
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if !at.On {
		return
	}
	now := s.now()
	s.st.LastRun, s.st.LastError = now, ""
	defer s.save()

	s.handoffs(ctx, set, now)

	if set.Playback.TracearrURL == "" || set.Playback.TracearrKey == "" {
		s.st.LastError = "Tracearr is not set up (Settings › Media server)"
		return
	}
	h := s.history(set.Playback)
	cands, err := s.candidates(ctx, h, now)
	if err != nil {
		s.st.LastError = "reading Tracearr's history: " + err.Error()
		s.Log.Printf("attention: %s", s.st.LastError)
		return
	}
	budget := at.PerDay
	if budget <= 0 {
		budget = 3
	}
	for _, a := range s.st.Actions {
		if now.Sub(a.At) < 24*time.Hour && a.Error == "" {
			budget--
		}
	}
	for _, c := range cands {
		if budget <= 0 {
			return
		}
		if s.act(ctx, set, c, now) {
			budget--
		}
	}
}

// candidates reads the plays and picks what qualifies, most recent first.
func (s *Service) candidates(ctx context.Context, h History, now time.Time) ([]candidate, error) {
	var out []candidate
	movies, err := h.History(ctx, "movie", now.AddDate(0, 0, -movieDays))
	if err != nil {
		return nil, err
	}
	type mv struct {
		title string
		users map[string]string
		when  time.Time
	}
	byMovie := map[int64]*mv{}
	for _, p := range movies {
		if p.TMDB == 0 || !counts(p) {
			continue
		}
		m := byMovie[p.TMDB]
		if m == nil {
			m = &mv{title: p.Title, users: map[string]string{}}
			byMovie[p.TMDB] = m
		}
		m.users[p.User.ID] = p.User.Username
		if p.StartedAt != nil && p.StartedAt.After(m.when) {
			m.when = *p.StartedAt
		}
	}
	for id, m := range byMovie {
		if len(m.users) >= movieViewers {
			out = append(out, candidate{kind: "movie", tmdb: id, title: m.title, when: m.when,
				reason: fmt.Sprintf("watched by %d people in %d days (%s)", len(m.users), movieDays, names(m.users))})
		}
	}

	eps, err := h.History(ctx, "episode", now.AddDate(0, 0, -showDays))
	if err != nil {
		return nil, err
	}
	type watch struct {
		show, user, name string
		eps              map[[2]int]bool
		season           int
		when             time.Time
	}
	byWatch := map[[2]string]*watch{}
	for _, p := range eps {
		if p.ShowMediaID == "" || !counts(p) {
			continue
		}
		k := [2]string{p.ShowMediaID, p.User.ID}
		w := byWatch[k]
		if w == nil {
			w = &watch{show: p.ShowTitle, user: p.User.ID, name: p.User.Username, eps: map[[2]int]bool{}}
			byWatch[k] = w
		}
		w.eps[[2]int{p.Season, p.Episode}] = true
		if p.StartedAt != nil && p.StartedAt.After(w.when) {
			w.when, w.season = *p.StartedAt, p.Season
		}
	}
	done := map[string]bool{}
	for k, w := range byWatch {
		if len(w.eps) < showEpisodes || done[k[0]] {
			continue
		}
		done[k[0]] = true
		tvdb, _, title, err := h.ShowIDs(ctx, k[0])
		if err != nil || tvdb == 0 {
			continue
		}
		if title == "" {
			title = w.show
		}
		out = append(out, candidate{kind: "show", tvdb: tvdb, title: title, season: w.season, when: w.when,
			reason: fmt.Sprintf("%s watched %d episodes in %d days", w.name, len(w.eps), showDays)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].when.After(out[j].when) })
	return out, nil
}

func names(m map[string]string) string {
	var n []string
	for _, v := range m {
		if v != "" {
			n = append(n, v)
		}
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

// act moves one candidate to the upgrade profile and starts its searches; it
// reports whether it switched something.
func (s *Service) act(ctx context.Context, set config.Settings, c candidate, now time.Time) bool {
	kind, profile := "sonarr", set.Attention.TVProfile
	if c.kind == "movie" {
		kind, profile = "radarr", set.Attention.MovieProfile
	}
	if strings.TrimSpace(profile) == "" {
		return false
	}
	for _, cat := range set.Categories {
		if cat.Kind != kind || cat.ArrURL == "" {
			continue
		}
		key := fmt.Sprintf("%s:%s:%d:%d", kind, cat.Name, c.tvdb, c.tmdb)
		if t, ok := s.st.Seen[key]; ok && now.Sub(t) < 7*24*time.Hour {
			return false
		}
		a := s.arrFor(cat)
		var it arr.Item
		var found bool
		var err error
		if c.kind == "movie" {
			it, found, err = a.MovieByTMDB(ctx, c.tmdb)
		} else {
			it, found, err = a.SeriesByTVDB(ctx, c.tvdb)
		}
		if err != nil || !found {
			continue
		}
		p, err := a.ProfileByName(ctx, profile)
		if err != nil {
			s.st.LastError = err.Error()
			return false
		}
		if it.ProfileID == p.ID {
			if s.st.Seen == nil {
				s.st.Seen = map[string]time.Time{}
			}
			s.st.Seen[key] = now
			return false
		}
		act := Action{Kind: c.kind, Category: cat.Name, ArrID: it.ID, Title: it.Title, Reason: c.reason, At: now, Profile: p.Name, Previous: it.ProfileID}
		if act.Title == "" {
			act.Title = c.title
		}
		if err := a.SetProfile(ctx, it.ID, p.ID); err != nil {
			act.Error = "moving it to " + p.Name + ": " + err.Error()
			s.st.Actions = append(s.st.Actions, act)
			s.Log.Printf("attention: %s: %s", act.Title, act.Error)
			return false
		}
		if c.kind == "movie" {
			if err := a.SearchMovie(ctx, it.ID); err != nil {
				act.Error = "starting the search: " + err.Error()
			}
		} else {
			for _, sn := range searchSeasons(it, c.season) {
				if err := a.SearchSeason(ctx, it.ID, sn); err != nil {
					act.Error = "starting the search: " + err.Error()
					break
				}
				act.Seasons = append(act.Seasons, sn)
			}
		}
		s.st.Actions = append(s.st.Actions, act)
		s.Log.Printf("attention: moved %s to %s (%s)", act.Title, p.Name, c.reason)
		return true
	}
	return false
}

// searchSeasons is the season being watched and the next one, as far as the
// series has them (specials are skipped).
func searchSeasons(it arr.Item, cur int) []int {
	if cur < 1 {
		cur = 1
	}
	have := map[int]bool{}
	for _, s := range it.Seasons {
		have[s.Number] = true
	}
	var out []int
	for _, n := range []int{cur, cur + 1} {
		if len(it.Seasons) == 0 || have[n] {
			out = append(out, n)
		}
	}
	return out
}

// handoffs sends the whole-series search for shows whose first two seasons
// were searched and have nothing left in Sonarr's queue.
func (s *Service) handoffs(ctx context.Context, set config.Settings, now time.Time) {
	for i := range s.st.Actions {
		a := &s.st.Actions[i]
		if a.Kind != "show" || a.Handoff != nil || len(a.Seasons) == 0 || now.Sub(a.At) < handoffAfter {
			continue
		}
		cat, ok := set.Category(a.Category)
		if !ok || cat.ArrURL == "" {
			continue
		}
		ar := s.arrFor(cat)
		n, err := ar.QueuedFor(ctx, a.ArrID)
		if err != nil || n > 0 {
			continue
		}
		if err := ar.SearchSeries(ctx, a.ArrID); err != nil {
			a.Error = "handing the rest to Sonarr: " + err.Error()
			continue
		}
		t := now
		a.Handoff, a.Error = &t, ""
		s.Log.Printf("attention: %s: seasons %v done, Sonarr searches the rest", a.Title, a.Seasons)
	}
}
