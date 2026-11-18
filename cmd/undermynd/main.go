package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // so TZ works in a minimal image

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/history"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/logbuf"
	"github.com/z354prance/undermynd/internal/mediaserver"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/movies"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/progress"
	"github.com/z354prance/undermynd/internal/qbitapi"
	"github.com/z354prance/undermynd/internal/requests"
	"github.com/z354prance/undermynd/internal/rss"
	"github.com/z354prance/undermynd/internal/sabapi"
	"github.com/z354prance/undermynd/internal/scheduler"
	"github.com/z354prance/undermynd/internal/shadow"
	"github.com/z354prance/undermynd/internal/shows"
	"github.com/z354prance/undermynd/internal/state"
	"github.com/z354prance/undermynd/internal/storage"
	"github.com/z354prance/undermynd/internal/store"
	"github.com/z354prance/undermynd/internal/trash"
	"github.com/z354prance/undermynd/internal/tv"
	"github.com/z354prance/undermynd/internal/watch"
	"github.com/z354prance/undermynd/internal/web"
)

// dropPrivileges runs the app as PUID:PGID (Unraid: 99:100) when started as root,
// after making sure it owns its config and data folders.
func dropPrivileges(logger *log.Logger, env config.Env) {
	if os.Getuid() != 0 {
		return
	}
	uid, err1 := strconv.Atoi(os.Getenv("PUID"))
	gid, err2 := strconv.Atoi(os.Getenv("PGID"))
	if err1 != nil || err2 != nil || uid == 0 {
		return
	}
	for _, d := range []string{env.ConfigDir, env.DataDir, env.Work(), env.Complete()} {
		if _, err := os.Stat(d); err == nil {
			continue // never change the owner of a folder that already exists
		}
		if err := os.MkdirAll(d, 0o755); err == nil {
			_ = os.Chown(d, uid, gid)
		}
	}
	// Chown the top level of the config folder (settings, keys, job file) in case an earlier run was root.
	if ents, err := os.ReadDir(env.ConfigDir); err == nil {
		for _, e := range ents {
			_ = os.Chown(filepath.Join(env.ConfigDir, e.Name()), uid, gid)
		}
	}
	if err := syscall.Setgid(gid); err != nil {
		logger.Fatalf("setgid: %v", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		logger.Fatalf("setuid: %v", err)
	}
	syscall.Umask(0o002) // group-writable, so the arrs and Tdarr can use what we create
	logger.Printf("running as %d:%d", uid, gid)
}

// healthcheck is used by the container HEALTHCHECK (the image has no curl).
func healthcheck(addr string) {
	if len(addr) > 0 && addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/health")
	if err != nil || resp.StatusCode != 200 {
		os.Exit(1)
	}
}

func main() {
	logs := logbuf.New(2000)
	logger := log.New(io.MultiWriter(os.Stdout, logs), "undermynd ", log.LstdFlags)
	env := config.LoadEnv()
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck(env.PublicAddr)
		return
	}
	dropPrivileges(logger, env)
	cfg, err := config.Load(env)
	if err != nil {
		logger.Fatalf("config: %v", err)
	}
	// State lives in SQLite (/config/undermynd.db) unless UNDERMYND_STORE=json asks
	// for the old JSON files. The first SQLite start imports the JSON files and
	// keeps them as .bak.
	var (
		st    *store.Store
		blobs state.Blobs = state.Files{Dir: env.ConfigDir}
		dbh   *sql.DB
	)
	legacyJobs := filepath.Join(env.ConfigDir, "jobs.json")
	if strings.EqualFold(os.Getenv("UNDERMYND_STORE"), "json") {
		st, err = store.Open(legacyJobs)
		if err != nil {
			logger.Fatalf("store: %v", err)
		}
		logger.Printf("store: JSON files (UNDERMYND_STORE=json)")
	} else {
		h, err := openDatabase(logger, env.ConfigDir)
		if err != nil {
			logger.Fatalf("database: %v", err)
		}
		defer h.Close()
		st, err = store.OpenDB(h, legacyJobs)
		if err != nil {
			logger.Fatalf("store: %v", err)
		}
		blobs = db.Blobs{DB: h, LegacyDir: env.ConfigDir}
		dbh = h
		go keepBackups(logger, h, filepath.Join(env.ConfigDir, "backups"))
	}
	au, err := auth.New(env.ConfigDir)
	if err != nil {
		logger.Fatalf("auth: %v", err)
	}
	eng := pipeline.New(st, cfg, logger)
	qb := qbitapi.New(st, eng, cfg, logger)
	sab := sabapi.New(st, eng, cfg, logger)
	machine := http.NewServeMux()
	machine.Handle("/api", sab.Handler())
	machine.Handle("/sabnzbd/", sab.Handler())
	machine.Handle("/", qb.Handler())
	site := web.New(st, eng, cfg, au, logger)
	// The indexers, served to the arrs as Torznab/Newznab (roadmap D2), on the
	// same address and key as the download client.
	machine.Handle("/indexers/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site.Indexers == nil {
			http.NotFound(w, r)
			return
		}
		site.Indexers.Handler(func() string { return string(cfg.Get().ClientPassword) }).ServeHTTP(w, r)
	}))
	site.Logs = logs

	if am, err := web.NewAccessWith(blobs, cfg); err != nil {
		logger.Fatalf("access: %v", err)
	} else {
		if err := am.RecoverInterrupted(); err != nil {
			logger.Fatalf("access: %v", err)
		}
		site.Access = am
	}

	if cfg.Created {
		s := cfg.Get()
		logger.Printf("first run: download client login for Sonarr/Radarr/Lidarr -> username %q, password %q (shown once; rotate it in the admin page)", s.ClientUser, string(s.ClientPassword))
	}
	if k := au.SetupKey(); k != "" {
		logger.Printf("no admin account yet. Open /admin and use setup key: %s", k)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go eng.Run(ctx, env.TickInterval)
	pt := progress.NewWith(cfg, st, logger, blobs)
	site.Progress = pt
	site.Storage = storage.New(os.Getenv("UNDERMYND_STORAGE_ROOT"))
	if root := os.Getenv("UNDERMYND_STORAGE_ROOT"); root != "" {
		// The folders a re-encode may read and replace files in: the video libraries only.
		for _, f := range []string{"Movies", "Episodes", "Anime"} {
			eng.LibraryRoots = append(eng.LibraryRoots, filepath.Join(root, f))
		}
	}
	site.Storage.Persist(filepath.Join(env.ConfigDir, "storage-scan.json"))
	go site.Storage.Run(ctx, func() time.Duration { return time.Duration(cfg.Get().StorageScanMinutes) * time.Minute })
	go pt.Run(ctx)
	if dbh != nil {
		if n, err := metadata.Renormalize(dbh); err != nil {
			logger.Printf("metadata: renormalising titles: %v", err)
		} else if n > 0 {
			logger.Printf("metadata: %d stored titles compared the new way", n)
		}
		site.Metadata = newMetadata(dbh, cfg, logger)
		go site.Metadata.Run(ctx, func() bool { return cfg.Get().Metadata.TMDBKey != "" })
	}
	eng.AfterPlace = func(path string) {
		pb := cfg.Get().Playback
		if !pb.RefreshAfterPlace || pb.EmbyURL == "" {
			return
		}
		ms := mediaserver.Server{Kind: cfg.Get().Access.Provider, URL: pb.EmbyURL, Key: string(pb.EmbyKey)}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := ms.Updated(ctx, mediaserver.MapPath(filepath.Dir(path), pb.PathFrom, pb.PathTo)); err != nil {
				logger.Printf("media server: telling it about %s: %v", filepath.Base(path), err)
			}
		}()
	}
	rp := rss.New(cfg, eng, logger)
	rp.State = blobs
	site.RSS = rp
	if dbh != nil {
		hc := &http.Client{Timeout: 60 * time.Second}
		site.Shows = &shows.Service{Store: &shows.Store{DB: dbh, Now: time.Now}, Nyaa: shows.Nyaa{HTTP: hc}, AniList: shows.AniList{HTTP: hc},
			HTTP: hc, Cfg: cfg, Engine: eng, Log: logger, Now: time.Now, Grace: 24 * time.Hour, Every: 20 * time.Minute}
		site.Shows.Jobs = eng.Store.List
		eng.FolderTitles = site.Shows.FolderTitles
		rp.Claim = site.Shows.Claims
		go site.Shows.Run(ctx)
		site.Requests = &requests.Service{DB: dbh, TMDB: site.Metadata.TMDB, Meta: site.Metadata, Cfg: cfg, Shows: site.Shows, Log: logger, Now: time.Now,
			Arr: func(category string) *arr.Client {
				c, ok := cfg.Get().Category(category)
				if !ok || c.ArrURL == "" || c.ArrAPIKey == "" {
					return nil
				}
				return arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey))
			}}
		go func() {
			t := time.NewTicker(30 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if cfg.Get().Requests.Native() {
						site.Requests.Refresh(ctx)
					}
				}
			}
		}()
		site.Indexers = &indexers.Service{DB: dbh, HTTP: &http.Client{Timeout: 60 * time.Second}, Engine: eng, Log: logger, Now: time.Now, Gap: 2 * time.Second}
		site.Profiles = &decision.Store{DB: dbh}
		site.Shadow = &shadow.Service{DB: dbh, Cfg: cfg, Profiles: site.Profiles, Search: site.Indexers, Log: logger, Now: time.Now,
			Arr: func(cat string) *arr.Client {
				c, _ := cfg.Get().Category(cat)
				return arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey))
			}}
		go site.Shadow.Run(ctx)
		site.Budget = &scheduler.Budget{DB: dbh, Cfg: cfg, Now: time.Now}
		site.History = &history.Log{DB: dbh, Now: time.Now}
		site.History.Prune()
		site.Movies = &movies.Service{Budget: site.Budget, History: site.History, DB: dbh, Catalog: catalog.New(dbh), Cfg: cfg, Profiles: site.Profiles, Indexers: site.Indexers, Jobs: st,
			Move: pipeline.MoveFile, IsVideo: pipeline.IsVideo, Log: logger, Now: time.Now, Arr: site.Shadow.Arr}
		site.TV = &tv.Service{Budget: site.Budget, History: site.History, DB: dbh, Catalog: catalog.New(dbh), TMDB: site.Metadata.TMDB, Cfg: cfg, Profiles: site.Profiles, Indexers: site.Indexers, Jobs: st,
			Move: pipeline.MoveFile, IsVideo: pipeline.IsVideo, Log: logger, Now: time.Now, Arr: site.Shadow.Arr}
		eng.Native = func(ctx context.Context, j job.Job) (bool, []string, []string, error) {
			if own, placed, left, err := site.Movies.ImportJob(ctx, j); own {
				return own, placed, left, err
			}
			return site.TV.ImportJob(ctx, j)
		}
		go site.TV.Run(ctx)
		site.Trash = &trash.Syncer{Store: site.Profiles, Cfg: cfg, Log: logger,
			Client: &trash.Client{HTTP: &http.Client{Timeout: 2 * time.Minute}, URL: func() string { return cfg.Get().Trash.URL }}}
		go site.Trash.Run(ctx)
		site.Backlog = &scheduler.Backlog{DB: dbh, Cfg: cfg, Budget: site.Budget, Arr: site.Shadow.Arr, Log: logger, Now: time.Now}
		go site.Backlog.Run(ctx)
		if site.Shows != nil {
			site.Shows.Budget = site.Budget
		}
		if site.Requests != nil {
			site.Requests.Movies = site.Movies
			site.Requests.TV = site.TV
		}
		go site.Movies.Run(ctx)
	}
	go rp.Run(ctx)
	go watch.New(cfg, eng, logger).Run(ctx)

	servers := []*http.Server{
		{Addr: env.PublicAddr, Handler: site.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second},
		{Addr: env.MachineAddr, Handler: machine, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second},
	}
	errc := make(chan error, len(servers))
	for _, s := range servers {
		go func(s *http.Server) {
			logger.Printf("listening on %s", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}(s)
	}
	select {
	case <-ctx.Done():
	case err := <-errc:
		logger.Printf("server error: %v", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
}

// openDatabase opens /config/undermynd.db and brings its schema up to date,
// backing it up first when an existing database is about to be migrated.
func openDatabase(logger *log.Logger, configDir string) (*sql.DB, error) {
	path := filepath.Join(configDir, "undermynd.db")
	h, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	cur, err := db.Applied(h)
	if err != nil {
		h.Close()
		return nil, err
	}
	if cur > 0 && cur < db.Version() {
		if p, err := db.Backup(h, filepath.Join(configDir, "backups"), backupsKept, time.Now()); err != nil {
			h.Close()
			return nil, fmt.Errorf("backup before migrating: %w", err)
		} else {
			logger.Printf("database: backed up to %s before migrating from version %d to %d", p, cur, db.Version())
		}
	}
	if err := db.Migrate(h); err != nil {
		h.Close()
		return nil, err
	}
	logger.Printf("database: %s (schema version %d)", path, db.Version())
	return h, nil
}

const backupsKept = 7

// keepBackups writes a backup once a day, keeping the last week.
func keepBackups(logger *log.Logger, h *sql.DB, dir string) {
	for {
		list, _ := db.Backups(dir)
		due := len(list) == 0
		if !due {
			if fi, err := os.Stat(list[len(list)-1]); err != nil || time.Since(fi.ModTime()) > 24*time.Hour {
				due = true
			}
		}
		if due {
			if p, err := db.Backup(h, dir, backupsKept, time.Now()); err != nil {
				logger.Printf("database: %v", err)
			} else {
				logger.Printf("database: backed up to %s", p)
			}
		}
		time.Sleep(time.Hour)
	}
}

// newMetadata builds the metadata service over the database, reading the TMDB
// key and the anime list addresses from the settings each time they are used.
func newMetadata(h *sql.DB, cfg *config.Config, logger *log.Logger) *metadata.Service {
	listURL := func(set, def string) func() string {
		return func() string {
			switch v := strings.TrimSpace(set); v {
			case "":
				return def
			case "off":
				return ""
			}
			return strings.TrimSpace(set)
		}
	}
	return &metadata.Service{
		TMDB: metadata.NewTMDB(h, func() string { return string(cfg.Get().Metadata.TMDBKey) }),
		Lists: metadata.NewLists(h,
			func() string { return listURL(cfg.Get().Metadata.AnimeTitlesURL, metadata.DefaultAnimeTitlesURL)() },
			func() string { return listURL(cfg.Get().Metadata.AnimeIDsURL, metadata.DefaultAnimeIDsURL)() }),
		Catalog: catalog.New(h),
		Log:     logger,
		Now:     time.Now,
	}
}
