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
		"[SubsPlease] Kuma Kuma Kuma Bear S2 - 05 (1080p) [AAAA1111].mkv":            {"Kuma Kuma Kuma Bear", 2, 5},
		"[SubsPlease] Some Show 2nd Season - 12v2 (1080p) [AAAA1111].mkv":            {"Some Show", 2, 12},
		"[SubsPlease] Some Show Season 3 - 01 (720p) [AAAA1111].mkv":                 {"Some Show", 3, 1},
		"[Group] Another Show - S02E03 (1080p) [AAAA1111].mkv":                       {"Another Show", 2, 3},
		"[SubsPlease] Title With - Dash - 24 (1080p) [AAAA1111].mkv":                 {"Title With - Dash", 1, 24},
		"[SubsPlease] Plain Show - 100 (1080p) [AAAA1111].mkv":                       {"Plain Show", 1, 100},
		"Blue.Box.S02E06.1080p.WEB-DL.AAC2.0.H.264-GRP.mkv":                          {"Blue Box", 2, 6},
		"[Group] Blue Box (2024) - 07 (1080p) [AAAA1111].mkv":                        {"Blue Box", 1, 7},
		"[Group] Blue Box - EP07 (1080p) [AAAA1111].mkv":                             {"Blue Box", 1, 7},
		"[Group] Blue Box - 2x06 (1080p) [AAAA1111].mkv":                             {"Blue Box", 2, 6},
		"Blue_Box_S02E06v2_[AAAA1111].mkv":                                           {"Blue Box", 2, 6},
		"[Group] Blue Box S2 - 06 [WEB 1080p].mkv":                                   {"Blue Box", 2, 6},
		"[Trix] Blue Box S02E06 [WEB-DL 1080p AV1 Opus] (Tri Audio, Multi Subs).mkv": {"Blue Box", 2, 6},
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

func TestCleanKeepsEpisodeWhenTagsFollow(t *testing.T) {
	got := library.Clean("[Trix] Blue Box S02E06 [WEB-DL 1080p AV1 Opus] (Tri Audio, Multi Subs).mkv")
	if got != "Blue Box S02E06.mkv" {
		t.Fatalf("got %q", got)
	}
}

func TestMatchIgnoresYearAndPunctuation(t *testing.T) {
	folders := []string{"Blue Box (2024)", "Tom and Jerry", "Frierens Journey", "Other"}
	for title, want := range map[string]string{"Blue Box": "Blue Box (2024)", "Tom & Jerry": "Tom and Jerry", "Frieren's Journey": "Frierens Journey"} {
		if got, _ := library.Match(title, folders); got != want {
			t.Errorf("%q -> %q, want %q", title, got, want)
		}
	}
}

// Real names from the user's feeds (Nov 2026).
func TestParseRealFeedNames(t *testing.T) {
	cases := []struct {
		in      string
		title   string
		alias   string
		season  int
		ep      int
		res     string
		cleaned string
	}{
		{"[Judas] Seitokai ni mo Ana wa Aru! {Even the Student Council Has Its Holes} [Uncensored] - S01E07 [1080p][HEVC x265 10bit][Multi-Subs] (Weekly).mkv",
			"Seitokai ni mo Ana wa Aru!", "Even the Student Council Has Its Holes", 1, 7, "1080p",
			"Seitokai ni mo Ana wa Aru! - S01E07.mkv"},
		{"[Judas] Seitokai ni mo Ana wa Aru! {Even the Student Council Has Its Holes} [Uncensored] - S01E01 [1080p][HEVC x265 10bit][Multi-Subs] (Weekly)",
			"Seitokai ni mo Ana wa Aru!", "Even the Student Council Has Its Holes", 1, 1, "1080p", ""},
		{"[Trix] Blue Box S02E06 1080p NF WEB-DL Opus2.0 AV1 (Multi-Audio / Dual-Audio, Multi-Subs) (Ao no Hako Season 2) (Weekly)",
			"Blue Box", "", 2, 6, "1080p", ""},
		{"[SubsPlease] 86 - Eighty Six - 02 (1080p) [285887DF].mkv",
			"86 - Eighty Six", "", 1, 2, "1080p", "86 - Eighty Six - 2 (1080p).mkv"},
		{"[SubsPlease] One Piece - 1158 (1080p) [12345678].mkv",
			"One Piece", "", 1, 1158, "1080p", "One Piece - 1158 (1080p).mkv"},
	}
	for _, c := range cases {
		r, ok := library.Parse(c.in)
		if !ok || r.Title != c.title || r.Season != c.season || r.Episode != c.ep || r.Res != c.res {
			t.Errorf("%q\n -> %+v %v", c.in, r, ok)
			continue
		}
		if c.alias != "" && (len(r.Aliases) != 1 || r.Aliases[0] != c.alias) {
			t.Errorf("%q: aliases %q, want %q", c.in, r.Aliases, c.alias)
		}
		if c.alias == "" && len(r.Aliases) != 0 {
			t.Errorf("%q: unexpected aliases %q", c.in, r.Aliases)
		}
		if c.cleaned != "" {
			if got := library.Clean(c.in); got != c.cleaned {
				t.Errorf("Clean(%q)\n got  %q\n want %q", c.in, got, c.cleaned)
			}
		}
	}
}

func TestMatchAnyUsesAliases(t *testing.T) {
	folders := []string{"Seitokai ni mo Ana wa Aru", "Even the Student Council Has Its Holes Fan Folder", "Blue Box"}
	if f, _ := library.MatchAny([]string{"Seitokai ni mo Ana wa Aru!", "Even the Student Council Has Its Holes"}, folders); f != "Seitokai ni mo Ana wa Aru" {
		t.Fatalf("romaji title should match, got %q", f)
	}
	only := []string{"Even the Student Council Has Its Holes", "Blue Box"}
	if f, _ := library.MatchAny([]string{"Seitokai ni mo Ana wa Aru!", "Even the Student Council Has Its Holes"}, only); f != "Even the Student Council Has Its Holes" {
		t.Fatalf("a folder named after the English title should match through the alias, got %q", f)
	}
	two := []string{"Seitokai ni mo Ana wa Aru", "Even the Student Council Has Its Holes"}
	if f, c := library.MatchAny([]string{"Seitokai ni mo Ana wa Aru!", "Even the Student Council Has Its Holes"}, two); f != "" || len(c) != 2 {
		t.Fatalf("two different folders must not be guessed between: %q %v", f, c)
	}
}

func TestNormalizeThousands(t *testing.T) {
	a := library.Normalize("A Herbivorous Dragon of 5,000 Years Gets Unfairly Villainized")
	b := library.Normalize("A Herbivorous Dragon of 5000 Years Gets Unfairly Villainized")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	if library.Normalize("Re:Zero 2.5") != "re zero 2 5" || library.Normalize("1,000,000") != "1000000" {
		t.Fatal(library.Normalize("Re:Zero 2.5"), library.Normalize("1,000,000"))
	}
}
