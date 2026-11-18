package history_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/history"
)

func TestLog(t *testing.T) {
	h, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	l := &history.Log{DB: h, Now: func() time.Time { return now }}
	l.Add(history.Event{Kind: "movie", ItemID: 1, Item: "Heat (1995)", Event: history.Grabbed, Release: "Heat.1995.1080p.BluRay.x264-GRP"})
	now = now.Add(time.Minute)
	l.Add(history.Event{Kind: "movie", ItemID: 1, Item: "Heat (1995)", Event: history.Imported})
	l.Add(history.Event{Kind: "episode", ItemID: 5, Item: "Show S01E01", Event: history.Failed})
	var none *history.Log
	none.Add(history.Event{}) // a nil log records nothing and does not panic
	all, err := l.List("", 0, 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("%v %v", all, err)
	}
	mine, _ := l.List("movie", 1, 10)
	if len(mine) != 2 || mine[0].Event != history.Imported {
		t.Fatalf("newest first: %+v", mine)
	}
}
