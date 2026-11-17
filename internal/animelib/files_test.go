package animelib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/z354prance/undermynd/internal/catalog"
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
