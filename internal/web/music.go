package web

import (
	"net/http"

	"github.com/z354prance/undermynd/internal/musicbrainz"
)

func (s *Server) musicReady(w http.ResponseWriter) bool {
	if s.MusicBrainz == nil {
		jsonErr(w, http.StatusConflict, "music needs the database")
		return false
	}
	return true
}

// mbArtists searches MusicBrainz for artists.
func (s *Server) mbArtists(w http.ResponseWriter, r *http.Request) {
	if !s.musicReady(w) {
		return
	}
	as, err := s.MusicBrainz.SearchArtists(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"artists": as})
}

// mbAlbums lists an artist's albums, EPs and singles from MusicBrainz.
func (s *Server) mbAlbums(w http.ResponseWriter, r *http.Request) {
	if !s.musicReady(w) {
		return
	}
	al, err := s.MusicBrainz.Albums(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	type album struct {
		musicbrainz.Album
		Year  int    `json:"year"`
		Cover string `json:"cover"`
	}
	out := make([]album, len(al))
	for i, a := range al {
		out[i] = album{a, a.Year(), musicbrainz.Cover(a.ID)}
	}
	writeJSON(w, map[string]any{"albums": out})
}
