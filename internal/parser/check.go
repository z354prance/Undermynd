package parser

import (
	"sort"
	"strconv"
	"strings"
)

// Sample is one release an arr grabbed, with what the arr decided about it.
type Sample struct {
	Title    string
	Quality  string // the arr's quality name
	Season   int    // -1 when the arr gave none (movies)
	Episodes []int  // episode numbers in the season, from the arr's history records
	Absolute []int  // the arr's absolute numbers for the same episodes
	Year     int    // the movie's year (movies)
	Series   string // the arr's series or movie title, for display
}

// Diff is one release where HyveMynd read something the arr did not.
type Diff struct {
	Title  string `json:"title"`
	Field  string `json:"field"`
	Arr    string `json:"arr"`
	Ours   string `json:"ours"`
	Series string `json:"series,omitempty"`
}

// Report sums up a comparison.
type Report struct {
	Total    int            `json:"total"`
	Agree    int            `json:"agree"`
	Rate     float64        `json:"rate"`
	ByField  map[string]int `json:"byField"`
	Diffs    []Diff         `json:"diffs"`
	Coverage map[string]int `json:"qualities"` // how many samples per arr quality
}

// Compare parses each sample's title and counts where HyveMynd and the arr
// agree on quality and numbering (or year, for movies).
func Compare(samples []Sample) Report {
	r := Report{ByField: map[string]int{}, Coverage: map[string]int{}}
	for _, s := range samples {
		r.Total++
		r.Coverage[Canonical(s.Quality)]++
		in := Parse(s.Title)
		var diffs []Diff
		if q := in.Quality.Name(); Canonical(s.Quality) != q && !(s.Quality == "Unknown" && q == "Unknown") {
			diffs = append(diffs, Diff{Field: "quality", Arr: Canonical(s.Quality), Ours: q})
		}
		if s.Season >= 0 && len(s.Episodes) > 0 {
			if d := numbering(s, in); d != "" {
				diffs = append(diffs, Diff{Field: "episodes", Arr: arrEps(s), Ours: d})
			}
		}
		if s.Season < 0 && s.Year > 0 && in.Year != 0 && in.Year != s.Year && in.Year != s.Year-1 && in.Year != s.Year+1 {
			diffs = append(diffs, Diff{Field: "year", Arr: itoa(s.Year), Ours: itoa(in.Year)})
		}
		if len(diffs) == 0 {
			r.Agree++
			continue
		}
		for _, d := range diffs {
			d.Title, d.Series = s.Title, s.Series
			r.ByField[d.Field]++
			r.Diffs = append(r.Diffs, d)
		}
	}
	if r.Total > 0 {
		r.Rate = float64(r.Agree) / float64(r.Total)
	}
	sort.SliceStable(r.Diffs, func(i, j int) bool { return r.Diffs[i].Field < r.Diffs[j].Field })
	return r
}

// numbering returns what HyveMynd read when it does not match the arr, or "".
func numbering(s Sample, in Info) string {
	if in.FullSeason {
		for _, n := range in.Seasons {
			if n == s.Season {
				return ""
			}
		}
		if len(in.Seasons) == 0 {
			return "" // "Complete Series": every season
		}
		return "season pack " + join(in.Seasons)
	}
	if len(in.Absolute) > 0 {
		if sameSet(in.Absolute, s.Absolute) || sameSet(in.Absolute, s.Episodes) {
			return "" // anime read by absolute number; the arr mapped it
		}
		return "absolute " + join(in.Absolute)
	}
	if in.Daily {
		return "" // daily shows: the arr maps the date; nothing to compare here
	}
	got := "S" + join(in.Seasons) + "E" + join(in.Episodes)
	if len(in.Seasons) == 1 && in.Seasons[0] == s.Season && sameSet(in.Episodes, s.Episodes) {
		return ""
	}
	// Scene numbering differs from TVDB's for some shows; the arr maps it.
	// Count it as agreement only when the episode count matches.
	if len(in.Episodes) == len(s.Episodes) && len(in.Seasons) == 1 && len(s.Absolute) > 0 && sameSet(in.Episodes, s.Absolute) {
		return ""
	}
	if len(in.Episodes) == 0 {
		return "nothing"
	}
	return got
}

func arrEps(s Sample) string { return "S" + itoa(s.Season) + "E" + join(s.Episodes) }

func sameSet(a, b []int) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	x := append([]int(nil), a...)
	y := append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func join(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = itoa(n)
	}
	return strings.Join(s, ",")
}

func itoa(n int) string { return strconv.Itoa(n) }
