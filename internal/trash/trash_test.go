package trash

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z354prance/undermynd/internal/decision"
)

// zipOf packs testdata the way GitHub's repository zip is laid out.
func zipOf(t *testing.T) []byte {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		w, _ := z.Create("Guides-master/" + strings.TrimPrefix(filepath.ToSlash(p), "testdata/"))
		b, _ := os.ReadFile(p)
		w.Write(b)
		return nil
	})
	z.Close()
	return buf.Bytes()
}

const hdBlurayWeb = "d1d67249d3890e49bc12e275d989a7e9"

func TestPlanAndMerge(t *testing.T) {
	g, err := Read(zipOf(t))
	if err != nil {
		t.Fatal(err)
	}
	r := g.Apps["radarr"]
	if len(r.Profiles) != 1 || len(r.Formats) != 9 || len(r.Sizes) != 1 || r.Sizes[0].Type != "movie" {
		t.Fatalf("read: %d profiles, %d formats, %d sizes", len(r.Profiles), len(r.Formats), len(r.Sizes))
	}
	im, err := g.Plan("radarr", []string{hdBlurayWeb}, "movie")
	if err != nil {
		t.Fatal(err)
	}
	p := im.Profiles[0]
	if p.Name != "HD Bluray + WEB" || p.Cutoff != "Bluray-1080p" || p.Items[len(p.Items)-1].Name != "Bluray-1080p" || p.Scores["HD Bluray Tier 01"] == 0 || p.GuideID != hdBlurayWeb {
		t.Fatalf("profile: %+v", p)
	}
	if err := p.Check(); err != nil {
		t.Fatal(err)
	}
	if len(im.Formats) != 9 || len(im.Sizes) == 0 {
		t.Fatalf("formats %d sizes %d skipped %v", len(im.Formats), len(im.Sizes), im.Skipped)
	}
	// The formats work: a tier-01 WEB group scores.
	e := &decision.Engine{Profile: p, Formats: im.Formats}
	d := e.Evaluate(decision.Want{Movie: true, Titles: []string{"Dune Part Two"}, Year: 2024}, decision.Candidate{Title: "Dune.Part.Two.2024.1080p.AMZN.WEB-DL.DDP5.1.H.264-FLUX", Protocol: "usenet"})
	if d.Score <= 0 || !d.Accepted() {
		t.Fatalf("FLUX WEB-DL: score %d formats %v rejected %v", d.Score, d.Formats, d.Rejected)
	}

	// First sync: everything is new.
	ch := Merge("radarr", &im, nil, nil, nil)
	if len(ch) != 11 || ch[0].Action != "add" {
		t.Fatalf("first: %+v", ch)
	}
	// After a sync, a score changed here is kept and the rest follow the guides.
	stored := im.Profiles[0]
	stored.Source = Source("radarr")
	stored.Scores = map[string]int{}
	for k, v := range stored.GuideScores {
		stored.Scores[k] = v
	}
	stored.Scores["WEB Tier 01"] = 5
	var fs []decision.Format
	for _, f := range im.Formats {
		f.Source = Source("radarr")
		fs = append(fs, f)
	}
	im2, _ := g.Plan("radarr", []string{hdBlurayWeb}, "")
	ch = Merge("radarr", &im2, []decision.Profile{stored}, fs, nil)
	if len(ch) != 1 || ch[0].Action != "keep" || !strings.Contains(ch[0].Detail, "kept your scores: WEB Tier 01 5") || im2.Profiles[0].Scores["WEB Tier 01"] != 5 {
		t.Fatalf("second: %+v %v", ch, im2.Profiles[0].Scores)
	}
}
