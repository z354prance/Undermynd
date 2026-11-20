package web

import (
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/job"
)

func TestSeasonPack(t *testing.T) {
	for _, c := range []struct {
		in     string
		title  string
		season int
	}{
		{"Pucca S01 1080p AMZN WEB-DL DDP2.0 H.264-GRP", "Pucca", 1},
		{"Pucca.S02.1080p.AMZN.WEB-DL.DDP2.0.H.264-GRP", "Pucca", 2},
		{"The Bear Season 3 Complete 1080p", "The Bear", 3},
		{"Some.Show.Complete.Series.1080p", "Some Show Complete Series 1080p", 0},
	} {
		title, season := seasonPack(c.in)
		if title != c.title || season != c.season {
			t.Errorf("%q: got %q, %d; want %q, %d", c.in, title, season, c.title, c.season)
		}
	}
}

func TestImportedLeavesAndStuckCleanupFlags(t *testing.T) {
	old := time.Now().Add(-20 * time.Minute)
	j := job.Job{State: job.Imported, Reason: "cleanup: torbox: 429 (retrying)", History: []job.Transition{{At: old, To: job.Imported}}}
	if got := importedAt(j); !got.Equal(old) {
		t.Fatalf("importedAt %v", got)
	}
	if !cleanupStuck(j) {
		t.Fatal("a cleanup failing for 20 minutes should need attention")
	}
	j.History[0].At = time.Now()
	if cleanupStuck(j) {
		t.Fatal("a fresh import is not stuck")
	}
}
