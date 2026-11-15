package library_test

import (
	"testing"

	"github.com/z354prance/undermynd/internal/library"
)

func TestParseSubsPlease(t *testing.T) {
	r, ok := library.Parse("[SubsPlease] Kashita Maryoku wa Revo Barai de Kyousei Choushuu - 07 (1080p) [EDB4680B].mkv")
	if !ok || r.Title != "Kashita Maryoku wa Revo Barai de Kyousei Choushuu" || r.Episode != 7 || r.EpisodeS != "07" || r.Res != "1080p" || r.Season != 1 || r.Marked {
		t.Fatalf("%+v %v", r, ok)
	}
}

func TestParseSeasonMarkers(t *testing.T) {
	cases := map[string]struct {
		title  string
		season int
		ep     int
	}{
		"[SubsPlease] Kuma Kuma Kuma Bear S2 - 05 (1080p) [AAAA1111].mkv": {"Kuma Kuma Kuma Bear", 2, 5},
		"[SubsPlease] Some Show 2nd Season - 12v2 (1080p) [AAAA1111].mkv": {"Some Show", 2, 12},
		"[SubsPlease] Some Show Season 3 - 01 (720p) [AAAA1111].mkv":      {"Some Show", 3, 1},
		"[Group] Another Show - S02E03 (1080p) [AAAA1111].mkv":            {"Another Show", 2, 3},
		"[SubsPlease] Title With - Dash - 24 (1080p) [AAAA1111].mkv":      {"Title With - Dash", 1, 24},
		"[SubsPlease] Plain Show - 100 (1080p) [AAAA1111].mkv":            {"Plain Show", 1, 100},
	}
	for in, want := range cases {
		r, ok := library.Parse(in)
		if !ok || r.Title != want.title || r.Season != want.season || r.Episode != want.ep {
			t.Errorf("%q -> %+v %v", in, r, ok)
		}
	}
	if _, ok := library.Parse("Some.Movie.2020.1080p.BluRay.mkv"); ok {
		t.Error("a name without an episode must not parse")
	}
}

func TestMatchIsExactOrNothing(t *testing.T) {
	folders := []string{"Kuma Kuma Kuma Bear", "Kono Subarashii Sekai ni Bakuen wo!", "Kono Subarashii Sekai ni Shukufuku wo!", "Koroshi Ai"}
	if f, _ := library.Match("kono subarashii sekai ni bakuen wo", folders); f != "Kono Subarashii Sekai ni Bakuen wo!" {
		t.Fatalf("punctuation and case must not matter, got %q", f)
	}
	f, cands := library.Match("Kono Subarashii Sekai ni", folders)
	if f != "" || len(cands) == 0 {
		t.Fatalf("a partial title must not be accepted, only offered: %q %v", f, cands)
	}
	if f, c := library.Match("Totally New Show", folders); f != "" || len(c) != 0 {
		t.Fatalf("an unknown show matches nothing: %q %v", f, c)
	}
}

func TestCleanMatchesTheRenameScript(t *testing.T) {
	cases := map[string]string{
		"[SubsPlease] Kashita Maryoku wa Revo Barai de Kyousei Choushuu - 07 (1080p) [EDB4680B].mkv": "Kashita Maryoku wa Revo Barai de Kyousei Choushuu - 7 (1080p).mkv",
		"[SubsPlease] Kuma Kuma Kuma Bear S2 - 10 (1080p) [AAAA1111].mkv":                            "Kuma Kuma Kuma Bear S2 - 10 (1080p).mkv",
		"[SubsPlease] Show - 100 (1080p) [AAAA1111].mkv":                                             "Show - 100 (1080p).mkv",
		"Already Clean - 3 (720p).mkv":                                                               "Already Clean - 3 (720p).mkv",
	}
	for in, want := range cases {
		if got := library.Clean(in); got != want {
			t.Errorf("%q\n got  %q\n want %q", in, got, want)
		}
	}
}
