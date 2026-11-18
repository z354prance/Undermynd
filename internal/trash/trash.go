// Package trash reads the TRaSH Guides (roadmap G-1, Recyclarr parity): their
// custom formats with scores, quality profiles and quality sizes, turns the
// chosen profiles into Undermynd's, shows what would change, and keeps them
// in sync, leaving scores changed here alone.
package trash

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/decision"
	"github.com/z354prance/undermynd/internal/parser"
)

// DefaultURL is the guides repository as a zip.
const DefaultURL = "https://codeload.github.com/TRaSH-Guides/Guides/zip/refs/heads/master"

// Format is a guide custom format.
type Format struct {
	TrashID string          `json:"trash_id"`
	Name    string          `json:"name"`
	Scores  map[string]int  `json:"trash_scores"`
	Specs   json.RawMessage `json:"specifications"`
}

// Profile is a guide quality profile.
type Profile struct {
	TrashID           string `json:"trash_id"`
	Name              string `json:"name"`
	Description       string `json:"trash_description"`
	ScoreSet          string `json:"trash_score_set"`
	UpgradeAllowed    bool   `json:"upgradeAllowed"`
	Cutoff            string `json:"cutoff"`
	MinFormatScore    int    `json:"minFormatScore"`
	CutoffFormatScore int    `json:"cutoffFormatScore"`
	MinUpgradeScore   int    `json:"minUpgradeFormatScore"`
	Language          string `json:"language"`
	Items             []struct {
		Name    string   `json:"name"`
		Allowed bool     `json:"allowed"`
		Items   []string `json:"items"`
	} `json:"items"`
	FormatItems map[string]string `json:"formatItems"` // format name -> trash id
}

// Size is a guide quality-size file.
type Size struct {
	TrashID   string `json:"trash_id"`
	Type      string `json:"type"`
	Qualities []struct {
		Quality string  `json:"quality"`
		Min     float64 `json:"min"`
		Max     float64 `json:"max"`
	} `json:"qualities"`
}

// App is one arr's part of the guides.
type App struct {
	Formats  map[string]Format `json:"-"` // by trash id
	Profiles []Profile         `json:"profiles"`
	Sizes    []Size            `json:"sizes"`
}

// Guide is the guides as read.
type Guide struct {
	At   time.Time       `json:"at"`
	Apps map[string]*App `json:"apps"` // radarr, sonarr
}

// Client downloads the guides, keeping them for an hour.
type Client struct {
	HTTP *http.Client
	URL  func() string

	mu    sync.Mutex
	cache *Guide
}

// Get returns the guides, downloading them at most once an hour.
func (c *Client) Get(ctx context.Context) (*Guide, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache != nil && time.Since(c.cache.At) < time.Hour {
		return c.cache, nil
	}
	u := DefaultURL
	if c.URL != nil && c.URL() != "" {
		u = c.URL()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the TRaSH Guides are not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the TRaSH Guides answered HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return nil, err
	}
	g, err := Read(b)
	if err != nil {
		return nil, err
	}
	c.cache = g
	return g, nil
}

// Read reads the guides from a zip of the repository.
func Read(b []byte) (*Guide, error) {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("the TRaSH Guides download is not a zip: %w", err)
	}
	g := &Guide{At: time.Now(), Apps: map[string]*App{"radarr": {Formats: map[string]Format{}}, "sonarr": {Formats: map[string]Format{}}}}
	for _, f := range z.File {
		i := strings.Index(f.Name, "docs/json/")
		if i < 0 || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		parts := strings.Split(f.Name[i+len("docs/json/"):], "/")
		if len(parts) != 3 {
			continue
		}
		app := g.Apps[parts[0]]
		if app == nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 4<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		switch parts[1] {
		case "cf":
			var x Format
			if json.Unmarshal(data, &x) == nil && x.TrashID != "" {
				app.Formats[x.TrashID] = x
			}
		case "quality-profiles":
			var x Profile
			if json.Unmarshal(data, &x) == nil && x.TrashID != "" && len(x.Items) > 0 {
				app.Profiles = append(app.Profiles, x)
			}
		case "quality-size":
			var x Size
			if json.Unmarshal(data, &x) == nil && len(x.Qualities) > 0 {
				if x.Type == "" {
					x.Type = strings.TrimSuffix(path.Base(f.Name), ".json")
				}
				app.Sizes = append(app.Sizes, x)
			}
		}
	}
	for _, a := range g.Apps {
		sort.Slice(a.Profiles, func(i, j int) bool { return a.Profiles[i].Name < a.Profiles[j].Name })
	}
	if len(g.Apps["radarr"].Formats)+len(g.Apps["sonarr"].Formats) == 0 {
		return nil, fmt.Errorf("the TRaSH Guides download has no custom formats")
	}
	return g, nil
}

// Source is where synced profiles and formats say they came from.
func Source(app string) string { return "trash-" + app }

// Plan turns the chosen guide profiles of one app, the formats they score and
// a quality-size file into what Undermynd stores.
func (g *Guide) Plan(app string, profileIDs []string, sizeType string) (decision.Imported, error) {
	a := g.Apps[app]
	if a == nil {
		return decision.Imported{}, fmt.Errorf("no %s in the guides", app)
	}
	want := map[string]bool{}
	for _, id := range profileIDs {
		want[id] = true
	}
	var im decision.Imported
	needed := map[string]bool{}
	for _, p := range a.Profiles {
		if !want[p.TrashID] {
			continue
		}
		dp := decision.Profile{Name: p.Name, UpgradesAllowed: p.UpgradeAllowed, MinFormatScore: p.MinFormatScore,
			CutoffFormatScore: p.CutoffFormatScore, MinUpgradeScore: p.MinUpgradeScore, Scores: map[string]int{}, GuideID: p.TrashID}
		if p.Language != "" && p.Language != "Any" {
			dp.Language = p.Language
		}
		// The guides list qualities best first; Undermynd keeps them lowest first.
		for i := len(p.Items) - 1; i >= 0; i-- {
			it := p.Items[i]
			item := decision.Item{Name: parser.Canonical(it.Name), Allowed: it.Allowed}
			if len(it.Items) > 0 {
				item.Name = it.Name
				for _, q := range it.Items {
					item.Qualities = append(item.Qualities, parser.Canonical(q))
				}
			} else {
				item.Qualities = []string{item.Name}
			}
			dp.Items = append(dp.Items, item)
		}
		dp.Cutoff = p.Cutoff
		for _, it := range p.Items {
			if it.Name == p.Cutoff && len(it.Items) == 0 {
				dp.Cutoff = parser.Canonical(p.Cutoff) // a single quality, named as Radarr does
			}
		}
		set := p.ScoreSet
		for name, id := range p.FormatItems {
			f, ok := a.Formats[id]
			if !ok {
				continue
			}
			score, ok := f.Scores[set]
			if !ok {
				score = f.Scores["default"]
			}
			if score != 0 {
				dp.Scores[f.Name] = score
			}
			needed[id] = true
			_ = name
		}
		dp.GuideScores = map[string]int{}
		for k, v := range dp.Scores {
			dp.GuideScores[k] = v
		}
		im.Profiles = append(im.Profiles, dp)
	}
	if len(im.Profiles) == 0 && len(profileIDs) > 0 {
		return im, fmt.Errorf("none of the chosen %s profiles is in the guides any more", app)
	}
	// Formats: the guides' fields are an object; the arrs' API has a list.
	var arrFormats []map[string]any
	ids := make([]string, 0, len(needed))
	for id := range needed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		f := a.Formats[id]
		var specs []struct {
			Name           string                     `json:"name"`
			Implementation string                     `json:"implementation"`
			Negate         bool                       `json:"negate"`
			Required       bool                       `json:"required"`
			Fields         map[string]json.RawMessage `json:"fields"`
		}
		if err := json.Unmarshal(f.Specs, &specs); err != nil {
			continue
		}
		var out []map[string]any
		for _, s := range specs {
			var fields []map[string]any
			for k, v := range s.Fields {
				fields = append(fields, map[string]any{"name": k, "value": v})
			}
			out = append(out, map[string]any{"name": s.Name, "implementation": s.Implementation, "negate": s.Negate, "required": s.Required, "fields": fields})
		}
		arrFormats = append(arrFormats, map[string]any{"name": f.Name, "specifications": out})
	}
	fb, _ := json.Marshal(arrFormats)
	conv, err := decision.FromArr(app, []byte("[]"), fb, nil)
	if err != nil {
		return im, err
	}
	im.Formats, im.Skipped = conv.Formats, conv.Skipped
	if sizeType != "" {
		for _, s := range a.Sizes {
			if s.Type == sizeType {
				im.Sizes = map[string]decision.SizeLimit{}
				for _, q := range s.Qualities {
					im.Sizes[parser.Canonical(q.Quality)] = decision.SizeLimit{Min: q.Min, Max: q.Max}
				}
			}
		}
		if im.Sizes == nil {
			return im, fmt.Errorf("no %s quality sizes named %q in the guides", app, sizeType)
		}
	}
	return im, nil
}

// Change is one difference a sync would make.
type Change struct {
	Kind   string `json:"kind"` // profile, format, sizes
	Name   string `json:"name"`
	Action string `json:"action"` // add, change, keep
	Detail string `json:"detail,omitempty"`
}

// Merge keeps scores changed here since the last sync, and lists what the
// sync would change.
func Merge(app string, im *decision.Imported, existing []decision.Profile, formats []decision.Format, sizes map[string]decision.SizeLimit) []Change {
	src := Source(app)
	var out []Change
	// Names that clashed with another source carry " (trash-app)" here.
	plain := func(m map[string]int) map[string]int {
		o := map[string]int{}
		for k, v := range m {
			o[strings.TrimSuffix(k, " ("+src+")")] = v
		}
		return o
	}
	byName := map[string]decision.Profile{}
	for _, p := range existing {
		if p.Source == src {
			p.Scores, p.GuideScores = plain(p.Scores), plain(p.GuideScores)
			byName[strings.ToLower(strings.TrimSuffix(p.Name, " ("+src+")"))] = p
		}
	}
	for i := range im.Profiles {
		p := &im.Profiles[i]
		old, ok := byName[strings.ToLower(p.Name)]
		if !ok {
			out = append(out, Change{Kind: "profile", Name: p.Name, Action: "add", Detail: fmt.Sprintf("%d scored formats, cutoff %s", len(p.Scores), p.Cutoff)})
			continue
		}
		var kept, changed []string
		for f, v := range old.Scores {
			if g, ok := old.GuideScores[f]; !ok || g != v {
				// Changed here since the last sync: keep it.
				p.Scores[f] = v
				kept = append(kept, fmt.Sprintf("%s %d", f, v))
			}
		}
		for f, v := range p.GuideScores {
			if old.GuideScores[f] != v {
				changed = append(changed, fmt.Sprintf("%s %d → %d", f, old.GuideScores[f], v))
			}
		}
		for f := range old.GuideScores {
			if _, ok := p.GuideScores[f]; !ok {
				changed = append(changed, f+" dropped")
			}
		}
		sort.Strings(kept)
		sort.Strings(changed)
		detail := ""
		if len(changed) > 0 {
			detail = "scores: " + strings.Join(changed, ", ")
		}
		if old.Cutoff != p.Cutoff {
			detail = strings.TrimPrefix(detail+"; cutoff "+old.Cutoff+" → "+p.Cutoff, "; ")
		}
		if len(kept) > 0 {
			detail = strings.TrimPrefix(detail+"; kept your scores: "+strings.Join(kept, ", "), "; ")
		}
		action := "keep"
		if len(changed) > 0 || old.Cutoff != p.Cutoff || len(old.Items) != len(p.Items) {
			action = "change"
		}
		out = append(out, Change{Kind: "profile", Name: p.Name, Action: action, Detail: detail})
	}
	fByName := map[string]decision.Format{}
	for _, f := range formats {
		if f.Source == src {
			fByName[strings.ToLower(strings.TrimSuffix(f.Name, " ("+src+")"))] = f
		}
	}
	for _, f := range im.Formats {
		old, ok := fByName[strings.ToLower(f.Name)]
		switch {
		case !ok:
			out = append(out, Change{Kind: "format", Name: f.Name, Action: "add"})
		case !sameSpecs(old.Specs, f.Specs):
			out = append(out, Change{Kind: "format", Name: f.Name, Action: "change", Detail: "conditions changed"})
		}
	}
	if len(im.Sizes) > 0 {
		diff := 0
		for q, l := range im.Sizes {
			if sizes[q] != l {
				diff++
			}
		}
		if diff > 0 {
			out = append(out, Change{Kind: "sizes", Name: app, Action: "change", Detail: fmt.Sprintf("%d quality sizes", diff)})
		}
	}
	return out
}

func sameSpecs(a, b []decision.Spec) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
