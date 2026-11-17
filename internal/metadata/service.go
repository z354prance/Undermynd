package metadata

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/catalog"
)

// Service fills the catalog from TMDB and the anime lists.
type Service struct {
	TMDB    *TMDB
	Lists   *Lists
	Catalog *catalog.Catalog
	Log     *log.Logger
	Now     func() time.Time
}

// Candidate is a TMDB show that a set of titles may refer to.
type Candidate struct {
	TMDBID    int64  `json:"tmdbId"`
	Name      string `json:"name"`
	Original  string `json:"original"`
	Year      string `json:"year"`
	Animation bool   `json:"animation"`
	Via       string `json:"via"` // "anime lists" or "TMDB search"
}

// Identify lists the TMDB shows the titles may name: first those the anime lists
// lead to (title, then AniDB, TVDB and TMDB), then TMDB search results. It never
// decides; a person or a later exact rule picks.
func (s *Service) Identify(ctx context.Context, titles ...string) ([]Candidate, error) {
	var out []Candidate
	seen := map[int64]bool{}
	add := func(c Candidate) {
		if c.TMDBID != 0 && !seen[c.TMDBID] {
			seen[c.TMDBID] = true
			out = append(out, c)
		}
	}
	if s.Lists != nil {
		for _, t := range titles {
			tvdbs, err := s.Lists.TVDBForTitle(ctx, t)
			if err != nil {
				return nil, err
			}
			for _, tv := range tvdbs {
				id, err := s.TMDB.FindByTVDB(ctx, tv)
				if err != nil {
					return nil, err
				}
				if id == 0 || seen[id] {
					continue
				}
				d, err := s.TMDB.TV(ctx, id, false)
				if err != nil {
					return nil, err
				}
				add(Candidate{TMDBID: id, Name: d.Name, Original: d.OriginalName, Year: yearOf(d.FirstAirDate), Animation: true, Via: "anime lists"})
			}
		}
	}
	for _, t := range titles {
		if strings.TrimSpace(t) == "" {
			continue
		}
		res, err := s.TMDB.SearchTV(ctx, t)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			add(Candidate{TMDBID: r.ID, Name: r.Name, Original: r.OriginalName, Year: yearOf(r.FirstAirDate), Animation: r.Animation(), Via: "TMDB search"})
		}
	}
	return out, nil
}

func yearOf(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

// AddSeries adds a TMDB show to the catalog with its titles and episodes. The
// episode order is TMDB's own; SetEpisodeOrder can switch it before files exist.
func (s *Service) AddSeries(ctx context.Context, tmdbID int64, kind, folder, numbering string) (catalog.Series, error) {
	if existing, err := s.Catalog.SeriesByTMDB(tmdbID); err == nil {
		return existing, fmt.Errorf("%s is already in the catalog", existing.Title)
	}
	tv, err := s.TMDB.TV(ctx, tmdbID, true)
	if err != nil {
		return catalog.Series{}, err
	}
	eps, err := s.episodes(ctx, tv, "", true)
	if err != nil {
		return catalog.Series{}, err
	}
	ser, err := s.Catalog.AddSeries(catalog.Series{Kind: kind, Title: tv.Name, Year: tv.Year(), TMDBID: tv.ID, TVDBID: tv.ExternalIDs.TVDB,
		IMDBID: tv.ExternalIDs.IMDB, Numbering: numbering, Folder: folder})
	if err != nil {
		return catalog.Series{}, err
	}
	if err := s.aliases(ctx, ser.ID, tv); err != nil {
		return ser, err
	}
	if err := s.Catalog.PutEpisodes(ser.ID, eps); err != nil {
		return ser, err
	}
	return s.Catalog.Series(ser.ID)
}

func (s *Service) aliases(ctx context.Context, seriesID int64, tv TV) error {
	titles := []string{tv.OriginalName}
	for _, a := range tv.AlternativeTitles.Results {
		titles = append(titles, a.Title)
	}
	for _, t := range titles {
		if err := s.Catalog.AddSeriesAlias(seriesID, t, "tmdb"); err != nil {
			return err
		}
	}
	if s.Lists == nil {
		return nil
	}
	more, err := s.Lists.TitlesForTVDB(ctx, tv.ExternalIDs.TVDB)
	if err != nil {
		return err
	}
	for _, t := range more {
		if err := s.Catalog.AddSeriesAlias(seriesID, t, "anime-offline-database"); err != nil {
			return err
		}
	}
	return nil
}

// episodes builds a show's episode list in the given order: "" for TMDB's own
// seasons, or an episode group id. Every regular episode gets an absolute number.
func (s *Service) episodes(ctx context.Context, tv TV, order string, fresh bool) ([]catalog.Episode, error) {
	if order != "" {
		return s.groupEpisodes(ctx, order, fresh)
	}
	seasons := append([]SeasonSummary(nil), tv.Seasons...)
	sort.Slice(seasons, func(i, j int) bool { return seasons[i].Number < seasons[j].Number })
	type season struct {
		n   int
		eps []TMDBEpisode
	}
	var all []season
	for _, se := range seasons {
		eps, err := s.TMDB.Season(ctx, tv.ID, se.Number, fresh)
		if err != nil {
			return nil, err
		}
		all = append(all, season{se.Number, eps})
	}
	// TMDB numbers some long shows absolutely inside their seasons (One Piece's
	// season 23 starts at episode 1156). Then the episode number is the absolute
	// number; otherwise absolute numbers are a running count.
	continuous, first := false, true
	for _, se := range all {
		if se.n == 0 || len(se.eps) == 0 {
			continue
		}
		if !first && se.eps[0].Number > 1 {
			continuous = true
		}
		first = false
	}
	var out []catalog.Episode
	count := 0
	for _, se := range all {
		for _, e := range se.eps {
			ep := catalog.Episode{Season: se.n, Number: e.Number, Title: e.Name, AirDate: e.AirDate}
			if se.n > 0 {
				count++
				ep.Absolute = count
				if continuous {
					ep.Absolute = e.Number
				}
			}
			out = append(out, ep)
		}
	}
	return out, nil
}

// groupEpisodes numbers episodes by a TMDB episode group: each group is a season
// (one named like "Specials" is season 0), episodes counted in group order.
func (s *Service) groupEpisodes(ctx context.Context, id string, fresh bool) ([]catalog.Episode, error) {
	g, err := s.TMDB.EpisodeGroup(ctx, id, fresh)
	if err != nil {
		return nil, err
	}
	groups := g.Groups
	sort.Slice(groups, func(i, j int) bool { return groups[i].Order < groups[j].Order })
	var out []catalog.Episode
	season, count := 0, 0
	for _, gr := range groups {
		eps := append([]TMDBEpisode(nil), gr.Episodes...)
		sort.Slice(eps, func(i, j int) bool { return eps[i].Order < eps[j].Order })
		special := strings.Contains(strings.ToLower(gr.Name), "special")
		n := 0
		if !special {
			season++
			n = season
		}
		for i, e := range eps {
			ep := catalog.Episode{Season: n, Number: i + 1, Title: e.Name, AirDate: e.AirDate}
			if !special {
				count++
				ep.Absolute = count
			}
			out = append(out, ep)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("episode group %s has no episodes", id)
	}
	return out, nil
}

// EpisodeOrders lists the orders a series can follow: TMDB's own, then its
// episode groups.
func (s *Service) EpisodeOrders(ctx context.Context, seriesID int64) ([]EpisodeGroupSummary, error) {
	ser, err := s.Catalog.Series(seriesID)
	if err != nil {
		return nil, err
	}
	tv, err := s.TMDB.TV(ctx, ser.TMDBID, false)
	if err != nil {
		return nil, err
	}
	out := []EpisodeGroupSummary{{ID: "", Name: "TMDB seasons"}}
	return append(out, tv.EpisodeGroups.Results...), nil
}

// SetEpisodeOrder renumbers a series' episodes by order ("" or a group id). It
// only works before any file is recorded for the series.
func (s *Service) SetEpisodeOrder(ctx context.Context, seriesID int64, order string) error {
	ser, err := s.Catalog.Series(seriesID)
	if err != nil {
		return err
	}
	tv, err := s.TMDB.TV(ctx, ser.TMDBID, false)
	if err != nil {
		return err
	}
	eps, err := s.episodes(ctx, tv, order, false)
	if err != nil {
		return err
	}
	return s.Catalog.ReplaceEpisodes(seriesID, order, eps)
}

// Refresh fetches a series again: new episodes, changed titles and air dates,
// and any new alternative titles. Existing episodes keep their files.
func (s *Service) Refresh(ctx context.Context, seriesID int64) error {
	ser, err := s.Catalog.Series(seriesID)
	if err != nil {
		return err
	}
	if ser.TMDBID == 0 {
		return errors.New("the series has no TMDB id")
	}
	tv, err := s.TMDB.TV(ctx, ser.TMDBID, true)
	if err != nil {
		return err
	}
	eps, err := s.episodes(ctx, tv, ser.EpisodeOrder, true)
	if err != nil {
		return err
	}
	if err := s.aliases(ctx, ser.ID, tv); err != nil {
		return err
	}
	return s.Catalog.PutEpisodes(ser.ID, eps)
}

// Run keeps metadata fresh until ctx ends: the anime lists once a week, and each
// series about once a day, a few at a time. Nothing runs without a TMDB key.
func (s *Service) Run(ctx context.Context, hasKey func() bool) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if hasKey() {
			s.tick(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	now := s.Now()
	if s.Lists != nil {
		if st := s.Lists.Status(); now.Sub(st.Checked) > 7*24*time.Hour {
			st = s.Lists.Refresh(ctx)
			s.Log.Printf("metadata: anime lists: %d titles (%s), %d ids %s%s", st.Titles, st.TitlesDate, st.IDs, st.TitlesError, st.IDsError)
		}
	}
	due, err := s.Catalog.SeriesDue(now.Add(-20*time.Hour), 25)
	if err != nil {
		s.Log.Printf("metadata: %v", err)
		return
	}
	for _, ser := range due {
		if err := s.Refresh(ctx, ser.ID); err != nil {
			s.Log.Printf("metadata: refresh %s: %v", ser.Title, err)
			if errors.Is(err, ErrNoKey) || ctx.Err() != nil {
				return
			}
		}
	}
}
