package animelib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/metadata"
)

func touch(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		p := filepath.Join(root, r)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func eps(list ...[3]int) []catalog.Episode { // season, number, absolute
	var out []catalog.Episode
	for _, e := range list {
		out = append(out, catalog.Episode{Season: e[0], Number: e[1], Absolute: e[2]})
	}
	return out
}

func TestScanFolderReadsSeasonFolders(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Season 2/Show - 3 (1080p).mkv", "Season 1/Show - 1 (720p).mkv", "Specials/Show - 1 (1080p).mkv",
		"Show - S03E04 (1080p).mkv", "NCOP.mkv", "Season 1/notes.txt", ".hidden/Show - 9.mkv")
	files, err := ScanFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][3]any{}
	for _, f := range files {
		got[f.Rel] = [3]any{f.OK, f.Season, f.Episode}
	}
	want := map[string][3]any{
		"Season 1/Show - 1 (720p).mkv":  {true, 1, 1},
		"Season 2/Show - 3 (1080p).mkv": {true, 2, 3},
		"Specials/Show - 1 (1080p).mkv": {true, 0, 1},
		"Show - S03E04 (1080p).mkv":     {true, 3, 4},
		"NCOP.mkv":                      {false, 0, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v want %v", k, got[k], v)
		}
	}
}

func files(list ...[2]int) []DiskFile { // season, episode
	var out []DiskFile
	for _, f := range list {
		out = append(out, DiskFile{OK: true, Season: f[0], Episode: f[1]})
	}
	return out
}

func TestFitsPicksTheNumbering(t *testing.T) {
	// One Piece: TMDB numbers absolutely inside its seasons; both readings agree.
	op := eps([3]int{1, 1, 1}, [3]int{1, 2, 2}, [3]int{2, 3, 3}, [3]int{2, 4, 4})
	f := Fits(files([2]int{1, 1}, [2]int{1, 2}, [2]int{2, 3}, [2]int{2, 4}), op)
	if len(f) != 1 || f[0].Numbering != catalog.ByAbsolute || f[0].Mapped != 4 {
		t.Fatalf("one piece: %+v", f)
	}
	// Hero Academia: TMDB restarts each season; the files count on.
	mha := eps([3]int{1, 1, 1}, [3]int{1, 2, 2}, [3]int{2, 1, 3}, [3]int{2, 2, 4})
	f = Fits(files([2]int{1, 1}, [2]int{1, 2}, [2]int{2, 3}, [2]int{2, 4}), mha)
	if f[0].Numbering != catalog.ByAbsolute || f[0].Mapped != 4 || f[1].Mapped != 2 {
		t.Fatalf("hero academia: %+v", f)
	}
	// An ordinary season show.
	f = Fits(files([2]int{1, 1}, [2]int{1, 2}, [2]int{2, 1}, [2]int{2, 2}), mha)
	if f[0].Numbering != catalog.BySeason || f[0].Mapped != 4 || f[0].Distinct != 4 {
		t.Fatalf("season show: %+v", f)
	}
	// One season: the readings agree, so it is a season show.
	one := eps([3]int{1, 1, 1}, [3]int{1, 2, 2})
	f = Fits(files([2]int{1, 1}, [2]int{1, 2}), one)
	if len(f) != 1 || f[0].Numbering != catalog.BySeason {
		t.Fatalf("one season: %+v", f)
	}
}

func TestBlueBoxNeedsItsEpisodeGroup(t *testing.T) {
	disk := files([2]int{1, 1}, [2]int{1, 2}, [2]int{2, 1}, [2]int{2, 2})
	tmdb := eps([3]int{1, 1, 1}, [3]int{1, 2, 2}, [3]int{1, 3, 3}, [3]int{1, 4, 4})
	for _, fit := range Fits(disk, tmdb) {
		if fit.Mapped == 4 && fit.Distinct == 4 {
			t.Fatalf("TMDB's single season should not fit: %+v", fit)
		}
	}
	group := eps([3]int{1, 1, 1}, [3]int{1, 2, 2}, [3]int{2, 1, 3}, [3]int{2, 2, 4})
	f := Fits(disk, group)
	if f[0].Numbering != catalog.BySeason || f[0].Mapped != 4 || f[0].Distinct != 4 {
		t.Fatalf("seasons group: %+v", f)
	}
}

func TestLooseNames(t *testing.T) {
	for name, want := range map[string][3]int{ // ok, season, episode
		"S01E05 - The Beginning.mkv":             {1, 1, 5},
		"S01E01-Spring Begins.mkv":               {1, 1, 1},
		"S02E10-Cold Dew.mkv":                    {1, 2, 10},
		"Episode 07.mkv":                         {1, 1, 7},
		"7 Seeds Ep 03.mkv":                      {1, 1, 3},
		"4 CUT HERO 12.mkv":                      {1, 1, 12},
		"12.mkv":                                 {1, 1, 12},
		"A.I.C.O.Incarnation.E04.1080p.mkv":      {1, 1, 4},
		"show.s02e10.mkv":                        {1, 2, 10},
		"Movie 2019.mkv":                         {0, 0, 0},
		"NCOP.mkv":                               {0, 0, 0},
		"Show - 2x03 [1080p].mkv":                {1, 2, 3},
		"[Group] Show 05 (1080p) [ABCD1234].mkv": {1, 1, 5},
	} {
		r, ok := looseParse(name)
		got := [3]int{0, r.Season, r.Episode}
		if ok {
			got[0] = 1
		} else {
			got = [3]int{}
		}
		if got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestSureMatches(t *testing.T) {
	c := func(id int64, name, via string, anim bool) metadata.Candidate {
		return metadata.Candidate{TMDBID: id, Name: name, Via: via, Animation: anim}
	}
	folder := "A Herbivorous Dragon of 5000 Years Gets Unfairly Villainized"
	m, _ := sure(folder, []string{folder}, []metadata.Candidate{c(139161, "A Herbivorous Dragon of 5,000 Years Gets Unfairly Villainized", "TMDB search", true), c(9, "Dragon", "TMDB search", true)})
	if m == nil || m.TMDBID != 139161 {
		t.Fatalf("exact animated title should be sure: %+v", m)
	}
	if m, _ := sure("Hunter x Hunter", []string{"Hunter x Hunter"}, []metadata.Candidate{c(1, "Hunter x Hunter", "TMDB search", true), c(2, "Hunter × Hunter", "TMDB search", true), c(3, "HUNTER x HUNTER", "TMDB search", true)}); m != nil {
		t.Fatalf("two shows with the same title must not be sure: %+v", m)
	}
	if m, _ := sure("Monster", []string{"Monster"}, []metadata.Candidate{c(5, "Monster", "TMDB search", false)}); m != nil {
		t.Fatalf("a live-action show with the title must not be sure: %+v", m)
	}
}

func TestSureBreaksListTiesByTitle(t *testing.T) {
	lists := []metadata.Candidate{
		{TMDBID: 31701, Name: "Burst Angel", Original: "爆裂天使", Via: "anime lists", Animation: true},
		{TMDBID: 110070, Name: "Bakuten!!", Original: "バクテン!!", Via: "anime lists", Animation: true},
	}
	m, _ := sure("Bakuten!!", []string{"Bakuten!!"}, lists)
	if m == nil || m.TMDBID != 110070 {
		t.Fatalf("got %+v", m)
	}
	if m, _ := sure("Bakuten", []string{"Bakuten"}, lists[:1]); m == nil {
		t.Fatal("one list show, nothing titled like the folder: still sure")
	}
	// The lists know only the wrong show; TMDB search finds the right title.
	search := metadata.Candidate{TMDBID: 110070, Name: "Bakuten!!", Via: "TMDB search", Animation: true}
	if m, why := sure("Bakuten!!", []string{"Bakuten!!"}, []metadata.Candidate{lists[0], search}); m != nil || why == "" {
		t.Fatalf("should go to a person: %+v %q", m, why)
	}
}

func TestExtrasPastTheSeasonEnd(t *testing.T) {
	// Ah! My Goddess: season 1 has 24 TMDB episodes, the folder has 26.
	tmdb := eps([3]int{1, 1, 1}, [3]int{1, 2, 2}, [3]int{2, 1, 3}, [3]int{2, 2, 4})
	disk := files([2]int{1, 1}, [2]int{1, 2}, [2]int{1, 3}, [2]int{2, 1}, [2]int{2, 2}, [2]int{2, 3})
	fit := Fits(disk, tmdb)[0]
	if n, ok := Extras(disk, tmdb, fit); fit.Numbering != catalog.BySeason || !ok || n != 2 {
		t.Fatalf("got %d %v for %+v", n, ok, fit)
	}
	// A file in the middle of a season that matches nothing is not an extra.
	disk = files([2]int{1, 1}, [2]int{3, 1})
	for _, f := range Fits(disk, tmdb) {
		if f.Numbering == catalog.BySeason {
			fit = f
		}
	}
	if _, ok := Extras(disk, tmdb, fit); ok {
		t.Fatal("a season TMDB does not have is not an extra")
	}
}

func TestCopies(t *testing.T) {
	tmdb := eps([3]int{1, 1, 1}, [3]int{1, 2, 2}, [3]int{1, 3, 3})
	// A v2 of episode 2 is a copy.
	disk := files([2]int{1, 1}, [2]int{1, 2}, [2]int{1, 2}, [2]int{1, 3})
	fit := Fits(disk, tmdb)[0]
	if c := Copies(disk, fit); c != 1 || fit.Distinct != 3 {
		t.Fatalf("v2: copies %d %+v", c, fit)
	}
	// Two seasons numbered alike landing on one season's episodes are not copies.
	two := eps([3]int{1, 1, 1}, [3]int{1, 2, 2}, [3]int{2, 1, 3}, [3]int{2, 2, 4})
	disk = files([2]int{1, 1}, [2]int{1, 2}, [2]int{2, 1}, [2]int{2, 2})
	for _, f := range Fits(disk, two) {
		if f.Numbering == catalog.ByAbsolute && Copies(disk, f) != 0 {
			t.Fatalf("absolute collisions across seasons counted as copies: %+v", f)
		}
	}
}
