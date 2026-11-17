package shows_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
	"github.com/z354prance/undermynd/internal/shows"
)

type fakeEngine struct {
	mu   sync.Mutex
	reqs []pipeline.SubmitRequest
}

func (f *fakeEngine) Submit(r pipeline.SubmitRequest) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	return job.Job{}, nil
}

func item(title, hash string, seeders int) string {
	return fmt.Sprintf(`<item><title>%s</title><link></link><nyaa:infoHash>%s</nyaa:infoHash><nyaa:seeders>%d</nyaa:seeders><nyaa:size>1.0 GiB</nyaa:size><pubDate>Sun, 16 Nov 2026 15:31:02 -0000</pubDate></item>`, title, hash, seeders)
}

func TestFollowAndGrab(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	dir := t.TempDir()
	lib := filepath.Join(dir, "anime")
	for _, f := range []string{"Seitokai ni mo Ana wa Aru/Season 1/Seitokai ni mo Ana wa Aru! - 1 (1080p).mkv", "Seitokai ni mo Ana wa Aru/Season 1/Seitokai ni mo Ana wa Aru! - 2 (1080p).mkv",
		"Seitokai ni mo Ana wa Aru/Season 1/Seitokai ni mo Ana wa Aru! - 3 (1080p).mkv", "Seitokai ni mo Ana wa Aru/Season 1/Seitokai ni mo Ana wa Aru! - 5 (1080p).mkv",
		"Seitokai ni mo Ana wa Aru/Season 1/Seitokai ni mo Ana wa Aru! - 6 (1080p).mkv", "Blue Box/Season 1/Blue Box - 1 (1080p).mkv"} {
		p := filepath.Join(lib, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	env := config.Env{ConfigDir: filepath.Join(dir, "c"), DataDir: filepath.Join(dir, "d"), TickInterval: time.Second}
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Get()
	s.Categories = []config.Category{{Name: "anime", Kind: "rss", LibraryDir: lib}}
	s.Rules = []config.Rule{{Name: "student council", MustContain: `\[Judas\].*Seitokai`, Category: "anime", Enabled: true}}
	if err := cfg.Set(s); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	releases := []string{
		item("[Judas] Seitokai ni mo Ana wa Aru! - S01E06 [1080p].mkv", "6666", 30),
		item("[SubsPlease] Seitokai ni mo Ana wa Aru! - 07 (1080p) [AAAA].mkv", "7777", 300),
		item("[Judas] Seitokai ni mo Ana wa Aru! - S01E08 [1080p].mkv", "8888", 40),
		item("[SubsPlease] Seitokai ni mo Ana wa Aru! - 04 (1080p) [BBBB].mkv", "4444", 90),
		item("[SubsPlease] Some Other Show - 07 (1080p) [CCCC].mkv", "9999", 900),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, `<?xml version="1.0"?><rss xmlns:nyaa="https://nyaa.si/xmlns/nyaa"><channel>`+strings.Join(releases, "")+`</channel></rss>`)
	}))
	defer srv.Close()
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	svc := &shows.Service{Store: &shows.Store{DB: h, Now: func() time.Time { return now }}, Nyaa: shows.Nyaa{BaseURL: srv.URL, HTTP: srv.Client()},
		HTTP: srv.Client(), Cfg: cfg, Engine: eng, Log: log.New(io.Discard, "", 0), Now: func() time.Time { return now }, Grace: 24 * time.Hour, Every: 20 * time.Minute}
	ctx := context.Background()

	list, err := svc.List(true)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	sc := list[1]
	if sc.Name != "Seitokai ni mo Ana wa Aru" || sc.Followed || sc.Summary.Files != 5 || sc.Summary.Gaps != 1 || sc.Summary.Latest != 6 || sc.HandMade != "student council" {
		t.Fatalf("student council: %+v", sc)
	}
	if g := svc.DefaultGroups(sc); len(g) != 1 || g[0] != "Judas" {
		t.Fatalf("groups from the hand-made rule: %v", g)
	}
	if svc.Claims("[Judas] Seitokai ni mo Ana wa Aru! - S01E07 [1080p]") != "" {
		t.Fatal("nothing is claimed before following")
	}

	// Follow with Judas first: 7 comes from SubsPlease because Judas skipped it (released 8); 8 from Judas.
	sh, err := svc.Save(sc.Folder, shows.Settings{Followed: true, Groups: []string{"Judas", "SubsPlease"}, Quality: "1080p", Titles: []string{"Even the Student Council Has Its Holes!"}}, false)
	if err != nil || sh.FromEpisode != 6 {
		t.Fatalf("save: %+v %v", sh, err)
	}
	started, err := svc.Check(ctx, sc.Folder)
	if err != nil || len(started) != 2 || !strings.Contains(started[0], "- 07") || !strings.Contains(started[1], "S01E08") {
		t.Fatalf("check: %v %v", started, err)
	}
	if len(eng.reqs) != 2 || eng.reqs[0].Category != "anime" || !strings.HasPrefix(eng.reqs[0].Magnet, "magnet:?xt=urn:btih:7777") {
		t.Fatalf("submitted: %+v", eng.reqs)
	}
	// Checking again grabs nothing twice.
	if started, _ := svc.Check(ctx, sc.Folder); len(started) != 0 {
		t.Fatalf("again: %v", started)
	}
	// The hand-made rule leaves Judas releases of the show to it now.
	if svc.Claims("[Judas] Seitokai ni mo Ana wa Aru! - S01E09 [1080p]") != "Seitokai ni mo Ana wa Aru" {
		t.Fatal("followed show should claim its group's releases")
	}
	if svc.Claims("[Erai-raws] Seitokai ni mo Ana wa Aru! - 09 [1080p]") != "" {
		t.Fatal("a group outside the order is not claimed")
	}
	// The importer learns the other title.
	if ft := svc.FolderTitles(lib); len(ft["Seitokai ni mo Ana wa Aru"]) != 1 {
		t.Fatalf("folder titles: %v", ft)
	}
	// Gap search grabs 4.
	gaps, err := svc.SearchGaps(ctx, sc.Folder)
	if err != nil || len(gaps) != 1 || !strings.Contains(gaps[0], "- 04") {
		t.Fatalf("gaps: %v %v", gaps, err)
	}
	// Interactive search warns about what would not be picked.
	res, err := svc.Search(ctx, sc.Folder, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	warn := map[string]string{}
	for _, r := range res {
		warn[r.Title] = r.Warning
	}
	if warn["[Judas] Seitokai ni mo Ana wa Aru! - S01E06 [1080p].mkv"] == "" || warn["[SubsPlease] Some Other Show - 07 (1080p) [CCCC].mkv"] == "" {
		t.Fatalf("warnings: %v", warn)
	}
	// Add New makes a folder and follows it from the start.
	sh, got, err := svc.Add(ctx, lib, "Some Other Show", shows.Settings{Groups: []string{"SubsPlease"}, Quality: "1080p"}, true)
	if err != nil || !sh.Followed || len(got) != 1 {
		t.Fatalf("add: %+v %v %v", sh, got, err)
	}
	if fi, err := os.Stat(filepath.Join(lib, "Some Other Show")); err != nil || !fi.IsDir() {
		t.Fatal("folder not made")
	}
}
