package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/secret"
)

// Book requests: visitors search for books (Open Library), the request waits
// in HyveMynd's queue like music, and approving it creates the request in
// BookOrbit through its API (an API key with read and write access), where
// BookOrbit finds and downloads the book.

// ------------------------------------------------------------ BookOrbit API

type bookOrbit struct {
	base string
	key  string
	http *http.Client
}

func (s *Server) bookOrbit() *bookOrbit {
	b := s.Cfg.Get().BookOrbit
	if !b.Ready() {
		return nil
	}
	return newBookOrbit(b.URL, string(b.APIKey))
}

func newBookOrbit(base, key string) *bookOrbit {
	return &bookOrbit{base: strings.TrimRight(strings.TrimSpace(base), "/"), key: strings.TrimSpace(key), http: &http.Client{Timeout: 20 * time.Second}}
}

func (b *bookOrbit) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+"/api/v1/"+strings.TrimLeft(path, "/"), rd)
	if err != nil {
		return errors.New("invalid BookOrbit address")
	}
	req.Header.Set("Authorization", "Bearer "+b.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return errors.New("BookOrbit could not be reached")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Message any `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := fmt.Sprint(e.Message)
		if e.Message == nil {
			msg = http.StatusText(resp.StatusCode)
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("BookOrbit refused the API key (%s)", msg)
		case http.StatusForbidden:
			return fmt.Errorf("BookOrbit refused: %s (the key needs read and write access and an owner who may request books)", msg)
		}
		return fmt.Errorf("BookOrbit answered %d: %s", resp.StatusCode, msg)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// bookCounts adds up BookOrbit's libraries: ebooks (and comics, PDFs) and
// audiobooks, by each book's main file format.
func (b *bookOrbit) bookCounts(ctx context.Context) (ebooks, audio int, err error) {
	var libs []struct {
		ID int `json:"id"`
	}
	if err := b.do(ctx, http.MethodGet, "libraries", nil, &libs); err != nil {
		return 0, 0, err
	}
	for _, l := range libs {
		var st struct {
			Total   int            `json:"totalBooks"`
			Formats map[string]int `json:"formatCounts"`
		}
		if err := b.do(ctx, http.MethodGet, fmt.Sprintf("libraries/%d/stats", l.ID), nil, &st); err != nil {
			return 0, 0, err
		}
		a := 0
		for f, n := range st.Formats {
			if audioFormat[strings.ToLower(strings.TrimPrefix(f, "."))] {
				a += n
			}
		}
		audio += a
		ebooks += max(0, st.Total-a)
	}
	return ebooks, audio, nil
}

var audioFormat = map[string]bool{"m4b": true, "m4a": true, "mp3": true, "aac": true, "ogg": true, "opus": true, "flac": true, "wav": true, "wma": true, "audiobook": true}

type bookAvail struct {
	Owned     *int `json:"ownedBookId"`
	Requested *int `json:"existingRequestId"`
}

// availability asks BookOrbit whether each book is already in a library or requested.
func (b *bookOrbit) availability(ctx context.Context, items []map[string]any) ([]bookAvail, error) {
	var out []bookAvail
	err := b.do(ctx, http.MethodPost, "book-requests/availability", map[string]any{"items": items}, &out)
	return out, err
}

// ------------------------------------------------------------ requests

type bookRequest struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"` // Open Library work key
	Title       string    `json:"title"`
	Authors     []string  `json:"authors,omitempty"`
	Year        int       `json:"year,omitempty"`
	ISBN13      string    `json:"isbn13,omitempty"`
	Cover       string    `json:"cover,omitempty"` // remote address
	Kind        string    `json:"kind"`            // ebook | audiobook
	Status      string    `json:"status"`          // pending | approved | declined
	Note        string    `json:"note,omitempty"`
	RequestedAt time.Time `json:"requestedAt"`
	DecidedAt   time.Time `json:"decidedAt,omitempty"`
}

const booksBlob = "book-requests.json"

var booksMu sync.Mutex

var olKeyRe = regexp.MustCompile(`^/works/OL[0-9]+W$`)

func (s *Server) loadBooks() []bookRequest {
	var list []bookRequest
	if s.Blobs == nil {
		return append([]bookRequest(nil), s.booksMem...)
	}
	if b, ok, err := s.Blobs.Load(booksBlob); err == nil && ok {
		_ = json.Unmarshal(b, &list)
	}
	return list
}

func (s *Server) saveBooks(list []bookRequest) error {
	keep := list[:0]
	for _, m := range list {
		if m.Status == "pending" || time.Since(m.DecidedAt) < 30*24*time.Hour {
			keep = append(keep, m)
		}
	}
	if s.Blobs == nil {
		s.booksMem = append([]bookRequest(nil), keep...)
		return nil
	}
	b, _ := json.Marshal(keep)
	return s.Blobs.Save(booksBlob, b)
}

func (s *Server) booksOn() bool { return s.Cfg.Get().Requests.Enabled && s.Cfg.Get().BookOrbit.Ready() }

type bookResult struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors,omitempty"`
	Year      int      `json:"year,omitempty"`
	ISBN13    string   `json:"isbn13,omitempty"`
	Cover     string   `json:"cover,omitempty"` // remote, for the request
	Image     string   `json:"image,omitempty"` // this server's /img link
	InLibrary bool     `json:"inLibrary"`
	Requested bool     `json:"requested"`
}

// openLibraryBase is Open Library's address (tests replace it).
var openLibraryBase = "https://openlibrary.org"

func openLibrarySearch(ctx context.Context, q string) ([]bookResult, error) {
	u := openLibraryBase + "/search.json?" + url.Values{"q": {q}, "limit": {"15"}, "fields": {"key,title,author_name,first_publish_year,cover_i,isbn"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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
		return nil, fmt.Errorf("Open Library answered %s", resp.Status)
	}
	var d struct {
		Docs []struct {
			Key     string   `json:"key"`
			Title   string   `json:"title"`
			Authors []string `json:"author_name"`
			Year    int      `json:"first_publish_year"`
			Cover   int64    `json:"cover_i"`
			ISBN    []string `json:"isbn"`
		} `json:"docs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&d); err != nil {
		return nil, err
	}
	var out []bookResult
	for _, x := range d.Docs {
		if !olKeyRe.MatchString(x.Key) || x.Title == "" {
			continue
		}
		r := bookResult{Key: x.Key, Title: x.Title, Year: x.Year}
		if len(x.Authors) > 3 {
			x.Authors = x.Authors[:3]
		}
		r.Authors = x.Authors
		for _, i := range x.ISBN {
			if len(i) == 13 && (strings.HasPrefix(i, "978") || strings.HasPrefix(i, "979")) {
				r.ISBN13 = i
				break
			}
		}
		if x.Cover > 0 {
			r.Cover = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", x.Cover)
			r.Image = imgURL(r.Cover)
		}
		out = append(out, r)
	}
	return out, nil
}

func bookKind(k string) string {
	if k == "audiobook" {
		return "audiobook"
	}
	return "ebook"
}

// bookSearch finds books for the public request box, marking those BookOrbit
// already has or someone already asked for.
func (s *Server) bookSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	kind := bookKind(r.URL.Query().Get("kind"))
	if len(q) < 2 || len(q) > 200 {
		jsonErr(w, http.StatusBadRequest, "enter between 2 and 200 characters")
		return
	}
	bo := s.bookOrbit()
	if !s.booksOn() || bo == nil {
		jsonErr(w, http.StatusServiceUnavailable, "book requests are not open")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := openLibrarySearch(ctx, q)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "book search is unavailable right now; try again later")
		return
	}
	booksMu.Lock()
	asked := map[string]bool{}
	for _, m := range s.loadBooks() {
		if m.Status == "pending" || m.Status == "approved" {
			asked[m.Key+"|"+m.Kind] = true
		}
	}
	booksMu.Unlock()
	if len(res) > 0 {
		items := make([]map[string]any, 0, len(res))
		for _, x := range res {
			it := map[string]any{"title": x.Title, "mediaKind": kind}
			if len(x.Authors) > 0 {
				it["author"] = x.Authors[0]
			}
			if x.ISBN13 != "" {
				it["isbn13"] = x.ISBN13
			}
			items = append(items, it)
		}
		if av, err := bo.availability(ctx, items); err == nil && len(av) == len(res) {
			for i := range res {
				res[i].InLibrary = av[i].Owned != nil
				res[i].Requested = av[i].Requested != nil
			}
		} else if err != nil && s.Log != nil {
			s.Log.Printf("books: BookOrbit availability: %v", err)
		}
	}
	for i := range res {
		if asked[res[i].Key+"|"+kind] {
			res[i].Requested = true
		}
	}
	if res == nil {
		res = []bookResult{}
	}
	writeJSON(w, map[string]any{"results": res})
}

// bookRequestCreate asks for a book.
func (s *Server) bookRequestCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key     string   `json:"key"`
		Title   string   `json:"title"`
		Authors []string `json:"authors"`
		Year    int      `json:"year"`
		ISBN13  string   `json:"isbn13"`
		Cover   string   `json:"cover"`
		Kind    string   `json:"kind"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Title, in.Kind = strings.TrimSpace(in.Title), bookKind(in.Kind)
	if !olKeyRe.MatchString(in.Key) || in.Title == "" || len(in.Title) > 400 || len(in.Authors) > 5 || len(in.ISBN13) > 13 {
		jsonErr(w, http.StatusBadRequest, "pick a book")
		return
	}
	if in.Cover != "" && !strings.HasPrefix(in.Cover, "https://covers.openlibrary.org/") {
		in.Cover = ""
	}
	if !s.booksOn() {
		jsonErr(w, http.StatusServiceUnavailable, "book requests are not open")
		return
	}
	if !reqLimit.allow(r) {
		w.Header().Set("Retry-After", "60")
		jsonErr(w, http.StatusTooManyRequests, "too many requests; try again in a minute")
		return
	}
	booksMu.Lock()
	list := s.loadBooks()
	pending := 0
	for _, m := range list {
		if m.Key == in.Key && m.Kind == in.Kind && (m.Status == "pending" || m.Status == "approved") {
			booksMu.Unlock()
			jsonErr(w, http.StatusConflict, in.Title+" has already been requested")
			return
		}
		if m.Status == "pending" {
			pending++
		}
	}
	if pending >= 200 {
		booksMu.Unlock()
		jsonErr(w, http.StatusTooManyRequests, "too many book requests are waiting; try again later")
		return
	}
	idb := make([]byte, 8)
	_, _ = rand.Read(idb)
	authors := []string{}
	for _, a := range in.Authors {
		if a = strings.TrimSpace(a); a != "" && len(a) < 200 {
			authors = append(authors, a)
		}
	}
	m := bookRequest{ID: hex.EncodeToString(idb), Key: in.Key, Title: in.Title, Authors: authors, Year: in.Year, ISBN13: in.ISBN13, Cover: in.Cover,
		Kind: in.Kind, Status: "pending", RequestedAt: time.Now()}
	list = append(list, m)
	err := s.saveBooks(list)
	booksMu.Unlock()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not save the request")
		return
	}
	if s.Cfg.Get().Requests.AutoBooks {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if _, err := s.decideBook(ctx, m.ID, "approve"); err != nil && s.Log != nil {
			s.Log.Printf("books: auto-approve of %s: %v", m.Title, err)
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": m.ID})
}

// decideBook approves (creates the request in BookOrbit) or declines.
func (s *Server) decideBook(ctx context.Context, id, action string) (bookRequest, error) {
	if action != "approve" && action != "decline" {
		return bookRequest{}, errors.New("invalid decision")
	}
	booksMu.Lock()
	var m bookRequest
	found := false
	for _, x := range s.loadBooks() {
		if x.ID == id {
			m, found = x, true
		}
	}
	booksMu.Unlock()
	if !found || m.Status != "pending" {
		return bookRequest{}, errors.New("the request is no longer waiting; refresh the list")
	}
	m.DecidedAt = time.Now()
	if action == "decline" {
		m.Status = "declined"
	} else {
		bo := s.bookOrbit()
		if bo == nil {
			return m, errors.New("BookOrbit is not connected (Settings › Connections)")
		}
		body := map[string]any{"title": m.Title, "mediaKind": m.Kind, "note": "Requested through HyveMynd"}
		if len(m.Authors) > 0 {
			body["authors"] = m.Authors
		}
		if m.ISBN13 != "" {
			body["isbn13"] = m.ISBN13
		}
		if m.Year > 0 {
			body["publishedYear"] = m.Year
		}
		if m.Cover != "" {
			body["coverUrl"] = m.Cover
		}
		if err := bo.do(ctx, http.MethodPost, "book-requests", body, nil); err != nil {
			return m, fmt.Errorf("BookOrbit did not take %s: %v", m.Title, err)
		}
		m.Status, m.Note = "approved", "sent to BookOrbit"
	}
	booksMu.Lock()
	defer booksMu.Unlock()
	list := s.loadBooks()
	for i := range list {
		if list[i].ID == id {
			list[i] = m
		}
	}
	return m, s.saveBooks(list)
}

func (s *Server) bookDecide(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	m, err := s.decideBook(ctx, r.PathValue("id"), r.PathValue("action"))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, m)
}

type bookView struct {
	bookRequest
	Image string `json:"image,omitempty"`
}

// bookLists gives the waiting book requests (oldest first) and the decided
// ones (newest first).
func (s *Server) bookLists() (waiting, decided []bookView) {
	booksMu.Lock()
	list := s.loadBooks()
	booksMu.Unlock()
	waiting, decided = []bookView{}, []bookView{}
	for _, m := range list {
		v := bookView{bookRequest: m, Image: imgURL(m.Cover)}
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

// ------------------------------------------------------------ settings

type bookOrbitView struct {
	URL       string `json:"url"`
	HasKey    bool   `json:"hasKey"`
	Category  string `json:"category"`
	CatExists bool   `json:"categoryExists"`
	Books     *int   `json:"books,omitempty"`
}

func (s *Server) adminBookOrbit(w http.ResponseWriter, r *http.Request) {
	b := s.Cfg.Get().BookOrbit
	v := bookOrbitView{URL: b.URL, HasKey: b.APIKey != "", Category: bookCategory(b)}
	if c, ok := s.Cfg.Get().Category(v.Category); ok && c.Books() {
		v.CatExists = true
	}
	writeJSON(w, v)
}

func bookCategory(b config.BookOrbit) string {
	if c := strings.TrimSpace(b.Category); c != "" {
		return c
	}
	return "books"
}

// adminBookOrbitSave keeps the address and key, and adds the books category
// BookOrbit's download client sends to, when there is none yet.
func (s *Server) adminBookOrbitSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL      string `json:"url"`
		APIKey   string `json:"apiKey"`
		Category string `json:"category"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
	if in.URL != "" && !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		jsonErr(w, http.StatusBadRequest, "the address must start with http:// or https://")
		return
	}
	in.Category = strings.TrimSpace(in.Category)
	if in.Category != "" && (strings.ContainsAny(in.Category, "/\\") || len(in.Category) > 40) {
		jsonErr(w, http.StatusBadRequest, "the category name may not contain slashes")
		return
	}
	next := s.Cfg.Get()
	next.BookOrbit.URL, next.BookOrbit.Category = in.URL, in.Category
	if in.APIKey != "" {
		next.BookOrbit.APIKey = secret.String(strings.TrimSpace(in.APIKey))
	}
	if in.URL == "" {
		next.BookOrbit.APIKey = ""
	}
	name := bookCategory(next.BookOrbit)
	if next.BookOrbit.Ready() {
		if c, ok := next.Category(name); !ok {
			next.Categories = append(next.Categories, config.Category{Name: name, Kind: "books"})
		} else if !c.Books() {
			jsonErr(w, http.StatusBadRequest, "the category "+name+" already exists for something else; choose another name for books")
			return
		}
	}
	if err := s.Cfg.Set(next); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.adminBookOrbit(w, r)
}

func (s *Server) adminBookOrbitTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL    string `json:"url"`
		APIKey string `json:"apiKey"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if key == "" {
		key = string(s.Cfg.Get().BookOrbit.APIKey)
	}
	if strings.TrimSpace(in.URL) == "" || key == "" {
		jsonErr(w, http.StatusBadRequest, "enter the address and an API key")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	bo := newBookOrbit(in.URL, key)
	eb, au, err := bo.bookCounts(ctx)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var sum struct {
		Pending int `json:"pending"`
	}
	reqOK := bo.do(ctx, http.MethodGet, "book-requests/summary", nil, &sum) == nil
	writeJSON(w, map[string]any{"ebooks": eb, "audiobooks": au, "requests": reqOK})
}
