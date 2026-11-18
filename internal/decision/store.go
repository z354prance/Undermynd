package decision

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Ladder is every quality the arrs know, lowest first (Radarr's order).
var Ladder = []string{"Unknown", "WORKPRINT", "CAM", "TELESYNC", "TELECINE", "REGIONAL", "DVDSCR", "SDTV", "DVD", "DVD-R",
	"WEBDL-480p", "WEBRip-480p", "Bluray-480p", "Bluray-576p", "HDTV-720p", "WEBDL-720p", "WEBRip-720p", "Bluray-720p",
	"HDTV-1080p", "WEBDL-1080p", "WEBRip-1080p", "Bluray-1080p", "Remux-1080p",
	"HDTV-2160p", "WEBDL-2160p", "WEBRip-2160p", "Bluray-2160p", "Remux-2160p", "BR-DISK", "Raw-HD"}

// Store keeps profiles, custom formats and quality sizes in the database.
type Store struct{ DB *sql.DB }

// ErrNotFound is returned for a profile or format that does not exist.
var ErrNotFound = errors.New("not found")

func (s *Store) list(table string, each func(id int64, source string, body []byte) error) error {
	rows, err := s.DB.Query(`SELECT id, source, body FROM ` + table + ` ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var src string
		var body []byte
		if err := rows.Scan(&id, &src, &body); err != nil {
			return err
		}
		if err := each(id, src, body); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Profiles lists the profiles, adding the defaults the first time.
func (s *Store) Profiles() ([]Profile, error) {
	var out []Profile
	err := s.list("profiles", func(id int64, src string, body []byte) error {
		var p Profile
		if err := json.Unmarshal(body, &p); err != nil {
			return err
		}
		p.ID, p.Source = id, src
		out = append(out, p)
		return nil
	})
	if err != nil || len(out) > 0 {
		return out, err
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM profiles`).Scan(&n)
	if n > 0 {
		return out, nil
	}
	for _, p := range Defaults() {
		if _, err := s.SaveProfile(p); err != nil {
			return nil, err
		}
	}
	return s.Profiles()
}

// Profile returns one profile.
func (s *Store) Profile(id int64) (Profile, error) {
	ps, err := s.Profiles()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, ErrNotFound
}

// ProfileByName returns a profile by its name, case ignored.
func (s *Store) ProfileByName(name string) (Profile, error) {
	ps, err := s.Profiles()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return Profile{}, ErrNotFound
}

// Check says what is wrong with a profile, or nil.
func (p Profile) Check() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("a profile needs a name")
	}
	if len(p.Items) == 0 {
		return fmt.Errorf("a profile needs at least one quality")
	}
	any := false
	for _, it := range p.Items {
		any = any || it.Allowed
	}
	if !any {
		return fmt.Errorf("allow at least one quality")
	}
	if p.Cutoff != "" {
		found := false
		for _, it := range p.Items {
			if strings.EqualFold(it.Name, p.Cutoff) {
				found = it.Allowed
			}
		}
		if !found {
			return fmt.Errorf("the cutoff %q must be one of the allowed qualities", p.Cutoff)
		}
	}
	return nil
}

func (s *Store) save(table string, id int64, name, source string, v any) (int64, error) {
	var other int64
	err := s.DB.QueryRow(`SELECT id FROM `+table+` WHERE name = ? COLLATE NOCASE AND id != ?`, name, id).Scan(&other)
	if err == nil {
		return 0, fmt.Errorf("%q is already used", name)
	}
	body, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		r, err := s.DB.Exec(`INSERT INTO `+table+` (name, source, body) VALUES (?, ?, ?)`, name, source, body)
		if err != nil {
			return 0, err
		}
		return r.LastInsertId()
	}
	r, err := s.DB.Exec(`UPDATE `+table+` SET name = ?, source = ?, body = ? WHERE id = ?`, name, source, body, id)
	if err != nil {
		return 0, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	return id, nil
}

// SaveProfile adds (ID 0) or replaces a profile and returns its ID.
func (s *Store) SaveProfile(p Profile) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	if err := p.Check(); err != nil {
		return 0, err
	}
	if p.Cutoff == "" {
		for _, it := range p.Items {
			if it.Allowed {
				p.Cutoff = it.Name
			}
		}
	}
	return s.save("profiles", p.ID, p.Name, p.Source, p)
}

// DeleteProfile removes a profile.
func (s *Store) DeleteProfile(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM profiles WHERE id = ?`, id)
	return err
}

// Formats lists the custom formats.
func (s *Store) Formats() ([]Format, error) {
	var out []Format
	err := s.list("custom_formats", func(id int64, src string, body []byte) error {
		var f Format
		if err := json.Unmarshal(body, &f); err != nil {
			return err
		}
		f.ID, f.Source = id, src
		out = append(out, f)
		return nil
	})
	return out, err
}

// SaveFormat adds or replaces a custom format.
func (s *Store) SaveFormat(f Format) (int64, error) {
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		return 0, fmt.Errorf("a custom format needs a name")
	}
	if len(f.Specs) == 0 {
		return 0, fmt.Errorf("a custom format needs at least one condition")
	}
	for _, sp := range f.Specs {
		if (sp.Kind == "title" || sp.Kind == "group" || sp.Kind == "edition") && compile(sp.Value) == nil {
			return 0, fmt.Errorf("%s: the pattern does not compile", sp.Name)
		}
	}
	var old string
	if f.ID != 0 {
		s.DB.QueryRow(`SELECT name FROM custom_formats WHERE id = ?`, f.ID).Scan(&old)
	}
	id, err := s.save("custom_formats", f.ID, f.Name, f.Source, f)
	if err != nil {
		return 0, err
	}
	if old != "" && old != f.Name {
		s.renameScores(old, f.Name)
	}
	return id, nil
}

// renameScores keeps profiles' scores pointing at a renamed format.
func (s *Store) renameScores(from, to string) {
	ps, _ := s.Profiles()
	for _, p := range ps {
		if v, ok := p.Scores[from]; ok {
			delete(p.Scores, from)
			p.Scores[to] = v
			s.SaveProfile(p)
		}
	}
}

// DeleteFormat removes a custom format and its scores.
func (s *Store) DeleteFormat(id int64) error {
	var name string
	s.DB.QueryRow(`SELECT name FROM custom_formats WHERE id = ?`, id).Scan(&name)
	if _, err := s.DB.Exec(`DELETE FROM custom_formats WHERE id = ?`, id); err != nil {
		return err
	}
	ps, _ := s.Profiles()
	for _, p := range ps {
		if _, ok := p.Scores[name]; ok {
			delete(p.Scores, name)
			s.SaveProfile(p)
		}
	}
	return nil
}

// Sizes returns the quality size limits.
func (s *Store) Sizes() (map[string]SizeLimit, error) {
	rows, err := s.DB.Query(`SELECT quality, min, max FROM quality_sizes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SizeLimit{}
	for rows.Next() {
		var q string
		var l SizeLimit
		if err := rows.Scan(&q, &l.Min, &l.Max); err != nil {
			return nil, err
		}
		out[q] = l
	}
	return out, rows.Err()
}

// SetSizes replaces the size limits of the qualities given.
func (s *Store) SetSizes(m map[string]SizeLimit) error {
	for q, l := range m {
		if _, err := s.DB.Exec(`INSERT INTO quality_sizes (quality, min, max) VALUES (?, ?, ?)
			ON CONFLICT(quality) DO UPDATE SET min = excluded.min, max = excluded.max`, q, l.Min, l.Max); err != nil {
			return err
		}
	}
	return nil
}

// Engine builds a decision engine for a profile with every stored format and size.
func (s *Store) Engine(p Profile) (*Engine, error) {
	fs, err := s.Formats()
	if err != nil {
		return nil, err
	}
	sz, err := s.Sizes()
	if err != nil {
		return nil, err
	}
	return &Engine{Profile: p, Formats: fs, Sizes: sz}, nil
}

func items(allowed ...string) []Item {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var out []Item
	for _, q := range Ladder {
		out = append(out, Item{Name: q, Qualities: []string{q}, Allowed: ok[q]})
	}
	return out
}

// Defaults are the profiles a new install starts with, like the arrs'.
func Defaults() []Profile {
	any := items()
	for i := range any {
		any[i].Allowed = any[i].Name != "Unknown" && any[i].Name != "BR-DISK" && any[i].Name != "Raw-HD"
	}
	return []Profile{
		{Name: "Any", Items: any, Cutoff: "Bluray-1080p", Scores: map[string]int{}},
		{Name: "HD-1080p", Items: items("HDTV-1080p", "WEBDL-1080p", "WEBRip-1080p", "Bluray-1080p"), Cutoff: "Bluray-1080p", UpgradesAllowed: true, Scores: map[string]int{}},
		{Name: "HD - 720p/1080p", Items: items("HDTV-720p", "WEBDL-720p", "WEBRip-720p", "Bluray-720p", "HDTV-1080p", "WEBDL-1080p", "WEBRip-1080p", "Bluray-1080p"), Cutoff: "Bluray-720p", UpgradesAllowed: true, Scores: map[string]int{}},
		{Name: "Ultra-HD", Items: items("HDTV-2160p", "WEBDL-2160p", "WEBRip-2160p", "Bluray-2160p", "Remux-2160p"), Cutoff: "Remux-2160p", UpgradesAllowed: true, Scores: map[string]int{}},
	}
}
