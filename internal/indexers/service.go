package indexers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/secret"
)

// Indexer is one configured indexer.
type Indexer struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // torznab or newznab
	URL        string `json:"url"`
	APIKey     string `json:"apiKey,omitempty"` // write-only: never sent back
	HasKey     bool   `json:"hasKey"`
	Categories []int  `json:"categories"` // empty: whatever a search asks for
	Priority   int    `json:"priority"`   // 1 (first) to 50; ties broken by name
	Enabled    bool   `json:"enabled"`
	QueryLimit int    `json:"queryLimit"` // a day; 0 is no limit
	GrabLimit  int    `json:"grabLimit"`  // a day; 0 is no limit
	Via        string `json:"via"`        // "prowlarr" when reached through Prowlarr
	// Health and use.
	Failures      int       `json:"failures"`
	DisabledUntil time.Time `json:"disabledUntil"`
	LastError     string    `json:"lastError"`
	LastOK        time.Time `json:"lastOk"`
	QueriesToday  int       `json:"queriesToday"`
	GrabsToday    int       `json:"grabsToday"`
}

// Protocol is torrent or usenet.
func (i Indexer) Protocol() string {
	if i.Kind == Newznab {
		return "usenet"
	}
	return "torrent"
}

// Submitter starts downloads (the pipeline engine).
type Submitter interface {
	Submit(pipeline.SubmitRequest) (job.Job, error)
	SubmitNZB(pipeline.SubmitNZBRequest) (job.Job, error)
}

// Service keeps the indexers and searches them.
type Service struct {
	DB     *sql.DB
	HTTP   *http.Client
	Engine Submitter
	Log    *log.Logger
	Now    func() time.Time
	// Gap is the least time between two requests to one indexer.
	Gap time.Duration

	mu   sync.Mutex
	last map[int64]time.Time
}

const cols = `i.id, i.name, i.kind, i.url, i.api_key, i.categories, i.priority, i.enabled, i.query_limit, i.grab_limit, i.via,
	COALESCE(s.failures, 0), COALESCE(s.disabled_until, ''), COALESCE(s.last_error, ''), COALESCE(s.last_ok, ''),
	COALESCE(u.queries, 0), COALESCE(u.grabs, 0)`

func (s *Service) today() string { return s.Now().UTC().Format("2006-01-02") }

func parseT(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(db.TimeFormat, v)
	return t
}

func (s *Service) scan(rows interface{ Scan(...any) error }) (Indexer, error) {
	var i Indexer
	var key, cats, until, lastOK string
	var enabled int
	err := rows.Scan(&i.ID, &i.Name, &i.Kind, &i.URL, &key, &cats, &i.Priority, &enabled, &i.QueryLimit, &i.GrabLimit, &i.Via,
		&i.Failures, &until, &i.LastError, &lastOK, &i.QueriesToday, &i.GrabsToday)
	if err != nil {
		return i, err
	}
	i.Enabled, i.DisabledUntil, i.LastOK = enabled != 0, parseT(until), parseT(lastOK)
	if key != "" {
		if plain, err := secret.Decrypt(key); err == nil {
			i.APIKey = plain
		}
		i.HasKey = true
	}
	i.Categories = []int{}
	for _, c := range strings.Split(cats, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
			i.Categories = append(i.Categories, n)
		}
	}
	return i, nil
}

// List returns every indexer, by priority then name, keys included (for use
// inside HyveMynd; strip them before sending anywhere).
func (s *Service) List() ([]Indexer, error) {
	rows, err := s.DB.Query(`SELECT `+cols+` FROM indexers i
		LEFT JOIN indexer_status s ON s.indexer_id = i.id
		LEFT JOIN indexer_usage u ON u.indexer_id = i.id AND u.day = ?
		ORDER BY i.priority, i.name`, s.today())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Indexer{}
	for rows.Next() {
		i, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Get returns one indexer.
func (s *Service) Get(id int64) (Indexer, error) {
	list, err := s.List()
	if err != nil {
		return Indexer{}, err
	}
	for _, i := range list {
		if i.ID == id {
			return i, nil
		}
	}
	return Indexer{}, errors.New("no such indexer")
}

// Public strips the key.
func Public(list []Indexer) []Indexer {
	out := make([]Indexer, len(list))
	for n, i := range list {
		i.APIKey = ""
		out[n] = i
	}
	return out
}

func catsString(c []int) string {
	s := make([]string, 0, len(c))
	for _, n := range c {
		s = append(s, strconv.Itoa(n))
	}
	return strings.Join(s, ",")
}

func validate(i *Indexer) error {
	i.Name = strings.TrimSpace(i.Name)
	i.URL = strings.TrimSpace(i.URL)
	if i.Name == "" {
		return errors.New("give the indexer a name")
	}
	if i.Kind != Torznab && i.Kind != Newznab {
		return errors.New("the kind is torznab (torrents) or newznab (usenet)")
	}
	if !strings.HasPrefix(i.URL, "http://") && !strings.HasPrefix(i.URL, "https://") {
		return errors.New("the address must start with http:// or https://")
	}
	if i.Priority < 1 || i.Priority > 50 {
		i.Priority = 25
	}
	if i.QueryLimit < 0 {
		i.QueryLimit = 0
	}
	if i.GrabLimit < 0 {
		i.GrabLimit = 0
	}
	return nil
}

// Save adds an indexer (ID 0) or changes one. An empty key keeps the old one.
func (s *Service) Save(i Indexer) (Indexer, error) {
	if err := validate(&i); err != nil {
		return i, err
	}
	if list, err := s.List(); err == nil {
		for _, o := range list {
			if o.ID != i.ID && strings.EqualFold(o.Name, i.Name) {
				return i, errors.New("an indexer with that name exists")
			}
		}
	}
	enabled := 0
	if i.Enabled {
		enabled = 1
	}
	key := ""
	if strings.TrimSpace(i.APIKey) != "" {
		enc, err := secret.Encrypt(strings.TrimSpace(i.APIKey))
		if err != nil {
			return i, err
		}
		key = enc
	}
	if i.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO indexers (name, kind, url, api_key, categories, priority, enabled, query_limit, grab_limit, via, added)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, i.Name, i.Kind, i.URL, key, catsString(i.Categories), i.Priority, enabled, i.QueryLimit, i.GrabLimit, i.Via, db.Stamp(s.Now()))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return i, errors.New("an indexer with that name exists")
			}
			return i, err
		}
		i.ID, _ = res.LastInsertId()
	} else {
		q := `UPDATE indexers SET name = ?, kind = ?, url = ?, categories = ?, priority = ?, enabled = ?, query_limit = ?, grab_limit = ?, via = ?`
		args := []any{i.Name, i.Kind, i.URL, catsString(i.Categories), i.Priority, enabled, i.QueryLimit, i.GrabLimit, i.Via}
		if key != "" {
			q += `, api_key = ?`
			args = append(args, key)
		}
		res, err := s.DB.Exec(q+` WHERE id = ?`, append(args, i.ID)...)
		if err != nil {
			return i, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return i, errors.New("no such indexer")
		}
		// A changed indexer gets a fresh start.
		_, _ = s.DB.Exec(`DELETE FROM indexer_status WHERE indexer_id = ?`, i.ID)
	}
	return s.Get(i.ID)
}

// Delete removes an indexer.
func (s *Service) Delete(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM indexers WHERE id = ?`, id)
	return err
}

// backoff is how long a failing indexer rests: 5 minutes, doubling, up to a day.
func backoff(failures int) time.Duration {
	d := 5 * time.Minute
	for n := 1; n < failures && d < 24*time.Hour; n++ {
		d *= 2
	}
	if d > 24*time.Hour {
		d = 24 * time.Hour
	}
	return d
}

func (s *Service) record(id int64, err error) {
	if err == nil {
		_, _ = s.DB.Exec(`INSERT INTO indexer_status (indexer_id, failures, disabled_until, last_error, last_ok) VALUES (?, 0, '', '', ?)
			ON CONFLICT(indexer_id) DO UPDATE SET failures = 0, disabled_until = '', last_error = '', last_ok = excluded.last_ok`, id, db.Stamp(s.Now()))
		return
	}
	var failures int
	_ = s.DB.QueryRow(`SELECT failures FROM indexer_status WHERE indexer_id = ?`, id).Scan(&failures)
	failures++
	until := db.Stamp(s.Now().Add(backoff(failures)))
	_, _ = s.DB.Exec(`INSERT INTO indexer_status (indexer_id, failures, disabled_until, last_error) VALUES (?, ?, ?, ?)
		ON CONFLICT(indexer_id) DO UPDATE SET failures = excluded.failures, disabled_until = excluded.disabled_until, last_error = excluded.last_error`,
		id, failures, until, err.Error())
}

func (s *Service) count(id int64, field string) {
	_, _ = s.DB.Exec(`INSERT INTO indexer_usage (indexer_id, day, `+field+`) VALUES (?, ?, 1)
		ON CONFLICT(indexer_id, day) DO UPDATE SET `+field+` = `+field+` + 1`, id, s.today())
}

// wait keeps at least Gap between two requests to one indexer.
func (s *Service) wait(ctx context.Context, id int64) error {
	s.mu.Lock()
	if s.last == nil {
		s.last = map[int64]time.Time{}
	}
	next := s.last[id].Add(s.Gap)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	s.last[id] = next
	s.mu.Unlock()
	if d := time.Until(next); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// Test asks an indexer what it can search, without saving anything.
func (s *Service) Test(ctx context.Context, i Indexer) (Caps, error) {
	if err := validate(&i); err != nil {
		return Caps{}, err
	}
	b, err := get(ctx, s.HTTP, capsURL(i.URL, i.APIKey), 2<<20)
	if err != nil {
		return Caps{}, err
	}
	caps, err := ParseCaps(b)
	if err == nil && i.ID != 0 {
		s.record(i.ID, nil)
	}
	return caps, err
}

// Skipped says why an indexer took no part in a search.
type Skipped struct {
	Indexer string `json:"indexer"`
	Reason  string `json:"reason"`
}

// SearchResult is what one search across the indexers found.
type SearchResult struct {
	Results []Result  `json:"results"`
	Skipped []Skipped `json:"skipped"`
	Errors  []Skipped `json:"errors"`
}

// usable says why an indexer cannot be asked now, or "" when it can.
func (s *Service) usable(i Indexer, protocol string) string {
	switch {
	case !i.Enabled:
		return "turned off"
	case protocol != "" && i.Protocol() != protocol:
		return "other protocol"
	case i.DisabledUntil.After(s.Now()):
		return fmt.Sprintf("resting after %d failure(s) until %s: %s", i.Failures, i.DisabledUntil.Local().Format("15:04"), i.LastError)
	case i.QueryLimit > 0 && i.QueriesToday >= i.QueryLimit:
		return fmt.Sprintf("reached its %d searches for today", i.QueryLimit)
	}
	return ""
}

// Search asks every usable indexer at once and returns what they found, most
// seeded (torrents) or newest first. protocol limits it to "torrent" or
// "usenet"; ids, when given, to those indexers.
func (s *Service) Search(ctx context.Context, q Query, protocol string, ids ...int64) (SearchResult, error) {
	list, err := s.List()
	if err != nil {
		return SearchResult{}, err
	}
	only := map[int64]bool{}
	for _, id := range ids {
		only[id] = true
	}
	var out SearchResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, i := range list {
		if len(only) > 0 && !only[i.ID] {
			continue
		}
		if why := s.usable(i, protocol); why != "" {
			if why != "other protocol" {
				out.Skipped = append(out.Skipped, Skipped{i.Name, why})
			}
			continue
		}
		wg.Add(1)
		go func(i Indexer) {
			defer wg.Done()
			ictx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			if err := s.wait(ictx, i.ID); err != nil {
				return
			}
			cats := q.Categories
			if len(cats) == 0 {
				cats = DefaultCategories(q.Type)
			}
			if len(i.Categories) > 0 {
				cats = i.Categories
			}
			s.count(i.ID, "queries")
			b, err := get(ictx, s.HTTP, searchURL(i.URL, i.APIKey, q, cats), 16<<20)
			var res []Result
			if err == nil {
				res, err = Parse(b, i.Protocol())
			}
			s.record(i.ID, err)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors = append(out.Errors, Skipped{i.Name, err.Error()})
				return
			}
			for _, r := range res {
				r.Indexer, r.IndexerID, r.IndexerPrio = i.Name, i.ID, i.Priority
				out.Results = append(out.Results, r)
			}
		}(i)
	}
	wg.Wait()
	sort.SliceStable(out.Results, func(a, b int) bool {
		x, y := out.Results[a], out.Results[b]
		if x.Protocol == "torrent" && y.Protocol == "torrent" && x.Seeders != y.Seeders {
			return x.Seeders > y.Seeders
		}
		return x.Published.After(y.Published)
	})
	if out.Results == nil {
		out.Results = []Result{}
	}
	return out, nil
}

// Grab sends a result to the pipeline in a category.
func (s *Service) Grab(ctx context.Context, r Result, category string) (job.Job, error) {
	i, err := s.Get(r.IndexerID)
	if err != nil {
		return job.Job{}, err
	}
	if i.GrabLimit > 0 && i.GrabsToday >= i.GrabLimit {
		return job.Job{}, fmt.Errorf("%s reached its %d grabs for today", i.Name, i.GrabLimit)
	}
	link := strings.TrimSpace(r.Link)
	var j job.Job
	switch {
	case strings.HasPrefix(link, "magnet:"):
		j, err = s.Engine.Submit(pipeline.SubmitRequest{Source: "indexer", Category: category, Magnet: link})
	case strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://"):
		var data []byte
		data, err = get(ctx, s.HTTP, link, 50<<20)
		if err != nil {
			return job.Job{}, fmt.Errorf("fetching the release: %w", err)
		}
		head := data[:min(len(data), 2048)]
		switch {
		case bytes.Contains(head, []byte("<nzb")):
			j, err = s.Engine.SubmitNZB(pipeline.SubmitNZBRequest{Source: "indexer", Category: category, Name: r.Title, NZB: data})
		case len(data) > 0 && data[0] == 'd':
			j, err = s.Engine.Submit(pipeline.SubmitRequest{Source: "indexer", Category: category, Torrent: data})
		case bytes.HasPrefix(bytes.TrimSpace(data), []byte("magnet:")):
			j, err = s.Engine.Submit(pipeline.SubmitRequest{Source: "indexer", Category: category, Magnet: string(bytes.TrimSpace(data))})
		default:
			return job.Job{}, errors.New("the indexer did not send a torrent or an NZB")
		}
	default:
		return job.Job{}, errors.New("the release has no download address")
	}
	if err != nil {
		return job.Job{}, err
	}
	s.count(i.ID, "grabs")
	s.Log.Printf("indexers: grabbed %s from %s into %s", r.Title, i.Name, category)
	return j, nil
}

// ProwlarrIndexer is one indexer as Prowlarr lists it.
type ProwlarrIndexer struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Enable         bool   `json:"enable"`
	Priority       int    `json:"priority"`
	Implementation string `json:"implementation"`
	Fields         []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	} `json:"fields"`
}

// ImportProwlarr adds Prowlarr's indexers. Newznab and Torznab indexers whose
// address and key Prowlarr shows are added directly; the rest (its built-in
// tracker definitions) are reached through Prowlarr's own per-indexer feed,
// marked "via prowlarr", until HyveMynd can reach them by itself. Indexers
// with a name already present are left as they are.
func (s *Service) ImportProwlarr(ctx context.Context, base, key string) (added []string, err error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/indexer", nil)
	if err != nil {
		return nil, errors.New("invalid Prowlarr address")
	}
	req.Header.Set("X-Api-Key", key)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("Prowlarr did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("Prowlarr refused the API key")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prowlarr answered HTTP %d", resp.StatusCode)
	}
	var list []ProwlarrIndexer
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, errors.New("Prowlarr's answer could not be read")
	}
	have, err := s.List()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, h := range have {
		names[strings.ToLower(h.Name)] = true
	}
	for _, p := range list {
		if names[strings.ToLower(p.Name)] {
			continue
		}
		field := func(n string) string {
			for _, f := range p.Fields {
				if strings.EqualFold(f.Name, n) {
					if v, ok := f.Value.(string); ok {
						return strings.TrimSpace(v)
					}
				}
			}
			return ""
		}
		i := Indexer{Name: p.Name, Kind: Torznab, Enabled: p.Enable, Priority: p.Priority}
		if strings.EqualFold(p.Protocol, "usenet") {
			i.Kind = Newznab
		}
		direct := strings.EqualFold(p.Implementation, "Newznab") || strings.EqualFold(p.Implementation, "Torznab")
		apiKey, baseURL := field("apiKey"), field("baseUrl")
		if direct && baseURL != "" && apiKey != "" && !strings.Contains(apiKey, "***") {
			i.URL = strings.TrimRight(baseURL, "/") + "/" + strings.Trim(firstNonEmpty(field("apiPath"), "/api"), "/")
			i.APIKey = apiKey
		} else {
			i.URL = fmt.Sprintf("%s/%d/api", base, p.ID)
			i.APIKey, i.Via = key, "prowlarr"
		}
		if _, err := s.Save(i); err != nil {
			return added, fmt.Errorf("%s: %w", p.Name, err)
		}
		names[strings.ToLower(p.Name)] = true
		added = append(added, p.Name)
	}
	return added, nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}
