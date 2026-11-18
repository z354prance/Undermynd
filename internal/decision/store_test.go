package decision_test

import (
	"path/filepath"
	"testing"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/decision"
)

func TestStore(t *testing.T) {
	h, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	s := &decision.Store{DB: h}
	ps, err := s.Profiles()
	if err != nil || len(ps) != 4 {
		t.Fatalf("defaults: %d %v", len(ps), err)
	}
	if _, err := s.SaveFormat(decision.Format{Name: "Bad", Specs: []decision.Spec{{Name: "x", Kind: "title", Value: "(unclosed"}}}); err == nil {
		t.Fatal("a pattern that does not compile was saved")
	}
	fid, err := s.SaveFormat(decision.Format{Name: "x265", Specs: []decision.Spec{{Name: "x", Kind: "title", Value: `x265`}}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.ProfileByName("hd-1080p")
	p.Scores = map[string]int{"x265": -100}
	if _, err := s.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveFormat(decision.Format{ID: fid, Name: "x265 (HD)", Specs: []decision.Spec{{Name: "x", Kind: "title", Value: `x265`}}}); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.ProfileByName("HD-1080p"); p.Scores["x265 (HD)"] != -100 || len(p.Scores) != 1 {
		t.Fatalf("renamed score: %v", p.Scores)
	}
	bad := p
	bad.ID, bad.Name = 0, "Any"
	if _, err := s.SaveProfile(bad); err == nil {
		t.Fatal("two profiles named Any")
	}
	bad.Name, bad.Cutoff = "Other", "SDTV"
	if _, err := s.SaveProfile(bad); err == nil {
		t.Fatal("a cutoff that is not allowed")
	}

	im := decision.Imported{
		Profiles: []decision.Profile{{Name: "HD-1080p", Items: []decision.Item{{Name: "WEBDL-1080p", Qualities: []string{"WEBDL-1080p"}, Allowed: true}}, Scores: map[string]int{"x265 (HD)": -10000}}},
		Formats:  []decision.Format{{Name: "x265 (HD)", Specs: []decision.Spec{{Name: "x", Kind: "title", Value: `[xh]265`}}}},
		Sizes:    map[string]decision.SizeLimit{"WEBDL-1080p": {Min: 4, Max: 100}},
	}
	for round := 0; round < 2; round++ { // importing twice replaces, it does not add
		if np, nf, err := s.Save("tv", im); err != nil || np != 1 || nf != 1 {
			t.Fatalf("import: %d %d %v", np, nf, err)
		}
	}
	ps, _ = s.Profiles()
	fs, _ := s.Formats()
	if len(ps) != 5 || len(fs) != 2 {
		t.Fatalf("after import: %d profiles, %d formats", len(ps), len(fs))
	}
	p, err = s.ProfileByName("HD-1080p (tv)")
	if err != nil || p.Scores["x265 (HD) (tv)"] != -10000 || p.Source != "tv" {
		t.Fatalf("imported profile: %+v %v", p, err)
	}
	if sz, _ := s.Sizes(); sz["WEBDL-1080p"].Max != 100 {
		t.Fatalf("sizes %v", sz)
	}
	if err := s.DeleteFormat(fid); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.ProfileByName("HD-1080p"); len(p.Scores) != 0 {
		t.Fatalf("scores of a deleted format: %v", p.Scores)
	}
}
