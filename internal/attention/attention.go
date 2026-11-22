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
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

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
	SeriesByID(ctx context.Context, id int64) (arr.Item, error)
	LookupMovie(ctx context.Context, tmdb int64) (arr.MovieLookup, error)
	RootFolders(ctx context.Context) ([]arr.Folder, error)
	AddMovie(ctx context.Context, m arr.MovieLookup, profileID int64, path, root string, search bool) (arr.Item, error)
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
	Added    string     `json:"added,omitempty"`    // added to Radarr: the folder it was tied to
	Manual   bool       `json:"manual,omitempty"`   // done by the admin; not counted in the daily limits
	Reverted *time.Time `json:"reverted,omitempty"` // moved back to its old profile
	Note     string     `json:"note,omitempty"`     // why it was moved back
	Checked  bool       `json:"checked,omitempty"`  // looked at for anime once
}

// Pending is a much-watched movie Radarr does not have, waiting for the
// admin to decide (HyveMynd was not sure enough to add it itself).
type Pending struct {
	TMDB     int64     `json:"tmdb"`
	Title    string    `json:"title"`
	Year     int       `json:"year,omitempty"`
	Reason   string    `json:"reason"`
	Why      string    `json:"why"` // why it was not added by itself
	Category string    `json:"category"`
	Since    time.Time `json:"since"`
}

type saved struct {
	Actions []Action `json:"actions"`
	// Seen marks items found already on the upgrade profile, so they are not
	// looked up again every hour ("sonarr:tv:123").
	Seen map[string]time.Time `json:"seen,omitempty"`
	// Queue holds titles that qualified beyond the daily limit, oldest
	// first; they are done on later days even once their watching is older
	// than the window that counted it.
	Queue  []Queued  `json:"queue,omitempty"`
	Decide []Pending `json:"decide,omitempty"`
	// Dismissed movies (TMDB id) are not offered again.
	Dismissed map[int64]time.Time `json:"dismissed,omitempty"`
	// Skip holds titles moved back (undone, or anime): never moved again
	// ("show:tv:5").
	Skip      map[string]bool `json:"skip,omitempty"`
	LastRun   time.Time       `json:"lastRun"`
	LastError string          `json:"lastError,omitempty"`
}

// Queued is a title waiting for its turn.
type Queued struct {
	Kind   string    `json:"kind"`
	TVDB   int64     `json:"tvdb,omitempty"`
	TMDB   int64     `json:"tmdb,omitempty"`
	Title  string    `json:"title"`
	Year   int       `json:"year,omitempty"`
	Season int       `json:"season,omitempty"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"` // when it first qualified
}

// queueKeep is how long a queued title waits at most.
const queueKeep = 60 * 24 * time.Hour

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
	Queue     []Queued  `json:"queue"`
	Decide    []Pending `json:"decide"`
	LastRun   time.Time `json:"lastRun"`
	LastError string    `json:"lastError,omitempty"`
}

// Snapshot returns the recent actions, newest first.
func (s *Service) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	out := Status{LastRun: s.st.LastRun, LastError: s.st.LastError, Queue: append([]Queued{}, s.st.Queue...), Decide: append([]Pending{}, s.st.Decide...)}
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
	year   int
	anime  bool // a play of it was tagged Anime
	season int  // the season the watcher is on (shows)
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

	s.revertAnime(ctx, set, now)
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
	// Each kind has its own daily limit: shows (Sonarr) and movies (Radarr).
	budget := map[string]int{"show": at.PerDay, "movie": at.MoviePerDay}
	for k, n := range budget {
		if n <= 0 {
			budget[k] = 3
		}
	}
	for _, a := range s.st.Actions {
		if now.Sub(a.At) < 24*time.Hour && a.Error == "" && !a.Manual && a.Reverted == nil {
			budget[a.Kind]--
		}
	}
	s.enqueue(cands, now)
	var keep []Queued
	for _, q := range s.st.Queue {
		if budget[q.Kind] <= 0 {
			keep = append(keep, q)
			continue
		}
		switch s.act(ctx, set, candidate{kind: q.Kind, tvdb: q.TVDB, tmdb: q.TMDB, title: q.Title, year: q.Year, season: q.Season, reason: q.Reason}, now) {
		case moved:
			budget[q.Kind]--
		case retry:
			keep = append(keep, q)
		}
	}
	s.st.Queue = keep
}

// enqueue adds new candidates to the queue (oldest first) and refreshes the
// ones already in it; titles waiting longer than queueKeep are dropped.
func (s *Service) enqueue(cands []candidate, now time.Time) {
	idx := map[string]int{}
	var q []Queued
	for _, x := range s.st.Queue {
		if now.Sub(x.Since) > queueKeep {
			continue
		}
		idx[qkey(x.Kind, x.TVDB, x.TMDB)] = len(q)
		q = append(q, x)
	}
	// candidates come newest first; add the oldest first so the queue keeps
	// its order by when each qualified.
	for i := len(cands) - 1; i >= 0; i-- {
		c := cands[i]
		k := qkey(c.kind, c.tvdb, c.tmdb)
		if j, ok := idx[k]; ok {
			q[j].Reason, q[j].Title = c.reason, c.title
			if c.season > 0 {
				q[j].Season = c.season
			}
			continue
		}
		idx[k] = len(q)
		q = append(q, Queued{Kind: c.kind, TVDB: c.tvdb, TMDB: c.tmdb, Title: c.title, Year: c.year, Season: c.season, Reason: c.reason, Since: now})
	}
	s.st.Queue = q
}

func qkey(kind string, tvdb, tmdb int64) string { return fmt.Sprintf("%s:%d:%d", kind, tvdb, tmdb) }

type outcome int

const (
	moved   outcome = iota // switched to the upgrade profile
	settled                // nothing to do: already on it, or no arr has it
	retry                  // could not finish now; try again later
)

// candidates reads the plays and picks what qualifies, most recent first.
func (s *Service) candidates(ctx context.Context, h History, now time.Time) ([]candidate, error) {
	var out []candidate
	movies, err := h.History(ctx, "movie", now.AddDate(0, 0, -movieDays))
	if err != nil {
		return nil, err
	}
	type mv struct {
		title string
		year  int
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
			m = &mv{title: p.Title, year: p.Year, users: map[string]string{}}
			byMovie[p.TMDB] = m
		}
		m.users[p.User.ID] = p.User.Username
		if p.StartedAt != nil && p.StartedAt.After(m.when) {
			m.when = *p.StartedAt
		}
	}
	for id, m := range byMovie {
		if len(m.users) >= movieViewers {
			out = append(out, candidate{kind: "movie", tmdb: id, title: m.title, year: m.year, when: m.when,
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
		anime            bool
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
		if hasAnime(p.Genres) {
			w.anime = true
		}
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
		if w.anime {
			continue
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

// act moves one candidate to the upgrade profile and starts its searches.
func (s *Service) act(ctx context.Context, set config.Settings, c candidate, now time.Time) outcome {
	kind, profile := "sonarr", set.Attention.TVProfile
	if c.kind == "movie" {
		kind, profile = "radarr", set.Attention.MovieProfile
	}
	if strings.TrimSpace(profile) == "" {
		return settled // that kind is switched off
	}
	reachable := false
	for _, cat := range set.Categories {
		if cat.Kind != kind || cat.ArrURL == "" {
			continue
		}
		key := fmt.Sprintf("%s:%s:%d:%d", kind, cat.Name, c.tvdb, c.tmdb)
		if t, ok := s.st.Seen[key]; ok && now.Sub(t) < 7*24*time.Hour {
			return settled
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
		if err != nil {
			continue
		}
		reachable = true
		if !found {
			continue
		}
		if s.st.Skip[skipKey(c.kind, cat.Name, it.ID)] {
			return settled
		}
		if c.kind == "show" && isAnime(it, set) {
			return settled // anime is left alone
		}
		p, err := a.ProfileByName(ctx, profile)
		if err != nil {
			s.st.LastError = err.Error()
			return retry
		}
		if it.ProfileID == p.ID {
			if s.st.Seen == nil {
				s.st.Seen = map[string]time.Time{}
			}
			s.st.Seen[key] = now
			return settled
		}
		act := Action{Kind: c.kind, Category: cat.Name, ArrID: it.ID, Title: it.Title, Reason: c.reason, At: now, Profile: p.Name, Previous: it.ProfileID}
		if act.Title == "" {
			act.Title = c.title
		}
		if err := a.SetProfile(ctx, it.ID, p.ID); err != nil {
			act.Error = "moving it to " + p.Name + ": " + err.Error()
			s.st.Actions = append(s.st.Actions, act)
			s.Log.Printf("attention: %s: %s", act.Title, act.Error)
			return retry
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
		return moved
	}
	if !reachable {
		return retry // no arr answered: keep it for the next check
	}
	if c.kind == "movie" {
		return s.addMissingMovie(ctx, set, c, now)
	}
	return settled // no arr has it
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
		if a.Kind != "show" || a.Handoff != nil || a.Reverted != nil || len(a.Seasons) == 0 || now.Sub(a.At) < handoffAfter {
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

// addMissingMovie handles a much-watched movie Radarr does not have. When
// HyveMynd is sure (Radarr's lookup of the TMDB id gives the same title and
// year, and a Radarr library folder holds an untied folder of that name),
// it adds the movie tied to that folder, so Radarr adopts the file already
// there and the search upgrades it. Otherwise the movie waits for the admin.
func (s *Service) addMissingMovie(ctx context.Context, set config.Settings, c candidate, now time.Time) outcome {
	if _, gone := s.st.Dismissed[c.tmdb]; gone {
		return settled
	}
	for _, p := range s.st.Decide {
		if p.TMDB == c.tmdb {
			return settled // already waiting for a decision
		}
	}
	var cat config.Category
	for _, x := range set.Categories {
		if x.Kind == "radarr" && x.ArrURL != "" {
			cat = x
			break
		}
	}
	if cat.Name == "" {
		return settled
	}
	a := s.arrFor(cat)
	wait := func(why string) outcome {
		s.st.Decide = append(s.st.Decide, Pending{TMDB: c.tmdb, Title: c.title, Year: c.year, Reason: c.reason, Why: why, Category: cat.Name, Since: now})
		return settled
	}
	m, err := a.LookupMovie(ctx, c.tmdb)
	if err != nil {
		var se *arr.StatusError
		if (errors.As(err, &se) && se.Code != 404) || strings.Contains(err.Error(), "not reachable") {
			return retry
		}
		return wait("Radarr could not look it up by its TMDB id")
	}
	if !sameMovie(c, m) {
		return wait(fmt.Sprintf("Radarr knows TMDB %d as %s (%d), which does not match what was watched", c.tmdb, m.Title, m.Year))
	}
	roots, err := a.RootFolders(ctx)
	if err != nil {
		return retry
	}
	folder := matchFolder(roots, m)
	if folder == "" {
		return wait("no folder for it in Radarr's library folders, so adding it could download a second copy")
	}
	p, err := a.ProfileByName(ctx, set.Attention.MovieProfile)
	if err != nil {
		s.st.LastError = err.Error()
		return retry
	}
	it, err := a.AddMovie(ctx, m, p.ID, folder, "", true)
	act := Action{Kind: "movie", Category: cat.Name, ArrID: it.ID, Title: m.Title, Reason: c.reason, At: now, Profile: p.Name, Added: folder}
	if err != nil {
		act.Error = "adding it to Radarr: " + err.Error()
		s.st.Actions = append(s.st.Actions, act)
		return wait("adding it failed: " + err.Error())
	}
	s.st.Actions = append(s.st.Actions, act)
	s.Log.Printf("attention: added %s to Radarr at %s on %s (%s)", m.Title, folder, p.Name, c.reason)
	return moved
}

// sameMovie compares what was watched with Radarr's lookup: the same title
// (or original title) and, when the play has one, a year at most one apart.
func sameMovie(c candidate, m arr.MovieLookup) bool {
	t := norm(c.title)
	if t == "" || (t != norm(m.Title) && t != norm(m.OriginalTitle)) {
		return false
	}
	if c.year != 0 && m.Year != 0 {
		d := c.year - m.Year
		return d >= -1 && d <= 1
	}
	return true
}

// matchFolder finds an untied folder in Radarr's library folders named for
// the movie: Radarr's own folder name, or "Title (Year)" with any tags such
// as {tmdb-603} or [imdb-...] ignored.
func matchFolder(roots []arr.Folder, m arr.MovieLookup) string {
	want := map[string]bool{}
	if m.Folder != "" {
		want[norm(stripTags(m.Folder))] = true
	}
	if m.Year != 0 {
		want[norm(fmt.Sprintf("%s (%d)", m.Title, m.Year))] = true
	}
	for _, r := range roots {
		for _, u := range r.Unmapped {
			if want[norm(stripTags(u.Name))] {
				return u.Path
			}
		}
	}
	return ""
}

func stripTags(s string) string {
	for _, p := range [][2]string{{"{", "}"}, {"[", "]"}} {
		for {
			i := strings.Index(s, p[0])
			j := strings.Index(s, p[1])
			if i < 0 || j < i {
				break
			}
			s = s[:i] + s[j+1:]
		}
	}
	return s
}

// norm keeps letters and digits, lower case ("Alien³" and "Alien 3" differ;
// "Spider-Man: No Way Home" and "Spider Man No Way Home" match).
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Decide adds a waiting movie to Radarr (on the upgrade profile, tied to its
// folder when there is one, else in the first library folder, and searched)
// or dismisses it so it is not offered again.
func (s *Service) Decide(ctx context.Context, tmdb int64, add bool) error {
	set := s.Cfg.Get()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	defer s.save()
	idx := -1
	for i, p := range s.st.Decide {
		if p.TMDB == tmdb {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("that movie is not waiting for a decision")
	}
	p := s.st.Decide[idx]
	drop := func() { s.st.Decide = append(s.st.Decide[:idx], s.st.Decide[idx+1:]...) }
	if !add {
		if s.st.Dismissed == nil {
			s.st.Dismissed = map[int64]time.Time{}
		}
		s.st.Dismissed[tmdb] = s.now()
		drop()
		return nil
	}
	cat, ok := set.Category(p.Category)
	if !ok {
		return fmt.Errorf("category %s is gone", p.Category)
	}
	if strings.TrimSpace(set.Attention.MovieProfile) == "" {
		return fmt.Errorf("choose the Radarr upgrade profile first")
	}
	a := s.arrFor(cat)
	m, err := a.LookupMovie(ctx, tmdb)
	if err != nil {
		return err
	}
	pr, err := a.ProfileByName(ctx, set.Attention.MovieProfile)
	if err != nil {
		return err
	}
	roots, err := a.RootFolders(ctx)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return fmt.Errorf("Radarr has no library folder")
	}
	folder := matchFolder(roots, m)
	it, err := a.AddMovie(ctx, m, pr.ID, folder, roots[0].Path, true)
	if err != nil {
		return err
	}
	where := folder
	if where == "" {
		where = roots[0].Path + " (new folder)"
	}
	s.st.Actions = append(s.st.Actions, Action{Kind: "movie", Category: cat.Name, ArrID: it.ID, Title: m.Title, Reason: p.Reason + " · added by you", At: s.now(), Profile: pr.Name, Added: where, Manual: true})
	drop()
	return nil
}

func skipKey(kind, cat string, id int64) string { return fmt.Sprintf("%s:%s:%d", kind, cat, id) }

func hasAnime(genres []string) bool {
	for _, g := range genres {
		if strings.EqualFold(strings.TrimSpace(g), "anime") {
			return true
		}
	}
	return false
}

// isAnime says whether a Sonarr series is anime: Sonarr's series type, its
// genres, or a folder inside one of HyveMynd's own anime libraries.
func isAnime(it arr.Item, set config.Settings) bool {
	if strings.EqualFold(it.SeriesType, "anime") || hasAnime(it.Genres) {
		return true
	}
	if it.Path == "" {
		return false
	}
	p := strings.ToLower(strings.TrimRight(it.Path, "/")) + "/"
	for _, c := range set.Categories {
		if d := c.OwnLibrary(); d != "" && strings.HasPrefix(p, strings.ToLower(strings.TrimRight(d, "/"))+"/") {
			return true
		}
	}
	return false
}

// revertAnime looks once at each show already moved and puts anime back on
// the profile it had.
func (s *Service) revertAnime(ctx context.Context, set config.Settings, now time.Time) {
	for i := range s.st.Actions {
		a := &s.st.Actions[i]
		if a.Kind != "show" || a.Checked || a.Reverted != nil || a.Error != "" {
			continue
		}
		cat, ok := set.Category(a.Category)
		if !ok || cat.ArrURL == "" {
			continue
		}
		ar := s.arrFor(cat)
		it, err := ar.SeriesByID(ctx, a.ArrID)
		if err != nil {
			continue
		}
		a.Checked = true
		if !isAnime(it, set) {
			continue
		}
		if err := s.moveBack(ctx, ar, a, now, "anime: moved back to its old profile"); err != nil {
			a.Error = "moving the anime back: " + err.Error()
		}
	}
}

// moveBack puts a moved title back on its old profile and keeps it from
// being moved again. Searches already started cannot be taken back.
func (s *Service) moveBack(ctx context.Context, ar Arr, a *Action, now time.Time, note string) error {
	if a.Previous == 0 {
		return fmt.Errorf("its old profile is not known")
	}
	if err := ar.SetProfile(ctx, a.ArrID, a.Previous); err != nil {
		return err
	}
	t := now
	a.Reverted, a.Note = &t, note
	if s.st.Skip == nil {
		s.st.Skip = map[string]bool{}
	}
	s.st.Skip[skipKey(a.Kind, a.Category, a.ArrID)] = true
	s.Log.Printf("attention: %s: %s", a.Title, note)
	return nil
}

// Undo moves a title back to the profile it had before HyveMynd moved it,
// and never moves it again.
func (s *Service) Undo(ctx context.Context, kind, category string, arrID int64) error {
	set := s.Cfg.Get()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	defer s.save()
	for i := len(s.st.Actions) - 1; i >= 0; i-- {
		a := &s.st.Actions[i]
		if a.Kind != kind || a.Category != category || a.ArrID != arrID || a.Reverted != nil {
			continue
		}
		cat, ok := set.Category(category)
		if !ok {
			return fmt.Errorf("category %s is gone", category)
		}
		return s.moveBack(ctx, s.arrFor(cat), a, s.now(), "undone by you")
	}
	return fmt.Errorf("nothing to undo for that title")
}
