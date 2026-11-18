// Package decision decides which release to grab, the way Radarr and Sonarr
// do: a quality profile says which qualities are wanted and in what order,
// custom formats score releases, and every release that is turned down says why.
//
// It is pure: no database, no network. Profiles and formats are stored by
// Store; releases come from the indexers.
package decision

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/parser"
)

// Item is one step of a profile's quality ladder: a single quality or a group
// of qualities that count as equal.
type Item struct {
	Name      string   `json:"name"`
	Qualities []string `json:"qualities"` // quality names, e.g. "WEBDL-1080p"
	Allowed   bool     `json:"allowed"`
}

// Profile is a quality profile.
type Profile struct {
	ID                int64          `json:"id"`
	Name              string         `json:"name"`
	Items             []Item         `json:"items"` // lowest first
	Cutoff            string         `json:"cutoff"`
	UpgradesAllowed   bool           `json:"upgradesAllowed"`
	MinFormatScore    int            `json:"minFormatScore"`
	CutoffFormatScore int            `json:"cutoffFormatScore"`
	MinUpgradeScore   int            `json:"minUpgradeFormatScore"`
	Scores            map[string]int `json:"scores"`             // custom format name -> score
	Language          string         `json:"language,omitempty"` // "" or "Any": any; "Original": skipped here
	Source            string         `json:"source,omitempty"`   // where it was imported from
}

// Spec is one condition of a custom format.
type Spec struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`  // title, group, edition, source, resolution, modifier, language, size, releaseType, indexerFlag, year
	Value    string `json:"value"` // a regex for title/group/edition; a name for the others
	Min      int64  `json:"min,omitempty"`
	Max      int64  `json:"max,omitempty"`
	Negate   bool   `json:"negate"`
	Required bool   `json:"required"`
}

// Format is a custom format.
type Format struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Specs  []Spec `json:"specs"`
	Source string `json:"source,omitempty"`
}

// SizeLimit is a quality's size range in MB per minute of runtime (the arrs'
// quality definitions). Zero means no limit.
type SizeLimit struct {
	Min, Max float64
}

// Want is what a search is for.
type Want struct {
	Movie    bool
	Titles   []string // the title and its aliases
	Year     int
	Season   int
	Episodes []int // empty with Season set: a season pack is wanted
	Absolute []int
	Runtime  int // minutes per episode or movie, for size limits

	// What is on disk now, if anything.
	HaveQuality string
	HaveScore   int
	HaveVersion int
}

// Candidate is one release offered by an indexer.
type Candidate struct {
	Title     string
	Size      int64
	Indexer   string
	Priority  int // indexer priority, lower first
	Protocol  string
	Seeders   int
	Published time.Time
	Flags     []string // freeleech, internal...
	Blocked   bool     // on the blocklist
}

// Decision is the verdict on one candidate.
type Decision struct {
	Candidate Candidate   `json:"candidate"`
	Info      parser.Info `json:"-"`
	Quality   string      `json:"quality"`
	Rank      int         `json:"rank"` // position on the profile's ladder; higher is better
	Score     int         `json:"score"`
	Formats   []string    `json:"formats"`
	Rejected  []string    `json:"rejected"`
	Upgrade   bool        `json:"upgrade,omitempty"`
}

// Accepted reports whether nothing turned the release down.
func (d Decision) Accepted() bool { return len(d.Rejected) == 0 }

// Engine holds the profile, the formats and the size limits for one decision run.
type Engine struct {
	Profile Profile
	Formats []Format
	Sizes   map[string]SizeLimit
	Now     func() time.Time
	MinSeed int // torrents with fewer seeders are turned down (default 1)
}

// Rank returns a quality's position on the ladder (1 = lowest allowed or not),
// 0 when the profile does not list it, and whether it is allowed.
func (p Profile) Rank(quality string) (int, bool) {
	for i, it := range p.Items {
		for _, q := range it.Qualities {
			if strings.EqualFold(q, quality) {
				return i + 1, it.Allowed
			}
		}
		if len(it.Qualities) == 0 && strings.EqualFold(it.Name, quality) {
			return i + 1, it.Allowed
		}
	}
	return 0, false
}

// CutoffRank is the cutoff's position on the ladder.
func (p Profile) CutoffRank() int {
	for i, it := range p.Items {
		if strings.EqualFold(it.Name, p.Cutoff) {
			return i + 1
		}
		for _, q := range it.Qualities {
			if strings.EqualFold(q, p.Cutoff) {
				return i + 1
			}
		}
	}
	return len(p.Items)
}

// CutoffMet reports whether a file of this quality and score needs no upgrade.
func (p Profile) CutoffMet(quality string, score int) bool {
	r, _ := p.Rank(quality)
	if !p.UpgradesAllowed {
		return r > 0
	}
	return r >= p.CutoffRank() && score >= p.CutoffFormatScore
}

// Evaluate decides on one candidate.
func (e *Engine) Evaluate(w Want, c Candidate) Decision {
	in := parser.Parse(c.Title)
	d := Decision{Candidate: c, Info: in, Quality: in.Quality.Name()}
	no := func(f string, a ...any) { d.Rejected = append(d.Rejected, fmt.Sprintf(f, a...)) }

	rank, allowed := e.Profile.Rank(d.Quality)
	d.Rank = rank
	switch {
	case rank == 0:
		no("%s is not in the profile %s", d.Quality, e.Profile.Name)
	case !allowed:
		no("%s is not wanted by the profile %s", d.Quality, e.Profile.Name)
	}
	d.Formats, d.Score = e.Score(in, c)
	if d.Score < e.Profile.MinFormatScore {
		no("custom format score %d is below the profile's minimum %d", d.Score, e.Profile.MinFormatScore)
	}
	if c.Blocked {
		no("on the blocklist")
	}
	if !matchTitle(in, w.Titles) {
		no("title %q is not %s", in.Title, strings.Join(firstN(w.Titles, 3), " / "))
	}
	if w.Movie {
		if in.IsTV() {
			no("a TV release, not a movie")
		}
		if w.Year > 0 && in.Year > 0 && in.Year != w.Year && in.Year != w.Year-1 && in.Year != w.Year+1 {
			no("year %d, the movie is %d", in.Year, w.Year)
		}
	} else if r := e.numbering(w, in); r != "" {
		no("%s", r)
	}
	if lang := e.Profile.Language; lang != "" && lang != "Any" && lang != "Original" && !hasFold(in.Languages, lang) {
		no("language %s, the profile wants %s", strings.Join(in.Languages, ", "), lang)
	}
	if r := e.size(w, in, c); r != "" {
		no("%s", r)
	}
	if c.Protocol == "torrent" {
		min := e.MinSeed
		if min == 0 {
			min = 1
		}
		if c.Seeders < min {
			no("%d seeders", c.Seeders)
		}
	}
	// Against what is on disk.
	if w.HaveQuality != "" {
		haveRank, _ := e.Profile.Rank(w.HaveQuality)
		switch {
		case !e.Profile.UpgradesAllowed:
			no("upgrades are off in the profile, and a %s file is on disk", w.HaveQuality)
		case e.Profile.CutoffMet(w.HaveQuality, w.HaveScore):
			no("the file on disk (%s, score %d) already meets the cutoff", w.HaveQuality, w.HaveScore)
		case rank < haveRank:
			no("%s is lower than the %s on disk", d.Quality, w.HaveQuality)
		case rank == haveRank && in.Quality.Version > w.HaveVersion && w.HaveVersion > 0:
			d.Upgrade = true // a proper or repack of the same quality
		case rank == haveRank && d.Score < w.HaveScore+max(1, e.Profile.MinUpgradeScore):
			no("not better than the %s on disk (score %d vs %d)", w.HaveQuality, d.Score, w.HaveScore)
		default:
			d.Upgrade = true
		}
	}
	return d
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func firstN(v []string, n int) []string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

func hasFold(v []string, s string) bool {
	for _, x := range v {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func matchTitle(in parser.Info, titles []string) bool {
	if len(titles) == 0 {
		return true
	}
	got := library.Normalize(in.Title)
	if got == "" {
		return false
	}
	for _, t := range titles {
		n := library.Normalize(t)
		if n == got || strings.ReplaceAll(n, " ", "") == strings.ReplaceAll(got, " ", "") {
			return true
		}
		// "Show Name 2019" or "Show Name US" against "Show Name (2019)" or "Show Name (US)".
		if strings.HasPrefix(got, n+" ") || strings.HasPrefix(n, got+" ") {
			rest := strings.TrimPrefix(strings.TrimPrefix(got, n), strings.TrimPrefix(n, got))
			rest = strings.TrimSpace(rest)
			if len(rest) <= 4 {
				return true
			}
		}
	}
	return false
}

func (e *Engine) numbering(w Want, in parser.Info) string {
	if !in.IsTV() {
		return "not an episode or season"
	}
	if in.Daily {
		return "" // dated releases are matched by air date elsewhere
	}
	if len(in.Absolute) > 0 {
		want := w.Absolute
		if len(want) == 0 {
			want = w.Episodes
		}
		if len(want) == 0 {
			return "one episode (absolute " + parser.Episodes(in.Absolute) + "), a season is wanted"
		}
		for _, a := range want {
			for _, g := range in.Absolute {
				if a == g {
					return ""
				}
			}
		}
		return "episode " + parser.Episodes(in.Absolute) + " is not the one wanted"
	}
	sOK := false
	for _, s := range in.Seasons {
		if s == w.Season {
			sOK = true
		}
	}
	if !sOK {
		return fmt.Sprintf("season %s, season %d is wanted", parser.Episodes(in.Seasons), w.Season)
	}
	if len(w.Episodes) == 0 {
		if !in.FullSeason {
			return "a single episode, the whole season is wanted"
		}
		return ""
	}
	if in.FullSeason {
		return "a season pack, single episodes are wanted" // as the arrs do in an episode search
	}
	for _, x := range w.Episodes {
		for _, g := range in.Episodes {
			if x == g {
				return ""
			}
		}
	}
	return fmt.Sprintf("episode %s, episode %s is wanted", parser.Episodes(in.Episodes), parser.Episodes(w.Episodes))
}

func (e *Engine) size(w Want, in parser.Info, c Candidate) string {
	if c.Size <= 0 {
		return ""
	}
	mb := float64(c.Size) / (1 << 20)
	if mb < 20 && !in.IsTV() || mb < 5 {
		return fmt.Sprintf("only %.0f MB: a sample", mb)
	}
	lim, ok := e.Sizes[in.Quality.Name()]
	if !ok || w.Runtime <= 0 {
		return ""
	}
	n := 1
	if len(in.Episodes) > 1 {
		n = len(in.Episodes)
	}
	if in.FullSeason {
		return "" // the episode count of a pack is not known here
	}
	minutes := float64(w.Runtime * n)
	if lim.Min > 0 && mb < lim.Min*minutes {
		return fmt.Sprintf("%.0f MB is below the %s minimum of %.0f MB", mb, in.Quality.Name(), lim.Min*minutes)
	}
	if lim.Max > 0 && mb > lim.Max*minutes {
		return fmt.Sprintf("%.0f MB is above the %s maximum of %.0f MB", mb, in.Quality.Name(), lim.Max*minutes)
	}
	return ""
}

var (
	reMu    sync.Mutex
	reCache = map[string]*regexp2.Regexp{}
)

// compile reads a pattern with .NET rules, as the arrs (and the TRaSH Guides
// formats written for them) use: lookarounds and all. A pattern that does not
// compile never matches; BadRegex lists them.
func compile(p string) *regexp2.Regexp {
	reMu.Lock()
	defer reMu.Unlock()
	if re, ok := reCache[p]; ok {
		return re
	}
	re, err := regexp2.Compile(p, regexp2.IgnoreCase)
	if err != nil {
		re = nil
	} else {
		re.MatchTimeout = 100 * time.Millisecond
	}
	reCache[p] = re
	return re
}

func reMatch(re *regexp2.Regexp, s string) bool {
	if re == nil {
		return false
	}
	ok, err := re.MatchString(s)
	return err == nil && ok
}

// BadRegex lists the specs whose pattern does not compile, so they can be shown.
func BadRegex(f Format) []string {
	var out []string
	for _, s := range f.Specs {
		if s.Kind == "title" || s.Kind == "group" || s.Kind == "edition" {
			if compile(s.Value) == nil {
				out = append(out, s.Name)
			}
		}
	}
	return out
}

// Score returns the formats a release matches and the profile's total score for them.
func (e *Engine) Score(in parser.Info, c Candidate) ([]string, int) {
	var names []string
	total := 0
	for _, f := range e.Formats {
		if Matches(f, in, c) {
			names = append(names, f.Name)
			total += e.Profile.Scores[f.Name]
		}
	}
	return names, total
}

// Matches reports whether a release matches a custom format, by the arrs'
// rule: every required spec must pass, and of each kind with specs that are
// not required, at least one must pass.
func Matches(f Format, in parser.Info, c Candidate) bool {
	if len(f.Specs) == 0 {
		return false
	}
	byKind := map[string][2]int{} // kind -> [optional seen, optional passed]
	for _, s := range f.Specs {
		ok := specMatch(s, in, c)
		if s.Negate {
			ok = !ok
		}
		if s.Required {
			if !ok {
				return false
			}
			continue
		}
		k := byKind[s.Kind]
		k[0]++
		if ok {
			k[1]++
		}
		byKind[s.Kind] = k
	}
	for _, k := range byKind {
		if k[0] > 0 && k[1] == 0 {
			return false
		}
	}
	return true
}

func specMatch(s Spec, in parser.Info, c Candidate) bool {
	switch s.Kind {
	case "title":
		return reMatch(compile(s.Value), c.Title)
	case "group":
		return in.Group != "" && reMatch(compile(s.Value), in.Group)
	case "edition":
		return in.Edition != "" && reMatch(compile(s.Value), in.Edition)
	case "source":
		return sourceIs(in.Quality, s.Value)
	case "resolution":
		return fmt.Sprint(in.Quality.Resolution) == strings.TrimSuffix(strings.ToLower(s.Value), "p")
	case "modifier":
		return modifierIs(in.Quality, s.Value)
	case "language":
		if strings.EqualFold(s.Value, "original") || strings.EqualFold(s.Value, "any") {
			return true
		}
		return hasFold(in.Languages, s.Value)
	case "size":
		gb := float64(c.Size) / (1 << 30)
		return (s.Min == 0 || gb > float64(s.Min)) && (s.Max == 0 || gb <= float64(s.Max))
	case "releaseType":
		switch strings.ToLower(s.Value) {
		case "seasonpack":
			return in.FullSeason
		case "multiepisode":
			return len(in.Episodes) > 1
		case "singleepisode":
			return len(in.Episodes) == 1 || len(in.Absolute) == 1
		}
	case "indexerFlag":
		return hasFold(c.Flags, s.Value)
	case "year":
		return in.Year > 0 && (s.Min == 0 || int64(in.Year) >= s.Min) && (s.Max == 0 || int64(in.Year) <= s.Max)
	}
	return false
}

func sourceIs(q parser.Quality, v string) bool {
	switch strings.ToLower(v) {
	case "cam":
		return q.Source == parser.SourceCAM
	case "telesync":
		return q.Source == parser.SourceTelesync
	case "telecine":
		return q.Source == parser.SourceTelecine
	case "workprint":
		return q.Source == parser.SourceWorkprint
	case "dvd":
		return q.Source == parser.SourceDVD
	case "tv", "television":
		return q.Source == parser.SourceTV && q.Modifier != parser.ModRawHD
	case "televisionraw", "rawhd":
		return q.Source == parser.SourceTV && q.Modifier == parser.ModRawHD
	case "webdl", "web":
		return q.Source == parser.SourceWEBDL
	case "webrip":
		return q.Source == parser.SourceWEBRip
	case "bluray":
		return q.Source == parser.SourceBluray
	case "blurayraw":
		return q.Source == parser.SourceBluray && (q.Modifier == parser.ModRemux || q.Modifier == parser.ModBRDisk)
	case "unknown":
		return q.Source == parser.SourceUnknown
	}
	return false
}

func modifierIs(q parser.Quality, v string) bool {
	switch strings.ToLower(v) {
	case "none":
		return q.Modifier == parser.ModNone
	case "regional":
		return q.Modifier == parser.ModRegional
	case "screener":
		return q.Source == parser.SourceScreener
	case "rawhd":
		return q.Modifier == parser.ModRawHD
	case "brdisk":
		return q.Modifier == parser.ModBRDisk
	case "remux":
		return q.Modifier == parser.ModRemux
	}
	return false
}

// Decide evaluates every candidate and returns them best first: accepted
// before rejected, then by quality, custom format score, revision,
// whole seasons and more episodes, indexer priority, seeders, and age.
func (e *Engine) Decide(w Want, cs []Candidate) []Decision {
	out := make([]Decision, len(cs))
	for i, c := range cs {
		out[i] = e.Evaluate(w, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return Better(out[i], out[j], w) })
	return out
}

// Better orders two decisions the way the arrs pick between releases.
func Better(a, b Decision, w Want) bool {
	if a.Accepted() != b.Accepted() {
		return a.Accepted()
	}
	if a.Rank != b.Rank {
		return a.Rank > b.Rank
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Info.Quality.Version != b.Info.Quality.Version {
		return a.Info.Quality.Version > b.Info.Quality.Version
	}
	if a.Info.Quality.Real != b.Info.Quality.Real {
		return a.Info.Quality.Real > b.Info.Quality.Real
	}
	if a.Info.FullSeason != b.Info.FullSeason {
		return a.Info.FullSeason // whole seasons first, as Sonarr does
	}
	if len(a.Info.Episodes) != len(b.Info.Episodes) {
		return len(a.Info.Episodes) > len(b.Info.Episodes)
	}
	if a.Candidate.Priority != b.Candidate.Priority {
		return a.Candidate.Priority < b.Candidate.Priority
	}
	if a.Candidate.Protocol == "torrent" && b.Candidate.Protocol == "torrent" && a.Candidate.Seeders != b.Candidate.Seeders {
		return a.Candidate.Seeders > b.Candidate.Seeders
	}
	if a.Candidate.Protocol == "usenet" && b.Candidate.Protocol == "usenet" {
		if x, y := ageBucket(a.Candidate.Published), ageBucket(b.Candidate.Published); x != y {
			return x > y
		}
	}
	return a.Candidate.Size > b.Candidate.Size
}

// ageBucket groups usenet ages the way the arrs compare them: under an hour,
// a day, a week, older.
func ageBucket(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	h := time.Since(t).Hours()
	switch {
	case h < 1:
		return 1000
	case h <= 24:
		return 100
	case h <= 24*7:
		return 10
	}
	return 1
}
