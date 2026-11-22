package attention

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/tracearr"
)

type fakeHist struct{ movies, eps []tracearr.Play }

func (f *fakeHist) History(_ context.Context, kind string, _ time.Time) ([]tracearr.Play, error) {
	if kind == "movie" {
		return f.movies, nil
	}
	return f.eps, nil
}
func (f *fakeHist) ShowIDs(_ context.Context, id string) (int64, int64, string, error) {
	if id == "show-grey" {
		return 73762, 1416, "Grey's Anatomy", nil
	}
	return 0, 0, "", nil
}

type fakeArr struct {
	items   map[int64]arr.Item
	profile int64
	calls   []string
	queued  int
}

func (f *fakeArr) ProfileByName(_ context.Context, n string) (arr.Profile, error) {
	return arr.Profile{ID: 9, Name: n}, nil
}
func (f *fakeArr) SeriesByTVDB(_ context.Context, id int64) (arr.Item, bool, error) {
	it, ok := f.items[id]
	return it, ok, nil
}
func (f *fakeArr) MovieByTMDB(_ context.Context, id int64) (arr.Item, bool, error) {
	it, ok := f.items[id]
	return it, ok, nil
}
func (f *fakeArr) SetProfile(_ context.Context, id, p int64) error {
	f.calls = append(f.calls, "profile")
	return nil
}
func (f *fakeArr) SearchSeason(_ context.Context, id int64, s int) error {
	f.calls = append(f.calls, "season"+string(rune('0'+s)))
	return nil
}
func (f *fakeArr) SearchSeries(_ context.Context, id int64) error {
	f.calls = append(f.calls, "series")
	return nil
}
func (f *fakeArr) SearchMovie(_ context.Context, id int64) error {
	f.calls = append(f.calls, "movie")
	return nil
}
func (f *fakeArr) QueuedFor(_ context.Context, id int64) (int, error) { return f.queued, nil }

func play(user string, at time.Time, mut func(*tracearr.Play)) tracearr.Play {
	p := tracearr.Play{Watched: true, StartedAt: &at}
	p.User.ID, p.User.Username = user, user
	mut(&p)
	return p
}

func TestMovesAndSearches(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Playback.TracearrURL, st.Playback.TracearrKey = "http://t", "k"
	st.Categories = []config.Category{{Name: "tv", Kind: "sonarr", ArrURL: "http://s"}, {Name: "movies", Kind: "radarr", ArrURL: "http://r"}}
	st.Attention = config.Attention{On: true, TVProfile: "Upgrade", MovieProfile: "Upgrade"}
	if err := cfg.Set(st); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)
	h := &fakeHist{
		movies: []tracearr.Play{
			play("ann", now.Add(-48*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title = 603, "The Matrix" }),
			play("bob", now.Add(-24*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title = 603, "The Matrix" }),
			play("ann", now.Add(-24*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title = 604, "Alone" }),
			play("bob", now.Add(-24*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Watched, p.Percent = 604, false, 10 }),
		},
		eps: []tracearr.Play{
			play("ann", now.Add(-3*time.Hour), func(p *tracearr.Play) { p.ShowMediaID, p.Season, p.Episode = "show-grey", 4, 1 }),
			play("ann", now.Add(-2*time.Hour), func(p *tracearr.Play) { p.ShowMediaID, p.Season, p.Episode = "show-grey", 4, 2 }),
			play("ann", now.Add(-1*time.Hour), func(p *tracearr.Play) { p.ShowMediaID, p.Season, p.Episode = "show-grey", 4, 3 }),
		},
	}
	son := &fakeArr{items: map[int64]arr.Item{73762: {ID: 5, Title: "Grey's Anatomy", ProfileID: 1}}}
	son.items[73762] = withSeasons(son.items[73762], 1, 2, 3, 4, 5)
	rad := &fakeArr{items: map[int64]arr.Item{603: {ID: 7, Title: "The Matrix", ProfileID: 1}, 604: {ID: 8, Title: "Alone", ProfileID: 1}}}
	s := &Service{Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now },
		History: func(config.Playback) History { return h },
		Arr: func(c config.Category) Arr {
			if c.Kind == "sonarr" {
				return son
			}
			return rad
		}}
	s.Check(context.Background())
	if got := join(son.calls); got != "profile season4 season5" {
		t.Fatalf("sonarr calls %q", got)
	}
	if got := join(rad.calls); got != "profile movie" {
		t.Fatalf("radarr calls %q (only The Matrix had two viewers)", got)
	}
	// Two hours later with the queue empty, the rest goes to Sonarr.
	now = now.Add(3 * time.Hour)
	son.items[73762] = withProfile(son.items[73762], 9)
	rad.items[603] = withProfile(rad.items[603], 9)
	s.Check(context.Background())
	if got := join(son.calls); got != "profile season4 season5 series" {
		t.Fatalf("hand-off: %q", got)
	}
	if n := len(s.Snapshot().Actions); n != 2 {
		t.Fatalf("actions %d", n)
	}
}

func withSeasons(it arr.Item, ns ...int) arr.Item {
	for _, n := range ns {
		it.Seasons = append(it.Seasons, struct {
			Number    int  `json:"seasonNumber"`
			Monitored bool `json:"monitored"`
		}{n, true})
	}
	return it
}
func withProfile(it arr.Item, p int64) arr.Item { it.ProfileID = p; return it }
func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out
}

// Titles over the daily limit wait in the queue and are done on a later day,
// even when their watching has left the history window by then.
func TestBacklogCarriesOver(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Playback.TracearrURL, st.Playback.TracearrKey = "http://t", "k"
	st.Categories = []config.Category{{Name: "movies", Kind: "radarr", ArrURL: "http://r"}}
	st.Attention = config.Attention{On: true, MovieProfile: "Upgrade", PerDay: 1}
	if err := cfg.Set(st); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)
	h := &fakeHist{movies: []tracearr.Play{
		play("ann", now.Add(-5*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title = 1, "A" }),
		play("bob", now.Add(-5*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title = 1, "A" }),
		play("ann", now.Add(-2*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title = 2, "B" }),
		play("bob", now.Add(-2*time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title = 2, "B" }),
	}}
	rad := &fakeArr{items: map[int64]arr.Item{1: {ID: 11, Title: "A", ProfileID: 1}, 2: {ID: 12, Title: "B", ProfileID: 1}}}
	s := &Service{Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now },
		History: func(config.Playback) History { return h }, Arr: func(config.Category) Arr { return rad }}
	s.Check(context.Background())
	if got := join(rad.calls); got != "profile movie" {
		t.Fatalf("day one: %q", got)
	}
	if q := s.Snapshot().Queue; len(q) != 1 || q[0].Title != "B" {
		t.Fatalf("queue after day one: %+v", q)
	}
	// The history no longer shows B; the queue still does it the next day.
	h.movies = nil
	now = now.Add(25 * time.Hour)
	s.Check(context.Background())
	if got := join(rad.calls); got != "profile movie profile movie" {
		t.Fatalf("day two: %q", got)
	}
	if q := s.Snapshot().Queue; len(q) != 0 {
		t.Fatalf("queue after day two: %+v", q)
	}
}
