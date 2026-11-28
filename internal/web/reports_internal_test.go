package web

import (
	"testing"
	"time"
)

func TestFailClassAndTallies(t *testing.T) {
	for in, want := range map[string]string{
		"download failed: this release is gone from your providers: 40 of 40": "Missing articles",
		"download failed: no progress for 30 minutes":                         "Stalled",
		"blocked: x.exe is a program":                                         "Blocked file",
		"resolution check: 480p, grabbed as 1080p":                            "Fake or wrong quality",
		"something odd": "Other",
	} {
		if got := failClass(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
	now := time.Now()
	list := []outcome{
		{Indexer: "A", OK: true, Size: 100, Seconds: 10, At: now},
		{Indexer: "A", OK: false, Why: "Stalled", At: now},
		{Indexer: "B", OK: true, Size: 300, Seconds: 10, At: now},
	}
	ts := tallies(list, func(x outcome) string { return x.Indexer })
	if len(ts) != 2 || ts[0].Name != "A" || ts[0].Rate != 0.5 || ts[0].Speed != 10 || ts[0].TopFails[0].Name != "Stalled" {
		t.Fatalf("%+v", ts)
	}
}
