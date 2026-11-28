package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/z354prance/undermynd/internal/attention"
	"github.com/z354prance/undermynd/internal/gpu"
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

	"github.com/z354prance/undermynd/internal/auth"
	"github.com/z354prance/undermynd/internal/bt"
	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/logbuf"
	"github.com/z354prance/undermynd/internal/mediaserver"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/progress"
	"github.com/z354prance/undermynd/internal/qbitapi"
	"github.com/z354prance/undermynd/internal/rss"
	"github.com/z354prance/undermynd/internal/sabapi"
	"github.com/z354prance/undermynd/internal/shows"
	"github.com/z354prance/undermynd/internal/state"
	"github.com/z354prance/undermynd/internal/storage"
	"github.com/z354prance/undermynd/internal/store"
	"github.com/z354prance/undermynd/internal/usenet"
	"github.com/z354prance/undermynd/internal/vpn"
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

// envAliases lets HYVEMYND_* variables stand for the UNDERMYND_* ones the
// code reads; an UNDERMYND_* variable that is set wins, so existing
// containers keep working unchanged.
func envAliases() {
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, "HYVEMYND_"); ok {
			if _, set := os.LookupEnv("UNDERMYND_" + name); !set {
				os.Setenv("UNDERMYND_"+name, v)
			}
		}
	}
}

func main() {
	envAliases()
	logs := logbuf.New(2000)
	logger := log.New(io.MultiWriter(os.Stdout, logs), "hyvemynd ", log.LstdFlags)
	env := config.LoadEnv()
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck(env.PublicAddr)
		return
	}
	// Graphics cards (for Tdarr's load): their counters are opened while still
	// root, since dropping to PUID also drops --cap-add=PERFMON.
	var gpus *gpu.Monitor
	if os.Getenv("HYVEMYND_GPU") != "off" {
		gpus = &gpu.Monitor{Log: logger}
		gpus.Prepare()
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
	eng.Blobs = blobs
	qb := qbitapi.New(st, eng, cfg, logger)
	sab := sabapi.New(st, eng, cfg, logger)
	machine := http.NewServeMux()
	machine.Handle("/api", sab.Handler())
	machine.Handle("/sabnzbd/", sab.Handler())
	machine.Handle("/", qb.Handler())
	site := web.New(st, eng, cfg, au, logger)
	site.Blobs = blobs
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
	if site.Access != nil {
		go site.Access.Run(ctx) // switches on accounts once Emby confirms their Connect link
	}
	// The PIA tunnel and HyveMynd's own torrent client, which only uses it.
	tunnel := &vpn.Tunnel{Cfg: func() config.VPN { return cfg.Get().VPN }, Log: logger, State: blobs}
	go tunnel.Run(ctx)
	local := &bt.Client{Net: tunnel, Dir: filepath.Join(env.Work(), "torrents"), State: blobs, Log: logger,
		Limits: func() (int, int) { sp := cfg.Get().Speed; return sp.TorrentDownKB, sp.TorrentUpKB }}
	go local.Run(ctx)
	eng.Local = local
	// HyveMynd's own usenet downloader, for the owner's providers.
	news := &usenet.Downloader{Cfg: func() config.Usenet { return cfg.Get().Usenet }, Dir: filepath.Join(env.Work(), "usenet"), State: blobs, Log: logger,
		Limit: func() int { return cfg.Get().Speed.UsenetKB }}
	go news.Run(ctx)
	eng.News = news
	site.News = news
	site.VPN = &web.VPN{Tunnel: tunnel, Client: local}
	site.Attention = &attention.Service{Cfg: cfg, Log: logger, State: blobs}
	go site.Attention.Run(ctx)
	if gpus != nil {
		site.GPU = gpus
		go gpus.Run(ctx)
	}
	if dbh != nil {
		if n, err := metadata.Renormalize(dbh); err != nil {
			logger.Printf("metadata: renormalising titles: %v", err)
		} else if n > 0 {
			logger.Printf("metadata: %d stored titles compared the new way", n)
		}
		site.Metadata = newMetadata(dbh, cfg, logger)
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
	}
	go site.RunInsights(ctx)
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
