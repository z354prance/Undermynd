package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/indexers"
)

func (s *Server) profilesReady(w http.ResponseWriter) bool {
	if s.Profiles == nil {
		jsonErr(w, http.StatusConflict, "profiles need the database (UNDERMYND_STORE is set to json)")
		return false
	}
	return true
}

type formatView struct {
	decision.Format
	Unchecked []string `json:"unchecked,omitempty"`
}

func (s *Server) profilesList(w http.ResponseWriter, r *http.Request) {
	if !s.profilesReady(w) {
		return
	}
	ps, err := s.Profiles.Profiles()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs, err := s.Profiles.Formats()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]formatView, len(fs))
	for i, f := range fs {
		views[i] = formatView{Format: f, Unchecked: decision.BadRegex(f)}
		for _, sp := range f.Specs {
			switch sp.Kind {
			case "title", "group", "edition", "source", "resolution", "modifier", "language", "size", "releaseType", "indexerFlag", "year":
			default:
				views[i].Unchecked = append(views[i].Unchecked, sp.Name)
			}
		}
	}
	sizes, _ := s.Profiles.Sizes()
	var cats []string
	for _, c := range s.Cfg.Get().Categories {
		if c.ArrURL != "" && (c.Kind == "sonarr" || c.Kind == "radarr") {
			cats = append(cats, c.Name)
		}
	}
	writeJSON(w, map[string]any{"profiles": ps, "formats": views, "ladder": decision.Ladder, "sizes": sizes, "arrCategories": cats})
}

func (s *Server) profileSave(w http.ResponseWriter, r *http.Request) {
	if !s.profilesReady(w) {
		return
	}
	var p decision.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad profile")
		return
	}
	id, err := s.Profiles.SaveProfile(p)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) profileDelete(w http.ResponseWriter, r *http.Request) {
	if !s.profilesReady(w) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Profiles.DeleteProfile(id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) formatSave(w http.ResponseWriter, r *http.Request) {
	if !s.profilesReady(w) {
		return
	}
	var f decision.Format
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad custom format")
		return
	}
	id, err := s.Profiles.SaveFormat(f)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) formatDelete(w http.ResponseWriter, r *http.Request) {
	if !s.profilesReady(w) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Profiles.DeleteFormat(id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// profilesImport copies an arr's quality profiles, custom formats and quality
// sizes.
func (s *Server) profilesImport(w http.ResponseWriter, r *http.Request) {
	if !s.profilesReady(w) {
		return
	}
	var in struct {
		Category string `json:"category"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	c, ok := s.Cfg.Get().Category(in.Category)
	if !ok || c.ArrURL == "" || (c.Kind != "sonarr" && c.Kind != "radarr") {
		jsonErr(w, http.StatusBadRequest, "pick a category connected to Sonarr or Radarr")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	cl := arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey))
	var ps, fs, ds json.RawMessage
	if err := cl.Get(ctx, "/qualityprofile", &ps); err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := cl.Get(ctx, "/customformat", &fs); err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	cl.Get(ctx, "/qualitydefinition", &ds) // sizes are optional
	im, err := decision.FromArr(c.Kind, ps, fs, ds)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	np, nf, err := s.Profiles.Save(c.Name, im)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"profiles": np, "formats": nf, "sizes": len(im.Sizes), "unchecked": im.Skipped})
}

// profileTry runs a profile over real search results, or over pasted names,
// and shows each verdict with its reasons.
func (s *Server) profileTry(w http.ResponseWriter, r *http.Request) {
	if !s.profilesReady(w) {
		return
	}
	var in struct {
		Profile   int64    `json:"profile"`
		Movie     bool     `json:"movie"`
		Title     string   `json:"title"`
		Year      int      `json:"year"`
		Season    int      `json:"season"`
		Episode   int      `json:"episode"`
		Runtime   int      `json:"runtime"`
		Names     []string `json:"names"`
		Have      string   `json:"have"`
		HaveScore int      `json:"haveScore"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Title) == "" {
		jsonErr(w, http.StatusBadRequest, "give a title")
		return
	}
	p, err := s.Profiles.Profile(in.Profile)
	if errors.Is(err, decision.ErrNotFound) {
		jsonErr(w, http.StatusBadRequest, "no such profile")
		return
	} else if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	e, err := s.Profiles.Engine(p)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	want := decision.Want{Movie: in.Movie, Titles: []string{in.Title}, Year: in.Year, Season: in.Season, Runtime: in.Runtime, HaveQuality: in.Have, HaveScore: in.HaveScore, HaveVersion: 1}
	if in.Episode > 0 {
		want.Episodes = []int{in.Episode}
	}
	var cs []decision.Candidate
	var notes []string
	for _, n := range in.Names {
		if n = strings.TrimSpace(n); n != "" {
			cs = append(cs, decision.Candidate{Title: n, Protocol: "usenet"})
		}
	}
	if len(cs) == 0 {
		if s.Indexers == nil {
			jsonErr(w, http.StatusConflict, "no indexers to search; paste release names instead")
			return
		}
		q := indexers.Query{Text: in.Title, Type: "movie"}
		if in.Movie && in.Year > 0 {
			q.Text += " " + strconv.Itoa(in.Year)
		}
		if !in.Movie {
			q.Type, q.Season, q.Episode = "tv", in.Season, in.Episode
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		res, err := s.Indexers.Search(ctx, q, "")
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		for _, x := range append(res.Errors, res.Skipped...) {
			notes = append(notes, x.Indexer+": "+x.Reason)
		}
		for _, x := range res.Results {
			c := decision.Candidate{Title: x.Title, Size: x.Size, Indexer: x.Indexer, Priority: x.IndexerPrio, Protocol: x.Protocol, Seeders: x.Seeders, Published: x.Published}
			if x.Freeleech {
				c.Flags = []string{"freeleech"}
			}
			cs = append(cs, c)
		}
	}
	ds := e.Decide(want, cs)
	if len(ds) > 300 {
		ds = ds[:300]
	}
	writeJSON(w, map[string]any{"decisions": ds, "notes": notes})
}
