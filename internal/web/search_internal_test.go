package web

import "testing"

func TestSearchMatchesReleaseNames(t *testing.T) {
	cases := []struct {
		q, text string
		want    bool
	}{
		{"chicago fire s08", "Chicago.Fire.S08E16.1080p.AMZN.WEB-DL-KiNGS", true},
		{"fire chicago", "Chicago.Fire.S08E16", true},
		{"kings", "Chicago.Fire.S08E16.1080p.AMZN.WEB-DL-KiNGS", true},
		{"chicago med", "Chicago.Fire.S08E16", false},
		{"dandadan 18", "[SubsPlease] Dandadan - 18 (1080p)", true},
	}
	for _, c := range cases {
		words := splitWords(c.q)
		if got := matchAll(words, c.text); got != c.want {
			t.Errorf("%q in %q = %v, want %v", c.q, c.text, got, c.want)
		}
	}
}
