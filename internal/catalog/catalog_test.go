package catalog_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/db"
)

func newCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	h, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	c := catalog.New(h)
	c.Now = func() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }
	return c
}

func addShow(t *testing.T, c *catalog.Catalog, s catalog.Series) catalog.Series {
	t.Helper()
	got, err := c.AddSeries(s)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSeriesAndAliases(t *testing.T) {
	c := newCatalog(t)
	s := addShow(t, c, catalog.Series{Kind: "anime", Title: "Seitokai ni mo Ana wa Aru", TMDBID: 1001, Folder: "/media/Anime/Seitokai ni mo Ana wa Aru"})
	if s.ID == 0 || s.Numbering != catalog.BySeason || s.Added.IsZero() {
		t.Fatalf("stored series: %+v", s)
	}
	if err := c.AddSeriesAlias(s.ID, "Even the Student Council Has Its Holes", "anime-offline-database"); err != nil {
		t.Fatal(err)
	}
	// Same title in other case and punctuation is not a second alias.
	if err := c.AddSeriesAlias(s.ID, "seitokai ni mo ana wa aru!", "feed"); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.SeriesAliases(s.ID); len(a) != 2 {
		t.Fatalf("aliases %q", a)
	}
	for _, title := range []string{"Seitokai ni mo Ana wa Aru!", "EVEN THE STUDENT COUNCIL HAS ITS HOLES"} {
		got, _, err := c.FindSeries(title)
		if err != nil || got.ID != s.ID {
			t.Fatalf("%q -> %+v %v", title, got, err)
		}
	}
	if _, _, err := c.FindSeries("Blue Box"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unknown title: %v", err)
	}
	if got, err := c.SeriesByTMDB(1001); err != nil || got.ID != s.ID {
		t.Fatalf("by tmdb: %+v %v", got, err)
	}
	if _, err := c.AddSeries(catalog.Series{Kind: "anime", Title: "Dupe", TMDBID: 1001}); err == nil {
		t.Fatal("a TMDB id can only belong to one series")
	}
	if _, err := c.AddSeries(catalog.Series{Kind: "cartoon", Title: "X"}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
}

// Two shows sharing a title are never guessed between.
func TestFindSeriesRefusesAmbiguity(t *testing.T) {
	c := newCatalog(t)
	a := addShow(t, c, catalog.Series{Kind: "anime", Title: "Blue Box"})
	b := addShow(t, c, catalog.Series{Kind: "anime", Title: "Ao no Hako"})
	_ = c.AddSeriesAlias(b.ID, "Blue Box", "mistake")
	got, cands, err := c.FindSeries("Blue Box")
	if err != nil || got.ID != 0 || len(cands) != 2 {
		t.Fatalf("want no match and two candidates: %+v %v %v", got, cands, err)
	}
	// Titles that point at different shows are not merged either.
	if got, cands, _ := c.FindSeries("Ao no Hako", "Blue Box"); got.ID != 0 || len(cands) != 2 {
		t.Fatalf("got %+v %v", got, cands)
	}
	if got, _, _ := c.FindSeries("Ao no Hako"); got.ID != b.ID {
		t.Fatalf("a single match must still be found: %+v", got)
	}
	_ = a
}

func TestEpisodesBothNumberings(t *testing.T) {
	c := newCatalog(t)
	s := addShow(t, c, catalog.Series{Kind: "anime", Title: "One Piece", Numbering: catalog.ByAbsolute})
	eps := []catalog.Episode{
		{Season: 1, Number: 1, Absolute: 1, AirDate: "1999-10-20"},
		{Season: 23, Number: 1, Absolute: 1157, AirDate: "2026-11-01"},
		{Season: 23, Number: 2, Absolute: 1158, AirDate: "2026-11-08"},
		{Season: 23, Number: 3, Absolute: 1159, AirDate: "2026-11-22"},
		{Season: 0, Number: 1, Title: "Special"},
	}
	if err := c.PutEpisodes(s.ID, eps); err != nil {
		t.Fatal(err)
	}
	e, err := c.EpisodeByAbsolute(s.ID, 1158)
	if err != nil || e.Season != 23 || e.Number != 2 {
		t.Fatalf("absolute 1158 -> %+v %v", e, err)
	}
	if _, err := c.EpisodeByAbsolute(s.ID, 9999); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unknown absolute: %v", err)
	}
	// Updating keeps one row per season and number.
	if err := c.PutEpisodes(s.ID, []catalog.Episode{{Season: 23, Number: 2, Absolute: 1158, Title: "Named", AirDate: "2026-11-08"}}); err != nil {
		t.Fatal(err)
	}
	all, _ := c.Episodes(s.ID)
	if len(all) != 5 {
		t.Fatalf("want 5 episodes, got %d", len(all))
	}
	if e, _ := c.EpisodeBySeason(s.ID, 23, 2); e.Title != "Named" {
		t.Fatalf("update lost: %+v", e)
	}
	if err := c.SetNumbering(s.ID, catalog.BySeason); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Series(s.ID); got.Numbering != catalog.BySeason {
		t.Fatal("numbering not changed")
	}
	if err := c.SetNumbering(s.ID, "arcs"); err == nil {
		t.Fatal("an unknown numbering must be refused")
	}
}

func TestFilesMissingAndSeasons(t *testing.T) {
	c := newCatalog(t)
	s := addShow(t, c, catalog.Series{Kind: "anime", Title: "Seitokai ni mo Ana wa Aru"})
	var eps []catalog.Episode
	for i := 1; i <= 8; i++ {
		eps = append(eps, catalog.Episode{Season: 1, Number: i, AirDate: time.Date(2026, 10, i*7-6, 0, 0, 0, 0, time.UTC).Format("2006-01-02")})
	}
	eps[7].AirDate = "2026-11-30" // not aired yet
	if err := c.PutEpisodes(s.ID, eps); err != nil {
		t.Fatal(err)
	}
	stored, _ := c.Episodes(s.ID)
	for _, n := range []int{1, 2, 3, 5, 6} { // Judas skipped 4 and 7
		if _, err := c.AddFile(catalog.File{Path: "/media/Anime/S/Season 1/ep" + string(rune('0'+n)) + ".mkv", EpisodeIDs: []int64{stored[n-1].ID}, ReleaseGroup: "Judas", Resolution: "1080p"}); err != nil {
			t.Fatal(err)
		}
	}
	miss, err := c.Missing(s.ID)
	if err != nil || len(miss) != 2 || miss[0].Number != 4 || miss[1].Number != 7 {
		t.Fatalf("missing: %+v %v", miss, err)
	}
	sc, _ := c.Seasons(s.ID)
	if len(sc) != 1 || sc[0].Have != 5 || sc[0].Total != 8 {
		t.Fatalf("season counts: %+v", sc)
	}
	// Filling a hole from another group removes it from missing.
	if _, err := c.AddFile(catalog.File{Path: "/media/Anime/S/Season 1/ep4.mkv", EpisodeIDs: []int64{stored[3].ID}, ReleaseGroup: "SubsPlease"}); err != nil {
		t.Fatal(err)
	}
	if miss, _ := c.Missing(s.ID); len(miss) != 1 || miss[0].Number != 7 {
		t.Fatalf("after filling: %+v", miss)
	}
	fs, _ := c.EpisodeFiles(stored[3].ID)
	if len(fs) != 1 || fs[0].ReleaseGroup != "SubsPlease" {
		t.Fatalf("episode files: %+v", fs)
	}
	// A two-episode file covers both.
	if _, err := c.AddFile(catalog.File{Path: "/media/Anime/S/Season 1/ep7-8.mkv", EpisodeIDs: []int64{stored[6].ID, stored[7].ID}}); err != nil {
		t.Fatal(err)
	}
	if miss, _ := c.Missing(s.ID); len(miss) != 0 {
		t.Fatalf("nothing should be missing: %+v", miss)
	}
	if err := c.RemoveFile("/media/Anime/S/Season 1/ep4.mkv"); err != nil {
		t.Fatal(err)
	}
	if miss, _ := c.Missing(s.ID); len(miss) != 1 || miss[0].Number != 4 {
		t.Fatalf("after removing a file: %+v", miss)
	}
	if _, err := c.AddFile(catalog.File{Path: "/x.mkv"}); err == nil {
		t.Fatal("a file must hold episodes or a movie")
	}
}

func TestJobTargetsAndCascades(t *testing.T) {
	c := newCatalog(t)
	s := addShow(t, c, catalog.Series{Kind: "tv", Title: "Pantheon"})
	_ = c.PutEpisodes(s.ID, []catalog.Episode{{Season: 2, Number: 7}, {Season: 2, Number: 8}})
	eps, _ := c.Episodes(s.ID)
	m, err := c.AddMovie(catalog.Movie{Title: "Some Film", Year: 2026, TMDBID: 55})
	if err != nil || m.ID == 0 {
		t.Fatalf("movie: %+v %v", m, err)
	}
	for _, id := range []string{"job-tv", "job-movie"} {
		if _, err := c.DB.Exec(`INSERT INTO jobs (id, state, created, updated, data) VALUES (?, 'downloading', 'a', 'a', '{}')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SetJobTarget("job-tv", catalog.Target{EpisodeIDs: []int64{eps[0].ID, eps[1].ID}}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetJobTarget("job-movie", catalog.Target{MovieID: m.ID}); err != nil {
		t.Fatal(err)
	}
	if tg, _ := c.JobTarget("job-tv"); len(tg.EpisodeIDs) != 2 || tg.MovieID != 0 {
		t.Fatalf("tv target %+v", tg)
	}
	if tg, _ := c.JobTarget("job-movie"); tg.MovieID != m.ID || len(tg.EpisodeIDs) != 0 {
		t.Fatalf("movie target %+v", tg)
	}
	if err := c.SetJobTarget("missing-job", catalog.Target{MovieID: m.ID}); err == nil {
		t.Fatal("a target needs a stored job")
	}
	// A pruned job takes its target with it; a removed series takes its episodes.
	if _, err := c.DB.Exec(`DELETE FROM jobs WHERE id = 'job-tv'`); err != nil {
		t.Fatal(err)
	}
	if tg, _ := c.JobTarget("job-tv"); len(tg.EpisodeIDs) != 0 {
		t.Fatalf("target should go with the job: %+v", tg)
	}
	if _, err := c.DB.Exec(`DELETE FROM series WHERE id = ?`, s.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := c.Episodes(s.ID); len(left) != 0 {
		t.Fatal("episodes should go with their series")
	}
	if a, _ := c.SeriesAliases(s.ID); len(a) != 0 {
		t.Fatal("aliases should go with their series")
	}
}
