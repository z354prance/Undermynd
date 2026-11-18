package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/parser"
)

// parserTry shows what Undermynd reads from one release name.
func (s *Server) parserTry(w http.ResponseWriter, r *http.Request) {
	in := parser.Parse(r.URL.Query().Get("name"))
	writeJSON(w, map[string]any{"info": in, "quality": in.Quality.Name()})
}

// parserCheck compares Undermynd's reading of the releases an arr grabbed with
// what the arr decided about them.
func (s *Server) parserCheck(w http.ResponseWriter, r *http.Request) {
	c, ok := s.Cfg.Get().Category(r.URL.Query().Get("category"))
	if !ok || c.ArrURL == "" {
		jsonErr(w, http.StatusBadRequest, "pick a category connected to Sonarr or Radarr")
		return
	}
	max, _ := strconv.Atoi(r.URL.Query().Get("max"))
	if max <= 0 || max > 5000 {
		max = 1000
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	samples, err := arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey)).Samples(ctx, max)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	in := make([]parser.Sample, len(samples))
	for i, x := range samples {
		in[i] = parser.Sample(x)
	}
	writeJSON(w, parser.Compare(in))
}
