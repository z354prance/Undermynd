package trash

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/decision"
)

func TestApplyKeepsLocalScores(t *testing.T) {
	z := zipOf(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(z) }))
	defer srv.Close()
	h, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	st := &decision.Store{DB: h}
	// A format of the same name imported from Radarr stays as it is.
	if _, err := st.SaveFormat(decision.Format{Name: "Repack/Proper", Source: "movies", Specs: []decision.Spec{{Name: "p", Kind: "title", Value: "proper"}}}); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{Store: st, Log: log.New(io.Discard, "", 0), Client: &Client{HTTP: srv.Client(), URL: func() string { return srv.URL }}}
	ctx := context.Background()
	if _, err := s.Apply(ctx, "radarr", []string{hdBlurayWeb}, "movie"); err != nil {
		t.Fatal(err)
	}
	p, err := st.ProfileByName("HD Bluray + WEB")
	if err != nil || p.Source != "trash-radarr" || p.Scores["Repack/Proper (trash-radarr)"] == 0 {
		t.Fatalf("applied: %+v %v", p, err)
	}
	p.Scores["WEB Tier 01"] = 7
	if _, err := st.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	ch, err := s.Apply(ctx, "radarr", []string{hdBlurayWeb}, "movie")
	if err != nil {
		t.Fatal(err)
	}
	p, _ = st.ProfileByName("HD Bluray + WEB")
	if p.Scores["WEB Tier 01"] != 7 || len(ch) != 1 || ch[0].Action != "keep" {
		t.Fatalf("second apply: %v %+v", p.Scores, ch)
	}
	ps, _ := st.Profiles()
	fs, _ := st.Formats()
	if len(ps) != 5 || len(fs) != 10 {
		t.Fatalf("%d profiles, %d formats", len(ps), len(fs))
	}
	if sz, _ := st.Sizes(); len(sz) == 0 {
		t.Fatal("no sizes")
	}
}
