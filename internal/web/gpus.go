package web

import (
	"net/http"

	"github.com/z354prance/undermynd/internal/gpu"
)

// gpusList gives each graphics card's load, for the Home tiles and the Tdarr
// page. "on" is false when GPU watching is switched off.
func (s *Server) gpusList(w http.ResponseWriter, r *http.Request) {
	if s.GPU == nil {
		writeJSON(w, map[string]any{"on": false, "cards": []gpu.GPU{}})
		return
	}
	writeJSON(w, map[string]any{"on": true, "cards": s.GPU.Cards()})
}
