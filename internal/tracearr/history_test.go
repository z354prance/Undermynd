package tracearr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Tracearr sends some ids as text; a play is still read in full, and the
// cursor pages are followed.
func TestHistoryReadsOddFieldsAndPages(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"data":[{"media_type":"episode","show_title":"Suits","tmdb_id":"1234","tvdb_id":null,"year":"2011","percent_complete":"87.5","watched":true,
				"duration_ms":1800000,"total_duration_ms":2700000,"is_transcode":false,"started_at":"2026-11-27T20:00:00.000Z","user":{"id":7,"username":"sam"},"genres":["Drama"]}],
				"meta":{"nextCursor":"abc","pageSize":100}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"media_type":"episode","show_title":"Suits","started_at":"2026-11-26T20:00:00Z"}],"meta":{"nextCursor":null}}`)
	}))
	defer ts.Close()
	plays, err := New(ts.URL, "k").History(context.Background(), "episode", time.Now().AddDate(0, 0, -10))
	if err != nil || len(plays) != 2 {
		t.Fatalf("%d plays, %v", len(plays), err)
	}
	p := plays[0]
	if p.TMDB != 1234 || p.Year != 2011 || p.Percent != 87.5 || !p.Watched || p.DurationMs != 1800000 || p.TotalMs != 2700000 ||
		p.IsTranscode == nil || *p.IsTranscode || p.StartedAt == nil || p.User.ID != "7" || p.ShowTitle != "Suits" || len(p.Genres) != 1 {
		t.Fatalf("%+v", p)
	}
}
