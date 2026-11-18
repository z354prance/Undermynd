package shows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/pipeline"
)

// Submitter starts a download (the pipeline engine).
type Submitter interface {
	Submit(pipeline.SubmitRequest) (job.Job, error)
}

// Root is a library folder of shows and the category its downloads use.
type Root struct {
	Dir      string `json:"dir"`
	Category string `json:"category"`
}

// Show is one show folder with what it holds and its settings.
type Show struct {
	Folder   string `json:"folder"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	Category string `json:"category"`
	Record
	Summary Summary `json:"summary"`
	// HandMade names an enabled RSS rule whose pattern matches the folder:
	// a feed the person set up by hand.
	HandMade string `json:"handMade,omitempty"`
	// Unscanned: the folder has not been read yet.
	Unscanned bool `json:"unscanned,omitempty"`
}

// Service follows shows.
type Service struct {
	Store   *Store
	Nyaa    Nyaa
	AniList AniList
	HTTP    *http.Client
	Cfg     *config.Config
	Engine  Submitter
	// Jobs lists the pipeline's jobs, to show episodes being processed.
	Jobs func() []job.Job
	Log  *log.Logger
	Now  func() time.Time
	// Grace is how long a later group's release waits for the first choice.
	Grace time.Duration
	// Every is how often each followed show is checked.
	Every time.Duration

	mu   sync.Mutex
	scan ScanState
	busy sync.Mutex // one check or grab decision at a time
}

// Roots are the library folders of the categories no arr handles.
func (s *Service) Roots() []Root {
	var out []Root
	for _, c := range s.Cfg.Get().Categories {
		if c.LibraryDir != "" {
			out = append(out, Root{Dir: c.LibraryDir, Category: c.Name})
		}
	}
	return out
}

func (s *Service) rootOf(folder string) (Root, bool) {
	for _, r := range s.Roots() {
		if filepath.Dir(folder) == filepath.Clean(r.Dir) {
			return r, true
		}
	}
	return Root{}, false
}

// handMade finds the enabled RSS rule meant for a folder's show, and the
// groups it takes. Rules are written against release titles, so each is tried
// on titles made from the folder name: "[Group] Name - 01 (1080p)".
func handMade(rules []config.Rule, name string) (string, []string) {
	for _, r := range rules {
		if !r.Enabled || strings.TrimSpace(r.MustContain) == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + r.MustContain)
		if err != nil {
			continue
		}
		var groups []string
		for _, g := range knownGroups {
			for _, t := range []string{"[" + g + "] " + name + " - 01 (1080p)", "[" + g + "] " + name + " - S01E01 [1080p]"} {
				if re.MatchString(t) || re.MatchString(strings.ReplaceAll(t, " ", ".")) {
					groups = append(groups, g)
					break
				}
			}
		}
		if len(groups) > 0 && len(groups) < len(knownGroups) {
			return r.Name, groups
		}
		if len(groups) == len(knownGroups) {
			return r.Name, nil // the rule takes any group
		}
	}
	return "", nil
}

// ScanState says how the background folder scan is going.
type ScanState struct {
	Running  bool      `json:"running"`
	Done     int       `json:"done"`
	Total    int       `json:"total"`
	Finished time.Time `json:"finished"`
}

// Scanning returns the folder scan's state.
func (s *Service) Scanning() ScanState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scan
}

// List returns every show folder, by name, with what each held at the last
// scan. Folders are not read here: the scan runs in the background (at start,
// every half hour, and on Rescan), and opening a show reads that folder.
func (s *Service) List() ([]Show, error) {
	recs, err := s.Store.All()
	if err != nil {
		return nil, err
	}
	sums, err := s.Store.Summaries()
	if err != nil {
		return nil, err
	}
	rules := s.Cfg.Get().Rules
	var out []Show
	for _, root := range s.Roots() {
		names, err := library.Folders(root.Dir)
		if err != nil {
			return nil, fmt.Errorf("library folder %s: %w", root.Dir, err)
		}
		for _, n := range names {
			folder := filepath.Join(root.Dir, n)
			sh := s.blank(root, folder, recs)
			sum, ok := sums[folder]
			if !ok {
				sum = Summary{Seasons: []SeasonSummary{}}
				sh.Unscanned = true
			}
			sh.Summary = sum
			sh.HandMade, _ = handMade(rules, sh.Name)
			out = append(out, sh)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func (s *Service) blank(root Root, folder string, recs map[string]Record) Show {
	sh := Show{Folder: folder, Name: filepath.Base(folder), Root: root.Dir, Category: root.Category}
	if r, ok := recs[folder]; ok {
		sh.Record = r
	} else {
		sh.Record = Record{Folder: folder, Settings: Settings{Groups: []string{}, Titles: []string{}, Quality: "1080p"}}
	}
	return sh
}

func (s *Service) show(root Root, folder string, recs map[string]Record, rules []config.Rule) Show {
	sh := s.blank(root, folder, recs)
	files, _ := ScanFolder(folder)
	sh.Summary = Summarize(files)
	_ = s.Store.PutSummary(folder, sh.Summary)
	sh.HandMade, _ = handMade(rules, sh.Name)
	return sh
}

// StartScan reads every show folder again in the background; false when a
// scan is already running.
func (s *Service) StartScan() bool {
	s.mu.Lock()
	if s.scan.Running {
		s.mu.Unlock()
		return false
	}
	s.scan.Running, s.scan.Done, s.scan.Total = true, 0, 0
	s.mu.Unlock()
	go s.scanAll()
	return true
}

// ScanNow reads every show folder and waits for it.
func (s *Service) ScanNow() {
	s.mu.Lock()
	s.scan.Running = true
	s.mu.Unlock()
	s.scanAll()
}

func (s *Service) scanAll() {
	defer func() {
		s.mu.Lock()
		s.scan.Running, s.scan.Finished = false, s.Now()
		s.mu.Unlock()
	}()
	var folders []string
	for _, root := range s.Roots() {
		names, err := library.Folders(root.Dir)
		if err != nil {
			s.Log.Printf("shows: library folder %s: %v", root.Dir, err)
			continue
		}
		for _, n := range names {
			folders = append(folders, filepath.Join(root.Dir, n))
		}
	}
	s.mu.Lock()
	s.scan.Total = len(folders)
	s.mu.Unlock()
	present := map[string]bool{}
	for i, f := range folders {
		present[f] = true
		files, err := ScanFolder(f)
		if err == nil {
			_ = s.Store.PutSummary(f, Summarize(files))
		}
		s.mu.Lock()
		s.scan.Done = i + 1
		s.mu.Unlock()
	}
	if len(folders) > 0 {
		if sums, err := s.Store.Summaries(); err == nil {
			for f := range sums {
				if !present[f] {
					_ = s.Store.DropSummary(f)
				}
			}
		}
	}
}

func (s *Service) forget() {}

// Get returns one show folder, read now.
func (s *Service) Get(folder string) (Show, []DiskFile, error) {
	root, ok := s.rootOf(folder)
	if !ok {
		return Show{}, nil, errors.New("not a show folder in a library folder")
	}
	if fi, err := os.Stat(folder); err != nil || !fi.IsDir() {
		return Show{}, nil, errors.New("the folder is not there")
	}
	recs := map[string]Record{}
	if r, err := s.Store.Get(folder); err == nil {
		recs[folder] = r
	} else if !errors.Is(err, ErrNoRecord) {
		return Show{}, nil, err
	}
	files, err := ScanFolder(folder)
	if err != nil {
		return Show{}, nil, err
	}
	sh := s.show(root, folder, recs, s.Cfg.Get().Rules)
	return sh, files, nil
}

// knownGroups are release groups recognised in hand-made rules, to start a
// group order from.
var knownGroups = []string{"SubsPlease", "Erai-raws", "Judas", "Trix", "ToonsHub", "ASW", "EMBER", "Yameii", "VARYG", "DKB", "Anime Time", "HorribleSubs", "Exiled-Destiny", "Commie", "GJM", "Cyan", "Kaleido-subs", "Okay-Subs", "LostYears"}

// DefaultGroups is the group order a show starts with: the groups its
// hand-made rule takes, else SubsPlease.
func (s *Service) DefaultGroups(sh Show) []string {
	if _, groups := handMade(s.Cfg.Get().Rules, sh.Name); len(groups) > 0 {
		return groups
	}
	return []string{"SubsPlease"}
}

// Save stores a show's settings. Following a show that was not followed
// starts it at the newest episode on disk, unless the settings say where.
func (s *Service) Save(folder string, st Settings, fromStart bool) (Show, error) {
	sh, _, err := s.Get(folder)
	if err != nil {
		return Show{}, err
	}
	if st.Followed && (!sh.Followed || sh.Record.Added.IsZero()) && st.FromEpisode == 0 && !fromStart {
		st.FromEpisode = sh.Summary.Latest
	}
	if fromStart {
		st.FromEpisode = 0
	}
	if len(st.Groups) == 0 {
		st.Groups = s.DefaultGroups(sh)
	}
	if err := s.Store.Put(folder, st); err != nil {
		return Show{}, err
	}
	s.forget()
	sh, _, err = s.Get(folder)
	return sh, err
}

// names are the titles that belong to a show: its folder name and its other titles.
func names(sh Show) []string {
	return append([]string{sh.Name}, sh.Titles...)
}

func matchesShow(sh Show, r library.Release) bool {
	want := map[string]bool{}
	for _, n := range names(sh) {
		want[squash(n)] = true
	}
	for _, t := range append([]string{r.Title}, r.Aliases...) {
		if want[squash(t)] {
			return true
		}
	}
	return false
}

func seenOf(r Release) Seen {
	return Seen{Title: r.Title, Link: r.Link, Group: r.Parsed.Group, Res: r.Parsed.Res, Season: r.Parsed.Season, Marked: r.Parsed.Marked, Episode: r.Parsed.Episode, Size: r.Size, Seeders: r.Seeders}
}

// search asks nyaa under each of a show's titles (at most three) and keeps the
// releases that are this show.
func (s *Service) search(ctx context.Context, sh Show) ([]Release, error) {
	var out []Release
	have := map[string]bool{}
	var firstErr error
	for i, n := range names(sh) {
		if i >= 3 {
			break
		}
		res, err := s.Nyaa.Search(ctx, n)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, r := range res {
			if r.OK && !have[r.Title] && matchesShow(sh, r.Parsed) {
				have[r.Title] = true
				out = append(out, r)
			}
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// Active is an episode a pipeline job is working on.
type Active struct {
	Season  int    `json:"season"` // 0: the release names no season
	Episode int    `json:"episode"`
	State   string `json:"state"`
	Name    string `json:"name"`
}

func jobKey(sh Show, j job.Job) (key, bool) {
	if j.Category != sh.Category {
		return key{}, false
	}
	name := j.Name
	if !videoExt[strings.ToLower(filepath.Ext(name))] {
		name += ".mkv"
	}
	r, ok := library.Parse(name)
	if !ok || !matchesShow(sh, r) {
		return key{}, false
	}
	return keyOf(r.Season, r.Marked, r.Episode), true
}

func activeState(st job.State) bool {
	return st != job.Imported && st != job.Cleaned && st != job.Failed
}

// Activity lists the show's episodes that jobs are working on, and the
// episodes whose only jobs failed.
func (s *Service) Activity(sh Show) (active []Active, failed map[key]bool) {
	failed = map[key]bool{}
	if s.Jobs == nil {
		return nil, failed
	}
	busy := map[key]bool{}
	for _, j := range s.Jobs() {
		k, ok := jobKey(sh, j)
		if !ok {
			continue
		}
		if activeState(j.State) {
			busy[k] = true
			active = append(active, Active{Season: k.season, Episode: k.episode, State: string(j.State), Name: j.Name})
		} else if j.State == job.Failed {
			failed[k] = true
		}
	}
	for k := range busy {
		delete(failed, k)
	}
	return active, failed
}

// ActiveCounts says, per show folder, how many episodes jobs are working on.
func (s *Service) ActiveCounts(list []Show) map[string]int {
	out := map[string]int{}
	if s.Jobs == nil {
		return out
	}
	byTitle := map[string]Show{}
	for _, sh := range list {
		for _, n := range names(sh) {
			byTitle[sh.Category+"\x00"+squash(n)] = sh
		}
	}
	for _, j := range s.Jobs() {
		if !activeState(j.State) {
			continue
		}
		name := j.Name
		if !videoExt[strings.ToLower(filepath.Ext(name))] {
			name += ".mkv"
		}
		r, ok := library.Parse(name)
		if !ok {
			continue
		}
		for _, t := range append([]string{r.Title}, r.Aliases...) {
			if sh, ok := byTitle[j.Category+"\x00"+squash(t)]; ok {
				out[sh.Folder]++
				break
			}
		}
	}
	return out
}

// Check looks for new episodes of a followed show and grabs what Pick picks.
func (s *Service) Check(ctx context.Context, folder string) ([]string, error) {
	s.busy.Lock()
	defer s.busy.Unlock()
	sh, files, err := s.Get(folder)
	if err != nil {
		return nil, err
	}
	if !sh.Followed {
		return nil, errors.New("the show is not followed")
	}
	res, err := s.search(ctx, sh)
	_ = s.Store.SetChecked(folder, err)
	if err != nil {
		return nil, err
	}
	var list []Seen
	for _, r := range res {
		list = append(list, seenOf(r))
	}
	if err := s.Store.AddSeen(folder, list); err != nil {
		return nil, err
	}
	seen, err := s.Store.SeenFor(folder)
	if err != nil {
		return nil, err
	}
	// A grabbed episode whose download failed, and that is not in the folder,
	// is open again; the release that failed is not tried again.
	if _, failed := s.Activity(sh); len(failed) > 0 {
		changed := false
		for _, r := range seen {
			k := keyOf(r.Season, r.Marked, r.Episode)
			if !r.Grabbed.IsZero() && failed[k] && !Has(files, r.Season, r.Marked && r.Season > 1, r.Episode) {
				_ = s.Store.MarkFailed(folder, r.Title)
				changed = true
			}
		}
		if changed {
			if seen, err = s.Store.SeenFor(folder); err != nil {
				return nil, err
			}
		}
	}
	var started []string
	for _, p := range Pick(sh.Settings, files, seen, s.Now(), s.Grace) {
		if err := s.submit(ctx, sh, p.Title, p.Link); err != nil {
			s.Log.Printf("shows: %s: grabbing %s: %v", sh.Name, p.Title, err)
			continue
		}
		_ = s.Store.MarkGrabbed(folder, p.Title)
		s.Log.Printf("shows: %s: grabbed %s", sh.Name, p.Title)
		started = append(started, p.Title)
	}
	if sh.FillGaps && sh.Summary.Gaps > 0 && s.Now().Sub(sh.GapsChecked) >= 24*time.Hour {
		filled, err := s.fillGaps(ctx, sh, files, res)
		if err != nil {
			s.Log.Printf("shows: %s: gaps: %v", sh.Name, err)
		}
		_ = s.Store.SetGapsChecked(folder)
		started = append(started, filled...)
	}
	s.forget()
	return started, nil
}

// submit sends a release to the pipeline in the folder's category.
func (s *Service) submit(ctx context.Context, sh Show, title, link string) error {
	switch {
	case strings.HasPrefix(link, "magnet:"):
		_, err := s.Engine.Submit(pipeline.SubmitRequest{Source: "rss", Category: sh.Category, Magnet: link})
		return err
	case strings.HasPrefix(link, "http://"), strings.HasPrefix(link, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
		if err != nil {
			return err
		}
		resp, err := s.HTTP.Do(req)
		if err != nil {
			return errors.New("the torrent could not be fetched")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("the torrent link answered HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		if err != nil {
			return err
		}
		if len(data) == 0 || data[0] != 'd' {
			return errors.New("the link is not a torrent")
		}
		_, err = s.Engine.Submit(pipeline.SubmitRequest{Source: "rss", Category: sh.Category, Torrent: data})
		return err
	}
	return fmt.Errorf("%s has no usable link", title)
}

// Result is a search result with what it would mean for the show.
type Result struct {
	Release
	Group    string `json:"group"`
	Res      string `json:"res"`
	Season   int    `json:"season"`
	Marked   bool   `json:"marked"`
	Episode  int    `json:"episode"`
	Warning  string `json:"warning,omitempty"`
	ThisShow bool   `json:"thisShow"`
}

// Search runs an interactive search for a show; query defaults to its folder
// name, and episode, when set, narrows it to one episode.
func (s *Service) Search(ctx context.Context, folder, query string, episode int) ([]Result, error) {
	sh, files, err := s.Get(folder)
	if err != nil {
		return nil, err
	}
	// Search the show, not the episode: releases number episodes their own way
	// (a later cour starts again at 1), so the episode is put first, not required.
	q := strings.TrimSpace(query)
	if q == "" {
		q = sh.Name
	}
	res, err := s.Nyaa.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	st := sh.Settings
	if len(st.Groups) == 0 {
		st.Groups = s.DefaultGroups(sh)
	}
	var out []Result
	for _, r := range res {
		sn := seenOf(r)
		x := Result{Release: r, Group: sn.Group, Res: sn.Res, Season: sn.Season, Marked: sn.Marked, Episode: sn.Episode}
		x.ThisShow = r.OK && matchesShow(sh, r.Parsed)
		if !x.ThisShow {
			x.Warning = "the title is not this show's folder name or other titles"
		} else {
			x.Warning = Warning(st, files, sh.Summary.Season, sn)
		}
		out = append(out, x)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (episode > 0 && a.Episode == episode) != (episode > 0 && b.Episode == episode) {
			return episode > 0 && a.Episode == episode
		}
		if a.ThisShow != b.ThisShow {
			return a.ThisShow
		}
		return (a.Warning == "") && (b.Warning != "")
	})
	return out, nil
}

// Grab starts one release by hand for a show; the title must be one the last
// search returned, so the link comes from nyaa again.
func (s *Service) Grab(ctx context.Context, folder, title string) error {
	sh, _, err := s.Get(folder)
	if err != nil {
		return err
	}
	res, err := s.Nyaa.Search(ctx, title)
	if err != nil {
		return err
	}
	for _, r := range res {
		if r.Title == title {
			if err := s.submit(ctx, sh, r.Title, r.Link); err != nil {
				return err
			}
			if sh.Followed {
				_ = s.Store.AddSeen(folder, []Seen{seenOf(r)})
				_ = s.Store.MarkGrabbed(folder, r.Title)
			}
			s.Log.Printf("shows: %s: grabbed %s by hand", sh.Name, r.Title)
			return nil
		}
	}
	return errors.New("nyaa no longer lists that release")
}

// SearchGaps grabs the best release for each missing number in a folder.
func (s *Service) SearchGaps(ctx context.Context, folder string) ([]string, error) {
	s.busy.Lock()
	defer s.busy.Unlock()
	sh, files, err := s.Get(folder)
	if err != nil {
		return nil, err
	}
	return s.fillGaps(ctx, sh, files, nil)
}

// maxGapQueries caps how many single-episode searches one gap fill makes.
const maxGapQueries = 4

// fillGaps grabs releases for a folder's gaps: from what a show search finds
// (have, when the caller already searched), then by searching single episodes
// that are still missing. A followed show remembers what it grabbed.
func (s *Service) fillGaps(ctx context.Context, sh Show, files []DiskFile, have []Release) ([]string, error) {
	gaps := GapKeys(sh.Summary)
	if len(gaps) == 0 {
		return nil, nil
	}
	st := sh.Settings
	if len(st.Groups) == 0 {
		st.Groups = s.DefaultGroups(sh)
	}
	if have == nil {
		var err error
		if have, err = s.search(ctx, sh); err != nil {
			return nil, err
		}
	}
	found := map[key]bool{}
	byTitle := map[string]Release{}
	for _, r := range have {
		byTitle[r.Title] = r
		found[keyOf(r.Parsed.Season, r.Parsed.Marked, r.Parsed.Episode)] = true
	}
	// Episodes the show search did not reach: search them one by one.
	var keys []key
	for k := range gaps {
		if !found[k] {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].season != keys[j].season {
			return keys[i].season < keys[j].season
		}
		return keys[i].episode < keys[j].episode
	})
	for i, k := range keys {
		if i >= maxGapQueries || ctx.Err() != nil {
			break
		}
		q := fmt.Sprintf("%s %02d", sh.Name, k.episode)
		if k.season > 1 {
			q = fmt.Sprintf("%s S%02dE%02d", sh.Name, k.season, k.episode)
		}
		res, err := s.Nyaa.Search(ctx, q)
		if err != nil {
			continue
		}
		for _, r := range res {
			if r.OK && matchesShow(sh, r.Parsed) {
				byTitle[r.Title] = r
			}
		}
	}
	var list []Seen
	for _, r := range byTitle {
		list = append(list, seenOf(r))
	}
	seen := list
	if sh.Followed {
		if err := s.Store.AddSeen(sh.Folder, list); err != nil {
			return nil, err
		}
		var err error
		if seen, err = s.Store.SeenFor(sh.Folder); err != nil {
			return nil, err
		}
	}
	var started []string
	for _, p := range PickGaps(st, files, seen, gaps) {
		if err := s.submit(ctx, sh, p.Title, p.Link); err != nil {
			s.Log.Printf("shows: %s: grabbing %s: %v", sh.Name, p.Title, err)
			continue
		}
		if sh.Followed {
			_ = s.Store.MarkGrabbed(sh.Folder, p.Title)
		}
		s.Log.Printf("shows: %s: filled gap with %s", sh.Name, p.Title)
		started = append(started, p.Title)
	}
	return started, nil
}

// Discovered is a show found by Add New's search.
type Discovered struct {
	Title    string   `json:"title"`
	Groups   []string `json:"groups"`
	Res      []string `json:"res"`
	Latest   int      `json:"latest"`
	Releases int      `json:"releases"`
	Newest   string   `json:"newest"` // when the newest release came out
	Folder   string   `json:"folder,omitempty"`
}

// Discover searches nyaa for shows by any title, one entry per release title.
func (s *Service) Discover(ctx context.Context, q string) ([]Discovered, error) {
	res, err := s.Nyaa.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	all, _ := s.List()
	byName := map[string]string{}
	for _, sh := range all {
		for _, n := range names(sh) {
			byName[squash(n)] = sh.Folder
		}
	}
	idx := map[string]*Discovered{}
	var order []string
	newest := map[string]time.Time{}
	for _, r := range res {
		if !r.OK {
			continue
		}
		k := squash(r.Parsed.Title)
		d := idx[k]
		if d == nil {
			d = &Discovered{Title: r.Parsed.Title, Groups: []string{}, Res: []string{}, Folder: byName[k]}
			idx[k] = d
			order = append(order, k)
		}
		d.Releases++
		if r.Parsed.Episode > d.Latest {
			d.Latest = r.Parsed.Episode
		}
		if r.Parsed.Group != "" && !contains(d.Groups, r.Parsed.Group) {
			d.Groups = append(d.Groups, r.Parsed.Group)
		}
		if r.Parsed.Res != "" && !contains(d.Res, r.Parsed.Res) {
			d.Res = append(d.Res, r.Parsed.Res)
		}
		if r.Pub.After(newest[k]) {
			newest[k] = r.Pub
			d.Newest = r.Pub.UTC().Format(time.RFC3339)
		}
	}
	out := make([]Discovered, 0, len(order))
	for _, k := range order {
		out = append(out, *idx[k])
	}
	return out, nil
}

func contains(list []string, x string) bool {
	for _, y := range list {
		if strings.EqualFold(x, y) {
			return true
		}
	}
	return false
}

var unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// Add makes a show folder (or uses an existing one), follows it and checks
// nyaa for it at once. fromStart grabs every episode released so far.
func (s *Service) Add(ctx context.Context, rootDir, name string, st Settings, fromStart bool) (Show, []string, error) {
	name = strings.TrimSpace(unsafeName.ReplaceAllString(name, ""))
	name = strings.Trim(name, ". ")
	if name == "" {
		return Show{}, nil, errors.New("give the folder a name")
	}
	var root Root
	for _, r := range s.Roots() {
		if filepath.Clean(r.Dir) == filepath.Clean(rootDir) {
			root = r
		}
	}
	if root.Dir == "" {
		return Show{}, nil, errors.New("that is not a library folder")
	}
	folder := filepath.Join(root.Dir, name)
	if err := os.MkdirAll(folder, 0o777); err != nil {
		return Show{}, nil, err
	}
	_ = os.Chmod(folder, 0o777)
	st.Followed = true
	sh, err := s.Save(folder, st, fromStart)
	if err != nil {
		return Show{}, nil, err
	}
	s.Log.Printf("shows: added %s", folder)
	started, err := s.Check(ctx, folder)
	if err != nil {
		return sh, nil, fmt.Errorf("added; the first check failed: %w", err)
	}
	return sh, started, nil
}

// RefreshAniList looks a show up on AniList again (by its id, or its name).
func (s *Service) RefreshAniList(ctx context.Context, folder string) (*AniInfo, error) {
	rec, err := s.Store.Get(folder)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return nil, err
	}
	var a *AniInfo
	if rec.AniListID > 0 {
		a, err = s.AniList.Get(ctx, rec.AniListID)
	} else {
		a, err = s.AniList.Find(ctx, filepath.Base(folder))
	}
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetAniList(folder, a); err != nil {
		return nil, err
	}
	s.forget()
	return a, nil
}

// Claims says a release belongs to a followed show in a group of its order, so
// hand-made RSS rules leave it to the show and nothing is grabbed twice.
func (s *Service) Claims(title string) string {
	r, ok := library.Parse(title + ".mkv")
	if !ok {
		return ""
	}
	recs, err := s.Store.All()
	if err != nil {
		return ""
	}
	for folder, rec := range recs {
		sh := Show{Folder: folder, Name: filepath.Base(folder), Record: rec}
		if rec.Followed && matchesShow(sh, r) && groupRank(rec.Groups, r.Group) >= 0 {
			return sh.Name
		}
	}
	return ""
}

// FolderTitles maps each show folder in a library folder to its other titles,
// for the simple importer.
func (s *Service) FolderTitles(root string) map[string][]string {
	recs, err := s.Store.All()
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for f, r := range recs {
		if filepath.Dir(f) == filepath.Clean(root) && len(r.Titles) > 0 {
			out[filepath.Base(f)] = r.Titles
		}
	}
	return out
}

// Run checks followed shows, each about every s.Every, and refreshes their
// AniList status daily, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	s.StartScan()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if st := s.Scanning(); !st.Running && s.Now().Sub(st.Finished) >= 30*time.Minute {
			s.StartScan()
		}
		recs, err := s.Store.All()
		if err != nil {
			continue
		}
		for folder, r := range recs {
			if ctx.Err() != nil {
				return
			}
			if !r.Followed {
				continue
			}
			if s.Now().Sub(r.Checked) >= s.Every {
				if _, err := s.Check(ctx, folder); err != nil {
					s.Log.Printf("shows: %s: %v", filepath.Base(folder), err)
				}
				time.Sleep(3 * time.Second) // gentle with nyaa
			}
			if s.Now().Sub(r.AniListChecked) >= 24*time.Hour {
				if _, err := s.RefreshAniList(ctx, folder); err != nil {
					s.Log.Printf("shows: %s: AniList: %v", filepath.Base(folder), err)
				}
			}
		}
	}
}
