package animelib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/state"
)

// Folder states in an import.
const (
	Ready    = "ready"     // sure: confirm to add it
	Needs    = "needs you" // something is unsure; Reason says what
	Imported = "imported"  // in the catalog
	Skipped  = "skipped"
	Failed   = "failed"  // could not be looked up; Reason says why
	Waiting  = "waiting" // not checked yet in the running scan
)

const statusBlob = "anime-import.json"

// Option is one way to read a folder: an episode order and a numbering.
type Option struct {
	Order     string `json:"order"` // "" for TMDB's own seasons, else an episode group id
	OrderName string `json:"orderName"`
	Numbering string `json:"numbering"`
	Mapped    int    `json:"mapped"` // files that land on an episode
	Distinct  int    `json:"distinct"`
	// Extras are files left out because they sit past the end of their season:
	// extra episodes TMDB keeps among the specials. Set only when they are the
	// only files left out.
	Extras int `json:"extras,omitempty"`
}

// Folder is one show folder in an import.
type Folder struct {
	Path       string               `json:"path"`
	Name       string               `json:"name"`
	Files      int                  `json:"files"`    // video files with an episode number
	Unparsed   []string             `json:"unparsed"` // video files without one (left out)
	State      string               `json:"state"`
	Reason     string               `json:"reason,omitempty"`
	Match      *metadata.Candidate  `json:"match,omitempty"`
	Candidates []metadata.Candidate `json:"candidates,omitempty"`
	Options    []Option             `json:"options,omitempty"` // best first
	Unmapped   []string             `json:"unmapped,omitempty"`
	SeriesID   int64                `json:"seriesId,omitempty"`
	Picked     bool                 `json:"picked,omitempty"` // a person chose the match
	Note       string               `json:"note,omitempty"`   // worth knowing about a ready folder
	prev       string
}

// Status is the state of the last import scan.
type Status struct {
	Running  bool      `json:"running"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Done     int       `json:"done"`
	Total    int       `json:"total"`
	Error    string    `json:"error,omitempty"`
	Folders  []*Folder `json:"folders"`
	// Confirming is set while "confirm all ready" works through the folders;
	// Message says how the last one went.
	Confirming bool   `json:"confirming"`
	Message    string `json:"message,omitempty"`
}

// Importer scans the anime library folders and adds them to the catalog.
type Importer struct {
	Meta  *metadata.Service
	Cat   *catalog.Catalog
	Roots func() []string // the anime library folders
	Blobs state.Blobs
	Log   *log.Logger
	Now   func() time.Time

	mu   sync.Mutex
	st   Status
	load sync.Once
	busy sync.Mutex // one import action (confirm, choose) at a time
}

func (im *Importer) loadStatus() {
	im.load.Do(func() {
		if im.Blobs == nil {
			return
		}
		b, ok, err := im.Blobs.Load(statusBlob)
		if err != nil || !ok {
			return
		}
		var st Status
		if json.Unmarshal(b, &st) == nil {
			st.Running = false
			im.st = st
		}
	})
}

// save writes the status; call with mu held.
func (im *Importer) save() {
	if im.Blobs == nil {
		return
	}
	b, err := json.Marshal(im.st)
	if err == nil {
		err = im.Blobs.Save(statusBlob, b)
	}
	if err != nil {
		im.Log.Printf("anime import: save: %v", err)
	}
}

// Status returns a copy of the import's state.
func (im *Importer) Status() Status {
	im.loadStatus()
	im.mu.Lock()
	defer im.mu.Unlock()
	b, _ := json.Marshal(im.st)
	var out Status
	_ = json.Unmarshal(b, &out)
	if out.Folders == nil {
		out.Folders = []*Folder{}
	}
	return out
}

// Scan looks at every show folder again, in the background. It returns false
// when a scan is already running.
func (im *Importer) Scan() bool {
	im.loadStatus()
	im.mu.Lock()
	if im.st.Running {
		im.mu.Unlock()
		return false
	}
	old := map[string]*Folder{}
	for _, f := range im.st.Folders {
		old[f.Path] = f
	}
	if im.st.Confirming {
		im.mu.Unlock()
		return false
	}
	im.st = Status{Running: true, Started: im.Now(), Folders: im.st.Folders}
	im.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		err := im.scan(ctx, old)
		im.mu.Lock()
		im.st.Running = false
		im.st.Finished = im.Now()
		if err != nil {
			im.st.Error = err.Error()
		}
		im.save()
		im.mu.Unlock()
		im.Log.Printf("anime import: scanned %d folders", im.st.Total)
	}()
	return true
}

func (im *Importer) scan(ctx context.Context, old map[string]*Folder) error {
	var paths []string
	for _, root := range im.Roots() {
		names, err := library.Folders(root)
		if err != nil {
			return fmt.Errorf("library folder %s: %w", root, err)
		}
		for _, n := range names {
			paths = append(paths, filepath.Join(root, n))
		}
	}
	if len(paths) == 0 {
		return errors.New("no anime library folder is set: give a category with no arr a library folder in Settings")
	}
	sort.Slice(paths, func(i, j int) bool {
		return strings.ToLower(filepath.Base(paths[i])) < strings.ToLower(filepath.Base(paths[j]))
	})
	folders := make([]*Folder, 0, len(paths))
	for _, p := range paths {
		folders = append(folders, &Folder{Path: p, Name: filepath.Base(p), State: Waiting})
	}
	im.mu.Lock()
	im.st.Folders, im.st.Total = folders, len(folders)
	im.mu.Unlock()
	for i, f := range folders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		nf := im.examine(ctx, f.Path, old[f.Path])
		if errors.Is(nf.err, metadata.ErrNoKey) {
			return errors.New("add a TMDB key under Metadata first")
		}
		im.mu.Lock()
		folders[i] = nf.Folder
		im.st.Done = i + 1
		if (i+1)%10 == 0 {
			im.save()
		}
		im.mu.Unlock()
	}
	return nil
}

type examined struct {
	*Folder
	err error
}

// examine works out one folder: already in the catalog, skipped, or matched and
// checked against TMDB's episode orders.
func (im *Importer) examine(ctx context.Context, path string, was *Folder) examined {
	f := &Folder{Path: path, Name: filepath.Base(path)}
	if ser, err := im.Cat.SeriesByFolder(path); err == nil {
		f.State, f.SeriesID = Imported, ser.ID
		f.Match = &metadata.Candidate{TMDBID: ser.TMDBID, Name: ser.Title, Year: yearStr(ser.Year)}
		return examined{Folder: f}
	}
	files, err := ScanFolder(path)
	if err != nil {
		f.State, f.Reason = Failed, err.Error()
		return examined{Folder: f}
	}
	im.count(f, files)
	if was != nil && was.State == Skipped {
		f.State = Skipped
		return examined{Folder: f}
	}
	if f.Files == 0 {
		f.State, f.Reason = Needs, "no video files with an episode number in this folder"
		return examined{Folder: f}
	}
	if was != nil && was.Picked && was.Match != nil {
		f.Match, f.Picked, f.Candidates = was.Match, true, was.Candidates
		err := im.evaluate(ctx, f, files)
		return examined{Folder: f, err: err}
	}
	titles := []string{f.Name}
	if t := commonTitle(files); t != "" && library.Normalize(t) != library.Normalize(f.Name) {
		titles = append(titles, t)
	}
	cands, err := im.Meta.Identify(ctx, titles...)
	if err != nil {
		f.State, f.Reason = Failed, "looking it up: "+err.Error()
		return examined{Folder: f, err: err}
	}
	f.Candidates = trim(cands, 8)
	match, why := sure(f.Name, titles, cands)
	if match == nil {
		f.State, f.Reason = Needs, why
		if c := onlyExact(titles, cands, false); c != nil {
			f.Match = c
		} else if len(cands) > 0 {
			c := cands[0]
			f.Match = &c
		}
		return examined{Folder: f}
	}
	f.Match = match
	err = im.evaluate(ctx, f, files)
	return examined{Folder: f, err: err}
}

func (im *Importer) count(f *Folder, files []DiskFile) {
	f.Files, f.Unparsed = 0, nil
	for _, d := range files {
		if d.OK {
			f.Files++
		} else if len(f.Unparsed) < 10 {
			f.Unparsed = append(f.Unparsed, d.Rel)
		}
	}
}

func trim(c []metadata.Candidate, n int) []metadata.Candidate {
	if len(c) > n {
		return c[:n]
	}
	return c
}

func yearStr(y int) string {
	if y == 0 {
		return ""
	}
	return fmt.Sprint(y)
}

// commonTitle is the show title most of the files give.
func commonTitle(files []DiskFile) string {
	n := map[string]int{}
	best, bestN := "", 0
	for _, f := range files {
		if f.OK {
			n[f.Title]++
			if n[f.Title] > bestN {
				best, bestN = f.Title, n[f.Title]
			}
		}
	}
	return best
}

// sure picks the match only when it is certain: the anime lists lead to exactly
// one show, or, for a show the lists do not know, exactly one TMDB result carries
// the folder's own title.
func sure(folder string, titles []string, cands []metadata.Candidate) (*metadata.Candidate, string) {
	var lists []metadata.Candidate
	for _, c := range cands {
		if c.Via == "anime lists" {
			lists = append(lists, c)
		}
	}
	switch {
	case len(lists) == 1:
		c := lists[0]
		// A synonym can lead the lists to the wrong show. When TMDB has a
		// show whose own title is the folder's, and the lists' show is not it,
		// a person decides.
		if exact := exactTitles(titles, cands, true); len(exact) > 0 && onlyExact(titles, lists, false) == nil {
			return nil, fmt.Sprintf("the anime lists point at %s, but TMDB has a show titled exactly like the folder", c.Name)
		}
		return &c, ""
	case len(lists) > 1:
		// A short title can be a synonym of more than one show (Bakuten!! is
		// also a nickname of Burst Angel). The one whose own TMDB title is the
		// folder's is the show; otherwise a person picks.
		if c := onlyExact(titles, lists, false); c != nil {
			return c, ""
		}
		return nil, fmt.Sprintf("the anime lists point at %d different shows", len(lists))
	case len(cands) == 0:
		return nil, "nothing on TMDB has this title: search for the show below"
	}
	exact := exactTitles(titles, cands, true)
	if len(exact) == 1 {
		c := exact[0]
		return &c, ""
	}
	if len(exact) > 1 {
		return nil, fmt.Sprintf("%d TMDB shows have this exact title", len(exact))
	}
	return nil, "not in the anime lists yet (a new show?), so it was matched by title only"
}

// exactTitles returns the candidates whose TMDB title or original title is one
// of titles, ignoring case, spacing and punctuation.
func exactTitles(titles []string, cands []metadata.Candidate, animated bool) []metadata.Candidate {
	want := map[string]bool{}
	for _, t := range titles {
		want[squash(t)] = true
	}
	var out []metadata.Candidate
	for _, c := range cands {
		if (!animated || c.Animation) && (want[squash(c.Name)] || want[squash(c.Original)]) {
			out = append(out, c)
		}
	}
	return out
}

func onlyExact(titles []string, cands []metadata.Candidate, animated bool) *metadata.Candidate {
	if e := exactTitles(titles, cands, animated); len(e) == 1 {
		return &e[0]
	}
	return nil
}

// squash compares titles ignoring spaces as well as case and punctuation.
func squash(t string) string { return strings.ReplaceAll(library.Normalize(t), " ", "") }

// maxOrders caps how many TMDB episode groups are tried for one folder.
const maxOrders = 8

// evaluate tries the match's episode orders against the folder's files and sets
// the folder's options and state.
func (im *Importer) evaluate(ctx context.Context, f *Folder, files []DiskFile) error {
	orders, err := im.Meta.Orders(ctx, f.Match.TMDBID)
	if err != nil {
		f.State, f.Reason = Failed, "reading the show from TMDB: "+err.Error()
		return err
	}
	f.Options, f.Unmapped, f.Note = nil, nil, ""
	var bestMap map[int]int
	for i, o := range orders {
		if i > maxOrders {
			break
		}
		if i > 0 && len(f.Options) == 1 && fits(f.Options[0], f.Files) {
			break // TMDB's own seasons already place every file one way
		}
		eps, err := im.Meta.Plan(ctx, f.Match.TMDBID, o.ID)
		if err != nil {
			if i == 0 {
				f.State, f.Reason = Failed, "reading the episodes from TMDB: "+err.Error()
				return err
			}
			continue
		}
		for _, fit := range Fits(files, eps) {
			opt := Option{Order: o.ID, OrderName: o.Name, Numbering: fit.Numbering, Mapped: fit.Mapped, Distinct: fit.Distinct}
			if n, ok := Extras(files, eps, fit); ok {
				opt.Extras = n
			}
			f.Options = append(f.Options, opt)
			if bestMap == nil || better(fit, Fit{Mapped: len(bestMap), Distinct: distinctOf(bestMap)}) {
				bestMap = fit.m
			}
		}
	}
	sort.SliceStable(f.Options, func(i, j int) bool {
		a, b := f.Options[i], f.Options[j]
		if a.Distinct != b.Distinct {
			return a.Distinct > b.Distinct
		}
		return a.Mapped > b.Mapped
	})
	for i, d := range files {
		if _, ok := bestMap[i]; d.OK && !ok && len(f.Unmapped) < 10 {
			f.Unmapped = append(f.Unmapped, d.Rel)
		}
	}
	best := f.Options[0]
	full := 0
	for _, o := range f.Options {
		if fits(o, f.Files) {
			full++
		}
	}
	switch {
	case best.Mapped+best.Extras < f.Files:
		f.State = Needs
		f.Reason = fmt.Sprintf("%d of %d files do not match an episode in any order TMDB has", f.Files-best.Mapped, f.Files)
	case best.Distinct < best.Mapped:
		f.State = Needs
		f.Reason = fmt.Sprintf("%d files land on an episode another file already has: check for duplicates or a numbering mix", f.Files-best.Distinct)
	case best.Order != "":
		f.State = Needs
		f.Reason = fmt.Sprintf("TMDB's own seasons do not fit these files; its episode group %q does", best.OrderName)
	case full > 1:
		f.State = Needs
		f.Reason = "the files fit more than one way: choose how they are numbered"
	default:
		f.State, f.Reason = Ready, ""
		if best.Extras > 0 {
			f.Note = fmt.Sprintf("%d extra episode(s) past the end of a season are left out: TMDB keeps them among the specials", best.Extras)
		}
	}
	return nil
}

// fits says an option puts every file on its own episode.
func fits(o Option, files int) bool { return o.Distinct == o.Mapped && o.Mapped+o.Extras == files }

func distinctOf(m map[int]int) int {
	seen := map[int]bool{}
	for _, v := range m {
		seen[v] = true
	}
	return len(seen)
}

func (im *Importer) folder(path string) (*Folder, error) {
	for _, f := range im.st.Folders {
		if f.Path == path {
			return f, nil
		}
	}
	return nil, errors.New("that folder is not in the last scan")
}

// Choose sets a folder's TMDB show by hand and checks its episode orders.
func (im *Importer) Choose(ctx context.Context, path string, c metadata.Candidate) (Folder, error) {
	im.busy.Lock()
	defer im.busy.Unlock()
	im.loadStatus()
	im.mu.Lock()
	f, err := im.folder(path)
	im.mu.Unlock()
	if err != nil {
		return Folder{}, err
	}
	files, err := ScanFolder(path)
	if err != nil {
		return Folder{}, err
	}
	nf := *f
	nf.Match, nf.Picked = &c, true
	im.count(&nf, files)
	if nf.Files == 0 {
		return Folder{}, errors.New("no video files with an episode number in this folder")
	}
	if err := im.evaluate(ctx, &nf, files); err != nil {
		return Folder{}, err
	}
	im.mu.Lock()
	*f = nf
	im.save()
	im.mu.Unlock()
	return nf, nil
}

// Skip leaves a folder out of the import (or brings it back).
func (im *Importer) Skip(path string, skip bool) error {
	im.loadStatus()
	im.mu.Lock()
	defer im.mu.Unlock()
	f, err := im.folder(path)
	if err != nil {
		return err
	}
	switch {
	case skip && f.State != Imported:
		f.prev, f.State = f.State, Skipped
	case !skip && f.State == Skipped:
		f.State = Needs
		if f.prev != "" {
			f.State = f.prev
		}
		if f.Match == nil && f.Reason == "" {
			f.Reason = "rescan to look this folder up"
		}
	}
	im.save()
	return nil
}

// Confirm adds a folder's show to the catalog in the chosen way (an index into
// its options) and records the episodes already on disk.
func (im *Importer) Confirm(ctx context.Context, path string, option int) (SyncResult, error) {
	im.busy.Lock()
	defer im.busy.Unlock()
	im.loadStatus()
	im.mu.Lock()
	f, err := im.folder(path)
	var snap Folder
	if err == nil {
		snap = *f
	}
	im.mu.Unlock()
	if err != nil {
		return SyncResult{}, err
	}
	if snap.State == Imported {
		return SyncResult{}, errors.New("already imported")
	}
	if snap.Match == nil || option < 0 || option >= len(snap.Options) {
		return SyncResult{}, errors.New("choose the show and how its files are numbered first")
	}
	opt := snap.Options[option]
	ser, err := im.Meta.AddSeries(ctx, snap.Match.TMDBID, "anime", path, opt.Numbering)
	if err != nil {
		if ser.ID != 0 {
			if existing, e := im.Cat.Series(ser.ID); e == nil && existing.Folder != path {
				return SyncResult{}, fmt.Errorf("%s is already in the catalog for %s", existing.Title, existing.Folder)
			}
			_ = im.Cat.RemoveSeries(ser.ID)
		}
		return SyncResult{}, err
	}
	if opt.Order != "" {
		if err := im.Meta.SetEpisodeOrder(ctx, ser.ID, opt.Order); err != nil {
			_ = im.Cat.RemoveSeries(ser.ID)
			return SyncResult{}, err
		}
	}
	if err := im.Cat.SetMonitored(ser.ID, true); err != nil {
		return SyncResult{}, err
	}
	_ = im.Cat.AddSeriesAlias(ser.ID, snap.Name, "folder")
	res, err := im.Sync(ser.ID)
	if err != nil {
		return res, err
	}
	im.mu.Lock()
	if f, err := im.folder(path); err == nil {
		f.prev, f.State, f.SeriesID, f.Reason = f.State, Imported, ser.ID, ""
	}
	im.save()
	im.mu.Unlock()
	im.Log.Printf("anime import: added %s (%s, %s): %d files recorded", ser.Title, opt.Numbering, orderLabel(opt), res.Recorded)
	return res, nil
}

func orderLabel(o Option) string {
	if o.Order == "" {
		return "TMDB seasons"
	}
	return o.OrderName
}

// ConfirmReady confirms every ready folder with its best option.
func (im *Importer) ConfirmReady(ctx context.Context) (done int, errs []string) {
	st := im.Status()
	for _, f := range st.Folders {
		if f.State != Ready {
			continue
		}
		if _, err := im.Confirm(ctx, f.Path, 0); err != nil {
			errs = append(errs, f.Name+": "+err.Error())
			continue
		}
		done++
	}
	return done, errs
}

// StartConfirmReady confirms every ready folder in the background. It returns
// false when a scan or another confirm is running.
func (im *Importer) StartConfirmReady() bool {
	im.loadStatus()
	im.mu.Lock()
	if im.st.Running || im.st.Confirming {
		im.mu.Unlock()
		return false
	}
	im.st.Confirming, im.st.Message = true, ""
	im.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		n, errs := im.ConfirmReady(ctx)
		msg := fmt.Sprintf("added %d show(s)", n)
		if len(errs) > 0 {
			msg += "; not added: " + strings.Join(errs, "; ")
		}
		im.mu.Lock()
		im.st.Confirming, im.st.Message = false, msg
		im.save()
		im.mu.Unlock()
		im.Log.Printf("anime import: %s", msg)
	}()
	return true
}

// Remove takes a series out of the catalog (undoing its import). Its folder goes
// back to the import list. Nothing on disk changes.
func (im *Importer) Remove(seriesID int64) error {
	ser, err := im.Cat.Series(seriesID)
	if err != nil {
		return err
	}
	if err := im.Cat.RemoveSeries(seriesID); err != nil {
		return err
	}
	im.loadStatus()
	im.mu.Lock()
	defer im.mu.Unlock()
	if f, err := im.folder(ser.Folder); err == nil {
		f.SeriesID = 0
		f.State, f.Reason = Needs, "removed from the library: confirm again to add it back"
		if f.prev != "" && f.prev != Imported {
			f.State, f.Reason = f.prev, ""
		}
	}
	im.save()
	im.Log.Printf("anime import: removed %s from the library", ser.Title)
	return nil
}

// SyncResult says what a folder check changed.
type SyncResult struct {
	Recorded int      `json:"recorded"` // new files recorded
	Removed  int      `json:"removed"`  // records of files no longer on disk
	Files    int      `json:"files"`    // video files on disk with an episode number
	Unmapped []string `json:"unmapped"` // files that match no episode
}

// Sync records new files in a series' folder and forgets ones that are gone.
// A folder that cannot be read, or reads as empty, changes nothing, so a share
// that is briefly unmounted never wipes the records.
func (im *Importer) Sync(seriesID int64) (SyncResult, error) {
	var res SyncResult
	ser, err := im.Cat.Series(seriesID)
	if err != nil {
		return res, err
	}
	if ser.Folder == "" {
		return res, errors.New("the series has no folder")
	}
	if _, err := os.Stat(ser.Folder); err != nil {
		return res, fmt.Errorf("folder %s: %w", ser.Folder, err)
	}
	files, err := ScanFolder(ser.Folder)
	if err != nil {
		return res, err
	}
	eps, err := im.Cat.Episodes(seriesID)
	if err != nil {
		return res, err
	}
	known, err := im.Cat.SeriesFiles(seriesID)
	if err != nil {
		return res, err
	}
	have := map[string]bool{}
	for _, k := range known {
		have[k.Path] = true
	}
	m, _ := MapFiles(files, eps, ser.Numbering)
	onDisk := map[string]bool{}
	for i, d := range files {
		if !d.OK {
			continue
		}
		res.Files++
		onDisk[d.Path] = true
		j, ok := m[i]
		if !ok {
			if len(res.Unmapped) < 20 {
				res.Unmapped = append(res.Unmapped, d.Rel)
			}
			continue
		}
		if have[d.Path] {
			continue
		}
		if _, err := im.Cat.AddFile(catalog.File{Path: d.Path, Size: d.Size, EpisodeIDs: []int64{eps[j].ID}, Resolution: d.Res}); err != nil {
			return res, err
		}
		res.Recorded++
	}
	if res.Files == 0 {
		return res, nil
	}
	for _, k := range known {
		if !onDisk[k.Path] {
			if _, err := os.Stat(k.Path); errors.Is(err, os.ErrNotExist) {
				if err := im.Cat.RemoveFile(k.Path); err == nil {
					res.Removed++
				}
			}
		}
	}
	return res, nil
}

// SyncAll checks every anime series' folder.
func (im *Importer) SyncAll() {
	all, err := im.Cat.ListSeries()
	if err != nil {
		im.Log.Printf("anime library: %v", err)
		return
	}
	for _, s := range all {
		if s.Kind != "anime" || s.Folder == "" {
			continue
		}
		res, err := im.Sync(s.ID)
		if err != nil {
			im.Log.Printf("anime library: %s: %v", s.Title, err)
			continue
		}
		if res.Recorded > 0 || res.Removed > 0 {
			im.Log.Printf("anime library: %s: %d new file(s), %d gone", s.Title, res.Recorded, res.Removed)
		}
	}
}

// Run checks the anime series' folders every half hour until ctx ends.
func (im *Importer) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			im.SyncAll()
		}
	}
}
