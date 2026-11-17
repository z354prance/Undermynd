package metadata_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/metadata"
)

// fakeTMDB serves a few shows shaped like the real ones the B3 spike looked at.
type fakeTMDB struct {
	mu       sync.Mutex
	srv      *httptest.Server
	key      string
	requests map[string]int
	seasons  map[string][]map[string]any // "tv/season" -> episodes
}

func ep(season, number int, air string) map[string]any {
	return map[string]any{"season_number": season, "episode_number": number, "name": "", "air_date": air}
}

func newFake(t *testing.T) *fakeTMDB {
	f := &fakeTMDB{key: "goodkey", requests: map[string]int{}, seasons: map[string][]map[string]any{
		// One Piece style: TMDB numbers absolutely inside seasons.
		"1/1": {ep(1, 1, "1999-10-20"), ep(1, 2, "1999-10-27")},
		"1/2": {ep(2, 3, "2000-01-01"), ep(2, 4, "2000-01-08")},
		// Blue Box style: both cours in one TMDB season.
		"2/1": {ep(1, 1, "2024-10-03"), ep(1, 2, "2024-10-10"), ep(1, 3, "2026-10-01"), ep(1, 4, "2026-10-08")},
		// Hero Academia style: seasons restart at 1, with a specials season.
		"3/0": {ep(0, 1, "2017-01-01")},
		"3/1": {ep(1, 1, "2016-04-03"), ep(1, 2, "2016-04-10")},
		"3/2": {ep(2, 1, "2017-04-01"), ep(2, 2, "2017-04-08")},
	}}
	shows := map[string]map[string]any{
		"1": {"id": 1, "name": "One Piece", "original_name": "ワンピース", "first_air_date": "1999-10-20",
			"seasons": []map[string]any{{"season_number": 1}, {"season_number": 2}}, "external_ids": map[string]any{"tvdb_id": 81797, "imdb_id": "tt0388629"},
			"alternative_titles": map[string]any{"results": []map[string]any{{"title": "Wan Pisu"}}}, "episode_groups": map[string]any{"results": []any{}}},
		"2": {"id": 2, "name": "Blue Box", "original_name": "アオのハコ", "first_air_date": "2024-10-03",
			"seasons": []map[string]any{{"season_number": 1}}, "external_ids": map[string]any{"tvdb_id": 429934},
			"alternative_titles": map[string]any{"results": []map[string]any{{"title": "Ao no Hako"}}},
			"episode_groups":     map[string]any{"results": []map[string]any{{"id": "g-seasons", "name": "Seasons", "type": 6, "episode_count": 4}}}},
		"3": {"id": 3, "name": "My Hero Academia", "original_name": "僕のヒーローアカデミア", "first_air_date": "2016-04-03",
			"seasons": []map[string]any{{"season_number": 0}, {"season_number": 1}, {"season_number": 2}}, "external_ids": map[string]any{"tvdb_id": 305074},
			"alternative_titles": map[string]any{"results": []map[string]any{{"title": "Boku no Hero Academia"}}}, "episode_groups": map[string]any{"results": []any{}}},
		"4": {"id": 4, "name": "Even the Student Council Has Its Holes!", "original_name": "生徒会にも穴はある！", "first_air_date": "2026-10-04",
			"seasons": []map[string]any{}, "external_ids": map[string]any{"tvdb_id": 463051},
			"alternative_titles": map[string]any{"results": []any{}}, "episode_groups": map[string]any{"results": []any{}}},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests[r.URL.Path]++
		f.mu.Unlock()
		if r.URL.Query().Get("api_key") != f.key {
			w.WriteHeader(401)
			io.WriteString(w, `{"status_code":7,"status_message":"Invalid API key"}`)
			return
		}
		p := strings.Trim(r.URL.Path, "/")
		parts := strings.Split(p, "/")
		var out any
		switch {
		case p == "configuration":
			out = map[string]any{"images": map[string]any{}}
		case p == "search/tv":
			q := strings.ToLower(r.URL.Query().Get("query"))
			var res []map[string]any
			for _, s := range shows {
				if strings.Contains(strings.ToLower(s["name"].(string)), q) {
					res = append(res, map[string]any{"id": s["id"], "name": s["name"], "original_name": s["original_name"], "first_air_date": s["first_air_date"], "genre_ids": []int{16}})
				}
			}
			out = map[string]any{"results": res}
		case len(parts) == 2 && parts[0] == "find":
			var tv []map[string]any
			for _, s := range shows {
				if ext := s["external_ids"].(map[string]any); ext["tvdb_id"] != nil && strings.TrimSpace(jsonNum(ext["tvdb_id"])) == parts[1] {
					tv = append(tv, map[string]any{"id": s["id"]})
				}
			}
			out = map[string]any{"tv_results": tv}
		case len(parts) == 3 && parts[0] == "tv" && parts[1] == "episode_group":
			out = map[string]any{"id": "g-seasons", "name": "Seasons", "type": 6, "groups": []map[string]any{
				{"name": "Season 2", "order": 2, "episodes": []map[string]any{
					{"season_number": 1, "episode_number": 4, "order": 1, "air_date": "2026-10-08"},
					{"season_number": 1, "episode_number": 3, "order": 0, "air_date": "2026-10-01"}}},
				{"name": "Season 1", "order": 1, "episodes": []map[string]any{
					{"season_number": 1, "episode_number": 1, "order": 0, "air_date": "2024-10-03"},
					{"season_number": 1, "episode_number": 2, "order": 1, "air_date": "2024-10-10"}}},
				{"name": "Specials", "order": 0, "episodes": []map[string]any{}},
			}}
		case len(parts) == 2 && parts[0] == "tv":
			s, ok := shows[parts[1]]
			if !ok {
				w.WriteHeader(404)
				return
			}
			out = s
		case len(parts) == 4 && parts[0] == "tv" && parts[2] == "season":
			f.mu.Lock()
			out = map[string]any{"episodes": f.seasons[parts[1]+"/"+parts[3]]}
			f.mu.Unlock()
		default:
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (f *fakeTMDB) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

type rig struct {
	svc  *metadata.Service
	fake *fakeTMDB
	cat  *catalog.Catalog
	key  string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	h, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	r := &rig{fake: newFake(t), cat: catalog.New(h), key: "goodkey"}
	tm := metadata.NewTMDB(h, func() string { return " " + r.key + "\n" })
	tm.BaseURL, tm.Gap = r.fake.srv.URL, 0
	lists := metadata.NewLists(h, func() string { return "" }, func() string { return "" })
	r.svc = &metadata.Service{TMDB: tm, Lists: lists, Catalog: r.cat, Log: log.New(io.Discard, "", 0), Now: time.Now}
	return r
}

const titlesJSON = `{"$schema":"x","license":{"name":"ODbL"},"lastUpdate":"2026-07-04","data":[
 {"sources":["https://anidb.net/anime/19308","https://anilist.co/anime/191656"],"title":"Seitokai ni mo Ana wa Aru!","synonyms":["Even the Student Council Has Its Holes","Seitokai ni mo Ana wa Aru!"]},
 {"sources":["https://anidb.net/anime/69"],"title":"One Piece","synonyms":["OP","Wan Pisu"]},
 {"sources":["https://anidb.net/anime/411"],"title":"One Piece Movie 01","synonyms":[]},
 {"sources":["https://myanimelist.net/anime/1"],"title":"No Usable Id","synonyms":["Ignored"]}
]}`

const idsJSON = `{"19308":{"tvdb_id":463051,"tvdb_season":1,"tvdb_epoffset":0,"anilist_id":191656},
 "69":{"tvdb_id":81797,"tvdb_season":-1,"tvdb_epoffset":0},
 "411":{"tvdb_id":"81797","tvdb_season":0,"tvdb_epoffset":1}}`

func (r *rig) importLists(t *testing.T) {
	t.Helper()
	n, date, err := r.svc.Lists.ImportTitles(strings.NewReader(titlesJSON))
	if err != nil || n != 3 || date != "2026-07-04" {
		t.Fatalf("titles: %d %q %v", n, date, err)
	}
	if n, err := r.svc.Lists.ImportIDs(strings.NewReader(idsJSON)); err != nil || n != 3 {
		t.Fatalf("ids: %d %v", n, err)
	}
}

func TestAddSeriesAbsoluteTMDBNumbering(t *testing.T) {
	r := newRig(t)
	r.importLists(t)
	s, err := r.svc.AddSeries(context.Background(), 1, "anime", "/media/Anime/One Piece", catalog.ByAbsolute)
	if err != nil {
		t.Fatal(err)
	}
	if s.TVDBID != 81797 || s.IMDBID != "tt0388629" || s.Year != 1999 || s.Numbering != catalog.ByAbsolute {
		t.Fatalf("series: %+v", s)
	}
	e, err := r.cat.EpisodeByAbsolute(s.ID, 4)
	if err != nil || e.Season != 2 || e.Number != 4 {
		t.Fatalf("TMDB's own absolute numbers must be kept: %+v %v", e, err)
	}
	al, _ := r.cat.SeriesAliases(s.ID)
	joined := strings.Join(al, "|")
	for _, want := range []string{"One Piece", "ワンピース", "Wan Pisu", "OP"} {
		if !strings.Contains(joined, want) {
			t.Errorf("alias %q missing from %q", want, joined)
		}
	}
	if strings.Contains(joined, "Movie") {
		t.Errorf("a movie's title must not become a series alias: %q", joined)
	}
	if _, err := r.svc.AddSeries(context.Background(), 1, "anime", "", ""); err == nil {
		t.Fatal("adding the same show twice must fail")
	}
}

func TestRunningAbsoluteAndSpecials(t *testing.T) {
	r := newRig(t)
	s, err := r.svc.AddSeries(context.Background(), 3, "anime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.cat.EpisodeByAbsolute(s.ID, 3)
	if err != nil || e.Season != 2 || e.Number != 1 {
		t.Fatalf("absolute 3 should be S2E1: %+v %v", e, err)
	}
	sp, err := r.cat.EpisodeBySeason(s.ID, 0, 1)
	if err != nil || sp.Absolute != 0 {
		t.Fatalf("specials get no absolute number: %+v %v", sp, err)
	}
	if f, _, err := r.cat.FindSeries("Boku no Hero Academia"); err != nil || f.ID != s.ID {
		t.Fatalf("TMDB alternative titles must be aliases: %v", err)
	}
}

// Blue Box: TMDB has one season, the releases and the library use two. Choosing
// the "Seasons" episode group renumbers before any file is recorded.
func TestEpisodeGroupOrder(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s, err := r.svc.AddSeries(ctx, 2, "anime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.cat.EpisodeBySeason(s.ID, 2, 2); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("in TMDB's own order there is no season 2")
	}
	orders, err := r.svc.EpisodeOrders(ctx, s.ID)
	if err != nil || len(orders) != 2 || orders[1].ID != "g-seasons" {
		t.Fatalf("orders %+v %v", orders, err)
	}
	if err := r.svc.SetEpisodeOrder(ctx, s.ID, "g-seasons"); err != nil {
		t.Fatal(err)
	}
	e, err := r.cat.EpisodeBySeason(s.ID, 2, 2)
	if err != nil || e.Absolute != 4 || e.AirDate != "2026-10-08" {
		t.Fatalf("S2E2 in group order: %+v %v", e, err)
	}
	if got, _ := r.cat.Series(s.ID); got.EpisodeOrder != "g-seasons" {
		t.Fatalf("order not saved: %q", got.EpisodeOrder)
	}
	if _, err := r.cat.AddFile(catalog.File{Path: "/x/Blue Box S02E02.mkv", EpisodeIDs: []int64{e.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetEpisodeOrder(ctx, s.ID, ""); !errors.Is(err, catalog.ErrHasFiles) {
		t.Fatalf("renumbering after files exist must be refused, got %v", err)
	}
	// A refresh keeps the chosen order.
	if err := r.svc.Refresh(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cat.EpisodeBySeason(s.ID, 2, 2); err != nil {
		t.Fatalf("refresh must keep the group order: %v", err)
	}
}

func TestRefreshAddsNewEpisodesAndCaches(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s, err := r.svc.AddSeries(ctx, 3, "anime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	before := r.fake.count("/tv/3")
	if _, err := r.svc.TMDB.TV(ctx, 3, false); err != nil {
		t.Fatal(err)
	}
	if r.fake.count("/tv/3") != before {
		t.Fatal("a recent answer must come from the cache")
	}
	r.fake.mu.Lock()
	r.fake.seasons["3/2"] = append(r.fake.seasons["3/2"], ep(2, 3, "2017-04-15"))
	r.fake.mu.Unlock()
	if err := r.svc.Refresh(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if r.fake.count("/tv/3") == before {
		t.Fatal("a refresh must ask TMDB again")
	}
	e, err := r.cat.EpisodeBySeason(s.ID, 2, 3)
	if err != nil || e.Absolute != 5 {
		t.Fatalf("new episode after refresh: %+v %v", e, err)
	}
}

func TestIdentifyThroughListsAndSearch(t *testing.T) {
	r := newRig(t)
	r.importLists(t)
	c, err := r.svc.Identify(context.Background(), "Even the Student Council Has Its Holes")
	if err != nil || len(c) == 0 || c[0].TMDBID != 4 || c[0].Via != "anime lists" {
		t.Fatalf("lists route: %+v %v", c, err)
	}
	c, err = r.svc.Identify(context.Background(), "Blue Box")
	if err != nil || len(c) != 1 || c[0].TMDBID != 2 || c[0].Via != "TMDB search" {
		t.Fatalf("search route: %+v %v", c, err)
	}
}

func TestKeyProblems(t *testing.T) {
	r := newRig(t)
	r.key = "wrong"
	err := r.svc.TMDB.Check(context.Background())
	var se *metadata.StatusError
	if !errors.As(err, &se) || se.Code != 401 || !strings.Contains(err.Error(), "refused the API key") {
		t.Fatalf("bad key: %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatal("the key must never appear in an error")
	}
	r.key = ""
	if err := r.svc.TMDB.Check(context.Background()); !errors.Is(err, metadata.ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
	r.key = "goodkey"
	if err := r.svc.TMDB.Check(context.Background()); err != nil {
		t.Fatalf("good key (with spaces around it): %v", err)
	}
}

func TestListsRefreshKeepsOldCopyOnFailure(t *testing.T) {
	r := newRig(t)
	ok := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !ok {
			w.WriteHeader(500)
			return
		}
		if strings.Contains(req.URL.Path, "titles") {
			io.WriteString(w, titlesJSON)
		} else {
			io.WriteString(w, idsJSON)
		}
	}))
	defer srv.Close()
	r.svc.Lists.TitlesURL = func() string { return srv.URL + "/titles" }
	r.svc.Lists.IDsURL = func() string { return srv.URL + "/ids" }
	st := r.svc.Lists.Refresh(context.Background())
	if st.Titles != 3 || st.IDs != 3 || st.TitlesError != "" || st.TitlesDate != "2026-07-04" {
		t.Fatalf("first refresh: %+v", st)
	}
	ok = false
	st = r.svc.Lists.Refresh(context.Background())
	if st.TitlesError == "" || st.Titles != 3 {
		t.Fatalf("a failed download must keep the last counts and record the error: %+v", st)
	}
	if ids, _ := r.svc.Lists.TVDBForTitle(context.Background(), "one piece"); len(ids) != 1 || ids[0] != 81797 {
		t.Fatalf("old lists must still answer: %v", ids)
	}
	if got := r.svc.Lists.Status(); got.TitlesError == "" {
		t.Fatal("status must be saved")
	}
}
