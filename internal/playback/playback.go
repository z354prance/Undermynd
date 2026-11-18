// Package playback withholds Emby streams from chosen libraries before the public
// dashboard shows them. Ported from Overmynd (OVERMYND_HIDDEN_EMBY_LIBRARIES).
package playback

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/mediaserver"
	"github.com/z354prance/undermynd/internal/tracearr"
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

// Emby is the media server (Emby or Jellyfin) whose libraries can be hidden.
type Emby struct {
	URL  string
	Key  string
	Kind string // "emby" (default) or "jellyfin"
}

type library struct {
	ID     string `json:"Id"`
	ItemID string `json:"ItemId"`
}

func (e Emby) server() mediaserver.Server {
	return mediaserver.Server{Kind: e.Kind, URL: e.URL, Key: e.Key}
}

func (e Emby) get(ctx context.Context, path string, out any) error {
	if err := e.server().Do(ctx, http.MethodGet, path, nil, out); err != nil {
		var st mediaserver.StatusError
		if errors.As(err, &st) {
			return errors.New("media server returned an error")
		}
		return err
	}
	return nil
}

// Check proves the address and key work.
func (e Emby) Check(ctx context.Context) (name string, err error) {
	return e.server().Check(ctx)
}

// Filter drops Emby sessions that belong to an excluded library (comma-separated IDs).
// A session that cannot be placed in a library is withheld rather than made public.
// Sessions from other servers pass; ones with no server type are withheld.
func Filter(ctx context.Context, e Emby, sessions []tracearr.Session, excluded string) []tracearr.Session {
	blocked := map[string]bool{}
	for _, id := range strings.Split(excluded, ",") {
		if id = strings.TrimSpace(id); id != "" {
			blocked[id] = true
		}
	}
	if len(blocked) == 0 {
		return sessions
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out := make([]tracearr.Session, 0, len(sessions))
	var roots map[string]bool
	loaded := false
	for _, s := range sessions {
		if !strings.EqualFold(s.ServerType, "emby") {
			if s.ServerType != "" {
				out = append(out, s)
			}
			continue
		}
		if s.LibraryID != "" {
			if !blocked[s.LibraryID] {
				out = append(out, s)
			}
			continue
		}
		if e.URL == "" || e.Key == "" || !idPattern.MatchString(s.RatingKey) {
			continue
		}
		if !loaded {
			loaded = true
			var libs []library
			if e.get(ctx, "/Library/VirtualFolders", &libs) == nil {
				roots = map[string]bool{}
				for _, l := range libs {
					if l.ItemID != "" {
						roots[l.ItemID] = true
					}
				}
			}
		}
		if len(roots) == 0 {
			continue
		}
		var anc []library
		if e.get(ctx, "/Items/"+s.RatingKey+"/Ancestors", &anc) != nil {
			continue
		}
		identified, hidden := false, false
		for _, a := range anc {
			if blocked[a.ID] {
				hidden = true
			}
			if roots[a.ID] {
				identified = true
			}
		}
		if identified && !hidden {
			out = append(out, s)
		}
	}
	return out
}
