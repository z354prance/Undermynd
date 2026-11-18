// Package shadow runs HyveMynd's decision engine beside Sonarr and Radarr
// (roadmap F3): for every grab an arr makes, it searches HyveMynd's own
// indexers for the same thing, decides with the profile of the same name, and
// records whether it would have picked the same release. Nothing is grabbed.
package shadow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/indexers"
	"github.com/z354prance/undermynd/internal/library"
	"github.com/z354prance/undermynd/internal/parser"
)

// Searcher is the part of the indexer service shadow mode uses.
type Searcher interface {
	Search(ctx context.Context, q indexers.Query, protocol string, ids ...int64) (indexers.SearchResult, error)
}

// Verdicts.
const (
	Same     = "same"     // HyveMynd's first pick is the release the arr grabbed
	Equal    = "equal"    // another release of the same quality and score
	Better   = "better"   // HyveMynd's pick ranks above the arr's by its own rules
	Worse    = "worse"    // the arr's pick ranks above HyveMynd's (not found, or ranked lower)
	Rejected = "rejected" // HyveMynd would turn the arr's pick down
	None     = "none"     // nothing acceptable found
	Skipped  = "skipped"  // not compared (no profile, no indexers, over the budget)
)

// Service watches the arrs' grabs.
type Service struct {
	DB       *sql.DB
	Cfg      *config.Config
	Profiles *decision.Store
	Search   Searcher
	Arr      func(category string) *arr.Client
	Log      *log.Logger
	Now      func() time.Time
}

// Row is one comparison.
type Row struct {
	ID         int64     `json:"id"`
	Category   string    `json:"category"`
	At         time.Time `json:"at"`
	Item       string    `json:"item"`
	Profile    string    `json:"profile"`
	ArrTitle   string    `json:"arrTitle"`
	ArrQuality string    `json:"arrQuality"`
	ArrIndexer string    `json:"arrIndexer"`
	OurTitle   string    `json:"ourTitle"`
	OurQuality string    `json:"ourQuality"`
	OurScore   int       `json:"ourScore"`
	ArrScore   int       `json:"arrScore"`
	Verdict    string    `json:"verdict"`
	Note       string    `json:"note"`
}

// Run checks the arrs' history every ten minutes while shadow mode is on.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		if s.Cfg.Get().Shadow.Enabled {
			if n, err := s.Check(ctx); err != nil {
				s.Log.Printf("shadow: %v", err)
			} else if n > 0 {
				s.Log.Printf("shadow: compared %d new grab(s)", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) budget() int {
	n := s.Cfg.Get().Shadow.PerHour
	if n <= 0 {
		n = 20
	}
	var used int
	s.DB.QueryRow(`SELECT COUNT(*) FROM shadow WHERE at >= ? AND verdict != ?`, db.Stamp(s.Now().Add(-time.Hour)), Skipped).Scan(&used)
	return n - used
}

// Check compares every new grab of every Sonarr and Radarr category and
// returns how many it recorded.
func (s *Service) Check(ctx context.Context) (int, error) {
	n := 0
	var errs []string
	for _, c := range s.Cfg.Get().Categories {
		if c.ArrURL == "" || (c.Kind != "sonarr" && c.Kind != "radarr") {
			continue
		}
		k, err := s.category(ctx, c)
		n += k
		if err != nil {
			errs = append(errs, c.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return n, errors.New(strings.Join(errs, "; "))
	}
	return n, nil
}

func (s *Service) seen(key string) bool {
	var id int64
	return s.DB.QueryRow(`SELECT id FROM shadow WHERE grab_key = ?`, key).Scan(&id) == nil
}

func (s *Service) category(ctx context.Context, c config.Category) (int, error) {
	cl := s.Arr(c.Name)
	grabs, err := cl.RecentGrabs(ctx, 30)
	if err != nil {
		return 0, err
	}
	var names map[int]string
	n := 0
	first := true
	var any int
	s.DB.QueryRow(`SELECT COUNT(*) FROM shadow WHERE category = ?`, c.Name).Scan(&any)
	for i := len(grabs) - 1; i >= 0; i-- { // oldest first
		g := grabs[i]
		if s.seen(g.Key) {
			continue
		}
		// The first time a category is checked, only the latest few grabs are
		// compared: the rest happened before shadow mode was on.
		if any == 0 && first && i >= 5 {
			continue
		}
		first = false
		if names == nil {
			if names, err = cl.ProfileNames(ctx); err != nil {
				return n, err
			}
		}
		row := s.compare(ctx, c, g, names[g.ProfileID])
		if err := s.save(row, g.Key); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// profileFor finds HyveMynd's profile for an arr profile name: the one
// imported from this category, else one of the same name.
func (s *Service) profileFor(category, name string) (decision.Profile, bool) {
	if name == "" {
		return decision.Profile{}, false
	}
	ps, err := s.Profiles.Profiles()
	if err != nil {
		return decision.Profile{}, false
	}
	var plain *decision.Profile
	for i, p := range ps {
		if p.Source == category && (strings.EqualFold(p.Name, name) || strings.EqualFold(p.Name, name+" ("+category+")")) {
			return p, true
		}
		if strings.EqualFold(p.Name, name) {
			plain = &ps[i]
		}
	}
	if plain != nil {
		return *plain, true
	}
	return decision.Profile{}, false
}

func (s *Service) compare(ctx context.Context, c config.Category, g arr.GrabRecord, arrProfile string) Row {
	movie := c.Kind == "radarr"
	row := Row{Category: c.Name, At: s.Now(), Item: g.ItemTitle, ArrTitle: g.Title, ArrQuality: g.Quality, ArrIndexer: g.Indexer}
	if movie && g.Year > 0 {
		row.Item += fmt.Sprintf(" (%d)", g.Year)
	}
	if !movie && g.Season >= 0 {
		row.Item += fmt.Sprintf(" S%02d", g.Season)
		if len(g.Episodes) > 0 {
			row.Item += fmt.Sprintf("E%02d", g.Episodes[0])
			if len(g.Episodes) > 1 {
				row.Item += fmt.Sprintf("-E%02d", g.Episodes[len(g.Episodes)-1])
			}
		}
	}
	p, ok := s.profileFor(c.Name, arrProfile)
	if !ok {
		row.Verdict, row.Note = Skipped, fmt.Sprintf("no HyveMynd profile named %q: import the profiles from %s on the Profiles page", arrProfile, c.Name)
		return row
	}
	row.Profile = p.Name
	if s.budget() <= 0 {
		row.Verdict, row.Note = Skipped, "over the hourly search budget"
		return row
	}
	e, err := s.Profiles.Engine(p)
	if err != nil {
		row.Verdict, row.Note = Skipped, err.Error()
		return row
	}
	want := decision.Want{Movie: movie, Titles: []string{g.ItemTitle}, Year: g.Year, Season: g.Season, Runtime: g.Runtime}
	q := indexers.Query{Text: g.ItemTitle, Type: "movie", TMDB: g.TMDB, IMDB: g.IMDB}
	if !movie {
		q = indexers.Query{Text: g.ItemTitle, Type: "tv", TVDB: g.TVDB, Season: g.Season}
		// A whole-season grab is a season search; otherwise the episodes.
		if len(g.Episodes) == 1 || (len(g.Episodes) > 1 && !isPack(g.Title)) {
			want.Episodes = g.Episodes
			want.Absolute = g.Absolute
			q.Episode = g.Episodes[0]
		}
	}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := s.Search.Search(sctx, q, "")
	if err != nil {
		row.Verdict, row.Note = Skipped, "search: "+err.Error()
		return row
	}
	var cs []decision.Candidate
	found := false
	for _, x := range res.Results {
		c := decision.Candidate{Title: x.Title, Size: x.Size, Indexer: x.Indexer, Priority: x.IndexerPrio, Protocol: x.Protocol, Seeders: x.Seeders, Published: x.Published}
		if x.Freeleech {
			c.Flags = []string{"freeleech"}
		}
		if sameRelease(x.Title, g.Title) {
			found = true
		}
		cs = append(cs, c)
	}
	arrCand := decision.Candidate{Title: g.Title, Size: g.Size, Indexer: g.Indexer, Protocol: "usenet", Seeders: 1}
	arrD := e.Evaluate(want, arrCand)
	row.ArrScore = arrD.Score
	ds := e.Decide(want, cs)
	var top *decision.Decision
	if len(ds) > 0 && ds[0].Accepted() {
		top = &ds[0]
		row.OurTitle, row.OurQuality, row.OurScore = top.Candidate.Title, top.Quality, top.Score
	}
	var notes []string
	if !found {
		notes = append(notes, "HyveMynd's indexers did not return the arr's release")
	}
	if len(res.Errors) > 0 {
		notes = append(notes, fmt.Sprintf("%d indexer(s) failed", len(res.Errors)))
	}
	switch {
	case !arrD.Accepted():
		row.Verdict = Rejected
		notes = append([]string{"the arr's pick: " + strings.Join(arrD.Rejected, "; ")}, notes...)
	case top == nil:
		row.Verdict = None
		if len(ds) > 0 {
			notes = append(notes, "best found: "+ds[0].Candidate.Title+" ("+strings.Join(ds[0].Rejected, "; ")+")")
		}
	case sameRelease(top.Candidate.Title, g.Title):
		row.Verdict = Same
	case top.Quality == arrD.Quality && top.Score == arrD.Score:
		row.Verdict = Equal
	case decision.Better(*top, arrD, want):
		row.Verdict = Better
	default:
		row.Verdict = Worse
	}
	row.Note = strings.Join(notes, ". ")
	return row
}

func isPack(title string) bool { return parser.Parse(title).FullSeason }

// sameRelease compares two release names, ignoring separators and case.
func sameRelease(a, b string) bool {
	return library.Normalize(strings.TrimSuffix(a, ".nzb")) == library.Normalize(strings.TrimSuffix(b, ".nzb"))
}

func (s *Service) save(r Row, key string) error {
	_, err := s.DB.Exec(`INSERT INTO shadow (category, grab_key, at, item, profile, arr_title, arr_quality, arr_indexer,
		our_title, our_quality, our_score, arr_score, verdict, note) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Category, key, db.Stamp(r.At), r.Item, r.Profile, r.ArrTitle, r.ArrQuality, r.ArrIndexer,
		r.OurTitle, r.OurQuality, r.OurScore, r.ArrScore, r.Verdict, r.Note)
	return err
}

// Report sums up the comparisons since a time.
type Report struct {
	Since     time.Time      `json:"since"`
	Total     int            `json:"total"`
	Compared  int            `json:"compared"` // not skipped
	Agree     int            `json:"agree"`    // same or equal
	Rate      float64        `json:"rate"`
	ByVerdict map[string]int `json:"byVerdict"`
	Rows      []Row          `json:"rows"`
}

// Report lists the comparisons since a time, newest first.
func (s *Service) Report(since time.Time, limit int) (Report, error) {
	r := Report{Since: since, ByVerdict: map[string]int{}}
	rows, err := s.DB.Query(`SELECT id, category, at, item, profile, arr_title, arr_quality, arr_indexer, our_title, our_quality,
		our_score, arr_score, verdict, note FROM shadow WHERE at >= ? ORDER BY at DESC, id DESC`, db.Stamp(since))
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var x Row
		var at string
		if err := rows.Scan(&x.ID, &x.Category, &at, &x.Item, &x.Profile, &x.ArrTitle, &x.ArrQuality, &x.ArrIndexer,
			&x.OurTitle, &x.OurQuality, &x.OurScore, &x.ArrScore, &x.Verdict, &x.Note); err != nil {
			return r, err
		}
		x.At, _ = time.Parse(db.TimeFormat, at)
		r.Total++
		r.ByVerdict[x.Verdict]++
		if x.Verdict != Skipped {
			r.Compared++
		}
		if x.Verdict == Same || x.Verdict == Equal {
			r.Agree++
		}
		if len(r.Rows) < limit {
			r.Rows = append(r.Rows, x)
		}
	}
	if r.Compared > 0 {
		r.Rate = float64(r.Agree) / float64(r.Compared)
	}
	return r, rows.Err()
}
