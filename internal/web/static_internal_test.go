package web

import "testing"

func TestFontsAndLookEmbedded(t *testing.T) {
	for _, p := range []string{"static/assets/hyve.css", "static/assets/fonts/barlow-latin-800-normal.woff2", "static/assets/fonts/OFL-Barlow.txt", "static/assets/fonts/ibm-plex-mono-latin-400-normal.woff2"} {
		if _, err := staticFS.ReadFile(p); err != nil {
			t.Fatal(p, err)
		}
	}
}
