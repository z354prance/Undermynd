package guard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestName(t *testing.T) {
	for _, n := range []string{"Movie.2020/Movie.2020.mkv.exe", "setup.EXE", "Codec/install.msi", "Play Me.lnk", "x.scr", "run.bat", "a.vbs", "evil‮vkm.exe"} {
		if Name(n) == "" {
			t.Errorf("%s allowed", n)
		}
	}
	for _, n := range []string{"Movie.2020.1080p.mkv", "Show/S01E01.mkv", "Sample/sample.mkv", "Movie.nfo", "Subs/English.srt", "cover.jpg", "RARBG.txt", "Visit.url", "Show.part01.rar"} {
		if why := Name(n); why != "" {
			t.Errorf("%s blocked: %s", n, why)
		}
	}
}

func TestContent(t *testing.T) {
	dir := t.TempDir()
	write := func(n string, b []byte) string { p := filepath.Join(dir, n); os.WriteFile(p, b, 0o644); return p }
	if Content(write("ok.mkv", []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3})) != "" {
		t.Error("real mkv blocked")
	}
	if Content(write("ok.mp4", append([]byte{0, 0, 0, 0x20}, "ftypisom"...))) != "" {
		t.Error("real mp4 blocked")
	}
	if Content(write("ok.ts", []byte{0x47, 0x40, 0x11})) != "" {
		t.Error("real ts blocked")
	}
	for n, b := range map[string][]byte{
		"fake.mkv":  []byte("MZ\x90\x00\x03"),
		"fake.mp4":  []byte("PK\x03\x04rest"),
		"fake.avi":  []byte("\x7fELF\x02"),
		"fake.mp3":  []byte("  <html><body>download codec"),
		"fake2.mkv": []byte("Rar!\x1a\x07\x00"),
	} {
		if Content(write(n, b)) == "" {
			t.Errorf("%s allowed", n)
		}
	}
	if Content(write("notes.txt", []byte("MZ"))) != "" {
		t.Error("non-media file checked")
	}
}
