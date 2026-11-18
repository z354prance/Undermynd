package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderName(t *testing.T) {
	put := func(dir string, names ...string) string {
		d := filepath.Join(t.TempDir(), dir)
		for _, n := range names {
			p := filepath.Join(d, "Season 1", n)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte("x"), 0o644)
		}
		_ = os.MkdirAll(d, 0o755)
		return d
	}
	sub := func(s string) Release {
		r, ok := Parse(s)
		if !ok {
			t.Fatalf("parse %q", s)
		}
		return r
	}
	for _, c := range []struct {
		name, folder string
		files        []string
		release      string
		want         string
	}{
		// The library's Rename.ps1 style: bare number, resolution in parentheses.
		{"ps1 style", "Seitokai ni mo Ana wa Aru", []string{"Seitokai ni mo Ana wa Aru - 1 (1080p).mkv", "Seitokai ni mo Ana wa Aru - 2 (1080p).mkv"},
			"[Judas] Seitokai ni mo Ana wa Aru! {Even the Student Council Has Its Holes} [Uncensored] - S01E07 [1080p][HEVC x265 10bit]",
			"Seitokai ni mo Ana wa Aru - 7 (1080p).mkv"},
		// A folder in SxxEyy with tags.
		{"marked tags", "Blue Box", []string{"Blue Box - S01E01 [SubsPlease 1080p].mkv", "Blue Box - S01E02 [SubsPlease 1080p].mkv"},
			"[SubsPlease] Ao no Hako - 03 (1080p) [ABCD1234]", "Blue Box - S01E03 [SubsPlease 1080p].mkv"},
		// Padded absolute numbers.
		{"padded", "One Piece", []string{"One Piece - 0998.mkv", "One Piece - 0999.mkv"},
			"[SubsPlease] One Piece - 1159 (1080p) [ABCD1234]", "One Piece - 1159.mkv"},
		{"padded short", "One Piece", []string{"One Piece - 0998.mkv", "One Piece - 0999.mkv"},
			"[SubsPlease] One Piece - 5 (1080p) [ABCD1234]", "One Piece - 0005.mkv"},
		// A new folder follows the release.
		{"new folder", "Show Name", nil, "[SubsPlease] Show Name S2 - 07 (1080p) [ABCD1234]", "Show Name - S02E07 (1080p).mkv"},
	} {
		dir := put(c.folder, c.files...)
		got := FolderName(c.folder, FolderStyle(dir), sub(c.release+".mkv"), ".mkv")
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
