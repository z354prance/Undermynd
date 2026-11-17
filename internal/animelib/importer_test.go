package animelib_test

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/animelib"
	"github.com/z354prance/undermynd/internal/catalog"
	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/metadata"
	"github.com/z354prance/undermynd/internal/state"
)

func e(season, number int) map[string]any {
	return map[string]any{"season_number": season, "episode_number": number, "name": "", "air_date": "2020-01-01"}
}

func show(id int, name, orig string, tvdb int, seasons []int, groups []map[string]any) map[string]any {
	var ss []map[string]any
	for _, n := range seasons {
		ss = append(ss, map[string]any{"season_number": n})
	}
	return map[string]any{"id": id, "name": name, "original_name": orig, "first_air_date": "2020-01-01", "seasons": ss,
		"external_ids": map[string]any{"tvdb_id": tvdb}, "alternative_titles": map[string]any{"results": []any{}},
		"episode_groups": map[string]any{"results": groups}}
}

// fakeTMDB serves four shows shaped like the user's library.
func fakeTMDB(t *testing.T) *httptest.Server {
	shows := map[string]map[string]any{
		"1": show(1, "One Piece", "ワンピース", 81797, []int{1, 2}, []map[string]any{}),
		"2": show(2, "Blue Box", "アオのハコ", 429934, []int{1}, []map[string]any{{"id": "g-seasons", "name": "Seasons", "type": 6, "episode_count": 4}}),
		"3": show(3, "My Hero Academia", "僕のヒーローアカデミア", 305074, []int{1, 2}, []map[string]any{}),
		"5": show(5, "Magic Repo Man", "Kashita Maryoku wa Ribo Barai", 0, []int{1}, []map[string]any{}),
		"6": show(6, "Backflip!!", "バクテン!!", 999, []int{1}, []map[string]any{}),
		"7": show(7, "Burst Angel", "爆裂天使", 888, []int{1}, []map[string]any{}),
		"8": show(8, "Bleach", "BLEACH", 74796, []int{1, 2}, []map[string]any{}),
	}
	shows["6"]["alternative_titles"] = map[string]any{"results": []map[string]any{{"title": "Bakuten!!"}}}
	seasons := map[string][]map[string]any{
		"1/1": {e(1, 1), e(1, 2)}, "1/2": {e(2, 3), e(2, 4)},
		"2/1": {e(1, 1), e(1, 2), e(1, 3), e(1, 4)},
		"3/1": {e(1, 1), e(1, 2)}, "3/2": {e(2, 1), e(2, 2)},
		"5/1": {e(1, 1), e(1, 2), e(1, 3)},
		"6/1": {e(1, 1), e(1, 2)}, "7/1": {e(1, 1), e(1, 2), e(1, 3)},
		"8/1": {e(1, 1), e(1, 2)}, "8/2": {e(2, 1), e(2, 2), e(2, 3)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Trim(r.URL.Path, "/")
		parts := strings.Split(p, "/")
		var out any
		switch {
		case p == "search/tv":
			q := strings.ToLower(r.URL.Query().Get("query"))
			res := []map[string]any{}
			for _, s := range shows {
				if strings.Contains(strings.ToLower(s["name"].(string)), q) {
					res = append(res, map[string]any{"id": s["id"], "name": s["name"], "original_name": s["original_name"], "first_air_date": "2020-01-01", "genre_ids": []int{16}})
				}
			}
			out = map[string]any{"results": res}
		case len(parts) == 2 && parts[0] == "find":
			res := []map[string]any{}
			for _, s := range shows {
				b, _ := json.Marshal(s["external_ids"].(map[string]any)["tvdb_id"])
				if string(b) == parts[1] {
					res = append(res, map[string]any{"id": s["id"]})
				}
			}
			out = map[string]any{"tv_results": res}
		case len(parts) == 3 && parts[1] == "episode_group":
			out = map[string]any{"id": "g-seasons", "name": "Seasons", "type": 6, "groups": []map[string]any{
				{"name": "Season 1", "order": 1, "episodes": []map[string]any{
					{"season_number": 1, "episode_number": 1, "order": 0}, {"season_number": 1, "episode_number": 2, "order": 1}}},
				{"name": "Season 2", "order": 2, "episodes": []map[string]any{
					{"season_number": 1, "episode_number": 3, "order": 0}, {"season_number": 1, "episode_number": 4, "order": 1}}},
			}}
		case len(parts) == 2 && parts[0] == "tv":
			s, ok := shows[parts[1]]
			if !ok {
				w.WriteHeader(404)
				return
			}
			out = s
		case len(parts) == 4 && parts[2] == "season":
			out = map[string]any{"episodes": seasons[parts[1]+"/"+parts[3]]}
		default:
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const titlesJSON = `{"lastUpdate":"2026-07-04","data":[
 {"sources":["https://anidb.net/anime/69"],"title":"One Piece","synonyms":[]},
 {"sources":["https://anidb.net/anime/100"],"title":"Ao no Hako","synonyms":["Blue Box"]},
 {"sources":["https://anidb.net/anime/200"],"title":"Boku no Hero Academia","synonyms":["My Hero Academia"]},
 {"sources":["https://anidb.net/anime/400"],"title":"Bakuten!!","synonyms":[]},
 {"sources":["https://anidb.net/anime/401"],"title":"Bakuretsu Tenshi","synonyms":["Bakuten!!"]},
 {"sources":["https://anidb.net/anime/500"],"title":"Bleach","synonyms":[]},
 {"sources":["https://anidb.net/anime/501"],"title":"Bleach: Sennen Kessen-hen","synonyms":["Bleach Thousand-Year Blood War"]}
]}`

const idsJSON = `{"69":{"tvdb_id":81797,"tvdb_season":-1},"100":{"tvdb_id":429934,"tvdb_season":1},"200":{"tvdb_id":305074,"tvdb_season":1},"400":{"tvdb_id":999,"tvdb_season":1},"401":{"tvdb_id":888,"tvdb_season":1},"500":{"tvdb_id":74796,"tvdb_season":1},"501":{"tvdb_id":74796,"tvdb_season":2}}`

func touch(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		p := filepath.Join(root, r)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func newImporter(t *testing.T) (*animelib.Importer, string) {
	t.Helper()
	dir := t.TempDir()
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(h)
	tm := metadata.NewTMDB(h, func() string { return "key" })
	tm.BaseURL, tm.Gap = fakeTMDB(t).URL, 0
	lists := metadata.NewLists(h, func() string { return "" }, func() string { return "" })
	if _, _, err := lists.ImportTitles(strings.NewReader(titlesJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := lists.ImportIDs(strings.NewReader(idsJSON)); err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	svc := &metadata.Service{TMDB: tm, Lists: lists, Catalog: cat, Log: logger, Now: time.Now}
	lib := filepath.Join(dir, "Anime")
	touch(t, lib,
		"One Piece/Season 1/One Piece - 1 (1080p).mkv", "One Piece/Season 1/One Piece - 2 (1080p).mkv",
		"One Piece/Season 2/One Piece - 3 (1080p).mkv", "One Piece/Season 2/One Piece - 4 (1080p).mkv", "One Piece/NCOP.mkv",
		"Blue Box/Season 1/Blue Box - 1 (1080p).mkv", "Blue Box/Season 1/Blue Box - 2 (1080p).mkv",
		"Blue Box/Season 2/Blue Box - 1 (1080p).mkv",
		"Boku no Hero Academia/Season 1/Boku no Hero Academia - 1 (720p).mkv", "Boku no Hero Academia/Season 1/Boku no Hero Academia - 2 (720p).mkv",
		"Boku no Hero Academia/Season 2/Boku no Hero Academia - 3 (1080p).mkv", "Boku no Hero Academia/Season 2/Boku no Hero Academia - 4 (1080p).mkv",
		"Kashita Maryoku wa Revo Barai/Season 1/Kashita Maryoku wa Revo Barai - 1 (1080p).mkv",
		"Bakuten!!/Season 1/Bakuten!! - 1 (1080p).mkv", "Bakuten!!/Season 1/Bakuten!! - 2 (1080p).mkv",
		"Bleach/Season 1/Bleach - 1 (1080p).mkv", "Bleach/Season 1/Bleach - 2 (1080p).mkv",
		"Bleach Thousand-Year Blood War/Season 1/Bleach Thousand-Year Blood War - 1 (1080p).mkv",
		"Bleach Thousand-Year Blood War/Season 1/Bleach Thousand-Year Blood War - 2 (1080p).mkv",
	)
	im := &animelib.Importer{Meta: svc, Cat: cat, Roots: func() []string { return []string{lib} }, Blobs: state.Files{Dir: dir}, Log: logger, Now: time.Now}
	return im, lib
}

func scan(t *testing.T, im *animelib.Importer) map[string]*animelib.Folder {
	t.Helper()
	if !im.Scan() {
		t.Fatal("scan did not start")
	}
	deadline := time.Now().Add(20 * time.Second)
	for im.Status().Running {
		if time.Now().After(deadline) {
			t.Fatal("scan did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := im.Status()
	if st.Error != "" {
		t.Fatal(st.Error)
	}
	out := map[string]*animelib.Folder{}
	for _, f := range st.Folders {
		out[f.Name] = f
	}
	return out
}

func TestImportLibrary(t *testing.T) {
	im, lib := newImporter(t)
	ctx := context.Background()
	got := scan(t, im)
	if f := got["One Piece"]; f.State != animelib.Ready || f.Match.TMDBID != 1 || f.Options[0].Numbering != catalog.ByAbsolute || f.Files != 4 || len(f.Unparsed) != 1 {
		t.Fatalf("one piece: %+v", f)
	}
	if f := got["Boku no Hero Academia"]; f.State != animelib.Ready || f.Match.TMDBID != 3 || f.Options[0].Numbering != catalog.ByAbsolute {
		t.Fatalf("hero academia: %+v %+v", f, f.Options)
	}
	bb := got["Blue Box"]
	if bb.State != animelib.Needs || bb.Options[0].Order != "g-seasons" || bb.Options[0].Mapped != 3 || bb.Options[0].Distinct != 3 {
		t.Fatalf("blue box: %+v %+v", bb, bb.Options)
	}
	if f := got["Bakuten!!"]; f.State != animelib.Ready || f.Match.TMDBID != 6 {
		t.Fatalf("bakuten: %+v %+v", f, f.Match)
	}
	if f := got["Kashita Maryoku wa Revo Barai"]; f.State != animelib.Needs || f.Reason == "" {
		t.Fatalf("new show: %+v", f)
	}

	if n, errs := im.ConfirmReady(ctx); n != 5 || len(errs) != 0 {
		t.Fatalf("confirm ready: %d %v", n, errs)
	}
	// A sequel in its own folder that TMDB counts as season 2 is kept with the show.
	bleach, err := im.Cat.SeriesByFolder(filepath.Join(lib, "Bleach"))
	if err != nil {
		t.Fatal(err)
	}
	places, _ := im.Cat.Places(bleach)
	if len(places) != 2 || places[1].FirstSeason != 2 || filepath.Base(places[1].Folder) != "Bleach Thousand-Year Blood War" {
		t.Fatalf("bleach places: %+v", places)
	}
	if sc, _ := im.Cat.Seasons(bleach.ID); len(sc) != 2 || sc[0].Have != 2 || sc[1].Have != 2 || sc[1].Total != 3 {
		t.Fatalf("bleach seasons: %+v", sc)
	}
	if _, err := im.Confirm(ctx, bb.Path, 0); err != nil {
		t.Fatal(err)
	}
	ser, err := im.Cat.SeriesByFolder(bb.Path)
	if err != nil || ser.EpisodeOrder != "g-seasons" || ser.Numbering != catalog.BySeason || !ser.Monitored {
		t.Fatalf("blue box series: %+v %v", ser, err)
	}
	seasons, _ := im.Cat.Seasons(ser.ID)
	if len(seasons) != 2 || seasons[0].Have != 2 || seasons[1].Have != 1 || seasons[1].Total != 2 {
		t.Fatalf("blue box seasons: %+v", seasons)
	}
	mha, _ := im.Cat.SeriesByFolder(filepath.Join(lib, "Boku no Hero Academia"))
	if seasons, _ := im.Cat.Seasons(mha.ID); len(seasons) != 2 || seasons[1].Have != 2 {
		t.Fatalf("hero academia seasons: %+v", seasons)
	}
	if s, _, err := im.Cat.FindSeries("Boku no Hero Academia"); err != nil || s.ID != mha.ID {
		t.Fatalf("folder name should be an alias: %+v %v", s, err)
	}

	// The new show: pick it by hand, then confirm.
	kashita := got["Kashita Maryoku wa Revo Barai"]
	cands, err := im.Meta.Search(ctx, "5")
	if err != nil || len(cands) != 1 {
		t.Fatalf("search by id: %+v %v", cands, err)
	}
	f, err := im.Choose(ctx, kashita.Path, cands[0])
	if err != nil || f.State != animelib.Ready {
		t.Fatalf("choose: %+v %v", f, err)
	}
	if _, err := im.Confirm(ctx, kashita.Path, 0); err != nil {
		t.Fatal(err)
	}

	// A file that goes away is forgotten; a new one is recorded.
	op, _ := im.Cat.SeriesByFolder(filepath.Join(lib, "One Piece"))
	os.Remove(filepath.Join(lib, "One Piece/Season 2/One Piece - 4 (1080p).mkv"))
	touch(t, lib, "Blue Box/Season 2/Blue Box - 2 (1080p).mkv")
	im.SyncAll()
	if seasons, _ := im.Cat.Seasons(op.ID); seasons[1].Have != 1 {
		t.Fatalf("one piece after delete: %+v", seasons)
	}
	if seasons, _ := im.Cat.Seasons(ser.ID); seasons[1].Have != 2 {
		t.Fatalf("blue box after new file: %+v", seasons)
	}

	// A folder that reads empty (an unmounted share) changes nothing.
	os.RemoveAll(filepath.Join(lib, "One Piece"))
	os.MkdirAll(filepath.Join(lib, "One Piece"), 0o755)
	if res, err := im.Sync(op.ID); err != nil || res.Removed != 0 {
		t.Fatalf("empty folder: %+v %v", res, err)
	}
	if seasons, _ := im.Cat.Seasons(op.ID); seasons[0].Have != 2 {
		t.Fatalf("records kept: %+v", seasons)
	}

	// Undo an import: the series goes, the folder is back in the list.
	if err := im.Remove(ser.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := im.Cat.Series(ser.ID); err == nil {
		t.Fatal("series still there")
	}
	for _, f := range im.Status().Folders {
		if f.Path == bb.Path && f.State == animelib.Imported {
			t.Fatal("folder still imported")
		}
	}
	// A rescan sees imported folders as imported without looking them up again.
	got = scan(t, im)
	if got["Boku no Hero Academia"].State != animelib.Imported || got["Blue Box"].State != animelib.Needs {
		t.Fatalf("rescan: %+v %+v", got["Boku no Hero Academia"], got["Blue Box"])
	}
}
