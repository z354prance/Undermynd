package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/playback"
	"github.com/z354prance/undermynd/internal/seerr"
	"github.com/z354prance/undermynd/internal/storage"
	"github.com/z354prance/undermynd/internal/tdarr"
	"github.com/z354prance/undermynd/internal/tracearr"
	"github.com/z354prance/undermynd/internal/usenet"
)

// Test everything (Settings › Getting started): every connection that is set
// up is tried at once, the same way each page's Test button does, and the
// folders HyveMynd writes to are checked for room and write access.

type testResult struct {
	Group  string `json:"group"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"`
	Detail string `json:"detail"`
	Page   string `json:"page"` // settings page to open
	Ms     int64  `json:"ms"`
}

// testOrder keeps the groups in the order the settings menu has them.
var testOrder = map[string]int{"Folders": 0, "Arrs": 1, "Downloading": 2, "Tdarr": 3, "Media server": 4, "Requests": 5, "Other": 6}

func (s *Server) adminTestAll(w http.ResponseWriter, r *http.Request) {
	cfg := s.Cfg.Get()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var (
		mu  sync.Mutex
		out []testResult
		wg  sync.WaitGroup
	)
	run := func(group, name, page string, f func(ctx context.Context) (string, bool, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cn := context.WithTimeout(ctx, 20*time.Second)
			defer cn()
			start := time.Now()
			detail, warn, err := f(c)
			res := testResult{Group: group, Name: name, Page: page, OK: err == nil, Warn: warn && err == nil, Detail: detail, Ms: time.Since(start).Milliseconds()}
			if err != nil {
				res.Detail = err.Error()
				if c.Err() == context.DeadlineExceeded {
					res.Detail = "no answer within 20 seconds"
				}
			}
			mu.Lock()
			out = append(out, res)
			mu.Unlock()
		}()
	}

	for _, d := range []struct{ name, dir string }{{"Work folder", s.Cfg.Env.Work()}, {"Complete folder", s.Cfg.Env.Complete()}} {
		d := d
		run("Folders", d.name, "system", func(context.Context) (string, bool, error) { return checkFolder(d.dir) })
	}
	if s.Engine != nil {
		for _, root := range s.Engine.LibraryRoots {
			root := root
			run("Folders", "Library "+filepath.Base(root), "system", func(context.Context) (string, bool, error) {
				ents, err := os.ReadDir(root)
				if err != nil {
					return "", false, fmt.Errorf("%s cannot be read: %v", root, err)
				}
				return fmt.Sprintf("%s · %d folders", root, len(ents)), false, nil
			})
		}
	}

	for _, c := range cfg.Categories {
		c := c
		switch c.Kind {
		case "sonarr", "radarr", "lidarr":
		default:
			continue
		}
		label := strings.ToUpper(c.Kind[:1]) + c.Kind[1:] + " · " + c.Name
		if c.ArrURL == "" || c.ArrAPIKey == "" {
			run("Arrs", label, "arrs", func(context.Context) (string, bool, error) {
				return "", false, fmt.Errorf("no address or API key")
			})
			continue
		}
		run("Arrs", label, "arrs", func(ctx context.Context) (string, bool, error) {
			app, ver, err := arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey)).Check(ctx)
			if err != nil {
				return "", false, err
			}
			return strings.TrimSpace(app + " " + ver), false, nil
		})
	}

	if cfg.TorBox.Token != "" {
		run("Downloading", "TorBox", "clients", func(ctx context.Context) (string, bool, error) {
			d, err := torboxMe(ctx, cfg.TorBox.BaseURL, string(cfg.TorBox.Token))
			if err != nil {
				return "", false, err
			}
			if cfg.TorBox.Off {
				return d + " · switched off here: nothing new is sent", true, nil
			}
			return d, false, nil
		})
	}
	for _, n := range cfg.Usenet.Servers {
		n := n
		if !n.On {
			continue
		}
		run("Downloading", "Usenet · "+n.Label(), "usenet", func(ctx context.Context) (string, bool, error) {
			if err := usenet.Test(ctx, n, nil); err != nil {
				return "", false, err
			}
			role := "main"
			if n.Priority > 0 {
				role = fmt.Sprintf("backup %d", n.Priority)
			}
			return fmt.Sprintf("signed in · %s · %d connections", role, n.Conns), false, nil
		})
	}
	if cfg.VPN.On {
		run("Downloading", "VPN", "vpn", func(context.Context) (string, bool, error) {
			if s.VPN == nil || s.VPN.Tunnel == nil {
				return "", false, fmt.Errorf("the tunnel is not running")
			}
			st := s.VPN.Tunnel.Status()
			switch st.State {
			case "up":
				where := st.RegionName
				if where == "" {
					where = st.Server
				}
				if st.Port == 0 {
					return where + " · no forwarded port, so peers cannot reach the torrent client", true, nil
				}
				return fmt.Sprintf("%s · port %d open", where, st.Port), false, nil
			case "connecting":
				return "connecting…", true, nil
			}
			if st.Error != "" {
				return "", false, fmt.Errorf("down: %s", st.Error)
			}
			return "", false, fmt.Errorf("down")
		})
	}

	if cfg.Tdarr.URL != "" {
		run("Tdarr", "Tdarr", "transcode", func(ctx context.Context) (string, bool, error) {
			nodes, busy, err := tdarr.New(cfg.Tdarr.URL, string(cfg.Tdarr.APIKey)).Check(ctx)
			if err != nil {
				return "", false, err
			}
			if nodes == 0 {
				return "no Tdarr node is connected, so nothing gets converted", true, nil
			}
			return fmt.Sprintf("%d node%s · %d workers busy", nodes, plural(nodes), busy), false, nil
		})
	}

	if cfg.Playback.EmbyURL != "" && cfg.Playback.EmbyKey != "" {
		run("Media server", "Emby or Jellyfin", "playback", func(ctx context.Context) (string, bool, error) {
			name, err := playback.Emby{URL: cfg.Playback.EmbyURL, Key: string(cfg.Playback.EmbyKey), Kind: cfg.Access.Provider}.Check(ctx)
			return name, false, err
		})
	}
	if cfg.Playback.TracearrURL != "" {
		run("Media server", "Tracearr", "playback", func(ctx context.Context) (string, bool, error) {
			return "answers", false, tracearr.New(cfg.Playback.TracearrURL, string(cfg.Playback.TracearrKey)).Check(ctx)
		})
	}

	if cfg.Requests.Enabled && cfg.Requests.URL != "" {
		run("Requests", "Seerr", "public", func(ctx context.Context) (string, bool, error) {
			return "signed in as its request user", false, seerr.New(cfg.Requests.URL, string(cfg.Requests.APIKey), cfg.Requests.UserID).CheckUser(ctx)
		})
	}
	if cfg.BookOrbit.URL != "" {
		run("Requests", "BookOrbit", "arrs", func(ctx context.Context) (string, bool, error) {
			if cfg.BookOrbit.APIKey == "" {
				return "", false, fmt.Errorf("no API key")
			}
			bo := newBookOrbit(cfg.BookOrbit.URL, string(cfg.BookOrbit.APIKey))
			eb, au, err := bo.bookCounts(ctx)
			if err != nil {
				return "", false, err
			}
			var sum struct{}
			if bo.do(ctx, http.MethodGet, "book-requests/summary", nil, &sum) != nil {
				return fmt.Sprintf("%d ebooks · %d audiobooks · but book requests are refused: the key needs write access", eb, au), true, nil
			}
			return fmt.Sprintf("%d ebooks · %d audiobooks · requests allowed", eb, au), false, nil
		})
	}
	if cfg.Metadata.TMDBKey != "" {
		run("Other", "TMDB", "metadata", func(ctx context.Context) (string, bool, error) {
			key := string(cfg.Metadata.TMDBKey)
			return "key accepted", false, metadata.NewTMDB(nil, func() string { return key }).Check(ctx)
		})
	}

	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool {
		if testOrder[out[i].Group] != testOrder[out[j].Group] {
			return testOrder[out[i].Group] < testOrder[out[j].Group]
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, map[string]any{"results": out, "at": time.Now()})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// checkFolder makes sure a folder exists, takes a file and has room.
func checkFolder(dir string) (string, bool, error) {
	if dir == "" {
		return "", false, fmt.Errorf("not set")
	}
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return "", false, fmt.Errorf("%s cannot be created: %v", dir, err)
	}
	f, err := os.CreateTemp(dir, ".hyvemynd-test-*")
	if err != nil {
		return "", false, fmt.Errorf("%s is not writable: %v", dir, err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	free, total, err := storage.Space(dir)
	if err != nil || total == 0 {
		return dir + " · writable", false, nil
	}
	share := float64(free) / float64(total)
	d := fmt.Sprintf("%s · writable · %.0f GB free (%.0f%%)", dir, float64(free)/1e9, share*100)
	return d, share < 0.1, nil
}

// torboxMe asks TorBox who the token belongs to.
func torboxMe(ctx context.Context, base, token string) (string, error) {
	if base == "" {
		base = "https://api.torbox.app"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/api/user/me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("TorBox refused the API token")
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("TorBox answered HTTP %d", resp.StatusCode)
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Email        string `json:"email"`
			Plan         int    `json:"plan"`
			PremiumUntil string `json:"premium_expires_at"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &env) != nil || !env.Success {
		return "", fmt.Errorf("TorBox did not accept the token")
	}
	d := "token accepted"
	if env.Data.PremiumUntil != "" {
		if t, err := time.Parse(time.RFC3339, env.Data.PremiumUntil); err == nil {
			days := int(time.Until(t).Hours() / 24)
			d += fmt.Sprintf(" · plan ends %s (%d days)", t.Format("Jan 2, 2006"), days)
		}
	}
	return d, nil
}
