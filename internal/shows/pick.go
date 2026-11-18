package shows

import (
	"sort"
	"strings"
	"time"
)

// key groups releases of one episode: a release that says "S02E05" and one
// that says "- 05" in season 2's folder are the same only by season; an
// unmarked number and a season 1 number are the same episode.
type key struct{ season, episode int }

func keyOf(season int, marked bool, episode int) key {
	if marked && season > 1 {
		return key{season, episode}
	}
	return key{0, episode}
}

// groupRank is a group's place in the order; -1 when it is not in it. An empty
// order takes any group.
func groupRank(order []string, group string) int {
	if len(order) == 0 {
		return 0
	}
	for i, g := range order {
		if strings.EqualFold(strings.TrimSpace(g), strings.TrimSpace(group)) {
			return i
		}
	}
	return -1
}

func qualityOK(want, res string) bool {
	return want == "" || want == "any" || strings.EqualFold(want, res)
}

// Warning says why a release would not be grabbed by itself, or "" when it
// would be acceptable.
func Warning(st Settings, files []DiskFile, newest int, r Seen) string {
	switch {
	case r.Episode == 0:
		return "no episode number in the title"
	case groupRank(st.Groups, r.Group) < 0:
		if r.Group == "" {
			return "no group in the title"
		}
		return r.Group + " is not in the group order"
	case !qualityOK(st.Quality, r.Res):
		if r.Res == "" {
			return "no resolution in the title"
		}
		return r.Res + ", not " + st.Quality
	case Has(files, r.Season, r.Marked && r.Season > 1, r.Episode):
		return "this episode is already in the folder"
	case r.Marked && r.Season > 1 && r.Season < newest:
		return "an older season (gap filling handles those)"
	}
	return ""
}

// Pick decides which releases of a followed show to grab: for each episode the
// folder lacks and nothing was grabbed for yet, the best group in the order at
// the chosen quality. A later group is used only when the first choice skipped
// the episode: it released a later one, or has not released this one within
// the grace period after the later group did.
func Pick(st Settings, files []DiskFile, seen []Seen, now time.Time, grace time.Duration) []Seen {
	sum := Summarize(files)
	newest := sum.Season
	by := map[key][]Seen{}
	grabbed := map[key]bool{}
	latestBy := map[int]map[int]int{} // rank -> season key -> newest episode
	for _, r := range seen {
		if r.Episode == 0 {
			continue
		}
		k := keyOf(r.Season, r.Marked, r.Episode)
		if !r.Grabbed.IsZero() {
			grabbed[k] = true
		}
		by[k] = append(by[k], r)
		if rank := groupRank(st.Groups, r.Group); rank >= 0 {
			if latestBy[rank] == nil {
				latestBy[rank] = map[int]int{}
			}
			if r.Episode > latestBy[rank][k.season] {
				latestBy[rank][k.season] = r.Episode
			}
		}
	}
	var picks []Seen
	for k, list := range by {
		if grabbed[k] {
			continue
		}
		if k.season == 0 || k.season == newest {
			if k.episode <= st.FromEpisode {
				continue
			}
		} else if k.season < newest {
			continue
		}
		best, bestRank := Seen{}, -1
		for _, r := range list {
			if r.Failed || Warning(st, files, newest, r) != "" {
				continue
			}
			rank := groupRank(st.Groups, r.Group)
			if bestRank < 0 || rank < bestRank || (rank == bestRank && r.Seeders > best.Seeders) {
				best, bestRank = r, rank
			}
		}
		if bestRank < 0 {
			continue
		}
		if bestRank > 0 {
			skipped := false
			for rank := 0; rank < bestRank; rank++ {
				if latestBy[rank][k.season] > k.episode {
					skipped = true // a better group moved past this episode
				}
			}
			if !skipped && now.Sub(best.First) < grace {
				continue // give the better groups time to release it
			}
		}
		picks = append(picks, best)
	}
	sort.Slice(picks, func(i, j int) bool {
		if picks[i].Season != picks[j].Season {
			return picks[i].Season < picks[j].Season
		}
		return picks[i].Episode < picks[j].Episode
	})
	return picks
}

// GapKeys are the episodes missing between the ones in a folder.
func GapKeys(sum Summary) map[key]bool {
	out := map[key]bool{}
	for _, ss := range sum.Seasons {
		for _, g := range ss.Gaps {
			out[keyOf(ss.Season, ss.Season > 1, g)] = true
		}
	}
	return out
}

// PickGaps picks, for each missing episode, the best release there is: the
// first group of the order that has it, at the chosen quality. Old gaps do not
// wait for a better group. Episodes already in the folder, grabbed, or whose
// release failed are left alone.
func PickGaps(st Settings, files []DiskFile, seen []Seen, gaps map[key]bool) []Seen {
	grabbed := map[key]bool{}
	for _, r := range seen {
		if !r.Grabbed.IsZero() {
			grabbed[keyOf(r.Season, r.Marked, r.Episode)] = true
		}
	}
	best := map[key]Seen{}
	bestRank := map[key]int{}
	for _, r := range seen {
		k := keyOf(r.Season, r.Marked, r.Episode)
		if !gaps[k] || grabbed[k] || r.Failed || r.Episode == 0 {
			continue
		}
		rank := groupRank(st.Groups, r.Group)
		if rank < 0 || !qualityOK(st.Quality, r.Res) || Has(files, r.Season, r.Marked && r.Season > 1, r.Episode) {
			continue
		}
		if b, ok := bestRank[k]; !ok || rank < b || (rank == b && r.Seeders > best[k].Seeders) {
			best[k], bestRank[k] = r, rank
		}
	}
	var out []Seen
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Episode < out[j].Episode
	})
	return out
}
