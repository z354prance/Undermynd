// Command b4check runs the B4 metadata layer against the real TMDB and the real
// anime lists on the user's shows. Spike branch only; never merged.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/metadata"
)

func main() {
	dir, _ := os.MkdirTemp("", "b4")
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Migrate(h); err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	key := os.Getenv("TMDB_API_KEY")
	svc := &metadata.Service{
		TMDB:    metadata.NewTMDB(h, func() string { return key }),
		Lists:   metadata.NewLists(h, func() string { return metadata.DefaultAnimeTitlesURL }, func() string { return metadata.DefaultAnimeIDsURL }),
		Catalog: catalog.New(h),
		Log:     log.New(os.Stdout, "", 0),
		Now:     time.Now,
	}
	fmt.Println("# B4 check against real sources")
	if err := svc.TMDB.Check(ctx); err != nil {
		log.Fatal("key: ", err)
	}
	start := time.Now()
	st := svc.Lists.Refresh(ctx)
	fmt.Printf("- lists: %d anime with titles (dated %s), %d ID mappings, took %s; errors: %q %q\n", st.Titles, st.TitlesDate, st.IDs, time.Since(start).Round(time.Second), st.TitlesError, st.IDsError)
	for _, q := range []struct{ title, kind string }{
		{"Blue Box", "anime"}, {"Even the Student Council Has Its Holes", "anime"}, {"One Piece", "anime"},
		{"Boku no Hero Academia", "anime"}, {"Kashita Maryoku wa Revo Barai de Kyousei Choushuu", "anime"},
	} {
		fmt.Printf("\n## %s\n", q.title)
		cands, err := svc.Identify(ctx, q.title)
		if err != nil {
			fmt.Println("- identify:", err)
			continue
		}
		for i, c := range cands {
			if i < 3 {
				fmt.Printf("- candidate %d: TMDB %d %q (%s) via %s, animation %v\n", i+1, c.TMDBID, c.Name, c.Year, c.Via, c.Animation)
			}
		}
		if len(cands) == 0 {
			continue
		}
		s, err := svc.AddSeries(ctx, cands[0].TMDBID, q.kind, "", "")
		if err != nil {
			fmt.Println("- add:", err)
			continue
		}
		eps, _ := svc.Catalog.Episodes(s.ID)
		al, _ := svc.Catalog.SeriesAliases(s.ID)
		seasons, _ := svc.Catalog.Seasons(s.ID)
		fmt.Printf("- added %q (%d): %d episodes, %d aliases, seasons %v\n", s.Title, s.Year, len(eps), len(al), seasons)
		if f, _, err := svc.Catalog.FindSeries(q.title); err != nil || f.ID != s.ID {
			fmt.Printf("- WARNING: the feed title does not find the series (%v)\n", err)
		} else {
			fmt.Println("- feed title finds the series: yes")
		}
		for _, abs := range []int{90, 1158} {
			if e, err := svc.Catalog.EpisodeByAbsolute(s.ID, abs); err == nil {
				fmt.Printf("- absolute %d -> S%dE%d (%s)\n", abs, e.Season, e.Number, e.AirDate)
			}
		}
		orders, _ := svc.EpisodeOrders(ctx, s.ID)
		for _, o := range orders {
			if o.Type == 6 || o.Type == 2 {
				if err := svc.SetEpisodeOrder(ctx, s.ID, o.ID); err != nil {
					fmt.Printf("- order %q: %v\n", o.Name, err)
					continue
				}
				sc, _ := svc.Catalog.Seasons(s.ID)
				fmt.Printf("- with episode group %q (type %d): seasons %v\n", o.Name, o.Type, sc)
				_ = svc.SetEpisodeOrder(ctx, s.ID, "")
			}
		}
		if err := svc.Refresh(ctx, s.ID); err != nil {
			fmt.Println("- refresh:", err)
		} else {
			fmt.Println("- refresh: ok")
		}
	}
}
