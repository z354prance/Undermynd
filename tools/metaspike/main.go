// Command metaspike is the roadmap B3 spike: it checks, on the user's shows,
// whether TMDB plus the anime-offline-database and an ID mapping list give
// Undermynd what it needs. It prints a report and never prints the key.
// It lives on a spike branch only and is not part of the app.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

var key = strings.TrimSpace(os.Getenv("TMDB_API_KEY"))
var client = &http.Client{Timeout: 60 * time.Second}

type show struct {
	label   string
	queries []string // TMDB search terms, first that finds an animation result wins
	feed    []string // titles seen in the user's release names
}

var shows = []show{
	{"Blue Box", []string{"Blue Box", "Ao no Hako"}, []string{"Blue Box", "Ao no Hako"}},
	{"Seitokai ni mo Ana wa Aru", []string{"Seitokai ni mo Ana wa Aru", "Even the Student Council Has Its Holes"}, []string{"Seitokai ni mo Ana wa Aru!", "Even the Student Council Has Its Holes"}},
	{"One Piece", []string{"One Piece"}, []string{"One Piece"}},
	{"Boku no Hero Academia", []string{"My Hero Academia", "Boku no Hero Academia"}, []string{"Boku no Hero Academia"}},
	{"Kashita Maryoku (current SubsPlease show)", []string{"Kashita Maryoku wa Revo Barai de Kyousei Choushuu"}, []string{"Kashita Maryoku wa Revo Barai de Kyousei Choushuu"}},
}

func get(u string, out any) error {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	return do(req, out)
}

func do(req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.ReplaceAll(string(b), key, "***"))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// tmdb accepts either kind of TMDB credential: the short API key (v3) as a
// query parameter, or the long read access token (starts "eyJ") as a bearer header.
func tmdb(path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	var bearer string
	if strings.HasPrefix(key, "eyJ") {
		bearer = key
	} else {
		q.Set("api_key", key)
	}
	req, _ := http.NewRequest("GET", "https://api.themoviedb.org/3"+path+"?"+q.Encode(), nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	err := do(req, out)
	if err != nil {
		return fmt.Errorf("tmdb %s: %s", path, strings.ReplaceAll(err.Error(), key, "***"))
	}
	return nil
}

func norm(s string) string {
	var b strings.Builder
	space := true
	for _, c := range strings.ToLower(strings.NewReplacer("&", " and ", "'", "", "’", "").Replace(s)) {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			b.WriteRune(c)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

type tvResult struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	OriginalName string   `json:"original_name"`
	FirstAir     string   `json:"first_air_date"`
	Genres       []int    `json:"genre_ids"`
	Origin       []string `json:"origin_country"`
}

func main() {
	if key == "" {
		fmt.Println("TMDB_API_KEY is not set (add it as a repository secret). Stopping.")
		os.Exit(1)
	}
	fmt.Println("# B3 metadata spike report")
	kind := "API key (v3)"
	if strings.HasPrefix(key, "eyJ") {
		kind = "read access token (v4)"
	}
	fmt.Printf("credential: %s, %d characters\n", kind, len(key))
	tmdbIDs := map[string]int64{}
	for _, s := range shows {
		fmt.Printf("\n## %s\n", s.label)
		var pick tvResult
		for _, q := range s.queries {
			var r struct {
				Results []tvResult `json:"results"`
			}
			if err := tmdb("/search/tv", url.Values{"query": {q}}, &r); err != nil {
				fmt.Println("- search error:", err)
				continue
			}
			for _, x := range r.Results {
				anim := false
				for _, g := range x.Genres {
					if g == 16 {
						anim = true
					}
				}
				if anim {
					pick = x
					break
				}
			}
			if pick.ID != 0 {
				fmt.Printf("- TMDB search %q found: id %d, %q (original %q), first aired %s\n", q, pick.ID, pick.Name, pick.OriginalName, pick.FirstAir)
				break
			}
			fmt.Printf("- TMDB search %q: no animation result among %d results\n", q, len(r.Results))
		}
		if pick.ID == 0 {
			continue
		}
		tmdbIDs[s.label] = pick.ID
		var d struct {
			Seasons []struct {
				Number int    `json:"season_number"`
				Count  int    `json:"episode_count"`
				Name   string `json:"name"`
			} `json:"seasons"`
			ExternalIDs struct {
				TVDB int64  `json:"tvdb_id"`
				IMDB string `json:"imdb_id"`
			} `json:"external_ids"`
			Alt struct {
				Results []struct {
					Title string `json:"title"`
					Iso   string `json:"iso_3166_1"`
				} `json:"results"`
			} `json:"alternative_titles"`
			Groups struct {
				Results []struct {
					Name  string `json:"name"`
					Type  int    `json:"type"`
					Count int    `json:"episode_count"`
				} `json:"results"`
			} `json:"episode_groups"`
		}
		if err := tmdb(fmt.Sprintf("/tv/%d", pick.ID), url.Values{"append_to_response": {"external_ids,alternative_titles,episode_groups"}}, &d); err != nil {
			fmt.Println("- details error:", err)
			continue
		}
		fmt.Printf("- external ids: TVDB %d, IMDb %s\n", d.ExternalIDs.TVDB, d.ExternalIDs.IMDB)
		var alts []string
		for _, a := range d.Alt.Results {
			alts = append(alts, a.Title)
		}
		fmt.Printf("- TMDB alternative titles (%d): %s\n", len(alts), strings.Join(first(alts, 12), " | "))
		have := map[string]bool{norm(pick.Name): true, norm(pick.OriginalName): true}
		for _, a := range alts {
			have[norm(a)] = true
		}
		for _, f := range s.feed {
			fmt.Printf("- feed title %q in TMDB titles: %v\n", f, have[norm(f)])
		}
		fmt.Printf("- seasons: ")
		var parts []string
		for _, se := range d.Seasons {
			parts = append(parts, fmt.Sprintf("S%d=%d", se.Number, se.Count))
		}
		fmt.Println(strings.Join(parts, " "))
		for _, g := range d.Groups.Results {
			fmt.Printf("- episode group: %q type %d, %d episodes\n", g.Name, g.Type, g.Count)
		}
		// How episodes are numbered inside each season: restart at 1, or continue (absolute)?
		abs := 0
		continuous := true
		var lastSeason, lastFirst, lastLast int
		for _, se := range d.Seasons {
			if se.Number == 0 {
				continue
			}
			var sd struct {
				Episodes []struct {
					Number  int    `json:"episode_number"`
					AirDate string `json:"air_date"`
				} `json:"episodes"`
			}
			if err := tmdb(fmt.Sprintf("/tv/%d/season/%d", pick.ID, se.Number), nil, &sd); err != nil {
				fmt.Println("- season error:", err)
				continue
			}
			if len(sd.Episodes) == 0 {
				continue
			}
			f, l := sd.Episodes[0].Number, sd.Episodes[len(sd.Episodes)-1].Number
			if f != 1 {
				continuous = false
			}
			lastSeason, lastFirst, lastLast = se.Number, f, l
			abs += len(sd.Episodes)
		}
		mode := "each season starts at episode 1"
		if !continuous {
			mode = "episode numbers do NOT restart each season (TMDB already numbers some seasons absolutely)"
		}
		fmt.Printf("- numbering: %s; last season S%d runs E%d-E%d; running count of regular episodes: %d\n", mode, lastSeason, lastFirst, lastLast, abs)
	}

	fmt.Println("\n## anime-offline-database")
	var aod struct {
		LastUpdate string `json:"lastUpdate"`
		License    any    `json:"license"`
		Data       []struct {
			Sources  []string `json:"sources"`
			Title    string   `json:"title"`
			Type     string   `json:"type"`
			Episodes int      `json:"episodes"`
			Synonyms []string `json:"synonyms"`
		} `json:"data"`
	}
	var aodErr error
	for _, u := range []string{
		"https://github.com/manami-project/anime-offline-database/releases/latest/download/anime-offline-database-minified.json",
		"https://raw.githubusercontent.com/manami-project/anime-offline-database/master/anime-offline-database-minified.json",
	} {
		if aodErr = get(u, &aod); aodErr == nil {
			fmt.Println("- downloaded from", u)
			break
		}
		fmt.Println("- could not download", u, ":", aodErr)
	}
	anidb := map[string][]int64{}
	if aodErr == nil {
		lic, _ := json.Marshal(aod.License)
		fmt.Printf("- %d entries, last update %s, licence %s\n", len(aod.Data), aod.LastUpdate, lic)
		for _, s := range shows {
			want := map[string]bool{}
			for _, f := range append(append([]string{}, s.feed...), s.queries...) {
				want[norm(f)] = true
			}
			var hits []string
			for _, e := range aod.Data {
				names := append([]string{e.Title}, e.Synonyms...)
				match := false
				for _, n := range names {
					if want[norm(n)] {
						match = true
						break
					}
				}
				if !match {
					continue
				}
				var ids []string
				for _, src := range e.Sources {
					if strings.Contains(src, "anidb.net/anime/") {
						var id int64
						fmt.Sscanf(src[strings.LastIndex(src, "/")+1:], "%d", &id)
						anidb[s.label] = append(anidb[s.label], id)
						ids = append(ids, fmt.Sprintf("anidb %d", id))
					}
					if strings.Contains(src, "anilist.co/anime/") {
						ids = append(ids, "anilist "+src[strings.LastIndex(src, "/")+1:])
					}
				}
				feedHit := []string{}
				for _, f := range s.feed {
					for _, n := range names {
						if norm(n) == norm(f) {
							feedHit = append(feedHit, f)
							break
						}
					}
				}
				hits = append(hits, fmt.Sprintf("%q (%s, %d eps, %d synonyms; %s; matches feed titles %q)", e.Title, e.Type, e.Episodes, len(e.Synonyms), strings.Join(ids, ", "), feedHit))
			}
			sort.Strings(hits)
			fmt.Printf("- %s: %d entries\n", s.label, len(hits))
			for _, h := range first(hits, 8) {
				fmt.Println("  -", h)
			}
		}
	}

	fmt.Println("\n## ID mapping (anime-offline-database to TMDB)")
	var mapping map[string]map[string]any
	var mapURL string
	for _, u := range []string{
		"https://raw.githubusercontent.com/Kometa-Team/Anime-IDs/master/anime_ids.json",
		"https://raw.githubusercontent.com/Kometa-Team/Anime-IDs/main/anime_ids.json",
	} {
		if err := get(u, &mapping); err == nil {
			mapURL = u
			break
		} else {
			fmt.Println("- could not download", u, ":", err)
		}
	}
	if mapURL != "" {
		fmt.Printf("- %s: %d entries keyed by AniDB id\n", mapURL, len(mapping))
		for _, v := range mapping {
			var ks []string
			for k := range v {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			fmt.Println("- fields:", strings.Join(ks, ", "))
			break
		}
		for _, s := range shows {
			ids := anidb[s.label]
			var found []string
			hit := false
			for _, id := range ids {
				e, ok := mapping[fmt.Sprint(id)]
				if !ok {
					found = append(found, fmt.Sprintf("anidb %d: not in list", id))
					continue
				}
				var tm []string
				for k, v := range e {
					if strings.Contains(k, "tmdb") || strings.Contains(k, "tvdb") {
						tm = append(tm, fmt.Sprintf("%s=%v", k, v))
						if strings.Contains(k, "tmdb") && fmt.Sprint(v) == fmt.Sprint(tmdbIDs[s.label]) {
							hit = true
						}
					}
				}
				sort.Strings(tm)
				found = append(found, fmt.Sprintf("anidb %d: %s", id, strings.Join(tm, " ")))
			}
			for _, f := range found {
				if i := strings.Index(f, "tvdb_id="); i >= 0 && !hit && tmdbIDs[s.label] != 0 {
					var tv int64
					fmt.Sscanf(f[i+len("tvdb_id="):], "%d", &tv)
					var r struct {
						TV []struct {
							ID int64 `json:"id"`
						} `json:"tv_results"`
					}
					if tv != 0 && tmdb(fmt.Sprintf("/find/%d", tv), url.Values{"external_source": {"tvdb_id"}}, &r) == nil && len(r.TV) > 0 && r.TV[0].ID == tmdbIDs[s.label] {
						hit = true
					}
				}
			}
			fmt.Printf("- %s (TMDB %d): mapping reaches the TMDB id (directly or through TVDB): %v\n", s.label, tmdbIDs[s.label], hit)
			for _, f := range first(found, 8) {
				fmt.Println("  -", f)
			}
		}
	}
}

func first(s []string, n int) []string {
	if len(s) > n {
		return append(s[:n:n], fmt.Sprintf("... and %d more", len(s)-n))
	}
	return s
}
