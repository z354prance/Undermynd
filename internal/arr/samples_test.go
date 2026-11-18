package arr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSamplesJoinsTheEpisodesOfOneGrab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/history" || r.URL.Query().Get("eventType") != "1" || r.URL.Query().Get("includeEpisode") != "true" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`{"totalRecords":3,"records":[
			{"sourceTitle":"Show.S01E01E02.1080p.WEB.h264-GRP","downloadId":"A","quality":{"quality":{"name":"WEBDL-1080p"}},"episode":{"seasonNumber":1,"episodeNumber":2,"absoluteEpisodeNumber":2},"series":{"title":"Show"}},
			{"sourceTitle":"Show.S01E01E02.1080p.WEB.h264-GRP","downloadId":"A","quality":{"quality":{"name":"WEBDL-1080p"}},"episode":{"seasonNumber":1,"episodeNumber":1,"absoluteEpisodeNumber":1},"series":{"title":"Show"}},
			{"sourceTitle":"Other.S02E03.720p.HDTV.x264-GRP","downloadId":"B","quality":{"quality":{"name":"HDTV-720p"}},"episode":{"seasonNumber":2,"episodeNumber":3}}]}`))
	}))
	defer srv.Close()
	s, err := New("sonarr", srv.URL, "k").Samples(context.Background(), 10)
	if err != nil || len(s) != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if s[0].Season != 1 || len(s[0].Episodes) != 2 || s[0].Episodes[0] != 1 || s[0].Series != "Show" || s[1].Quality != "HDTV-720p" {
		t.Fatalf("%+v", s)
	}
}
