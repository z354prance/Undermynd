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
	"github.com/z354prance/undermynd/internal/mediaserver"
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
	lastProfile int64
	lookups     map[int64]arr.MovieLookup
	roots       []arr.Folder
	items       map[int64]arr.Item
	profile     int64
	calls       []string
	queued      int
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
	f.lastProfile = p
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
func (f *fakeArr) LookupMovie(_ context.Context, id int64) (arr.MovieLookup, error) {
	if m, ok := f.lookups[id]; ok {
		return m, nil
	}
	return arr.MovieLookup{}, &arr.StatusError{Code: 404, Kind: "radarr"}
}
func (f *fakeArr) RootFolders(context.Context) ([]arr.Folder, error) { return f.roots, nil }
func (f *fakeArr) AddMovie(_ context.Context, m arr.MovieLookup, p int64, path, root string, search bool) (arr.Item, error) {
	f.calls = append(f.calls, "add:"+path+root)
	it := arr.Item{ID: 99, Title: m.Title, ProfileID: p}
	f.items[m.TMDB] = it
	return it, nil
}
func (f *fakeArr) SeriesByID(_ context.Context, id int64) (arr.Item, error) {
	for _, it := range f.items {
		if it.ID == id {
			return it, nil
		}
	}
	return arr.Item{}, &arr.StatusError{Code: 404, Kind: "sonarr"}
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
	// One season at a time: after 4 and 5 (the last), back to season 1.
	if got := join(son.calls); got != "profile season4 season5 season1" {
		t.Fatalf("next season: %q", got)
	}
	// Not again while Sonarr is still busy with it.
	son.queued = 2
	now = now.Add(3 * time.Hour)
	s.Check(context.Background())
	son.queued = 0
	if got := join(son.calls); got != "profile season4 season5 season1" {
		t.Fatalf("searched while the queue was busy: %q", got)
	}
	for _, want := range []string{"season2", "season3"} {
		now = now.Add(3 * time.Hour)
		s.Check(context.Background())
		if c := son.calls[len(son.calls)-1]; c != want {
			t.Fatalf("want %s, got %v", want, son.calls)
		}
	}
	now = now.Add(3 * time.Hour)
	s.Check(context.Background())
	var grey Action
	for _, a := range s.Snapshot().Actions {
		if a.Kind == "show" {
			grey = a
		}
	}
	if grey.Handoff == nil || len(son.calls) != 6 {
		t.Fatalf("should be done after every season: %+v %v", grey, son.calls)
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
	st.Attention = config.Attention{On: true, MovieProfile: "Upgrade", PerDay: 5, MoviePerDay: 1}
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

// A much-watched movie Radarr does not have is added when Radarr's lookup
// matches and its folder is already in Radarr's library folder; otherwise it
// waits for a decision, and Decide adds or dismisses it.
func TestMissingMovieAddedOrWaits(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Playback.TracearrURL, st.Playback.TracearrKey = "http://t", "k"
	st.Categories = []config.Category{{Name: "movies", Kind: "radarr", ArrURL: "http://r"}}
	st.Attention = config.Attention{On: true, MovieProfile: "Upgrade", PerDay: 5}
	if err := cfg.Set(st); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)
	two := func(id int64, title string, year int) []tracearr.Play {
		var out []tracearr.Play
		for _, u := range []string{"ann", "bob"} {
			out = append(out, play(u, now.Add(-time.Hour), func(p *tracearr.Play) { p.TMDB, p.Title, p.Year = id, title, year }))
		}
		return out
	}
	var plays []tracearr.Play
	plays = append(plays, two(603, "The Matrix", 1999)...)
	plays = append(plays, two(604, "Heat", 1995)...)
	plays = append(plays, two(605, "Alien", 1979)...)
	h := &fakeHist{movies: plays}
	root := arr.Folder{Path: "/movies"}
	root.Unmapped = append(root.Unmapped, struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}{"The Matrix (1999) {tmdb-603}", "/movies/The Matrix (1999) {tmdb-603}"})
	rad := &fakeArr{items: map[int64]arr.Item{}, roots: []arr.Folder{root}, lookups: map[int64]arr.MovieLookup{
		603: {Title: "The Matrix", Year: 1999, TMDB: 603, Folder: "The Matrix (1999)"},
		604: {Title: "Heat", Year: 1995, TMDB: 604, Folder: "Heat (1995)"}, // no folder for it
		605: {Title: "Aliens", Year: 1986, TMDB: 605},                      // does not match
	}}
	s := &Service{Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now },
		History: func(config.Playback) History { return h }, Arr: func(config.Category) Arr { return rad }}
	s.Check(context.Background())
	if got := join(rad.calls); got != "add:/movies/The Matrix (1999) {tmdb-603}" {
		t.Fatalf("calls %q", got)
	}
	d := s.Snapshot().Decide
	if len(d) != 2 {
		t.Fatalf("waiting %+v", d)
	}
	// A second pass does not offer them twice.
	s.Check(context.Background())
	if n := len(s.Snapshot().Decide); n != 2 {
		t.Fatalf("waiting after a second pass: %d", n)
	}
	if err := s.Decide(context.Background(), 604, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(context.Background(), 605, false); err != nil {
		t.Fatal(err)
	}
	if got := rad.calls[len(rad.calls)-1]; got != "add:/movies" {
		t.Fatalf("Heat should go to a new folder in the first library folder: %q", got)
	}
	s.Check(context.Background())
	if n := len(s.Snapshot().Decide); n != 0 {
		t.Fatalf("a dismissed movie came back: %+v", s.Snapshot().Decide)
	}
}

// Anime in Sonarr is never moved; anime moved by an earlier version is put
// back on its old profile and left alone after; Undo moves a title back.
func TestAnimeLeftAloneAndMovedBack(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Playback.TracearrURL, st.Playback.TracearrKey = "http://t", "k"
	st.Categories = []config.Category{{Name: "tv", Kind: "sonarr", ArrURL: "http://s"}, {Name: "anime", Kind: "rss", LibraryDir: "/media/Anime"}}
	st.Attention = config.Attention{On: true, TVProfile: "Upgrade", PerDay: 5}
	if err := cfg.Set(st); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)
	eps := func(show string) []tracearr.Play {
		var out []tracearr.Play
		for e := 1; e <= 3; e++ {
			e := e
			out = append(out, play("ann", now.Add(-time.Duration(e)*time.Hour), func(p *tracearr.Play) { p.ShowMediaID, p.Season, p.Episode = show, 1, e }))
		}
		return out
	}
	h := &fakeHist{eps: eps("show-grey")}
	son := &fakeArr{items: map[int64]arr.Item{73762: {ID: 5, Title: "Frieren", ProfileID: 1, Path: "/media/Anime/Frieren"}}}
	s := &Service{Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now },
		History: func(config.Playback) History { return h }, Arr: func(config.Category) Arr { return son }}
	s.Check(context.Background())
	if len(son.calls) != 0 {
		t.Fatalf("anime in an anime folder was touched: %v", son.calls)
	}
	// An earlier version moved it: it goes back to profile 1 and stays.
	s.st.Actions = append(s.st.Actions, Action{Kind: "show", Category: "tv", ArrID: 5, Title: "Frieren", At: now, Previous: 1, Seasons: []int{1}})
	s.Check(context.Background())
	if son.lastProfile != 1 || s.Snapshot().Actions[0].Reverted == nil {
		t.Fatalf("not moved back: profile %d %+v", son.lastProfile, s.Snapshot().Actions[0])
	}
	// Undo on an ordinary show.
	s.st.Actions = append(s.st.Actions, Action{Kind: "show", Category: "tv", ArrID: 6, Title: "Suits", At: now, Previous: 2, Checked: true})
	if err := s.Undo(context.Background(), "show", "tv", 6); err != nil || son.lastProfile != 2 {
		t.Fatalf("undo: %v, profile %d", err, son.lastProfile)
	}
	if !s.st.Skip["show:tv:6"] {
		t.Fatal("an undone show should not be moved again")
	}
}

type fakeMedia struct {
	libs  []mediaserver.Library
	paths map[string]string
}

func (f fakeMedia) Libraries(context.Context) ([]mediaserver.Library, error) { return f.libs, nil }
func (f fakeMedia) ItemPath(_ context.Context, id string) (string, error)    { return f.paths[id], nil }

// A show in a mixed Shows library whose files are in the anime folder is
// left alone and leaves the queue; a show already on the upgrade profile
// never waits in the queue.
func TestQueueScreenedForAnimeAndProfile(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Playback.TracearrURL, st.Playback.TracearrKey = "http://t", "k"
	st.Playback.PathFrom, st.Playback.PathTo = "/media", "/mnt/user/media"
	st.Categories = []config.Category{{Name: "tv", Kind: "sonarr", ArrURL: "http://s"}, {Name: "anime", Kind: "rss", LibraryDir: "/media/Anime"}}
	st.Attention = config.Attention{On: true, TVProfile: "Upgrade", PerDay: 1}
	if err := cfg.Set(st); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)
	eps := func(show, item string, n int) []tracearr.Play {
		var out []tracearr.Play
		for e := 1; e <= 3; e++ {
			e := e
			out = append(out, play("ann", now.Add(-time.Duration(n*10+e)*time.Minute), func(p *tracearr.Play) {
				p.ShowMediaID, p.Season, p.Episode, p.LibraryID, p.RatingKey = show, 1, e, "lib-shows", item
			}))
		}
		return out
	}
	var plays []tracearr.Play
	plays = append(plays, eps("show-a", "it-a", 1)...) // ordinary, waits for tomorrow
	plays = append(plays, eps("show-b", "it-b", 2)...) // ordinary, already on the profile
	plays = append(plays, eps("show-c", "it-c", 3)...) // anime by its folder
	plays = append(plays, eps("show-d", "it-d", 4)...) // ordinary, earliest: moved first
	h := &fakeHist{eps: plays}
	ids := map[string]int64{"show-a": 1, "show-b": 2, "show-c": 3, "show-d": 4}
	hist := histIDs{h, ids}
	son := &fakeArr{items: map[int64]arr.Item{1: {ID: 11, Title: "A", ProfileID: 1}, 2: {ID: 12, Title: "B", ProfileID: 9}, 3: {ID: 13, Title: "Rich Girl", ProfileID: 1, Path: "/tv/Rich Girl"}, 4: {ID: 14, Title: "D", ProfileID: 1}}}
	med := fakeMedia{libs: []mediaserver.Library{{ID: "lib-shows", Name: "Shows", Paths: []string{"/mnt/user/media/TV", "/mnt/user/media/TV2"}}},
		paths: map[string]string{"it-c": "/mnt/user/media/Anime/Rich Girl Caretaker/S01E01.mkv", "it-a": "/mnt/user/media/TV/A/S01E01.mkv"}}
	s := &Service{Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now },
		History: func(config.Playback) History { return hist }, Arr: func(config.Category) Arr { return son },
		Media: func(config.Settings) Media { return med }}
	s.Check(context.Background())
	if got := join(son.calls); got != "profile season1 season2" {
		t.Fatalf("calls %q", got)
	}
	q := s.Snapshot().Queue
	// D qualified earliest so went first; A waits; B (already on the
	// profile) and C (anime by its folder) never wait.
	if len(q) != 1 || q[0].TVDB != 1 {
		t.Fatalf("queue should hold only A: %+v", q)
	}
}

// histIDs maps each show's Tracearr id to a TVDB id.
type histIDs struct {
	*fakeHist
	ids map[string]int64
}

func (h histIDs) ShowIDs(_ context.Context, id string) (int64, int64, string, error) {
	return h.ids[id], 0, id, nil
}

// Shows go one at a time: the second waits until every season of the first
// has been searched.
func TestShowsOneAtATime(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	cfg, err := config.Load(config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Get()
	st.Playback.TracearrURL, st.Playback.TracearrKey = "http://t", "k"
	st.Categories = []config.Category{{Name: "tv", Kind: "sonarr", ArrURL: "http://s"}}
	st.Attention = config.Attention{On: true, TVProfile: "Upgrade", PerDay: 10}
	if err := cfg.Set(st); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	eps := func(show string, n int) []tracearr.Play {
		var out []tracearr.Play
		for e := 1; e <= 3; e++ {
			e := e
			out = append(out, play("ann", now.Add(-time.Duration(n*10+e)*time.Minute), func(p *tracearr.Play) { p.ShowMediaID, p.Season, p.Episode = show, 1, e }))
		}
		return out
	}
	h := &fakeHist{eps: append(eps("show-a", 2), eps("show-b", 1)...)}
	hist := histIDs{h, map[string]int64{"show-a": 1, "show-b": 2}}
	son := &fakeArr{items: map[int64]arr.Item{1: withSeasons(arr.Item{ID: 11, Title: "A", ProfileID: 1}, 1, 2), 2: withSeasons(arr.Item{ID: 12, Title: "B", ProfileID: 1}, 1)}}
	s := &Service{Cfg: cfg, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now },
		History: func(config.Playback) History { return hist }, Arr: func(config.Category) Arr { return son }}
	s.Check(context.Background())
	if got := join(son.calls); got != "profile season1 season2" {
		t.Fatalf("first show only: %q", got)
	}
	if q := s.Snapshot().Queue; len(q) != 1 || q[0].TVDB != 2 {
		t.Fatalf("B should wait: %+v", q)
	}
	// A has no more seasons: the next pass marks it done and starts B.
	son.items[1] = withProfile(son.items[1], 9)
	now = now.Add(3 * time.Hour)
	s.Check(context.Background())
	if got := join(son.calls); got != "profile season1 season2 profile season1" {
		t.Fatalf("B after A: %q", got)
	}
}
