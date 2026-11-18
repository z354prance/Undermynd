package shows

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanTidy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Seitokai ni mo Ana wa Aru")
	put := func(rel string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("Season 1/Seitokai ni mo Ana wa Aru - 1 (1080p).mkv")
	put("Season 1/Seitokai ni mo Ana wa Aru - 2 (1080p).mkv")
	put("Season 1/Seitokai ni mo Ana wa Aru! - S01E03.mkv")          // other title and form
	put("Season 1/Seitokai ni mo Ana wa Aru! - S01E03.ass")          // its subtitles
	put("[Judas] Seitokai ni mo Ana wa Aru - 04 (1080p) [ABCD].mkv") // loose at the top
	put("Season 1/Seitokai ni mo Ana wa Aru - 5 (1080p).mkv")
	put("Season 1/Seitokai ni mo Ana wa Aru - 05 (720p).mkv") // the same episode twice
	put("Season 1/extras.mkv")                                // no episode number

	files, err := ScanFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := planTidy(dir, files)
	want := map[string]string{
		filepath.Join("Season 1", "Seitokai ni mo Ana wa Aru! - S01E03.mkv"): filepath.Join("Season 1", "Seitokai ni mo Ana wa Aru - 3.mkv"),
		"[Judas] Seitokai ni mo Ana wa Aru - 04 (1080p) [ABCD].mkv":          filepath.Join("Season 1", "Seitokai ni mo Ana wa Aru - 4 (1080p).mkv"),
	}
	if len(p.Renames) != len(want) {
		t.Fatalf("renames %+v", p.Renames)
	}
	for _, r := range p.Renames {
		if want[r.From] != r.To {
			t.Errorf("%s -> %s, want %s", r.From, r.To, want[r.From])
		}
	}
	if len(p.Unreadable) != 1 || len(p.Duplicates) != 1 || len(p.Duplicates[0]) != 2 {
		t.Fatalf("unreadable %v duplicates %v", p.Unreadable, p.Duplicates)
	}

	// Applying moves the video and its subtitles; a second plan has no renames left.
	s := &Service{}
	n, skipped := applyPlan(s, dir, p, p.Renames)
	if n != 2 || len(skipped) != 0 {
		t.Fatalf("applied %d, skipped %v", n, skipped)
	}
	if _, err := os.Stat(filepath.Join(dir, "Season 1", "Seitokai ni mo Ana wa Aru - 3.ass")); err != nil {
		t.Fatal("subtitles should follow their video")
	}
	files, _ = ScanFolder(dir)
	if p2 := planTidy(dir, files); len(p2.Renames) != 0 {
		t.Fatalf("still to rename: %+v", p2.Renames)
	}
}
