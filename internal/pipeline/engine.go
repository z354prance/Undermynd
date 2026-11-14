// Package pipeline runs every job from hand-off to cleanup.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/backend"
	"github.com/z354prance/undermynd/internal/bencode"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/store"
)

type ImportChecker interface {
	Imported(ctx context.Context, downloadID string) (bool, error)
}

type Engine struct {
	Store *store.Store
	Cfg   *config.Config
	Log   *log.Logger
	Now   func() time.Time

	// Override replaces the configured backend (tests).
	Override backend.Backend
	// ArrFor replaces the arr client lookup (tests).
	ArrFor func(config.Category) ImportChecker

	submitMu sync.Mutex
	bmu      sync.Mutex
	bkey     string
	bcache   backend.Backend
}

func New(s *store.Store, c *config.Config, l *log.Logger) *Engine {
	if l == nil {
		l = log.New(io.Discard, "", 0)
	}
	return &Engine{Store: s, Cfg: c, Log: l, Now: time.Now}
}

func (e *Engine) backend() backend.Backend {
	if e.Override != nil {
		return e.Override
	}
	s := e.Cfg.Get()
	key := s.QBit.URL + "|" + s.QBit.Username + "|" + string(s.QBit.Password) + "|" + s.QBit.SaveDir
	e.bmu.Lock()
	defer e.bmu.Unlock()
	if e.bcache == nil || e.bkey != key {
		e.bcache = backend.NewQBit(s.QBit.URL, s.QBit.Username, string(s.QBit.Password), s.QBit.SaveDir)
		e.bkey = key
	}
	return e.bcache
}

func (e *Engine) arrFor(c config.Category) ImportChecker {
	if e.ArrFor != nil {
		return e.ArrFor(c)
	}
	if c.ArrURL == "" || c.Kind == "" || c.Kind == "rss" {
		return nil
	}
	return arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey))
}

// ---------------------------------------------------------------- intake

type SubmitRequest struct {
	Category string
	Magnet   string
	Torrent  []byte
}

// Submit creates (or returns) the job for a grab. The job ID is the infohash,
// so it matches the download ID the arr records.
func (e *Engine) Submit(req SubmitRequest) (job.Job, error) {
	var id, name string
	var size int64
	var err error
	switch {
	case len(req.Torrent) > 0:
		id, name, size, err = bencode.Torrent(req.Torrent)
	case req.Magnet != "":
		id, name, err = bencode.Magnet(req.Magnet)
	default:
		err = errors.New("no torrent or magnet supplied")
	}
	if err != nil {
		return job.Job{}, err
	}
	if name == "" {
		name = id
	}
	e.submitMu.Lock()
	defer e.submitMu.Unlock()
	if cur, ok := e.Store.Get(id); ok && cur.State != job.Cleaned && cur.State != job.Failed {
		return cur, nil // the arr asked again; same job
	}
	now := e.Now()
	j := job.Job{ID: id, Name: name, Category: req.Category, Kind: "torrent", State: job.Received,
		Size: size, Created: now, Updated: now, StateChanged: now, LastProgress: now}
	if c, ok := e.Cfg.Get().Category(req.Category); ok {
		j.Source = c.Kind
	} else {
		j.Source = "unknown"
	}
	if len(req.Torrent) > 0 {
		dir := filepath.Join(e.Cfg.Env.ConfigDir, "torrents")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return job.Job{}, err
		}
		j.TorrentPath = filepath.Join(dir, id+".torrent")
		if err := os.WriteFile(j.TorrentPath, req.Torrent, 0o600); err != nil {
			return job.Job{}, err
		}
	} else {
		j.Magnet = req.Magnet
	}
	j.History = []job.Transition{{At: now, To: job.Received, Reason: "grab received from " + j.Source}}
	if err := e.Store.Put(j); err != nil {
		return job.Job{}, err
	}
	e.Log.Printf("job %s received (%s, category %s)", short(id), name, req.Category)
	return j, nil
}

// ArrDelete handles the arr's delete call for an item.
func (e *Engine) ArrDelete(id string) error {
	_, err := e.Store.Update(id, func(j *job.Job) error {
		j.ArrDeleted = true
		switch j.State {
		case job.Ready, job.Imported, job.Cleaned, job.Failed:
			// the loop cleans up (or already has)
		default:
			j.Move(job.Failed, "cancelled by the arr", e.Now())
		}
		return nil
	})
	return err
}

// ------------------------------------------------------------ admin actions

func (e *Engine) Retry(id string) error {
	_, err := e.Store.Update(id, func(j *job.Job) error {
		if j.State != job.Failed {
			return errors.New("only a failed job can be retried")
		}
		j.FailCleaned, j.ArrDeleted, j.Flag = false, false, ""
		j.RemoteID, j.SavePath, j.StagingDir, j.CompleteDir, j.Files = "", "", "", "", nil
		j.Progress, j.Retries = 0, 0
		j.LastProgress = e.Now()
		j.Move(job.Received, "retried by admin", e.Now())
		return nil
	})
	return err
}

func (e *Engine) Cancel(id string) error {
	_, err := e.Store.Update(id, func(j *job.Job) error {
		if j.State == job.Cleaned {
			return nil
		}
		j.ArrDeleted = true // nothing will be told to the arr; clean up and finish
		if j.State != job.Failed && j.State != job.Imported {
			j.Move(job.Failed, "cancelled by admin", e.Now())
		}
		return nil
	})
	return err
}

func (e *Engine) MarkImported(id string) error {
	_, err := e.Store.Update(id, func(j *job.Job) error {
		if j.State != job.Ready {
			return errors.New("only a ready job can be marked imported")
		}
		j.Flag = ""
		j.Move(job.Imported, "marked imported by admin", e.Now())
		return nil
	})
	return err
}

// ------------------------------------------------------------------ loop

func (e *Engine) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		e.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) Tick(ctx context.Context) {
	for _, j := range e.Store.List() {
		if ctx.Err() != nil {
			return
		}
		// A job keeps moving through consecutive states in one tick, so a finished
		// download is staged and handed back without waiting for the next tick.
		for hops := 0; hops < 8; hops++ {
			if !j.NextRun.IsZero() && e.Now().Before(j.NextRun) {
				break
			}
			e.step(ctx, j)
			nj, ok := e.Store.Get(j.ID)
			if !ok || nj.State == j.State {
				break
			}
			j = nj
		}
	}
}

func (e *Engine) step(ctx context.Context, j job.Job) {
	switch j.State {
	case job.Received:
		e.doSubmit(ctx, j)
	case job.Submitted, job.Downloading:
		e.doDownload(ctx, j)
	case job.Downloaded:
		e.doDownloaded(ctx, j)
	case job.QueuedTranscode, job.Transcoding, job.Verifying:
		e.fail(j, "transcode requested but the Tdarr stage is not enabled in this build")
	case job.Ready:
		e.doReady(ctx, j)
	case job.Imported:
		e.doCleanup(ctx, j)
	case job.Failed:
		e.doFailed(ctx, j)
	}
}

// retry records a transient error and schedules the next attempt, failing the
// job after max attempts.
func (e *Engine) retry(j job.Job, err error, max int, what string) {
	e.Log.Printf("job %s %s: %v", short(j.ID), what, err)
	if j.Attempts+1 >= max {
		e.fail(j, fmt.Sprintf("%s failed: %v", what, err))
		return
	}
	wait := time.Duration(15*(j.Attempts+1)) * time.Second
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.Attempts++
		c.Reason = fmt.Sprintf("%s: %v (retrying)", what, err)
		c.NextRun = e.Now().Add(wait)
		c.Updated = e.Now()
		return nil
	})
}

func (e *Engine) fail(j job.Job, reason string) {
	e.Log.Printf("job %s failed: %s", short(j.ID), reason)
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		if c.State == job.Cleaned {
			return nil
		}
		c.Move(job.Failed, reason, e.Now())
		return nil
	})
}

func (e *Engine) doSubmit(ctx context.Context, j job.Job) {
	rid, save, err := e.backend().Add(ctx, j)
	if err != nil {
		e.retry(j, err, 4, "submit to client")
		return
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.Backend, c.RemoteID, c.SavePath = e.backend().Name(), rid, save
		c.LastProgress = e.Now()
		c.Move(job.Submitted, "sent to "+c.Backend, e.Now())
		return nil
	})
}

func (e *Engine) doDownload(ctx context.Context, j job.Job) {
	it, err := e.backend().Status(ctx, j.RemoteID)
	if err != nil {
		e.retry(j, err, 40, "client status")
		return
	}
	now := e.Now()
	switch it.Status {
	case backend.Missing:
		if j.State == job.Submitted && now.Sub(j.StateChanged) < 2*time.Minute {
			return
		}
		e.fail(j, "the item disappeared from the download client")
	case backend.Errored:
		e.reSubmitOrFail(ctx, j, it.Error)
	case backend.Complete:
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			c.Progress = 1
			c.Move(job.Downloaded, "download finished", now)
			return nil
		})
	default:
		stall := time.Duration(e.Cfg.Get().StallMinutes) * time.Minute
		moved := it.Progress > j.Progress
		if !moved && now.Sub(j.LastProgress) > stall {
			e.reSubmitOrFail(ctx, j, fmt.Sprintf("no progress for %d minutes", int(stall.Minutes())))
			return
		}
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			if moved {
				c.Progress, c.LastProgress = it.Progress, now
			}
			if c.State == job.Submitted {
				c.Move(job.Downloading, "client is downloading", now)
			}
			c.Updated = now
			return nil
		})
	}
}

func (e *Engine) reSubmitOrFail(ctx context.Context, j job.Job, why string) {
	if j.Retries >= 2 {
		e.fail(j, "stalled or errored: "+why)
		return
	}
	_ = e.backend().Remove(ctx, j.RemoteID, true)
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.Retries++
		c.Progress, c.LastProgress = 0, e.Now()
		c.Move(job.Received, "re-submitting after: "+why, e.Now())
		return nil
	})
}

// ---------------------------------------------------- stage and hand back

var mediaExt = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".m2ts": true, ".wmv": true, ".webm": true,
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true, ".wav": true, ".alac": true, ".ape": true, ".wv": true,
	".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true, ".vtt": true, ".sup": true,
}

func wanted(f backend.File) bool {
	if !mediaExt[strings.ToLower(filepath.Ext(f.Path))] {
		return false
	}
	base := strings.ToLower(filepath.Base(f.Path))
	if strings.Contains(base, "sample") && f.Size < 200<<20 {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(filepath.ToSlash(filepath.Dir(f.Path))), "/") {
		if part == "sample" || part == "samples" {
			return false
		}
	}
	return true
}

func (e *Engine) doDownloaded(ctx context.Context, j job.Job) {
	b := e.backend()
	staging := e.Cfg.Env.StagingDir(j.ID)
	if len(j.Files) == 0 {
		files, err := b.Files(ctx, j.RemoteID)
		if err != nil {
			e.retry(j, err, 10, "list files")
			return
		}
		var picked []backend.File
		for _, f := range files {
			if wanted(f) {
				picked = append(picked, f)
			}
		}
		if len(picked) == 0 {
			e.fail(j, "no media files in the download")
			return
		}
		strip := commonTop(picked)
		var rel []string
		for _, f := range picked {
			r := filepath.FromSlash(f.Path)
			if strip != "" {
				r = strings.TrimPrefix(r, strip+string(filepath.Separator))
			}
			src := filepath.Join(j.SavePath, filepath.FromSlash(f.Path))
			dst := filepath.Join(staging, r)
			if err := moveFile(src, dst); err != nil {
				e.retry(j, err, 6, "move to staging")
				return
			}
			st, err := os.Stat(dst)
			if err != nil || (f.Size > 0 && st.Size() != f.Size) {
				e.fail(j, "incomplete download: size mismatch on "+r)
				return
			}
			rel = append(rel, r)
		}
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			c.StagingDir, c.Files, c.Updated = staging, rel, e.Now()
			return nil
		})
		j.StagingDir, j.Files = staging, rel
	}
	// The local copy is verified: remove the client item and everything it left.
	if err := e.removeBackendItem(ctx, j); err != nil {
		e.retry(j, err, 10, "remove client item")
		return
	}
	cat, _ := e.Cfg.Get().Category(j.Category)
	if cat.Transcode {
		e.fail(j, "transcode requested but the Tdarr stage is not enabled in this build")
		return
	}
	e.handBack(j)
}

func commonTop(files []backend.File) string {
	var top string
	for i, f := range files {
		parts := strings.Split(filepath.ToSlash(f.Path), "/")
		if len(parts) < 2 {
			return ""
		}
		if i == 0 {
			top = parts[0]
		} else if parts[0] != top {
			return ""
		}
	}
	return top
}

func (e *Engine) handBack(j job.Job) {
	root := e.Cfg.Env.CompleteRoot(j.Category)
	dest := filepath.Join(root, config.SafeName(j.Name))
	if _, err := os.Stat(dest); err == nil {
		dest += "." + short(j.ID)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		e.retry(j, err, 6, "create complete folder")
		return
	}
	if err := os.Rename(j.StagingDir, dest); err != nil {
		e.retry(j, err, 6, "move to complete")
		return
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.CompleteDir, c.StagingDir = dest, ""
		c.Move(job.Ready, "moved to "+dest, e.Now())
		return nil
	})
	e.Log.Printf("job %s ready at %s", short(j.ID), dest)
}

// ------------------------------------------------------- import and clean

func (e *Engine) doReady(ctx context.Context, j job.Job) {
	now := e.Now()
	if j.ArrDeleted {
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			c.Move(job.Imported, "the arr removed the item after import", now)
			return nil
		})
		return
	}
	s := e.Cfg.Get()
	cat, _ := s.Category(j.Category)
	next := now.Add(time.Duration(s.ReconcileSeconds) * time.Second)
	if chk := e.arrFor(cat); chk != nil {
		ok, err := chk.Imported(ctx, j.ID)
		switch {
		case err != nil:
			e.Log.Printf("job %s import check: %v", short(j.ID), err)
		case ok:
			_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
				c.Move(job.Imported, "import event found in the arr's history", now)
				return nil
			})
			return
		}
	}
	flag := ""
	if h := int(now.Sub(j.StateChanged).Hours()); h >= s.FlagImportHours {
		flag = fmt.Sprintf("waiting for the arr to import for %d hours", h)
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error { c.NextRun, c.Flag = next, flag; return nil })
}

func (e *Engine) removeBackendItem(ctx context.Context, j job.Job) error {
	if j.RemoteID != "" {
		b := e.backend()
		if it, err := b.Status(ctx, j.RemoteID); err != nil {
			return err
		} else if it.Status != backend.Missing {
			if err := b.Remove(ctx, j.RemoteID, true); err != nil {
				return err
			}
			if it, err := b.Status(ctx, j.RemoteID); err != nil {
				return err
			} else if it.Status != backend.Missing {
				return errors.New("the client still lists the item")
			}
		}
	}
	if j.SavePath != "" {
		if err := e.removeTree(j.SavePath, e.Cfg.Get().QBit.SaveDir); err != nil {
			return err
		}
	}
	if j.TorrentPath != "" {
		if err := os.Remove(j.TorrentPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// removeTree deletes p only when it sits inside root, and verifies it is gone.
func (e *Engine) removeTree(p, root string) error {
	if p == "" {
		return nil
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to delete %s: outside %s", p, root)
	}
	if err := os.RemoveAll(p); err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("%s still exists", p)
	}
	return nil
}

func (e *Engine) cleanupFiles(ctx context.Context, j job.Job) error {
	env := e.Cfg.Env
	if err := e.removeTree(j.CompleteDir, filepath.Join(env.DataDir, "complete")); err != nil {
		return err
	}
	if err := e.removeTree(env.StagingDir(j.ID), filepath.Join(env.DataDir, "undermynd")); err != nil {
		return err
	}
	return e.removeBackendItem(ctx, j)
}

func (e *Engine) doCleanup(ctx context.Context, j job.Job) {
	if err := e.cleanupFiles(ctx, j); err != nil {
		e.retry2(j, err)
		return
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.Move(job.Cleaned, "staging, complete and client items confirmed gone", e.Now())
		return nil
	})
	e.Log.Printf("job %s cleaned", short(j.ID))
}

// retry2 backs off cleanup forever: it is retried until it succeeds.
func (e *Engine) retry2(j job.Job, err error) {
	e.Log.Printf("job %s cleanup: %v", short(j.ID), err)
	wait := time.Duration(30*(j.Attempts+1)) * time.Second
	if wait > 10*time.Minute {
		wait = 10 * time.Minute
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.Attempts++
		c.Reason = "cleanup: " + err.Error() + " (retrying)"
		c.NextRun = e.Now().Add(wait)
		c.Updated = e.Now()
		return nil
	})
}

func (e *Engine) doFailed(ctx context.Context, j job.Job) {
	if !j.FailCleaned {
		if err := e.cleanupFiles(ctx, j); err != nil {
			e.retry2(j, err)
			return
		}
		j.FailCleaned = true
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error { c.FailCleaned, c.Attempts, c.NextRun = true, 0, time.Time{}; return nil })
	}
	// The arr sees the failure (state "error") until it handles it and calls delete.
	if j.ArrDeleted || j.Source == "rss" {
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			c.Move(job.Cleaned, "failed job cleaned up; the arr has dealt with it", e.Now())
			return nil
		})
	}
}

// ---------------------------------------------------------------- files

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	var le *os.LinkError
	if !(errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV)) {
		if _, serr := os.Stat(dst); serr == nil {
			if _, serr2 := os.Stat(src); errors.Is(serr2, fs.ErrNotExist) {
				return nil // an earlier attempt already moved it
			}
		}
		return err
	}
	// Different filesystems: copy, verify the size, then remove the source.
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if st, err := os.Stat(src); err != nil || st.Size() != n {
		return errors.New("copy size mismatch")
	}
	return os.Remove(src)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
