package trash

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/decision"
)

// Syncer applies guide profiles to Undermynd's profiles.
type Syncer struct {
	Client *Client
	Store  *decision.Store
	Cfg    *config.Config
	Log    *log.Logger
}

func (s *Syncer) plan(ctx context.Context, app string, ids []string, size string) (decision.Imported, []Change, error) {
	g, err := s.Client.Get(ctx)
	if err != nil {
		return decision.Imported{}, nil, err
	}
	im, err := g.Plan(app, ids, size)
	if err != nil {
		return im, nil, err
	}
	ps, err := s.Store.Profiles()
	if err != nil {
		return im, nil, err
	}
	fs, err := s.Store.Formats()
	if err != nil {
		return im, nil, err
	}
	sz, err := s.Store.Sizes()
	if err != nil {
		return im, nil, err
	}
	return im, Merge(app, &im, ps, fs, sz), nil
}

// Preview lists what applying would change, and the conditions Undermynd
// cannot check.
func (s *Syncer) Preview(ctx context.Context, app string, ids []string, size string) ([]Change, []string, error) {
	im, ch, err := s.plan(ctx, app, ids, size)
	return ch, im.Skipped, err
}

// Apply stores the guide profiles, their formats and the sizes.
func (s *Syncer) Apply(ctx context.Context, app string, ids []string, size string) ([]Change, error) {
	if app != "radarr" && app != "sonarr" {
		return nil, fmt.Errorf("radarr or sonarr")
	}
	im, ch, err := s.plan(ctx, app, ids, size)
	if err != nil {
		return nil, err
	}
	if _, _, err := s.Store.Save(Source(app), im); err != nil {
		return nil, err
	}
	return ch, nil
}

// Run syncs the kept guide profiles once a day while sync is on.
func (s *Syncer) Run(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	select { // let the rest start first
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}
	for {
		st := s.Cfg.Get().Trash
		if st.Sync {
			for app, ids := range map[string][]string{"radarr": st.Radarr, "sonarr": st.Sonarr} {
				size := st.SizeRadarr
				if app == "sonarr" {
					size = st.SizeSonarr
				}
				if len(ids) == 0 && size == "" {
					continue
				}
				ch, err := s.Apply(ctx, app, ids, size)
				if err != nil {
					s.Log.Printf("trash: %s: %v", app, err)
					continue
				}
				n := 0
				for _, c := range ch {
					if c.Action != "keep" {
						n++
					}
				}
				if n > 0 {
					s.Log.Printf("trash: %s: %d change(s) from the guides", app, n)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
