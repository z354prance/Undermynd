package usenet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/z354prance/undermynd/internal/backend"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/guard"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/state"
)

// Name is the backend name jobs record.
const Name = "usenet"

// Stages of a download.
const (
	Queued      = "queued"
	Downloading = "downloading"
	Repairing   = "repairing"
	Unpacking   = "unpacking"
	Done        = "done"
	Failed      = "failed"
)

// task is one NZB.
type task struct {
	ID       string    `json:"id"`
	Save     string    `json:"save"` // the job's folder
	Stage    string    `json:"stage"`
	Error    string    `json:"error,omitempty"`
	Added    time.Time `json:"added"`
	Total    int64     `json:"total"`
	Password string    `json:"password,omitempty"`
	Paused   bool      `json:"paused,omitempty"`
	Log      []LogLine `json:"log,omitempty"` // what happened, newest last

	work       atomic.Int64 // bytes written by repair/unpack
	workTotal  atomic.Int64
	lastWork   atomic.Int64 // unix nanoseconds of the last progress
	gen        int          // bumped when a stuck run is given up
	done       atomic.Int64 // bytes fetched
	fromMain   atomic.Int64 // articles from main providers
	fromBackup atomic.Int64 // articles from backups
	cancel     context.CancelFunc
}

// LogLine is one entry of a download's log.
type LogLine struct {
	At  time.Time `json:"at"`
	Msg string    `json:"msg"`
}

const maxLog = 200

// note adds a line to the task's log (and HyveMynd's log). Callers must not
// hold d.mu.
func (d *Downloader) note(t *task, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	d.logf("job %s: %s", t.ID, msg)
	d.mu.Lock()
	t.Log = append(t.Log, LogLine{At: time.Now(), Msg: msg})
	if len(t.Log) > maxLog {
		t.Log = append(t.Log[:0:0], t.Log[len(t.Log)-maxLog:]...)
	}
	d.mu.Unlock()
}

// taskReporter is a task's reporter for the post-download steps.
type taskReporter struct {
	d *Downloader
	t *task
}

func (r taskReporter) Note(format string, a ...any) { r.d.note(r.t, format, a...) }
func (r taskReporter) Work(n int64) {
	r.t.work.Add(n)
	r.t.lastWork.Store(time.Now().UnixNano())
}

// Log returns a download's log.
func (d *Downloader) LogOf(id string) []LogLine {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	if t := d.tasks[id]; t != nil {
		return append([]LogLine(nil), t.Log...)
	}
	return nil
}

// progress is what a task has fetched, kept in its folder so a restart
// carries on.
type progress struct {
	Done    map[string]bool `json:"done"` // message ids fetched (or given up on)
	Missing int             `json:"missing"`
	Damaged int             `json:"damaged"`
	Names   map[int]string  `json:"names"`  // file index -> name used on disk
	Repair  bool            `json:"repair"` // repair volumes wanted
	// LostBytes is the size of the articles missing everywhere or damaged.
	LostBytes int64 `json:"lostBytes,omitempty"`
	// Lost lists those articles, and Tried the providers asked for them, so
	// they are asked again when a provider is added.
	Lost  map[string]bool `json:"lost,omitempty"`
	Tried []string        `json:"tried,omitempty"`
}

const progressFile = ".hyvemynd-usenet.json"
const nzbFile = ".hyvemynd.nzb"

// Downloader runs NZBs one at a time, at full speed.
type Downloader struct {
	Cfg   func() config.Usenet
	Dir   string // one folder per job under here
	State state.Blobs
	Log   *log.Logger
	Dial  Dialer // nil: direct connections
	// RetryWait is the pause when no provider answers (default a minute).
	RetryWait time.Duration
	// Limit gives the speed limit in KB a second (0: none).
	Limit func() int
	// UnpackStall and RepairStall: how long unpacking or a par2 repair may
	// write nothing before it is given up (defaults 10 minutes, an hour).
	UnpackStall, RepairStall time.Duration

	mu      sync.Mutex
	tasks   map[string]*task
	loaded  bool
	wake    chan struct{}
	pools   []*pool
	poolKey string
	speed   atomic.Int64 // bytes in the last second
	once    sync.Once
	postSem chan struct{}
	lim     *rate.Limiter
}

const stateName = "usenet.json"

func (d *Downloader) init() {
	d.once.Do(func() {
		d.wake = make(chan struct{}, 1)
		d.postSem = make(chan struct{}, 2) // two repairs or unpacks at once
		d.lim = rate.NewLimiter(rate.Inf, 16<<20)
	})
}

// throttle waits for n bytes of the speed limit.
func (d *Downloader) throttle(ctx context.Context, n int) {
	d.init()
	kb := 0
	if d.Limit != nil {
		kb = d.Limit()
	}
	if kb <= 0 {
		d.lim.SetLimit(rate.Inf)
		return
	}
	burst := max(kb<<10, 64<<10) // one second's worth
	if d.lim.Limit() != rate.Limit(kb<<10) {
		d.lim.SetLimit(rate.Limit(kb << 10))
		d.lim.SetBurst(burst)
	}
	for n > 0 {
		k := min(n, burst)
		if d.lim.WaitN(ctx, k) != nil {
			return
		}
		n -= k
	}
}

func (d *Downloader) logf(format string, a ...any) {
	if d.Log != nil {
		d.Log.Printf("usenet: "+format, a...)
	}
}

func (d *Downloader) load() {
	if d.loaded {
		return
	}
	d.loaded = true
	d.tasks = map[string]*task{}
	if d.State == nil {
		return
	}
	if b, ok, _ := d.State.Load(stateName); ok {
		var list []*task
		_ = json.Unmarshal(b, &list)
		for _, t := range list {
			if t.Stage == Downloading || t.Stage == Repairing || t.Stage == Unpacking {
				t.Stage = Queued // carry on after a restart
			}
			d.tasks[t.ID] = t
		}
	}
}

func (d *Downloader) save() {
	if d.State == nil {
		return
	}
	list := make([]*task, 0, len(d.tasks))
	for _, t := range d.tasks {
		list = append(list, t)
	}
	b, _ := json.Marshal(list)
	_ = d.State.Save(stateName, b)
}

func (d *Downloader) poke() {
	d.init()
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Name implements backend.Backend.
func (d *Downloader) Name() string { return Name }

// Ready says whether a provider is set up.
func (d *Downloader) Ready() bool { return d.Cfg().Ready() }

// Add implements backend.Backend.
func (d *Downloader) Add(ctx context.Context, j job.Job) (string, string, error) {
	if !d.Ready() {
		return "", "", backend.Permanent{Msg: "no usenet provider is set up for the built-in downloader"}
	}
	if j.NZBPath == "" {
		return "", "", backend.Permanent{Msg: "this job has no NZB"}
	}
	raw, err := os.ReadFile(j.NZBPath)
	if err != nil {
		return "", "", err
	}
	nzb, err := ParseNZB(bytes.NewReader(raw))
	if err != nil {
		return "", "", backend.Permanent{Msg: err.Error()}
	}
	for _, f := range nzb.Files {
		if why := guard.Name(f.Name); why != "" {
			return "", "", backend.Permanent{Msg: "blocked: " + why}
		}
	}
	save := filepath.Join(d.Dir, j.ID)
	os.RemoveAll(save) // a fresh start for a re-submitted job
	if err := os.MkdirAll(save, 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(save, nzbFile), raw, 0o644); err != nil {
		return "", "", err
	}
	t := &task{ID: j.ID, Save: save, Stage: Queued, Added: time.Now(), Password: nzb.Password,
		Log: []LogLine{{At: time.Now(), Msg: fmt.Sprintf("added: %d files in the NZB", len(nzb.Files))}}}
	for _, f := range nzb.Files {
		if !isRepairVolume(f.Name) {
			t.Total += f.Bytes
		}
	}
	d.mu.Lock()
	d.load()
	if old := d.tasks[j.ID]; old != nil && old.cancel != nil {
		old.cancel()
	}
	d.tasks[j.ID] = t
	d.save()
	d.mu.Unlock()
	d.poke()
	return j.ID, save, nil
}

// Status implements backend.Backend. Waiting in the queue, repairing and
// unpacking answer ErrBusy with what is happening, so the pipeline does not
// count them as a stalled download.
func (d *Downloader) Status(ctx context.Context, id string) (backend.Item, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	t := d.tasks[id]
	if t == nil {
		return backend.Item{Status: backend.Missing}, nil
	}
	switch t.Stage {
	case Done:
		return backend.Item{Status: backend.Complete, Progress: 1}, nil
	case Failed:
		return backend.Item{Status: backend.Errored, Error: t.Error}, nil
	case Queued:
		if t.Paused {
			return backend.Item{}, fmt.Errorf("%w: paused", backend.ErrBusy)
		}
		if t.Error != "" {
			return backend.Item{}, fmt.Errorf("%w: %s", backend.ErrBusy, t.Error)
		}
		return backend.Item{}, fmt.Errorf("%w: waiting for the usenet download before it", backend.ErrBusy)
	case Repairing:
		return backend.Item{}, fmt.Errorf("%w: repairing with par2%s", backend.ErrBusy, workNote(t))
	case Unpacking:
		return backend.Item{}, fmt.Errorf("%w: unpacking%s", backend.ErrBusy, workNote(t))
	}
	if t.Error != "" {
		return backend.Item{}, fmt.Errorf("%w: %s", backend.ErrBusy, t.Error)
	}
	p := 0.0
	if t.Total > 0 {
		p = float64(t.done.Load()) / float64(t.Total)
		if p > 0.99 {
			p = 0.99
		}
	}
	return backend.Item{Status: backend.Downloading, Progress: p}, nil
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	default:
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	}
}

// workNote describes post-download progress: " 1.2 of 3.0 GB".
func workNote(t *task) string {
	if t.lastWork.Load() == 0 {
		return " (waiting for another unpack to finish)"
	}
	w, tot := t.work.Load(), t.workTotal.Load()
	if w <= 0 {
		return ""
	}
	gb := func(n int64) string { return fmt.Sprintf("%.1f", float64(n)/(1<<30)) }
	if tot > 0 {
		return " " + gb(min(w, tot)) + " of " + gb(tot) + " GB"
	}
	return " " + gb(w) + " GB so far"
}

// Files implements backend.Backend.
func (d *Downloader) Files(ctx context.Context, id string) ([]backend.File, error) {
	d.mu.Lock()
	d.load()
	t := d.tasks[id]
	d.mu.Unlock()
	if t == nil {
		return nil, backend.Permanent{Msg: "the usenet downloader has no such download"}
	}
	var out []backend.File
	err := filepath.WalkDir(t.Save, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() || strings.HasPrefix(de.Name(), ".hyvemynd") {
			return err
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(t.Save, p)
		out = append(out, backend.File{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	return out, err
}

// Remove implements backend.Backend.
func (d *Downloader) Remove(ctx context.Context, id string, deleteFiles bool) error {
	d.mu.Lock()
	d.load()
	t := d.tasks[id]
	delete(d.tasks, id)
	d.save()
	d.mu.Unlock()
	if t != nil && t.cancel != nil {
		t.cancel()
	}
	if deleteFiles && id != "" {
		dir := filepath.Join(d.Dir, id)
		if filepath.Dir(dir) == filepath.Clean(d.Dir) {
			return os.RemoveAll(dir)
		}
	}
	return nil
}

// Pause holds a download: it stops if running and is skipped until Resume.
func (d *Downloader) Pause(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	t := d.tasks[id]
	if t == nil || (t.Stage != Queued && t.Stage != Downloading) {
		return errors.New("only a waiting or downloading usenet download can be paused")
	}
	t.Paused = true
	t.Log = append(t.Log, LogLine{At: time.Now(), Msg: "paused"})
	if t.Stage == Downloading && t.cancel != nil {
		t.Stage = Queued
		t.cancel() // what was fetched is kept; it carries on from there
	}
	d.save()
	return nil
}

// Resume lets a paused download carry on.
func (d *Downloader) Resume(id string) error {
	d.mu.Lock()
	t := d.tasks[id]
	if t == nil {
		d.mu.Unlock()
		return errors.New("no such usenet download")
	}
	t.Paused = false
	t.Log = append(t.Log, LogLine{At: time.Now(), Msg: "resumed"})
	d.save()
	d.mu.Unlock()
	d.poke()
	return nil
}

// Top puts a waiting download first in the queue.
func (d *Downloader) Top(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	t := d.tasks[id]
	if t == nil || t.Stage != Queued {
		return errors.New("only a waiting usenet download can be moved")
	}
	first := t.Added
	for _, o := range d.tasks {
		if o.Added.Before(first) {
			first = o.Added
		}
	}
	t.Added = first.Add(-time.Second)
	d.save()
	return nil
}

// Detail is one download as the admin sees it.
type Detail struct {
	Stage      string  `json:"stage"`
	Progress   float64 `json:"progress"`
	Speed      int64   `json:"speed"`              // while it is the one downloading
	Position   int     `json:"position,omitempty"` // in the waiting queue, from 1
	Paused     bool    `json:"paused"`
	Size       int64   `json:"size"`
	Note       string  `json:"note,omitempty"`
	FromMain   int64   `json:"fromMain"`
	FromBackup int64   `json:"fromBackup"`
}

// Details reports every download the downloader has.
func (d *Downloader) Details() map[string]Detail {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	var q []*task
	out := map[string]Detail{}
	for id, t := range d.tasks {
		x := Detail{Stage: t.Stage, Paused: t.Paused, Size: t.Total, Note: t.Error, FromMain: t.fromMain.Load(), FromBackup: t.fromBackup.Load()}
		if t.Total > 0 {
			x.Progress = min(1, float64(t.done.Load())/float64(t.Total))
		}
		if t.Stage == Downloading {
			x.Speed = d.speed.Load()
		}
		if t.Stage == Queued && !t.Paused {
			q = append(q, t)
		}
		out[id] = x
	}
	sort.Slice(q, func(i, j int) bool { return q[i].Added.Before(q[j].Added) })
	for i, t := range q {
		x := out[t.ID]
		x.Position = i + 1
		out[t.ID] = x
	}
	return out
}

// Stats is what the admin shows.
type Stats struct {
	Active   string       `json:"active,omitempty"` // job id
	Stage    string       `json:"stage,omitempty"`
	Progress float64      `json:"progress"`
	Queued   int          `json:"queued"`
	Speed    int64        `json:"speed"` // bytes a second
	Servers  []ServerStat `json:"servers"`
}

// ServerStat is one provider's state.
type ServerStat struct {
	Name    string `json:"name"`
	Problem string `json:"problem,omitempty"`
}

// Stats reports what the downloader is doing.
func (d *Downloader) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	s := Stats{Speed: d.speed.Load()}
	for _, t := range d.tasks {
		switch t.Stage {
		case Queued:
			s.Queued++
		case Downloading, Repairing, Unpacking:
			s.Active, s.Stage = t.ID, t.Stage
			if t.Total > 0 {
				s.Progress = float64(t.done.Load()) / float64(t.Total)
			}
		}
	}
	for _, p := range d.pools {
		s.Servers = append(s.Servers, ServerStat{Name: p.srv.Label(), Problem: p.problem()})
	}
	return s
}

// Run works through the queue until ctx ends.
func (d *Downloader) Run(ctx context.Context) {
	d.init()
	go d.meter(ctx)
	for {
		t := d.next()
		if t == nil {
			select {
			case <-ctx.Done():
				d.closePools()
				return
			case <-d.wake:
			case <-time.After(30 * time.Second):
			}
			continue
		}
		tctx, cancel := context.WithCancel(ctx)
		d.mu.Lock()
		t.cancel = cancel
		d.mu.Unlock()
		pr, err := d.fetchSafe(tctx, t)
		removed := tctx.Err() != nil && ctx.Err() == nil
		cancel()
		if err == nil && !removed {
			// Downloaded: repair and unpacking happen on their own, so the
			// next download starts now and a slow unpack holds nothing up.
			d.mu.Lock()
			if pr.Missing+pr.Damaged > 0 {
				t.Stage = Repairing
			} else {
				t.Stage = Unpacking
			}
			t.work.Store(0)
			t.workTotal.Store(0)
			d.save()
			d.mu.Unlock()
			go d.postRun(ctx, t, pr)
			continue
		}
		d.mu.Lock()
		if _, still := d.tasks[t.ID]; still {
			switch {
			case ctx.Err() != nil:
				t.Stage = Queued // shutting down: carry on next time
			case removed:
				// removed, paused or replaced while running
			case errors.Is(err, errAllDown):
				// The providers are unreachable: wait, do not fail the job.
				t.Stage, t.Error = Queued, err.Error()
			default:
				t.Stage, t.Error = Failed, err.Error()
			}
			d.save()
		}
		d.mu.Unlock()
		if !removed && ctx.Err() == nil {
			if errors.Is(err, errAllDown) {
				d.note(t, "%v; trying again in a minute", err)
			} else {
				d.note(t, "failed: %v", err)
			}
		}
		if ctx.Err() != nil {
			d.closePools()
			return
		}
		if errors.Is(err, errAllDown) {
			select {
			case <-ctx.Done():
				d.closePools()
				return
			case <-time.After(d.retryWait()):
			}
		}
	}
}

// postRun repairs and unpacks a downloaded task, one task at a time, and
// gives up when the work stops making progress, so nothing waits forever.
func (d *Downloader) postRun(ctx context.Context, t *task, pr *progress) {
	t.lastWork.Store(0) // "waiting its turn" until a slot frees
	select {
	case d.postSem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-d.postSem }()
	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	d.mu.Lock()
	gen, still := t.gen, d.tasks[t.ID] == t
	t.cancel = pcancel // Remove stops the unpack too
	d.mu.Unlock()
	if !still {
		return
	}
	t.lastWork.Store(time.Now().UnixNano())
	res := make(chan error, 1)
	go func() { res <- d.postSafe(pctx, t, pr) }()
	tick := time.NewTicker(d.watchEvery())
	defer tick.Stop()
	var err error
	stalled := false
wait:
	for {
		select {
		case err = <-res:
			break wait
		case <-ctx.Done():
			return // shutting down: done again after the restart
		case <-tick.C:
			d.mu.Lock()
			st := t.Stage
			d.mu.Unlock()
			limit, what := d.stall(Unpacking), "unpacking"
			if st == Repairing {
				limit, what = d.stall(Repairing), "the par2 repair"
			}
			if idle := time.Since(time.Unix(0, t.lastWork.Load())); idle > limit {
				err = fmt.Errorf("%s stopped making progress (nothing written for %s); given up so the queue can carry on", what, idle.Round(time.Minute))
				stalled = true
				pcancel() // stops 7-Zip
				break wait
			}
		}
	}
	d.mu.Lock()
	mine := d.tasks[t.ID] == t && t.gen == gen
	if mine {
		if err == nil {
			t.Stage, t.Error = Done, ""
		} else {
			t.Stage, t.Error = Failed, err.Error()
		}
		if stalled {
			t.gen++ // whatever the stuck work does later is ignored
		}
		d.save()
	}
	d.mu.Unlock()
	if !mine {
		return
	}
	if err == nil {
		d.note(t, "finished")
	} else {
		d.note(t, "failed: %v", err)
	}
}

func (d *Downloader) stall(stage string) time.Duration {
	if stage == Repairing {
		if d.RepairStall > 0 {
			return d.RepairStall
		}
		return time.Hour
	}
	if d.UnpackStall > 0 {
		return d.UnpackStall
	}
	return 10 * time.Minute
}

func (d *Downloader) watchEvery() time.Duration {
	w := d.stall(Unpacking) / 4
	if w > 30*time.Second {
		w = 30 * time.Second
	}
	return w
}

// RetryWait is how long to wait when no provider answers. Default a minute.
func (d *Downloader) retryWait() time.Duration {
	if d.RetryWait > 0 {
		return d.RetryWait
	}
	return time.Minute
}

// meter keeps the speed figure.
func (d *Downloader) meter(ctx context.Context) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	var last int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		var now int64
		d.mu.Lock()
		for _, t := range d.tasks {
			now += t.done.Load()
		}
		d.mu.Unlock()
		if now >= last {
			d.speed.Store(now - last)
		} else {
			d.speed.Store(0)
		}
		last = now
	}
}

// next picks the oldest queued task and marks it downloading.
func (d *Downloader) next() *task {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	var q []*task
	for _, t := range d.tasks {
		if t.Stage == Queued && !t.Paused {
			q = append(q, t)
		}
	}
	if len(q) == 0 {
		return nil
	}
	sort.Slice(q, func(i, j int) bool { return q[i].Added.Before(q[j].Added) })
	q[0].Stage = Downloading // Error stays until an article arrives
	d.save()
	return q[0]
}

func (d *Downloader) setStage(t *task, stage string) {
	d.mu.Lock()
	t.Stage = stage
	d.save()
	d.mu.Unlock()
}

// providers returns the connection pools, by priority, rebuilt when the
// settings change.
func (d *Downloader) providers() [][]*pool {
	cfg := d.Cfg()
	key, _ := json.Marshal(cfg.Servers)
	d.mu.Lock()
	defer d.mu.Unlock()
	if string(key) != d.poolKey {
		for _, p := range d.pools {
			p.closeAll()
		}
		d.pools, d.poolKey = nil, string(key)
		for _, s := range cfg.Servers {
			if s.On && s.Host != "" {
				d.pools = append(d.pools, newPool(s, d.Dial))
			}
		}
	}
	byPrio := map[int][]*pool{}
	var prios []int
	for _, p := range d.pools {
		if _, ok := byPrio[p.srv.Priority]; !ok {
			prios = append(prios, p.srv.Priority)
		}
		byPrio[p.srv.Priority] = append(byPrio[p.srv.Priority], p)
	}
	sort.Ints(prios)
	out := make([][]*pool, 0, len(prios))
	for _, pr := range prios {
		out = append(out, byPrio[pr])
	}
	return out
}

func (d *Downloader) closePools() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.pools {
		p.closeAll()
	}
}

// errAllDown means no provider could be reached for an article.
var errAllDown = errors.New("no usenet provider could be reached")

// fetch gets one article from the providers: main ones first (spread
// across them), then each backup level in turn. A provider that lacks it is
// skipped; a broken connection is retried once.
func (d *Downloader) fetchArticle(ctx context.Context, tiers [][]*pool, id string, turn int) ([]byte, int, error) {
	reached := false
	for ti, tier := range tiers {
		for k := range tier {
			p := tier[(turn+k)%len(tier)]
			for try := 0; try < 2; try++ {
				c, err := p.get(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return nil, 0, ctx.Err()
					}
					break // this provider is down: next one
				}
				var buf bytes.Buffer
				err = c.body(id, &buf)
				if err == nil {
					p.put(c, true)
					return buf.Bytes(), ti, nil
				}
				if errors.Is(err, ErrNoArticle) {
					p.put(c, true)
					reached = true
					break
				}
				p.put(c, false) // broken connection: drop it and retry
			}
		}
	}
	if !reached {
		return nil, 0, errAllDown
	}
	return nil, 0, ErrNoArticle
}

type work struct {
	file int
	seg  Segment
	turn int
}

// fetchSafe and postSafe turn a panic into a failed download, so a fault
// here never takes HyveMynd down.
func (d *Downloader) fetchSafe(ctx context.Context, t *task) (pr *progress, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	return d.fetch(ctx, t)
}

func (d *Downloader) postSafe(ctx context.Context, t *task, pr *progress) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	return d.post(ctx, t, pr)
}

// fetch downloads a task's articles (and the repair volumes when needed).
func (d *Downloader) fetch(ctx context.Context, t *task) (*progress, error) {
	raw, err := os.ReadFile(filepath.Join(t.Save, nzbFile))
	if err != nil {
		return nil, fmt.Errorf("the NZB is gone: %w", err)
	}
	nzb, err := ParseNZB(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	pr := d.loadProgress(t)
	// Names on disk: the subject's, or file-N until yEnc tells us.
	used := map[string]bool{}
	for i, f := range nzb.Files {
		n := pr.Names[i]
		if n == "" {
			n = f.Name
			if n == "" || used[n] {
				n = fmt.Sprintf("file-%03d", i+1)
			}
			pr.Names[i] = n
		}
		used[n] = true
	}
	var already, total int64
	var files int
	for i, f := range nzb.Files {
		if isRepairVolume(pr.Names[i]) && !pr.Repair {
			continue
		}
		files++
		for _, s := range f.Segments {
			total += s.Bytes
			if pr.Done[s.ID] {
				already += s.Bytes
			}
		}
	}
	var names []string
	for _, tier := range d.providers() {
		for _, p := range tier {
			names = append(names, p.srv.Label())
		}
	}
	// A provider added since articles went missing may have them.
	if added := newProviders(names, pr.Tried); len(pr.Lost) > 0 && len(added) > 0 {
		var n int
		for i, f := range nzb.Files {
			if _, err := os.Stat(filepath.Join(t.Save, pr.Names[i])); err != nil {
				continue // renamed or gone since: leave it to par2
			}
			for _, s := range f.Segments {
				if pr.Lost[s.ID] {
					delete(pr.Lost, s.ID)
					delete(pr.Done, s.ID)
					already -= s.Bytes
					n++
				}
			}
		}
		if n > 0 {
			// Counted again as they are fetched.
			pr.Missing, pr.Damaged, pr.LostBytes = 0, 0, 0
			for range pr.Lost {
				pr.Missing++
			}
			for _, f := range nzb.Files {
				for _, s := range f.Segments {
					if pr.Lost[s.ID] {
						pr.LostBytes += s.Bytes
					}
				}
			}
			d.note(t, "asking %s for the %d articles the other providers lacked", strings.Join(added, ", "), n)
		}
	}
	if len(names) > 0 {
		pr.Tried = names
	}
	t.done.Store(already)
	if already > 0 {
		d.note(t, "carrying on: %d of %d MB already here; %d files; providers %s", already>>20, total>>20, files, strings.Join(names, ", "))
	} else {
		d.note(t, "downloading %d files, %d MB, from %s", files, total>>20, strings.Join(names, ", "))
	}
	repairWanted := func(i int) bool { return !isRepairVolume(pr.Names[i]) || pr.Repair }
	if err := d.download(ctx, t, nzb, pr, repairWanted); err != nil {
		return nil, err
	}
	d.note(t, "downloaded: %d articles missing everywhere, %d damaged, %d from main providers, %d from backups",
		pr.Missing, pr.Damaged, t.fromMain.Load(), t.fromBackup.Load())
	if pr.Missing+pr.Damaged > 0 {
		var extra, segs, segBytes int64
		for i, f := range nzb.Files {
			if isRepairVolume(pr.Names[i]) {
				extra += f.Bytes
				continue
			}
			for _, s := range f.Segments {
				segs++
				segBytes += s.Bytes
			}
		}
		lost := pr.LostBytes
		if lost == 0 && segs > 0 { // kept by an older version: estimate
			lost = int64(pr.Missing+pr.Damaged) * (segBytes / segs)
		}
		// par2 can rebuild at most as much as its repair volumes hold: when
		// more is gone, say so now instead of trying for nothing.
		if extra == 0 {
			return nil, fmt.Errorf("%d articles are missing or damaged and the release has no par2 repair files; another release is needed", pr.Missing+pr.Damaged)
		}
		if lost > extra {
			return nil, fmt.Errorf("too much of this release is gone from your providers (%s missing or damaged; its repair files can rebuild at most %s); another release is needed", sizeText(lost), sizeText(extra))
		}
	}
	if pr.Missing+pr.Damaged > 0 && !pr.Repair {
		var extra int64
		for i, f := range nzb.Files {
			if isRepairVolume(pr.Names[i]) {
				extra += f.Bytes
			}
		}
		pr.Repair = true
		d.saveProgress(t, pr)
		d.mu.Lock()
		t.Total += extra
		d.mu.Unlock()
		d.note(t, "fetching the par2 repair volumes (%d MB)", extra>>20)
		if err := d.download(ctx, t, nzb, pr, func(i int) bool { return isRepairVolume(pr.Names[i]) }); err != nil {
			return nil, err
		}
	}
	return pr, nil
}

// unpackFn is unpack; tests replace it.
var unpackFn = unpack

// post restores names, repairs when needed and unpacks.
func (d *Downloader) post(ctx context.Context, t *task, pr *progress) error {
	r := taskReporter{d, t}
	if n := restoreNames(t.Save); n > 0 {
		d.note(t, "put back the real names of %d scrambled file(s) from the par2 set", n)
	}
	if pr.Missing+pr.Damaged > 0 {
		d.setStage(t, Repairing)
		var size int64
		ents, _ := os.ReadDir(t.Save)
		for _, e := range ents {
			if info, err := e.Info(); err == nil && !e.IsDir() {
				size += info.Size()
			}
		}
		t.work.Store(0)
		t.workTotal.Store(size)
		if err := repair(ctx, t.Save, r); err != nil {
			return err
		}
	}
	d.setStage(t, Unpacking)
	t.work.Store(0)
	t.workTotal.Store(archiveBytes(t.Save))
	t.lastWork.Store(time.Now().UnixNano())
	if n, err := unpackFn(ctx, t.Save, t.Password, r); err != nil {
		if errors.Is(err, errPassword) {
			return errors.New("the release is password protected and the NZB gives no password")
		}
		return err
	} else if n == 0 {
		d.note(t, "nothing to unpack")
	}
	// Leave only what was released: no par2, NZB or progress files.
	ents, _ := os.ReadDir(t.Save)
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if strings.HasSuffix(n, ".par2") || strings.HasSuffix(n, ".sfv") || strings.HasPrefix(n, ".hyvemynd") {
			os.Remove(filepath.Join(t.Save, e.Name()))
		}
	}
	return nil
}

// download fetches the wanted files' remaining articles.
func (d *Downloader) download(ctx context.Context, t *task, nzb NZB, pr *progress, want func(int) bool) error {
	tiers := d.providers()
	if len(tiers) == 0 {
		return errors.New("no usenet provider is switched on")
	}
	workers := 0
	for _, tier := range tiers {
		for _, p := range tier {
			workers += cap(p.slot)
		}
	}
	files := make([]*os.File, len(nzb.Files))
	defer func() {
		for _, f := range files {
			if f != nil {
				f.Close()
			}
		}
	}()
	var fmu sync.Mutex
	open := func(i int, yname string, size int64) (*os.File, error) {
		fmu.Lock()
		defer fmu.Unlock()
		if files[i] != nil {
			return files[i], nil
		}
		name := pr.Names[i]
		if yn := safeName(yname); yn != "" && strings.HasPrefix(name, "file-") {
			// The subject had no name: use yEnc's, if no other file has it.
			taken := false
			for k, n := range pr.Names {
				if k != i && n == yn {
					taken = true
				}
			}
			if !taken {
				name = yn
				pr.Names[i] = yn
			}
		}
		f, err := os.OpenFile(filepath.Join(t.Save, name), os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return nil, err
		}
		if size > 0 {
			if st, err := f.Stat(); err == nil && st.Size() < size {
				f.Truncate(size)
			}
		}
		files[i] = f
		return f, nil
	}

	jobs := make(chan work)
	var pmu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					pmu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("internal error: %v", r)
					}
					pmu.Unlock()
					cancel()
					for range jobs { // let the feeder finish
					}
				}
			}()
			for it := range jobs {
				body, tier, err := d.fetchArticle(ctx, tiers, it.seg.ID, it.turn)
				if err == nil {
					d.throttle(ctx, len(body))
					if tier == 0 {
						t.fromMain.Add(1)
					} else {
						t.fromBackup.Add(1)
					}
				}
				missing, damaged := false, false
				switch {
				case errors.Is(err, ErrNoArticle):
					missing = true
				case err != nil:
					pmu.Lock()
					if firstErr == nil && ctx.Err() == nil {
						firstErr = err
					}
					pmu.Unlock()
					cancel()
					continue
				default:
					p, derr := decodeYEnc(body)
					if derr != nil {
						damaged = true
						break
					}
					f, ferr := open(it.file, p.Name, p.Size)
					if ferr != nil {
						pmu.Lock()
						if firstErr == nil {
							firstErr = ferr
						}
						pmu.Unlock()
						cancel()
						continue
					}
					if _, werr := f.WriteAt(p.Data, p.Begin-1); werr != nil {
						pmu.Lock()
						if firstErr == nil {
							firstErr = werr
						}
						pmu.Unlock()
						cancel()
						continue
					}
					damaged = !p.CRCOK
				}
				if !missing {
					d.mu.Lock()
					t.Error = "" // a provider answers again
					d.mu.Unlock()
				}
				pmu.Lock()
				if missing || damaged {
					pr.LostBytes += it.seg.Bytes
					if pr.Lost == nil {
						pr.Lost = map[string]bool{}
					}
					pr.Lost[it.seg.ID] = true
				}
				pr.Done[it.seg.ID] = true
				if missing {
					pr.Missing++
				}
				if damaged {
					pr.Damaged++
				}
				pmu.Unlock()
				t.done.Add(it.seg.Bytes)
			}
		}()
	}
	// Keep progress on disk as it goes.
	stopSave := make(chan struct{})
	go func() {
		tk := time.NewTicker(5 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-stopSave:
				return
			case <-tk.C:
				pmu.Lock()
				d.saveProgress(t, pr)
				pmu.Unlock()
			}
		}
	}()
	turn := 0
feed:
	for i, f := range nzb.Files {
		if !want(i) {
			continue
		}
		for _, s := range f.Segments {
			pmu.Lock()
			skip := pr.Done[s.ID]
			pmu.Unlock()
			if skip {
				continue
			}
			select {
			case jobs <- work{file: i, seg: s, turn: turn}:
				turn++
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(jobs)
	wg.Wait()
	close(stopSave)
	pmu.Lock()
	d.saveProgress(t, pr)
	err := firstErr
	pmu.Unlock()
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func (d *Downloader) loadProgress(t *task) *progress {
	pr := &progress{Done: map[string]bool{}, Names: map[int]string{}}
	if b, err := os.ReadFile(filepath.Join(t.Save, progressFile)); err == nil {
		_ = json.Unmarshal(b, pr)
		if pr.Done == nil {
			pr.Done = map[string]bool{}
		}
		if pr.Names == nil {
			pr.Names = map[int]string{}
		}
	}
	return pr
}

// newProviders lists the providers in now that were not in before.
func newProviders(now, before []string) []string {
	var out []string
	for _, n := range now {
		if !slices.Contains(before, n) {
			out = append(out, n)
		}
	}
	if len(before) == 0 {
		return nil // never recorded: nothing to compare with
	}
	return out
}

func (d *Downloader) saveProgress(t *task, pr *progress) {
	b, _ := json.Marshal(pr)
	tmp := filepath.Join(t.Save, progressFile+".tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, filepath.Join(t.Save, progressFile))
	}
}

var _ backend.Backend = (*Downloader)(nil)
