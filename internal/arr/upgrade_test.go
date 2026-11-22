package arr_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/z354prance/undermynd/internal/arr"
)

func TestUpgradeCalls(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		switch r.URL.Path {
		case "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"HD-1080p"},{"id":4,"name":"Upgrade"}]`))
		case "/api/v3/series":
			if r.URL.Query().Get("tvdbId") != "73762" {
				t.Errorf("tvdb %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"id":5,"title":"Grey's Anatomy","qualityProfileId":1,"seasons":[{"seasonNumber":0},{"seasonNumber":1,"monitored":true}]}]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := arr.New("sonarr", srv.URL, "k")
	ctx := context.Background()
	p, err := c.ProfileByName(ctx, "upgrade")
	if err != nil || p.ID != 4 {
		t.Fatalf("profile %+v %v", p, err)
	}
	it, ok, err := c.SeriesByTVDB(ctx, 73762)
	if err != nil || !ok || it.ID != 5 || it.ProfileID != 1 || len(it.Seasons) != 2 {
		t.Fatalf("series %+v %v %v", it, ok, err)
	}
	if err := c.SetProfile(ctx, 5, 4); err != nil {
		t.Fatal(err)
	}
	if err := c.SearchSeason(ctx, 5, 4); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`PUT /api/v3/series/editor {"qualityProfileId":4,"seriesIds":[5]}`,
		`POST /api/v3/command {"name":"SeasonSearch","seasonNumber":4,"seriesId":5}`,
	}
	for i, w := range want {
		if got[2+i] != w {
			t.Errorf("call %d: got %s want %s", i, got[2+i], w)
		}
	}
}
