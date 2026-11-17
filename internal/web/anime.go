package web

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/z354prance/undermynd/internal/animelib"
	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/metadata"
)

func (s *Server) animeReady(w http.ResponseWriter) bool {
	if s.Anime == nil || s.Metadata == nil {
		jsonErr(w, http.StatusConflict, "the anime library needs the database (UNDERMYND_STORE is set to json)")
		return false
	}
	return true
}

type animeRow struct {
	ID          int64                 `json:"id"`
	Title       string                `json:"title"`
	Year        int                   `json:"year"`
	Folder      string                `json:"folder"`
	Numbering   string                `json:"numbering"`
	Order       string                `json:"order"`
	OrderName   string                `json:"orderName"`
	Seasons     []catalog.SeasonCount `json:"seasons"`
	Have        int                   `json:"have"`
	Total       int                   `json:"total"`
	Missing     int                   `json:"missing"`
	MissingBy   map[int]int           `json:"missingBySeason"`
	LatestAired string                `json:"latestAired"` // "S2E7" style, or "" when nothing has aired
	LatestDate  string                `json:"latestDate"`
	Upcoming    bool                  `json:"upcoming"` // more episodes are dated in the future
}

func (s *Server) orderName(ctx context.Context, ser catalog.Series) string {
	if ser.EpisodeOrder == "" {
		return "TMDB seasons"
	}
	orders, err := s.Metadata.Orders(ctx, ser.TMDBID)
	if err == nil {
		for _, o := range orders {
			if o.ID == ser.EpisodeOrder {
				return "TMDB group: " + o.Name
			}
		}
	}
	return "TMDB episode group"
}

func (s *Server) animeRow(ctx context.Context, ser catalog.Series, today string) (animeRow, error) {
	row := animeRow{ID: ser.ID, Title: ser.Title, Year: ser.Year, Folder: ser.Folder, Numbering: ser.Numbering, Order: ser.EpisodeOrder, OrderName: s.orderName(ctx, ser)}
	var err error
	if row.Seasons, err = s.Anime.Cat.Seasons(ser.ID); err != nil {
		return row, err
	}
	if row.Seasons == nil {
		row.Seasons = []catalog.SeasonCount{}
	}
	for _, c := range row.Seasons {
		row.Have += c.Have
		row.Total += c.Total
	}
	missing, err := s.Anime.Cat.Missing(ser.ID)
	if err != nil {
		return row, err
	}
	row.Missing = len(missing)
	row.MissingBy = map[int]int{}
	for _, e := range missing {
		row.MissingBy[e.Season]++
	}
	eps, err := s.Anime.Cat.Episodes(ser.ID)
	if err != nil {
		return row, err
	}
	for _, e := range eps {
		if e.Season == 0 || e.AirDate == "" {
			continue
		}
		if e.AirDate > today {
			row.Upcoming = true
			continue
		}
		if e.AirDate >= row.LatestDate {
			row.LatestDate = e.AirDate
			row.LatestAired = "S" + strconv.Itoa(e.Season) + "E" + strconv.Itoa(e.Number)
			if ser.Numbering == catalog.ByAbsolute && e.Absolute > 0 {
				row.LatestAired += " (" + strconv.Itoa(e.Absolute) + ")"
			}
		}
	}
	return row, nil
}

func (s *Server) animeSeries(w http.ResponseWriter, r *http.Request) {
	if !s.animeReady(w) {
		return
	}
	all, err := s.Anime.Cat.ListSeries()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	today := time.Now().UTC().Format("2006-01-02")
	out := []animeRow{}
	for _, ser := range all {
		if ser.Kind != "anime" {
			continue
		}
		row, err := s.animeRow(r.Context(), ser, today)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, row)
	}
	st := s.Anime.Status()
	needs := 0
	for _, f := range st.Folders {
		if f.State == "needs you" {
			needs++
		}
	}
	writeJSON(w, map[string]any{"series": out, "needs": needs, "scanned": st.Finished, "hasKey": s.Cfg.Get().Metadata.TMDBKey != "", "roots": s.animeRoots()})
}

// animeRoots are the library folders of the categories no arr handles.
func (s *Server) animeRoots() []string {
	var out []string
	for _, c := range s.Cfg.Get().Categories {
		if c.LibraryDir != "" {
			out = append(out, c.LibraryDir)
		}
	}
	return out
}

type animeEpisode struct {
	ID       int64  `json:"id"`
	Season   int    `json:"season"`
	Number   int    `json:"number"`
	Absolute int    `json:"absolute"`
	Title    string `json:"title"`
	AirDate  string `json:"airDate"`
	State    string `json:"state"` // have | missing | upcoming | undated
	Files    []struct {
		Name       string `json:"name"`
		Group      string `json:"group"`
		Resolution string `json:"resolution"`
		Size       int64  `json:"size"`
	} `json:"files"`
}

func (s *Server) seriesID(w http.ResponseWriter, r *http.Request) (catalog.Series, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return catalog.Series{}, false
	}
	ser, err := s.Anime.Cat.Series(id)
	if errors.Is(err, catalog.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "not in the library")
		return ser, false
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return ser, false
	}
	return ser, true
}

func (s *Server) animeShow(w http.ResponseWriter, r *http.Request) {
	if !s.animeReady(w) {
		return
	}
	ser, ok := s.seriesID(w, r)
	if !ok {
		return
	}
	today := time.Now().UTC().Format("2006-01-02")
	row, err := s.animeRow(r.Context(), ser, today)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	eps, err := s.Anime.Cat.Episodes(ser.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	files, err := s.Anime.Cat.SeriesFiles(ser.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	byEp := map[int64][]catalog.SeriesFile{}
	for _, f := range files {
		byEp[f.EpisodeID] = append(byEp[f.EpisodeID], f)
	}
	out := make([]animeEpisode, 0, len(eps))
	for _, e := range eps {
		ae := animeEpisode{ID: e.ID, Season: e.Season, Number: e.Number, Absolute: e.Absolute, Title: e.Title, AirDate: e.AirDate}
		for _, f := range byEp[e.ID] {
			ae.Files = append(ae.Files, struct {
				Name       string `json:"name"`
				Group      string `json:"group"`
				Resolution string `json:"resolution"`
				Size       int64  `json:"size"`
			}{filepath.Base(f.Path), f.ReleaseGroup, f.Resolution, f.Size})
		}
		switch {
		case len(ae.Files) > 0:
			ae.State = "have"
		case e.Season == 0:
			ae.State = "special" // specials are listed, never counted as missing
		case e.AirDate == "":
			ae.State = "undated"
		case e.AirDate > today:
			ae.State = "upcoming"
		default:
			ae.State = "missing"
		}
		out = append(out, ae)
	}
	aliases, _ := s.Anime.Cat.SeriesAliases(ser.ID)
	if aliases == nil {
		aliases = []string{}
	}
	writeJSON(w, map[string]any{
		"series": row, "tmdbId": ser.TMDBID, "tvdbId": ser.TVDBID, "updated": ser.Updated, "monitored": ser.Monitored,
		"episodes": out, "aliases": aliases, "files": len(files),
	})
}

func (s *Server) animeSeriesAction(w http.ResponseWriter, r *http.Request) {
	if !s.animeReady(w) {
		return
	}
	ser, ok := s.seriesID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	switch r.PathValue("action") {
	case "refresh":
		if err := s.Metadata.Refresh(ctx, ser.ID); err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		fallthrough
	case "rescan":
		res, err := s.Anime.Sync(ser.ID)
		if err != nil {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, res)
	case "remove":
		if err := s.Anime.Remove(ser.ID); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "orders":
		orders, err := s.Metadata.Orders(ctx, ser.TMDBID)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, orders)
	case "order":
		var in struct {
			Order string `json:"order"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if err := s.Metadata.SetEpisodeOrder(ctx, ser.ID, in.Order); err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, catalog.ErrHasFiles) {
				code = http.StatusConflict
			}
			jsonErr(w, code, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	default:
		jsonErr(w, http.StatusNotFound, "unknown action")
	}
}

func (s *Server) animeImportStatus(w http.ResponseWriter, r *http.Request) {
	if !s.animeReady(w) {
		return
	}
	st := s.Anime.Status()
	writeJSON(w, map[string]any{"status": st, "roots": s.animeRoots(), "hasKey": s.Cfg.Get().Metadata.TMDBKey != ""})
}

func (s *Server) animeImportAction(w http.ResponseWriter, r *http.Request) {
	if !s.animeReady(w) {
		return
	}
	var in struct {
		Path   string `json:"path"`
		Option int    `json:"option"`
		TMDBID int64  `json:"tmdbId"`
		Skip   bool   `json:"skip"`
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	switch r.PathValue("action") {
	case "scan":
		if s.Cfg.Get().Metadata.TMDBKey == "" {
			jsonErr(w, http.StatusConflict, "add a TMDB key under Metadata first")
			return
		}
		if !s.Anime.Scan() {
			jsonErr(w, http.StatusConflict, "a scan or confirm is already running")
			return
		}
		writeJSON(w, map[string]bool{"started": true})
	case "confirm-ready":
		if !s.Anime.StartConfirmReady() {
			jsonErr(w, http.StatusConflict, "a scan or confirm is already running")
			return
		}
		writeJSON(w, map[string]bool{"started": true})
	case "confirm":
		if !readJSON(w, r, &in) {
			return
		}
		res, err := s.Anime.Confirm(ctx, in.Path, in.Option)
		if err != nil {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, res)
	case "choose":
		if !readJSON(w, r, &in) {
			return
		}
		cands, err := s.Metadata.Search(ctx, strconv.FormatInt(in.TMDBID, 10))
		if err != nil || len(cands) == 0 {
			jsonErr(w, http.StatusBadGateway, "TMDB has no show "+strconv.FormatInt(in.TMDBID, 10))
			return
		}
		f, err := s.Anime.Choose(ctx, in.Path, cands[0])
		if err != nil {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, f)
	case "skip":
		if !readJSON(w, r, &in) {
			return
		}
		if err := s.Anime.Skip(in.Path, in.Skip); err != nil {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	default:
		jsonErr(w, http.StatusNotFound, "unknown action")
	}
}

func (s *Server) animeSearch(w http.ResponseWriter, r *http.Request) {
	if !s.animeReady(w) {
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, []metadata.Candidate{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.Metadata.Search(ctx, q)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if res == nil {
		res = []metadata.Candidate{}
	}
	writeJSON(w, res)
}

func (s *Server) placementReady(w http.ResponseWriter) bool {
	if s.Placer == nil {
		jsonErr(w, http.StatusConflict, "catalog placement needs the database (UNDERMYND_STORE is set to json)")
		return false
	}
	return true
}

func (s *Server) placementStatus(w http.ResponseWriter, r *http.Request) {
	if !s.placementReady(w) {
		return
	}
	dry := s.Placer.DryRun()
	counts := map[string]int{}
	for _, e := range dry {
		counts[e.Verdict]++
	}
	if len(dry) > 100 {
		dry = dry[:100]
	}
	writeJSON(w, map[string]any{"mode": s.Placer.Mode(), "queue": s.Placer.Queue(), "dryRun": dry, "counts": counts})
}

func (s *Server) placementMode(w http.ResponseWriter, r *http.Request) {
	if !s.placementReady(w) {
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.Placer.SetMode(in.Mode); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"mode": s.Placer.Mode()})
}

func (s *Server) placementResolve(w http.ResponseWriter, r *http.Request) {
	if !s.placementReady(w) {
		return
	}
	var in animelib.Resolution
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	dst, err := s.Placer.Resolve(ctx, r.PathValue("id"), in)
	if err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]string{"placed": dst})
}
