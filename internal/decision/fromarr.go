package decision

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/z354prance/undermynd/internal/parser"
)

// The arrs' API shapes for quality profiles, custom formats and quality
// definitions (Radarr v3+, Sonarr v4).
type arrQuality struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type arrItem struct {
	ID      int        `json:"id"`
	Name    string     `json:"name"`
	Quality arrQuality `json:"quality"`
	Items   []arrItem  `json:"items"`
	Allowed bool       `json:"allowed"`
}

type arrProfile struct {
	Name              string    `json:"name"`
	UpgradeAllowed    bool      `json:"upgradeAllowed"`
	Cutoff            int       `json:"cutoff"`
	Items             []arrItem `json:"items"`
	MinFormatScore    int       `json:"minFormatScore"`
	CutoffFormatScore int       `json:"cutoffFormatScore"`
	MinUpgradeScore   int       `json:"minUpgradeFormatScore"`
	FormatItems       []struct {
		Name  string `json:"name"`
		Score int    `json:"score"`
	} `json:"formatItems"`
	Language *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"language"`
}

type arrField struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

type arrFormat struct {
	Name  string `json:"name"`
	Specs []struct {
		Name           string     `json:"name"`
		Implementation string     `json:"implementation"`
		Negate         bool       `json:"negate"`
		Required       bool       `json:"required"`
		Fields         []arrField `json:"fields"`
	} `json:"specifications"`
}

type arrDefinition struct {
	Quality arrQuality `json:"quality"`
	MinSize float64    `json:"minSize"`
	MaxSize *float64   `json:"maxSize"`
}

// Imported is what was read from one arr.
type Imported struct {
	Profiles []Profile
	Formats  []Format
	Sizes    map[string]SizeLimit
	Skipped  []string // conditions Undermynd cannot check, by format
}

var (
	radarrSource = map[int]string{0: "unknown", 1: "cam", 2: "telesync", 3: "telecine", 4: "workprint", 5: "dvd", 6: "tv", 7: "webdl", 8: "webrip", 9: "bluray"}
	sonarrSource = map[int]string{0: "unknown", 1: "television", 2: "televisionRaw", 3: "webdl", 4: "webrip", 5: "dvd", 6: "bluray", 7: "blurayRaw"}
	modifiers    = map[int]string{0: "none", 1: "regional", 2: "screener", 3: "rawhd", 4: "brdisk", 5: "remux"}
	releaseTypes = map[int]string{1: "singleEpisode", 2: "multiEpisode", 3: "seasonPack"}
	langIDs      = map[int]string{-2: "Original", -1: "Any", 1: "English", 2: "French", 3: "Spanish", 4: "German", 5: "Italian",
		6: "Danish", 7: "Dutch", 8: "Japanese", 10: "Chinese", 11: "Russian", 12: "Polish", 13: "Vietnamese", 14: "Swedish",
		15: "Norwegian", 16: "Finnish", 17: "Turkish", 18: "Portuguese", 20: "Greek", 21: "Korean", 22: "Hungarian", 23: "Hebrew",
		25: "Czech"}
	flagBits = []struct {
		bit  int
		name string
	}{{1, "freeleech"}, {2, "halfleech"}, {4, "doubleupload"}, {8, "golden"}, {16, "approved"}, {32, "internal"}, {64, "internal"}, {128, "scene"}, {256, "freeleech75"}, {512, "freeleech25"}}
)

func field(fs []arrField, name string) json.RawMessage {
	for _, f := range fs {
		if strings.EqualFold(f.Name, name) {
			return f.Value
		}
	}
	return nil
}

func asInt(v json.RawMessage) int {
	var n float64
	if json.Unmarshal(v, &n) == nil {
		return int(n)
	}
	return 0
}

func asString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return strings.Trim(string(v), `"`)
}

// FromArr converts an arr's profiles, custom formats and quality definitions.
// kind is "radarr" or "sonarr" (their source numbers differ).
func FromArr(kind string, profiles, formats, definitions []byte) (Imported, error) {
	var im Imported
	var aps []arrProfile
	if err := json.Unmarshal(profiles, &aps); err != nil {
		return im, fmt.Errorf("quality profiles: %w", err)
	}
	var afs []arrFormat
	if len(formats) > 0 {
		if err := json.Unmarshal(formats, &afs); err != nil {
			return im, fmt.Errorf("custom formats: %w", err)
		}
	}
	sources := radarrSource
	if kind == "sonarr" {
		sources = sonarrSource
	}
	for _, af := range afs {
		f := Format{Name: af.Name}
		for _, as := range af.Specs {
			sp := Spec{Name: as.Name, Negate: as.Negate, Required: as.Required}
			v := field(as.Fields, "value")
			switch as.Implementation {
			case "ReleaseTitleSpecification":
				sp.Kind, sp.Value = "title", asString(v)
			case "ReleaseGroupSpecification":
				sp.Kind, sp.Value = "group", asString(v)
			case "EditionSpecification":
				sp.Kind, sp.Value = "edition", asString(v)
			case "SourceSpecification":
				sp.Kind, sp.Value = "source", sources[asInt(v)]
			case "ResolutionSpecification":
				sp.Kind, sp.Value = "resolution", fmt.Sprint(asInt(v))
			case "QualityModifierSpecification":
				sp.Kind, sp.Value = "modifier", modifiers[asInt(v)]
			case "LanguageSpecification":
				sp.Kind, sp.Value = "language", langIDs[asInt(v)]
				if sp.Value == "" {
					sp.Value = fmt.Sprint("language ", asInt(v))
				}
				if b := field(as.Fields, "exceptLanguage"); string(b) == "true" {
					sp.Negate = !sp.Negate
				}
			case "SizeSpecification":
				sp.Kind = "size"
				sp.Min, sp.Max = int64(asInt(field(as.Fields, "min"))), int64(asInt(field(as.Fields, "max")))
			case "ReleaseTypeSpecification":
				sp.Kind, sp.Value = "releaseType", releaseTypes[asInt(v)]
			case "IndexerFlagSpecification":
				sp.Kind = "indexerFlag"
				n := asInt(v)
				for _, fb := range flagBits {
					if n&fb.bit != 0 {
						sp.Value = fb.name
						break
					}
				}
			case "YearSpecification":
				sp.Kind = "year"
				sp.Min, sp.Max = int64(asInt(field(as.Fields, "min"))), int64(asInt(field(as.Fields, "max")))
			default:
				sp.Kind, sp.Value = as.Implementation, asString(v)
				im.Skipped = append(im.Skipped, af.Name+": "+as.Name+" ("+strings.TrimSuffix(as.Implementation, "Specification")+")")
			}
			if (sp.Kind == "title" || sp.Kind == "group" || sp.Kind == "edition") && compile(sp.Value) == nil {
				im.Skipped = append(im.Skipped, af.Name+": "+as.Name+" (pattern does not compile)")
			}
			f.Specs = append(f.Specs, sp)
		}
		if len(f.Specs) > 0 {
			im.Formats = append(im.Formats, f)
		}
	}
	for _, ap := range aps {
		p := Profile{Name: ap.Name, UpgradesAllowed: ap.UpgradeAllowed, MinFormatScore: ap.MinFormatScore,
			CutoffFormatScore: ap.CutoffFormatScore, MinUpgradeScore: ap.MinUpgradeScore, Scores: map[string]int{}}
		for _, it := range ap.Items {
			var item Item
			if len(it.Items) > 0 {
				item = Item{Name: it.Name, Allowed: it.Allowed}
				for _, sub := range it.Items {
					item.Qualities = append(item.Qualities, parser.Canonical(sub.Quality.Name))
				}
				if ap.Cutoff == it.ID {
					p.Cutoff = item.Name
				}
			} else {
				n := parser.Canonical(it.Quality.Name)
				item = Item{Name: n, Qualities: []string{n}, Allowed: it.Allowed}
				if ap.Cutoff == it.Quality.ID {
					p.Cutoff = n
				}
			}
			p.Items = append(p.Items, item)
		}
		for _, fi := range ap.FormatItems {
			if fi.Score != 0 {
				p.Scores[fi.Name] = fi.Score
			}
		}
		if ap.Language != nil {
			p.Language = ap.Language.Name
		}
		im.Profiles = append(im.Profiles, p)
	}
	if len(definitions) > 0 {
		var defs []arrDefinition
		if err := json.Unmarshal(definitions, &defs); err != nil {
			return im, fmt.Errorf("quality definitions: %w", err)
		}
		im.Sizes = map[string]SizeLimit{}
		for _, d := range defs {
			l := SizeLimit{Min: d.MinSize}
			if d.MaxSize != nil {
				l.Max = *d.MaxSize
			}
			im.Sizes[parser.Canonical(d.Quality.Name)] = l
		}
	}
	return im, nil
}

// Save stores what was imported from a category's arr. A profile or format
// whose name another source already uses gets the category in its name; one
// imported before from the same category is replaced.
func (s *Store) Save(category string, im Imported) (profiles, formats int, err error) {
	rename := map[string]string{}
	existing, err := s.Formats()
	if err != nil {
		return 0, 0, err
	}
	for _, f := range im.Formats {
		f.Source = category
		name := f.Name
		for _, e := range existing {
			if strings.EqualFold(e.Name, f.Name) {
				if e.Source == category {
					f.ID = e.ID
				} else {
					f.Name = f.Name + " (" + category + ")"
				}
			}
		}
		if f.ID == 0 {
			for _, e := range existing {
				if strings.EqualFold(e.Name, f.Name) && e.Source == category {
					f.ID = e.ID
				}
			}
		}
		rename[name] = f.Name
		if _, err := s.save("custom_formats", f.ID, f.Name, f.Source, f); err != nil {
			return profiles, formats, fmt.Errorf("%s: %w", f.Name, err)
		}
		formats++
	}
	ps, err := s.Profiles()
	if err != nil {
		return 0, formats, err
	}
	for _, p := range im.Profiles {
		p.Source = category
		scores := map[string]int{}
		for n, v := range p.Scores {
			if r, ok := rename[n]; ok {
				n = r
			}
			scores[n] = v
		}
		p.Scores = scores
		if p.GuideScores != nil {
			gs := map[string]int{}
			for n, v := range p.GuideScores {
				if r, ok := rename[n]; ok {
					n = r
				}
				gs[n] = v
			}
			p.GuideScores = gs
		}
		for _, e := range ps {
			if strings.EqualFold(e.Name, p.Name) {
				if e.Source == category {
					p.ID = e.ID
				} else {
					p.Name = p.Name + " (" + category + ")"
				}
			}
		}
		if p.ID == 0 {
			for _, e := range ps {
				if strings.EqualFold(e.Name, p.Name) && e.Source == category {
					p.ID = e.ID
				}
			}
		}
		if _, err := s.SaveProfile(p); err != nil {
			return profiles, formats, fmt.Errorf("%s: %w", p.Name, err)
		}
		profiles++
	}
	if len(im.Sizes) > 0 {
		if err := s.SetSizes(im.Sizes); err != nil {
			return profiles, formats, err
		}
	}
	return profiles, formats, nil
}
