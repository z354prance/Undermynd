package animelib

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/state"
)

// Placement modes.
const (
	ModeFolders = "folders" // place by folder name, as before the catalog
	ModeDryRun  = "dry-run" // place by folder name, and record what the catalog would do
	ModeCatalog = "catalog" // place by the catalog; anything unsure waits for a person
)

// Reasons a file waits for a person.
const (
	KindNoNumber  = "no-number"   // the name has no episode number
	KindUnknown   = "unknown"     // no show in the library has this title
	KindAmbiguous = "ambiguous"   // more than one show has this title
	KindNotOnTMDB = "not-on-tmdb" // the show is known, the episode is not on TMDB yet
	KindNoEpisode = "no-episode"  // the show has no such episode
	KindExists    = "exists"      // the episode already has a file
	KindNoFolder  = "no-folder"   // the show has no library folder
)

const (
	modeBlob   = "placement-mode.json"
	queueBlob  = "placement-queue.json"
	dryRunBlob = "placement-dryrun.json"
	dryRunKeep = 300
)

// Pending is a downloaded file waiting for a person.
type Pending struct {
	ID       string    `json:"id"`
	Src      string    `json:"src"`
	Name     string    `json:"name"`
	JobID    string    `json:"jobId,omitempty"`
	Root     string    `json:"root"`
	Added    time.Time `json:"added"`
	Kind     string    `json:"kind"`
	Reason   string    `json:"reason"`
	Title    string    `json:"title"`
	Aliases  []string  `json:"aliases,omitempty"`
	Season   int       `json:"season"`
	Marked   bool      `json:"marked"`
	Episode  int       `json:"episode"`
	Group    string    `json:"group,omitempty"`
	Res      string    `json:"res,omitempty"`
	SeriesID int64     `json:"seriesId,omitempty"`
	Series   string    `json:"series,omitempty"`
	EpLabel  string    `json:"epLabel,omitempty"` // the episode it would be, "S02E08" or "1181"
	Existing *Existing `json:"existing,omitempty"`
	// Candidates are shows the file could be (several with this title, or the
	// closest titles when none matched).
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Existing describes the file an episode already has.
type Existing struct {
	Path  string `json:"path"`
	Group string `json:"group,omitempty"`
	Res   string `json:"res,omitempty"`
	Size  int64  `json:"size"`
}

// Candidate is a show a waiting file might belong to.
type Candidate struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Year  int    `json:"year"`
}

// DryRunEntry compares placing by folder name with placing by the catalog.
type DryRunEntry struct {
	At         time.Time `json:"at"`
	Name       string    `json:"name"`
	FolderDst  string    `json:"folderDst,omitempty"`
	FolderWhy  string    `json:"folderWhy,omitempty"`
	CatalogDst string    `json:"catalogDst,omitempty"`
	CatalogWhy string    `json:"catalogWhy,omitempty"`
	Verdict    string    `json:"verdict"`            // same, better, different, worse, both-left
	ByFolder   bool      `json:"byFolder,omitempty"` // the show is not in the catalog, so folder names would place it too
}

// Placer puts finished anime downloads into the library using the catalog.
type Placer struct {
	Cat   *catalog.Catalog
	Meta  *metadata.Service
	Blobs state.Blobs
	Log   *log.Logger
	Now   func() time.Time

	mu       sync.Mutex
	loaded   bool
	mode     string
	queue    []*Pending
	dryRun   []DryRunEntry
	refreshd map[int64]time.Time
}

func (p *Placer) loadLocked() {
	if p.loaded {
		return
	}
	p.loaded = true
	p.mode = ModeDryRun
	if p.refreshd == nil {
		p.refreshd = map[int64]time.Time{}
	}
	if p.Blobs == nil {
		return
	}
	var m struct{ Mode string }
	if b, ok, _ := p.Blobs.Load(modeBlob); ok && json.Unmarshal(b, &m) == nil && validMode(m.Mode) {
		p.mode = m.Mode
	}
	if b, ok, _ := p.Blobs.Load(queueBlob); ok {
		_ = json.Unmarshal(b, &p.queue)
	}
	if b, ok, _ := p.Blobs.Load(dryRunBlob); ok {
		_ = json.Unmarshal(b, &p.dryRun)
	}
}

func validMode(m string) bool { return m == ModeFolders || m == ModeDryRun || m == ModeCatalog }

func (p *Placer) saveLocked(name string, v any) {
	if p.Blobs == nil {
		return
	}
	b, err := json.Marshal(v)
	if err == nil {
		err = p.Blobs.Save(name, b)
	}
	if err != nil {
		p.Log.Printf("placement: save %s: %v", name, err)
	}
}

// Mode says how anime files are placed. A new install starts in the dry run.
func (p *Placer) Mode() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	return p.mode
}

// SetMode changes how anime files are placed.
func (p *Placer) SetMode(m string) error {
	if !validMode(m) {
		return fmt.Errorf("unknown placement mode %q", m)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	p.mode = m
	p.saveLocked(modeBlob, struct{ Mode string }{m})
	p.Log.Printf("placement: anime files are now placed by %s", map[string]string{ModeFolders: "folder name", ModeDryRun: "folder name (catalog dry run)", ModeCatalog: "the catalog"}[m])
	return nil
}

// plan is where the catalog would put a file, or why it would not.
type plan struct {
	rel     library.Release
	ser     catalog.Series
	ep      catalog.Episode
	hasEp   bool
	dst     string
	kind    string
	reason  string
	exist   *Existing
	cands   []Candidate
	epLabel string
}

// decide works out where a file goes, without touching anything.
func (p *Placer) decide(src, root string) plan {
	base := filepath.Base(src)
	var pl plan
	rel, ok := library.Parse(base)
	if !ok {
		pl.kind, pl.reason = KindNoNumber, "the name has no episode number"
		return pl
	}
	pl.rel = rel
	ser, cands, err := p.Cat.FindSeries(append([]string{rel.Title}, rel.Aliases...)...)
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		pl.kind, pl.reason = KindUnknown, fmt.Sprintf("no show in the library is known as %q", rel.Title)
		pl.cands = p.closest(rel)
		return pl
	case err != nil:
		pl.kind, pl.reason = KindUnknown, err.Error()
		return pl
	case len(cands) > 0:
		names := make([]string, 0, len(cands))
		for _, c := range cands {
			pl.cands = append(pl.cands, Candidate{c.ID, c.Title, c.Year})
			names = append(names, c.Title)
		}
		sort.Strings(names)
		pl.kind, pl.reason = KindAmbiguous, fmt.Sprintf("%d shows are known as %q: %s", len(cands), rel.Title, strings.Join(names, ", "))
		return pl
	}
	pl.ser = ser
	season, marked, number := p.inPlace(ser, rel)
	return p.decideEpisode(pl, ser, season, marked, number, filepath.Ext(src))
}

// inPlace turns a release's numbers into the series' own when the series is
// kept in more than one folder. A sequel TMDB counts as a later season is
// numbered from 1 by its releases ("Bleach Thousand-Year Blood War - 03" is
// Bleach season 17 episode 3), so each folder's reading is tried: a folder
// named like the release wins; otherwise the reading that lands on an aired
// episode with no file, the most recent first.
func (p *Placer) inPlace(ser catalog.Series, rel library.Release) (season int, marked bool, number int) {
	places, err := p.Cat.Places(ser)
	if err != nil || len(places) < 2 {
		return rel.Season, rel.Marked, rel.Episode
	}
	eps, err := p.Cat.Episodes(ser.ID)
	if err != nil {
		return rel.Season, rel.Marked, rel.Episode
	}
	type reading struct {
		season int
		marked bool
		number int
		named  bool
	}
	titles := map[string]bool{}
	for _, t := range append([]string{rel.Title}, rel.Aliases...) {
		titles[squash(t)] = true
	}
	var readings []reading
	for _, pl := range places {
		r := reading{season: rel.Season, marked: rel.Marked, number: rel.Episode, named: titles[squash(filepath.Base(pl.Folder))]}
		if pl.FirstSeason > 1 {
			switch {
			case rel.Marked:
				r.season = pl.FirstSeason + rel.Season - 1
			case pl.Numbering == catalog.ByAbsolute:
				start := 0
				for _, e := range eps {
					if e.Season == pl.FirstSeason && e.Absolute > 0 && (start == 0 || e.Absolute < start) {
						start = e.Absolute
					}
				}
				r.number = start + rel.Episode - 1
			default:
				r.season, r.marked = pl.FirstSeason, true
			}
		}
		readings = append(readings, r)
	}
	for _, r := range readings {
		if r.named {
			return r.season, r.marked, r.number
		}
	}
	best, bestDate := -1, ""
	for i, r := range readings {
		var e catalog.Episode
		var err error
		if r.marked {
			e, err = p.Cat.EpisodeBySeason(ser.ID, r.season, r.number)
		} else {
			e, err = p.Cat.EpisodeByAbsolute(ser.ID, r.number)
		}
		if err != nil || e.AirDate == "" {
			continue
		}
		if files, _ := p.Cat.EpisodeFiles(e.ID); len(files) > 0 {
			continue
		}
		if e.AirDate > bestDate {
			best, bestDate = i, e.AirDate
		}
	}
	if best < 0 {
		best = 0
	}
	return readings[best].season, readings[best].marked, readings[best].number
}

// decideEpisode finishes a plan once the show is known.
func (p *Placer) decideEpisode(pl plan, ser catalog.Series, season int, marked bool, number int, ext string) plan {
	pl.ser = ser
	if ser.Folder == "" {
		pl.kind, pl.reason = KindNoFolder, ser.Title+" has no library folder"
		return pl
	}
	var (
		ep  catalog.Episode
		err error
	)
	if marked {
		ep, err = p.Cat.EpisodeBySeason(ser.ID, season, number)
		pl.epLabel = fmt.Sprintf("S%02dE%02d", season, number)
	} else {
		// An unmarked number counts from the show's first episode, as
		// SubsPlease numbers a second cour 13, 14 and so on.
		ep, err = p.Cat.EpisodeByAbsolute(ser.ID, number)
		pl.epLabel = fmt.Sprint(number)
	}
	if err != nil {
		eps, _ := p.Cat.Episodes(ser.ID)
		lastAbs, lastInSeason := 0, 0
		for _, e := range eps {
			if e.Absolute > lastAbs {
				lastAbs = e.Absolute
			}
			if e.Season == season && e.Number > lastInSeason {
				lastInSeason = e.Number
			}
		}
		past := (!marked && number > lastAbs) || (marked && number > lastInSeason)
		if past {
			pl.kind, pl.reason = KindNotOnTMDB, fmt.Sprintf("episode %s of %s is not on TMDB yet", pl.epLabel, ser.Title)
		} else {
			pl.kind, pl.reason = KindNoEpisode, fmt.Sprintf("%s has no episode %s on TMDB", ser.Title, pl.epLabel)
		}
		return pl
	}
	pl.ep, pl.hasEp = ep, true
	pl.epLabel = epLabel(ser, ep)
	pl.dst = p.destination(ser, ep, pl.rel.Res, ext)
	if files, _ := p.Cat.EpisodeFiles(ep.ID); len(files) > 0 {
		f := files[0]
		if _, serr := os.Stat(f.Path); serr == nil {
			pl.exist = &Existing{Path: f.Path, Group: f.ReleaseGroup, Res: f.Resolution, Size: f.Size}
		}
	}
	if pl.exist == nil {
		if fi, serr := os.Stat(pl.dst); serr == nil {
			pl.exist = &Existing{Path: pl.dst, Size: fi.Size()}
		}
	}
	if pl.exist != nil {
		what := "a file"
		if g := strings.TrimSpace(strings.Join([]string{pl.exist.Group, pl.exist.Res}, " ")); g != "" {
			what = g
		}
		pl.kind, pl.reason = KindExists, fmt.Sprintf("%s %s already has %s", ser.Title, pl.epLabel, what)
	}
	return pl
}

func epLabel(ser catalog.Series, ep catalog.Episode) string {
	if ser.Numbering == catalog.ByAbsolute && ep.Season > 0 && ep.Absolute > 0 {
		return fmt.Sprint(ep.Absolute)
	}
	return fmt.Sprintf("S%02dE%02d", ep.Season, ep.Number)
}

var unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// seasonFolder is a season's folder; specials go in "Specials".
func seasonFolder(folder string, season int) string {
	if season == 0 {
		return filepath.Join(folder, "Specials")
	}
	return library.SeasonDir(folder, season)
}

// destination is where an episode's file goes: the season folder of the
// show's folder that holds that season (an extra folder for a sequel TMDB counts
// as a later season), named after that folder, by absolute number or by season
// and episode as the folder is laid out.
func (p *Placer) destination(ser catalog.Series, ep catalog.Episode, res, ext string) string {
	place := catalog.Place{Folder: ser.Folder, FirstSeason: 1, Numbering: ser.Numbering}
	if ep.Season > 0 {
		places, _ := p.Cat.Places(ser)
		for _, x := range places {
			if x.Extra && x.FirstSeason <= ep.Season && x.FirstSeason > place.FirstSeason {
				place = x
			}
		}
	}
	season, abs := ep.Season, ep.Absolute
	if place.FirstSeason > 1 {
		season = ep.Season - place.FirstSeason + 1
		start := 0
		if eps, err := p.Cat.Episodes(ser.ID); err == nil {
			for _, e := range eps {
				if e.Season == place.FirstSeason && e.Absolute > 0 && (start == 0 || e.Absolute < start) {
					start = e.Absolute
				}
			}
		}
		if start > 0 && abs > 0 {
			abs = abs - start + 1
		}
	}
	show := strings.TrimSpace(unsafeName.ReplaceAllString(filepath.Base(place.Folder), ""))
	num := fmt.Sprintf("S%02dE%02d", season, ep.Number)
	if place.Numbering == catalog.ByAbsolute && season > 0 && abs > 0 {
		num = fmt.Sprint(abs)
	}
	name := show + " - " + num
	if res != "" {
		name += " (" + res + ")"
	}
	return filepath.Join(seasonFolder(place.Folder, season), name+ext)
}

// closest lists library shows whose title shares the release title's first words.
func (p *Placer) closest(rel library.Release) []Candidate {
	all, err := p.Cat.ListSeries()
	if err != nil {
		return nil
	}
	want := strings.Fields(library.Normalize(rel.Title))
	if len(want) == 0 {
		return nil
	}
	type scored struct {
		c Candidate
		n int
	}
	var out []scored
	for _, s := range all {
		if s.Kind != "anime" {
			continue
		}
		titles := append([]string{s.Title, filepath.Base(s.Folder)}, mustAliases(p.Cat, s.ID)...)
		best := 0
		for _, t := range titles {
			have := strings.Fields(library.Normalize(t))
			n := 0
			for n < len(have) && n < len(want) && have[n] == want[n] {
				n++
			}
			if n > best {
				best = n
			}
		}
		if best >= 2 || (best == 1 && len(want) == 1) {
			out = append(out, scored{Candidate{s.ID, s.Title, s.Year}, best})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].n > out[j].n })
	var res []Candidate
	for i := 0; i < len(out) && i < 5; i++ {
		res = append(res, out[i].c)
	}
	return res
}

func mustAliases(c *catalog.Catalog, id int64) []string {
	a, _ := c.SeriesAliases(id)
	return a
}

// Preview says where the catalog would put a file, or why it would not.
func (p *Placer) Preview(src, root string) (dst, why string) {
	pl := p.decideFile(src, root)
	if pl.kind == KindUnknown {
		return "", ByFolderName
	}
	if pl.kind != "" {
		return "", pl.reason
	}
	return pl.dst, ""
}

func (p *Placer) decideFile(src, root string) plan { return p.decide(src, root) }

// ByFolderName is Preview's answer for a show not in the catalog: it would be
// placed by folder name, as before.
const ByFolderName = "by folder name: the show is not in the catalog"

// Record keeps a dry-run comparison: where the folder names put the file and
// where the catalog would have.
func (p *Placer) Record(src, catalogDst, catalogWhy, folderDst, folderWhy string) {
	e := DryRunEntry{At: p.Now(), Name: filepath.Base(src), FolderDst: folderDst, FolderWhy: folderWhy, CatalogDst: catalogDst, CatalogWhy: catalogWhy}
	if catalogWhy == ByFolderName {
		e.ByFolder = true
		if folderDst != "" {
			e.CatalogDst, e.CatalogWhy = folderDst, ""
		} else {
			e.CatalogWhy = "not in the catalog, and " + folderWhy
		}
	}
	switch {
	case e.FolderDst == "" && e.CatalogDst == "":
		e.Verdict = "both-left"
	case e.FolderDst == "":
		e.Verdict = "better"
	case e.CatalogDst == "":
		e.Verdict = "worse"
	case filepath.Dir(e.FolderDst) == filepath.Dir(e.CatalogDst):
		e.Verdict = "same"
	default:
		e.Verdict = "different"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	p.dryRun = append(p.dryRun, e)
	if len(p.dryRun) > dryRunKeep {
		p.dryRun = p.dryRun[len(p.dryRun)-dryRunKeep:]
	}
	p.saveLocked(dryRunBlob, p.dryRun)
}

// Place puts a finished download in the library by the catalog. A file it
// cannot place with certainty waits for a person and stays where it is; why
// says so. err is only for a move that failed.
func (p *Placer) Place(src, jobID, root string, byFolder func() (string, string, error)) (dst, why string, err error) {
	pl := p.decideFile(src, root)
	if pl.kind == KindUnknown && byFolder != nil {
		// A show that is not in the catalog (a folder never imported) is
		// placed by folder name, as before the catalog.
		dst, why, err := byFolder()
		if err != nil || why == "" {
			if why == "" && err == nil {
				p.Log.Printf("placement: %s placed by folder name (the show is not in the catalog)", filepath.Base(src))
			}
			return dst, why, err
		}
	}
	if pl.kind != "" {
		p.enqueue(src, jobID, root, pl)
		return "", pl.reason + " (waiting in Library)", nil
	}
	if err := p.move(src, pl.dst, pl); err != nil {
		return "", "", err
	}
	return pl.dst, "", nil
}

// move puts the file at dst and records it against the episode.
func (p *Placer) move(src, dst string, pl plan) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(dst), 0o777)
	if err := moveFile(src, dst); err != nil {
		return err
	}
	_ = os.Chmod(dst, 0o666)
	if pl.hasEp {
		var size int64
		if fi, err := os.Stat(dst); err == nil {
			size = fi.Size()
		}
		if _, err := p.Cat.AddFile(catalog.File{Path: dst, Size: size, EpisodeIDs: []int64{pl.ep.ID}, ReleaseGroup: pl.rel.Group, Resolution: pl.rel.Res}); err != nil {
			p.Log.Printf("placement: recording %s: %v", dst, err)
		}
	}
	p.Log.Printf("placement: %s -> %s", filepath.Base(src), dst)
	return nil
}

// moveFile renames, or copies and removes when src and dst are on different
// file systems. It never overwrites.
func moveFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return os.Remove(src)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *Placer) enqueue(src, jobID, root string, pl plan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	item := &Pending{Src: src, Name: filepath.Base(src), JobID: jobID, Root: root, Added: p.Now()}
	for _, q := range p.queue {
		if q.Src == src {
			item = q // the same file again: refresh its reason
		}
	}
	fill(item, pl)
	if item.ID == "" {
		item.ID = newID()
		p.queue = append(p.queue, item)
	}
	p.saveLocked(queueBlob, p.queue)
}

func fill(item *Pending, pl plan) {
	item.Kind, item.Reason = pl.kind, pl.reason
	item.Title, item.Aliases, item.Season, item.Marked, item.Episode = pl.rel.Title, pl.rel.Aliases, pl.rel.Season, pl.rel.Marked, pl.rel.Episode
	item.Group, item.Res = pl.rel.Group, pl.rel.Res
	item.SeriesID, item.Series, item.EpLabel = pl.ser.ID, pl.ser.Title, pl.epLabel
	item.Existing, item.Candidates = pl.exist, pl.cands
}

// Queue lists the files waiting for a person, oldest first, dropping any that
// are no longer on disk.
func (p *Placer) Queue() []Pending {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	kept := p.queue[:0]
	for _, q := range p.queue {
		if _, err := os.Stat(q.Src); err == nil {
			kept = append(kept, q)
		}
	}
	if len(kept) != len(p.queue) {
		p.queue = kept
		p.saveLocked(queueBlob, p.queue)
	}
	out := make([]Pending, 0, len(p.queue))
	for _, q := range p.queue {
		out = append(out, *q)
	}
	return out
}

// DryRun lists the dry-run comparisons, newest first.
func (p *Placer) DryRun() []DryRunEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	out := make([]DryRunEntry, len(p.dryRun))
	for i, e := range p.dryRun {
		out[len(p.dryRun)-1-i] = e
	}
	return out
}

func (p *Placer) take(id string) (*Pending, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	for _, q := range p.queue {
		if q.ID == id {
			c := *q
			return &c, nil
		}
	}
	return nil, errors.New("that file is no longer waiting")
}

func (p *Placer) drop(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, q := range p.queue {
		if q.ID == id {
			p.queue = append(p.queue[:i], p.queue[i+1:]...)
			break
		}
	}
	p.saveLocked(queueBlob, p.queue)
}

// Resolution is a person's answer for a waiting file.
type Resolution struct {
	Action   string `json:"action"` // place, place-anyway, replace, keep-both, leave, new-show
	SeriesID int64  `json:"seriesId"`
	Season   int    `json:"season"`  // with Marked: the season
	Marked   bool   `json:"marked"`  // the number is within Season (else counted from the first episode)
	Episode  int    `json:"episode"` // the episode number
	Remember bool   `json:"remember"`
	TMDBID   int64  `json:"tmdbId"` // for new-show
}

// Resolve carries out a person's answer. It returns where the file went.
func (p *Placer) Resolve(ctx context.Context, id string, r Resolution) (string, error) {
	item, err := p.take(id)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(item.Src); err != nil {
		p.drop(id)
		return "", errors.New("the file is gone from the download folder")
	}
	rel := library.Release{Title: item.Title, Aliases: item.Aliases, Season: item.Season, Marked: item.Marked, Episode: item.Episode, Group: item.Group, Res: item.Res}
	season, marked, number := item.Season, item.Marked, item.Episode
	if r.Episode > 0 {
		season, marked, number = r.Season, r.Marked, r.Episode
	}
	switch r.Action {
	case "leave":
		p.drop(id)
		return "", nil
	case "new-show":
		if r.TMDBID == 0 || item.Root == "" {
			return "", errors.New("choose the TMDB show")
		}
		tv, err := p.Meta.Search(ctx, fmt.Sprint(r.TMDBID))
		if err != nil || len(tv) == 0 {
			return "", fmt.Errorf("TMDB has no show %d", r.TMDBID)
		}
		folder := filepath.Join(item.Root, strings.TrimSpace(unsafeName.ReplaceAllString(tv[0].Name, "")))
		if ser, err := p.Cat.SeriesByFolder(folder); err == nil {
			r.SeriesID = ser.ID
		} else {
			if err := os.MkdirAll(folder, 0o777); err != nil {
				return "", err
			}
			_ = os.Chmod(folder, 0o777)
			ser, err := p.Meta.AddSeries(ctx, r.TMDBID, "anime", folder, catalog.BySeason)
			if err != nil {
				return "", err
			}
			_ = p.Cat.SetMonitored(ser.ID, true)
			r.SeriesID = ser.ID
		}
		r.Remember = true
	}
	sid := r.SeriesID
	if sid == 0 {
		sid = item.SeriesID
	}
	if sid == 0 {
		return "", errors.New("choose the show")
	}
	ser, err := p.Cat.Series(sid)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(item.Src)
	pl := p.decideEpisode(plan{rel: rel}, ser, season, marked, number, ext)
	dst := pl.dst
	switch {
	case r.Action == "place-anyway" && pl.kind == KindNotOnTMDB:
		dst = p.placeholderDst(ser, season, marked, number, rel.Res, filepath.Ext(item.Src))
	case pl.kind == KindExists && r.Action == "replace":
		old := pl.exist.Path
		if err := os.Rename(old, old+".replaced"); err != nil {
			return "", fmt.Errorf("setting the old file aside: %w", err)
		}
		_ = p.Cat.RemoveFile(old)
		p.Log.Printf("placement: kept the replaced file as %s.replaced", old)
	case pl.kind == KindExists && r.Action == "keep-both":
		tag := rel.Group
		if tag == "" {
			tag = "2"
		}
		dst = strings.TrimSuffix(dst, ext) + " [" + strings.TrimSpace(unsafeName.ReplaceAllString(tag, "")) + "]" + ext
	case pl.kind != "":
		return "", errors.New(pl.reason)
	}
	if err := p.move(item.Src, dst, pl); err != nil {
		return "", err
	}
	if r.Remember || item.SeriesID == 0 {
		for _, t := range append([]string{rel.Title}, rel.Aliases...) {
			if strings.TrimSpace(t) != "" {
				_ = p.Cat.AddSeriesAlias(ser.ID, t, "placement")
			}
		}
	}
	p.drop(id)
	return dst, nil
}

// placeholderDst names a file for an episode TMDB does not list yet. It is not
// recorded; the folder check records it once TMDB adds the episode.
func (p *Placer) placeholderDst(ser catalog.Series, season int, marked bool, number int, res, ext string) string {
	eps, _ := p.Cat.Episodes(ser.ID)
	last := catalog.Episode{Season: 1}
	for _, e := range eps {
		if e.Season > 0 && (e.Absolute > last.Absolute) {
			last = e
		}
	}
	ep := catalog.Episode{Season: last.Season, Number: last.Number + (number - last.Absolute), Absolute: number}
	if marked {
		ep = catalog.Episode{Season: season, Number: number}
	}
	return p.destination(ser, ep, res, ext)
}

// Retry tries the waiting files again: a new episode on TMDB, a title learned
// since, or a file placed by hand all clear them. Shows waiting for TMDB are
// refreshed at most every six hours.
func (p *Placer) Retry(ctx context.Context) {
	for _, item := range p.Queue() {
		if ctx.Err() != nil {
			return
		}
		if item.Kind == KindNotOnTMDB && item.SeriesID != 0 && p.Meta != nil {
			p.mu.Lock()
			last := p.refreshd[item.SeriesID]
			due := p.Now().Sub(last) > 6*time.Hour
			if due {
				p.refreshd[item.SeriesID] = p.Now()
			}
			p.mu.Unlock()
			if due {
				if err := p.Meta.Refresh(ctx, item.SeriesID); err != nil {
					p.Log.Printf("placement: refreshing %s: %v", item.Series, err)
				}
			}
		}
		pl := p.decideFile(item.Src, item.Root)
		if pl.kind != "" {
			if pl.kind != item.Kind || pl.reason != item.Reason {
				p.enqueue(item.Src, item.JobID, item.Root, pl)
			}
			continue
		}
		if err := p.move(item.Src, pl.dst, pl); err != nil {
			p.Log.Printf("placement: %v", err)
			continue
		}
		p.drop(item.ID)
	}
}

// Run retries the waiting files every hour until ctx ends.
func (p *Placer) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Retry(ctx)
		}
	}
}
