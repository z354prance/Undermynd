package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/metadata"
)

// metadataTest checks a TMDB key: the one typed in, or the saved one when blank.
func (s *Server) metadataTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad request")
		return
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = strings.TrimSpace(string(s.Cfg.Get().Metadata.TMDBKey))
	}
	t := metadata.NewTMDB(nil, func() string { return key })
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := t.Check(ctx); err != nil {
		var se *metadata.StatusError
		if errors.As(err, &se) || errors.Is(err, metadata.ErrNoKey) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) metadataStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"enabled": s.Metadata != nil, "hasKey": s.Cfg.Get().Metadata.TMDBKey != ""}
	if s.Metadata != nil && s.Metadata.Lists != nil {
		out["lists"] = s.Metadata.Lists.Status()
	}
	writeJSON(w, out)
}

// metadataListsRefresh downloads the anime lists now, in the background.
func (s *Server) metadataListsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.Metadata == nil || s.Metadata.Lists == nil {
		jsonErr(w, http.StatusConflict, "metadata needs the database (UNDERMYND_STORE is set to json)")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		st := s.Metadata.Lists.Refresh(ctx)
		s.Log.Printf("metadata: anime lists refreshed: %d titles, %d ids %s%s", st.Titles, st.IDs, st.TitlesError, st.IDsError)
	}()
	writeJSON(w, map[string]any{"started": true})
}
