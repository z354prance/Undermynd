package music

import "testing"

func TestQuality(t *testing.T) {
	for name, want := range map[string]string{
		"Radiohead - OK Computer (1997) [FLAC 24-96]":        "FLAC 24bit",
		"Radiohead - OK Computer (1997) [FLAC] {CD}":         "FLAC",
		"Radiohead-OK_Computer-CD-FLAC-1997-GRP":             "FLAC",
		"Radiohead - OK Computer (1997) [MP3 320]":           "MP3-320",
		"Radiohead - OK Computer (1997) [V0]":                "MP3-VBR-V0",
		"Radiohead - OK Computer 1997 Mp3 256kbps":           "MP3-256",
		"Radiohead - OK Computer (1997) [ALAC]":              "ALAC",
		"Radiohead - OK Computer (1997) [AAC 320]":           "AAC-320",
		"Radiohead - OK Computer (1997)":                     "Unknown",
		"Radiohead - OK Computer (1997) [24bit Hi-Res] FLAC": "FLAC 24bit",
	} {
		if g := Quality(name); g != want {
			t.Errorf("%s: %s, want %s", name, g, want)
		}
	}
}

func TestCommonDir(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"Album/01.flac", "Album/02.flac", "Album/cover.jpg"}, "Album"},
		{[]string{"Album/CD1/01.flac", "Album/CD2/01.flac"}, "Album"},
		{[]string{"CD1/01.flac", "CD1/02.flac"}, ""},
		{[]string{"01.flac", "02.flac"}, ""},
	} {
		if g := commonDir(c.in); g != c.want {
			t.Errorf("%v: %q, want %q", c.in, g, c.want)
		}
	}
}
