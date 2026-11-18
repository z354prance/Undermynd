package decision

import (
	"strings"
	"testing"
	"time"
)

func hd() Profile {
	p := Defaults()[1] // HD-1080p, upgrades on, cutoff Bluray-1080p
	p.Scores = map[string]int{"x265 (HD)": -10000, "Repack/Proper": 5, "HQ group": 100}
	p.MinFormatScore = 0
	return p
}

var formats = []Format{
	{Name: "x265 (HD)", Specs: []Spec{
		{Name: "x265", Kind: "title", Value: `[xh][ ._-]?265|\bHEVC(\b|\d)`, Required: true},
		{Name: "Not 2160p", Kind: "resolution", Value: "2160", Negate: true, Required: true},
	}},
	{Name: "Repack/Proper", Specs: []Spec{{Name: "Proper", Kind: "title", Value: `\b(proper)\b`}, {Name: "Repack", Kind: "title", Value: `\b(repack)\b`}}},
	// A .NET lookbehind, as TRaSH formats use.
	{Name: "HQ group", Specs: []Spec{{Name: "group after year", Kind: "title", Value: `(?<=\b[12]\d{3}\b).*-(FLUX|NTb)\b`}}},
}

func TestEvaluate(t *testing.T) {
	e := &Engine{Profile: hd(), Formats: formats, Sizes: map[string]SizeLimit{"WEBDL-1080p": {Min: 2, Max: 100}}}
	w := Want{Movie: true, Titles: []string{"Dune Part Two"}, Year: 2024, Runtime: 166}
	gb := int64(1 << 30)
	cases := []struct {
		title  string
		size   int64
		reject string // part of a rejection, "" for accepted
		score  int
	}{
		{"Dune.Part.Two.2024.1080p.AMZN.WEB-DL.DDP5.1.H.264-FLUX", 8 * gb, "", 100},
		{"Dune.Part.Two.2024.1080p.WEB-DL.x265-GRP", 4 * gb, "below the profile's minimum", -10000},
		{"Dune.Part.Two.2024.720p.WEB-DL.H.264-GRP", 4 * gb, "not wanted by the profile", 0},
		{"Dune.Part.Two.2024.1080p.WEB-DL.H.264-GRP", 100 << 20, "below the WEBDL-1080p minimum", 0},
		{"Dune.Part.Two.2024.1080p.WEB-DL.H.264-GRP", 20 * gb, "above the WEBDL-1080p maximum", 0},
		{"Dune.2021.1080p.WEB-DL.H.264-GRP", 4 * gb, "year 2021", 0},
		{"Arrival.2016.1080p.WEB-DL.H.264-GRP", 4 * gb, "is not Dune Part Two", 0},
		{"Dune.Part.Two.2024.PROPER.1080p.WEB-DL.H.264-GRP", 4 * gb, "", 5},
	}
	for _, c := range cases {
		d := e.Evaluate(w, Candidate{Title: c.title, Size: c.size, Protocol: "usenet"})
		got := strings.Join(d.Rejected, "; ")
		if c.reject == "" && got != "" || c.reject != "" && !strings.Contains(got, c.reject) {
			t.Errorf("%s: rejected %q, want %q", c.title, got, c.reject)
		}
		if d.Score != c.score {
			t.Errorf("%s: score %d (%v), want %d", c.title, d.Score, d.Formats, c.score)
		}
	}
}

func TestUpgradesAndCutoff(t *testing.T) {
	e := &Engine{Profile: hd(), Formats: formats}
	w := Want{Movie: true, Titles: []string{"Heat"}, Year: 1995, HaveQuality: "WEBDL-1080p", HaveVersion: 1}
	if d := e.Evaluate(w, Candidate{Title: "Heat.1995.1080p.BluRay.x264-GRP", Protocol: "usenet"}); !d.Accepted() || !d.Upgrade {
		t.Fatalf("Bluray over WEBDL: %v", d.Rejected)
	}
	if d := e.Evaluate(w, Candidate{Title: "Heat.1995.1080p.HDTV.x264-GRP", Protocol: "usenet"}); d.Accepted() {
		t.Fatal("HDTV is lower than WEBDL")
	}
	w.HaveQuality = "Bluray-1080p"
	if d := e.Evaluate(w, Candidate{Title: "Heat.1995.1080p.BluRay.x264-OTHER", Protocol: "usenet"}); d.Accepted() || !strings.Contains(d.Rejected[0], "meets the cutoff") {
		t.Fatalf("cutoff met: %v", d.Rejected)
	}
}

func TestTVNumbering(t *testing.T) {
	e := &Engine{Profile: hd()}
	w := Want{Titles: []string{"Severance"}, Season: 2, Episodes: []int{3}}
	for title, ok := range map[string]bool{
		"Severance.S02E03.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb": true,
		"Severance.S02E04.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb": false,
		"Severance.S01E03.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb": false,
		"Severance.S02.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb":    false,
		"Severance.2022.1080p.WEB-DL.H.264-GRP":               false,
	} {
		d := e.Evaluate(w, Candidate{Title: title, Protocol: "torrent", Seeders: 5})
		if d.Accepted() != ok {
			t.Errorf("%s: %v", title, d.Rejected)
		}
	}
	w.Episodes = nil // the season is wanted
	if d := e.Evaluate(w, Candidate{Title: "Severance.S02E03.1080p.WEB-DL.H.264-NTb", Protocol: "usenet"}); d.Accepted() {
		t.Fatal("a single episode for a season search")
	}
	if d := e.Evaluate(w, Candidate{Title: "Severance.S02.1080p.WEB-DL.H.264-NTb", Protocol: "torrent"}); d.Accepted() {
		t.Fatal("no seeders")
	}
}

func TestDecideOrder(t *testing.T) {
	e := &Engine{Profile: hd(), Formats: formats}
	now := time.Now()
	w := Want{Titles: []string{"Show"}, Season: 1, Episodes: []int{5}}
	ds := e.Decide(w, []Candidate{
		{Title: "Show.S01E05.720p.WEB-DL-A", Protocol: "usenet"},
		{Title: "Show.S01E05.1080p.WEB-DL.H.264-B", Protocol: "usenet", Priority: 25, Published: now},
		{Title: "Show.S01E05.1080p.BluRay.x264-C", Protocol: "usenet", Priority: 50},
		{Title: "Show.S01.1080p.BluRay.x264-D", Protocol: "usenet", Priority: 10},
		{Title: "Show.S01E05.1080p.WEB-DL.H.264-NTb", Protocol: "usenet", Priority: 25, Published: now.Add(-time.Hour)},
		{Title: "Show.S01E05.REPACK.1080p.WEB-DL.H.264-E", Protocol: "usenet", Priority: 25},
	})
	var got []string
	for _, d := range ds {
		got = append(got, d.Candidate.Title[len(d.Candidate.Title)-1:])
	}
	// Bluray first, then WEBDL by score (repack +5 over plain), then by
	// priority and age; then the rejected: the season pack (an episode is
	// wanted) and the 720p.
	if strings.Join(got, "") != "CEBbDA" {
		t.Fatalf("order %s", strings.Join(got, ""))
	}
}

func TestFromArr(t *testing.T) {
	profiles := `[{"name":"HD Bluray + WEB","upgradeAllowed":true,"cutoff":7,"minFormatScore":0,"cutoffFormatScore":10000,
		"items":[{"quality":{"id":1,"name":"SDTV"},"items":[],"allowed":false},
			{"name":"WEB 1080p","id":1001,"items":[{"quality":{"id":3,"name":"WEBDL-1080p"},"allowed":true},{"quality":{"id":15,"name":"WEBRip-1080p"},"allowed":true}],"allowed":true},
			{"quality":{"id":7,"name":"Bluray-1080p"},"items":[],"allowed":true},
			{"quality":{"id":20,"name":"Bluray-1080p Remux"},"items":[],"allowed":false}],
		"formatItems":[{"format":1,"name":"x265 (HD)","score":-10000},{"format":2,"name":"Nothing","score":0}],
		"language":{"id":1,"name":"English"}}]`
	cfs := `[{"name":"x265 (HD)","specifications":[
		{"name":"x265","implementation":"ReleaseTitleSpecification","negate":false,"required":true,"fields":[{"name":"value","value":"[xh][ ._-]?265|\\bHEVC(\\b|\\d)"}]},
		{"name":"Not 2160p","implementation":"ResolutionSpecification","negate":true,"required":true,"fields":[{"name":"value","value":2160}]}]},
		{"name":"Remux","implementation":"x","specifications":[{"name":"Remux","implementation":"QualityModifierSpecification","negate":false,"required":false,"fields":[{"name":"value","value":5}]},
		{"name":"Odd","implementation":"SomethingNewSpecification","fields":[{"name":"value","value":1}]}]}]`
	defs := `[{"quality":{"id":3,"name":"WEBDL-1080p"},"minSize":4,"maxSize":100},{"quality":{"id":20,"name":"Bluray-1080p Remux"},"minSize":35,"maxSize":null}]`
	im, err := FromArr("sonarr", []byte(profiles), []byte(cfs), []byte(defs))
	if err != nil {
		t.Fatal(err)
	}
	p := im.Profiles[0]
	if p.Cutoff != "Bluray-1080p" || len(p.Items) != 4 || p.Items[1].Name != "WEB 1080p" || len(p.Items[1].Qualities) != 2 || p.Items[3].Name != "Remux-1080p" || p.Scores["x265 (HD)"] != -10000 || len(p.Scores) != 1 || p.Language != "English" {
		t.Fatalf("profile: %+v", p)
	}
	if r1, _ := p.Rank("WEBRip-1080p"); r1 != 2 {
		t.Fatalf("group rank %d", r1)
	}
	if im.Formats[1].Specs[0].Value != "remux" || len(im.Skipped) != 1 || im.Sizes["Remux-1080p"].Min != 35 || im.Sizes["WEBDL-1080p"].Max != 100 {
		t.Fatalf("formats %+v skipped %v sizes %v", im.Formats, im.Skipped, im.Sizes)
	}
	e := &Engine{Profile: p, Formats: im.Formats}
	if d := e.Evaluate(Want{Titles: []string{"Show"}, Season: 1, Episodes: []int{1}}, Candidate{Title: "Show.S01E01.1080p.WEB.x265-GRP", Protocol: "usenet"}); d.Accepted() || d.Score != -10000 {
		t.Fatalf("x265 HD: %+v", d)
	}
}
