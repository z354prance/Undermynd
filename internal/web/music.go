package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
)

// Music requests: Seerr has no music, so HyveMynd keeps these itself.
// Visitors search for artists (whole artists only, as the owner adds them);
// an approved artist is added to Lidarr with every album monitored and
// searched.

type musicRequest struct {
	ID          string    `json:"id"`
	MBID        string    `json:"mbid"`
	Name        string    `json:"name"`
	Detail      string    `json:"detail,omitempty"`
	Image       string    `json:"image,omitempty"` // remote address
	Status      string    `json:"status"`          // pending | approved | declined | failed
	Note        string    `json:"note,omitempty"`
	RequestedAt time.Time `json:"requestedAt"`
	DecidedAt   time.Time `json:"decidedAt,omitempty"`
}

const musicBlob = "music-requests.json"

var musicMu sync.Mutex

var mbidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Server) loadMusic() []musicRequest {
	var list []musicRequest
	if s.Blobs == nil {
		return s.musicMem
	}
	if b, ok, err := s.Blobs.Load(musicBlob); err == nil && ok {
		_ = json.Unmarshal(b, &list)
	}
	return list
}

func (s *Server) saveMusic(list []musicRequest) error {
	// Decided requests are kept 30 days for "recently decided".
	keep := list[:0]
	for _, m := range list {
		if m.Status == "pending" || time.Since(m.DecidedAt) < 30*24*time.Hour {
			keep = append(keep, m)
		}
	}
	if s.Blobs == nil {
		s.musicMem = append([]musicRequest(nil), keep...)
		return nil
	}
	b, _ := json.Marshal(keep)
	return s.Blobs.Save(musicBlob, b)
}

// lidarr is the client for the first Lidarr category, or nil.
func (s *Server) lidarr() *arr.Client {
	for _, c := range s.Cfg.Get().Categories {
		if c.Kind == "lidarr" && c.ArrURL != "" && c.ArrAPIKey != "" {
			return arr.New("lidarr", c.ArrURL, string(c.ArrAPIKey))
		}
	}
	return nil
}

func (s *Server) musicOn() bool { return s.Cfg.Get().Requests.Enabled && s.lidarr() != nil }

type artistResult struct {
	MBID      string `json:"mbid"`
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
	Overview  string `json:"overview,omitempty"`
	Image     string `json:"image,omitempty"` // this server's /img link
	InLibrary bool   `json:"inLibrary"`
	Requested bool   `json:"requested"`
}

// musicSearch finds artists through Lidarr's lookup, or MusicBrainz when
// Lidarr's metadata service is down.
func (s *Server) musicSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(q) < 2 || len(q) > 200 {
		jsonErr(w, http.StatusBadRequest, "enter between 2 and 200 characters")
		return
	}
	lc := s.lidarr()
	if !s.musicOn() || lc == nil {
		jsonErr(w, http.StatusServiceUnavailable, "music requests are not open")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	musicMu.Lock()
	asked := map[string]bool{}
	for _, m := range s.loadMusic() {
		if m.Status == "pending" || m.Status == "approved" {
			asked[m.MBID] = true
		}
	}
	musicMu.Unlock()
	var out []artistResult
	list, err := lc.LookupArtists(ctx, q)
	if err == nil {
		for _, a := range list {
			if !mbidRe.MatchString(a.ForeignID) {
				continue
			}
			ov := a.Overview
			if len(ov) > 400 {
				ov = ov[:400] + "…"
			}
			out = append(out, artistResult{MBID: a.ForeignID, Name: a.Name, Detail: strings.TrimSpace(strings.Join(nonEmpty(a.Type, a.Disambiguation), " · ")),
				Overview: ov, Image: imgURL(a.Image()), InLibrary: a.ID > 0, Requested: asked[a.ForeignID]})
		}
	} else {
		mb, merr := musicBrainzArtists(ctx, q)
		if merr != nil {
			jsonErr(w, http.StatusBadGateway, "artist search is unavailable right now; try again later")
			return
		}
		for _, a := range mb {
			a.Requested = asked[a.MBID]
			out = append(out, a)
		}
	}
	if len(out) > 20 {
		out = out[:20]
	}
	writeJSON(w, map[string]any{"results": out})
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// musicBrainzBase is MusicBrainz's API (tests replace it).
var musicBrainzBase = "https://musicbrainz.org/ws/2"

func musicBrainzArtists(ctx context.Context, q string) ([]artistResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, musicBrainzBase+"/artist?fmt=json&limit=15&query="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "HyveMynd/1 (https://github.com/z354prance/Undermynd)")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MusicBrainz answered %s", resp.Status)
	}
	var d struct {
		Artists []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Type           string `json:"type"`
			Disambiguation string `json:"disambiguation"`
			Country        string `json:"country"`
		} `json:"artists"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	var out []artistResult
	for _, a := range d.Artists {
		if mbidRe.MatchString(a.ID) {
			out = append(out, artistResult{MBID: a.ID, Name: a.Name, Detail: strings.Join(nonEmpty(a.Type, a.Country, a.Disambiguation), " · ")})
		}
	}
	return out, nil
}

// musicRequestCreate asks for a whole artist.
func (s *Server) musicRequestCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MBID   string `json:"mbid"`
		Name   string `json:"name"`
		Detail string `json:"detail"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.MBID, in.Name = strings.ToLower(strings.TrimSpace(in.MBID)), strings.TrimSpace(in.Name)
	if !mbidRe.MatchString(in.MBID) || in.Name == "" || len(in.Name) > 200 || len(in.Detail) > 200 {
		jsonErr(w, http.StatusBadRequest, "pick an artist")
		return
	}
	lc := s.lidarr()
	if !s.musicOn() || lc == nil {
		jsonErr(w, http.StatusServiceUnavailable, "music requests are not open")
		return
	}
	if !reqLimit.allow(r) {
		w.Header().Set("Retry-After", "60")
		jsonErr(w, http.StatusTooManyRequests, "too many requests; try again in a minute")
		return
	}
	musicMu.Lock()
	list := s.loadMusic()
	for _, m := range list {
		if m.MBID == in.MBID && (m.Status == "pending" || m.Status == "approved") {
			musicMu.Unlock()
			jsonErr(w, http.StatusConflict, in.Name+" has already been requested")
			return
		}
	}
	pending := 0
	for _, m := range list {
		if m.Status == "pending" {
			pending++
		}
	}
	if pending >= 200 {
		musicMu.Unlock()
		jsonErr(w, http.StatusTooManyRequests, "too many music requests are waiting; try again later")
		return
	}
	idb := make([]byte, 8)
	rand.Read(idb)
	m := musicRequest{ID: hex.EncodeToString(idb), MBID: in.MBID, Name: in.Name, Detail: in.Detail, Status: "pending", RequestedAt: time.Now()}
	list = append(list, m)
	err := s.saveMusic(list)
	musicMu.Unlock()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save the request")
		return
	}
	if s.Cfg.Get().Requests.AutoMusic {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if _, err := s.decideMusic(ctx, m.ID, "approve"); err != nil && s.Log != nil {
			s.Log.Printf("music: auto-approve of %s: %v", m.Name, err)
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": m.ID})
}

// decideMusic approves (adds to Lidarr) or declines a waiting request.
func (s *Server) decideMusic(ctx context.Context, id, action string) (musicRequest, error) {
	if action != "approve" && action != "decline" {
		return musicRequest{}, errors.New("invalid decision")
	}
	musicMu.Lock()
	list := s.loadMusic()
	idx := -1
	for i, m := range list {
		if m.ID == id {
			idx = i
		}
	}
	if idx < 0 || list[idx].Status != "pending" {
		musicMu.Unlock()
		return musicRequest{}, errors.New("the request is no longer waiting; refresh the list")
	}
	m := list[idx]
	musicMu.Unlock()
	m.DecidedAt = time.Now()
	if action == "decline" {
		m.Status = "declined"
	} else {
		lc := s.lidarr()
		if lc == nil {
			return m, errors.New("Lidarr is not connected (Settings › Categories)")
		}
		a, err := lc.ArtistByMBID(ctx, m.MBID)
		switch {
		case err != nil:
			return m, fmt.Errorf("Lidarr could not find %s: %v", m.Name, err)
		case a.ID > 0:
			m.Status, m.Note = "approved", "already in Lidarr"
		default:
			if _, err := lc.AddArtist(ctx, a); err != nil {
				return m, fmt.Errorf("Lidarr did not add %s: %v", m.Name, err)
			}
			m.Status, m.Note = "approved", "added to Lidarr; searching for every album"
		}
	}
	musicMu.Lock()
	defer musicMu.Unlock()
	list = s.loadMusic()
	for i := range list {
		if list[i].ID == id {
			list[i] = m
		}
	}
	return m, s.saveMusic(list)
}

func (s *Server) musicDecide(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	m, err := s.decideMusic(ctx, r.PathValue("id"), r.PathValue("action"))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, m)
}

type musicView struct {
	musicRequest
	Image string `json:"image,omitempty"`
}

// musicLists gives the waiting music requests (oldest first) and the
// decided ones (newest first).
func (s *Server) musicLists() (waiting, decided []musicView) {
	musicMu.Lock()
	list := s.loadMusic()
	musicMu.Unlock()
	waiting, decided = []musicView{}, []musicView{}
	for _, m := range list {
		v := musicView{musicRequest: m, Image: imgURL(m.Image)}
		if m.Status == "pending" {
			waiting = append(waiting, v)
		} else {
			decided = append(decided, v)
		}
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].RequestedAt.Before(waiting[j].RequestedAt) })
	sort.Slice(decided, func(i, j int) bool { return decided[i].DecidedAt.After(decided[j].DecidedAt) })
	return waiting, decided
}
