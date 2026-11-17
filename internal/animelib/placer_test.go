package animelib_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/z354prance/undermynd/internal/animelib"
	"github.com/z354prance/undermynd/internal/state"
)

func TestCatalogPlacement(t *testing.T) {
	im, lib := newImporter(t)
	ctx := context.Background()
	got := scan(t, im)
	if n, errs := im.ConfirmReady(ctx); n != 5 || len(errs) != 0 {
		t.Fatalf("confirm ready: %d %v", n, errs)
	}
	if _, err := im.Confirm(ctx, got["Blue Box"].Path, 0); err != nil {
		t.Fatal(err)
	}
	blobs := state.Files{Dir: t.TempDir()}
	p := &animelib.Placer{Cat: im.Cat, Meta: im.Meta, Blobs: blobs, Log: im.Log, Now: im.Now}
	if p.Mode() != animelib.ModeDryRun {
		t.Fatalf("a new install starts in the dry run, got %s", p.Mode())
	}
	dl := t.TempDir()
	put := func(name string) string {
		path := filepath.Join(dl, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// A season show, under another title it is known by, placed in its group order.
	src := put("[Trix] Ao no Hako S02E02 [1080p AV1].mkv")
	dst, why, err := p.Place(src, "job1", lib, nil)
	want := filepath.Join(lib, "Blue Box", "Season 2", "Blue Box - S02E02 (1080p).mkv")
	if err != nil || why != "" || dst != want {
		t.Fatalf("blue box: %q %q %v", dst, why, err)
	}
	bb, _ := im.Cat.SeriesByFolder(filepath.Join(lib, "Blue Box"))
	files, _ := im.Cat.SeriesFiles(bb.ID)
	found := false
	for _, f := range files {
		if f.Path == want && f.ReleaseGroup == "Trix" && f.Resolution == "1080p" {
			found = true
		}
	}
	if !found {
		t.Fatalf("not recorded with its group: %+v", files)
	}

	// An absolute show's next episode before TMDB lists it waits, then goes in by hand.
	src = put("[SubsPlease] One Piece - 5 (1080p) [ABCD1234].mkv")
	if _, why, err := p.Place(src, "job2", lib, nil); err != nil || why == "" {
		t.Fatalf("one piece 5 should wait: %q %v", why, err)
	}
	q := p.Queue()
	if len(q) != 1 || q[0].Kind != animelib.KindNotOnTMDB || q[0].SeriesID == 0 {
		t.Fatalf("queue: %+v", q)
	}
	dst, err = p.Resolve(ctx, q[0].ID, animelib.Resolution{Action: "place-anyway"})
	if err != nil || dst != filepath.Join(lib, "One Piece", "Season 2", "One Piece - 5 (1080p).mkv") {
		t.Fatalf("place anyway: %q %v", dst, err)
	}

	// An unknown title: suggested by its first words, placed over an existing
	// file by hand, the old file kept aside, and the title remembered.
	src = put("[SubsPlease] Boku no Hero Academia Vigilantes - 01 (1080p) [ABCD1234].mkv")
	if _, why, _ := p.Place(src, "job3", lib, nil); why == "" {
		t.Fatal("unknown title should wait")
	}
	q = p.Queue()
	mha, _ := im.Cat.SeriesByFolder(filepath.Join(lib, "Boku no Hero Academia"))
	if len(q) != 1 || q[0].Kind != animelib.KindUnknown || len(q[0].Candidates) == 0 || q[0].Candidates[0].ID != mha.ID {
		t.Fatalf("unknown: %+v", q)
	}
	if _, err := p.Resolve(ctx, q[0].ID, animelib.Resolution{Action: "place", SeriesID: mha.ID, Episode: 1}); err == nil {
		t.Fatal("placing over an episode that has a file must not happen without replace")
	}
	dst, err = p.Resolve(ctx, q[0].ID, animelib.Resolution{Action: "replace", SeriesID: mha.ID, Episode: 1, Remember: true})
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(lib, "Boku no Hero Academia", "Season 1", "Boku no Hero Academia - 1 (720p).mkv")
	if _, err := os.Stat(old + ".replaced"); err != nil {
		t.Fatalf("old file not kept aside: %v", err)
	}
	if s, _, err := im.Cat.FindSeries("Boku no Hero Academia Vigilantes"); err != nil || s.ID != mha.ID {
		t.Fatalf("title not remembered: %+v %v", s, err)
	}
	if len(p.Queue()) != 0 {
		t.Fatalf("queue should be empty: %+v", p.Queue())
	}

	// A sequel's release, numbered from 1, goes in the sequel's own folder.
	src = put("[SubsPlease] Bleach Thousand-Year Blood War - 03 (1080p) [ABCD1234].mkv")
	dst, why, err = p.Place(src, "job6", lib, nil)
	if err != nil || why != "" || dst != filepath.Join(lib, "Bleach Thousand-Year Blood War", "Season 1", "Bleach Thousand-Year Blood War - S01E03 (1080p).mkv") {
		t.Fatalf("sequel: %q %q %v", dst, why, err)
	}

	// A show that is not in the catalog goes by folder name when it can.
	src = put("[SubsPlease] Never Imported - 03 (1080p) [ABCD1234].mkv")
	called := false
	dst, why, err = p.Place(src, "job5", lib, func() (string, string, error) { called = true; return "/lib/Never Imported/Season 1/x.mkv", "", nil })
	if !called || err != nil || why != "" || dst == "" || len(p.Queue()) != 0 {
		t.Fatalf("fallback: %v %q %q %v %+v", called, dst, why, err, p.Queue())
	}
	// When folder names cannot place it either, it waits for a person.
	if _, why, _ := p.Place(src, "job5", lib, func() (string, string, error) { return "", "no folder", nil }); why == "" || len(p.Queue()) != 1 {
		t.Fatalf("fallback left: %q %+v", why, p.Queue())
	}
	p.Resolve(ctx, p.Queue()[0].ID, animelib.Resolution{Action: "leave"})

	// The episode already in the library: the new copy waits.
	src = put("[Judas] Ao no Hako - S01E01 [1080p].mkv")
	if _, why, _ := p.Place(src, "job4", lib, nil); why == "" {
		t.Fatal("an episode with a file should wait")
	}
	if q = p.Queue(); len(q) != 1 || q[0].Kind != animelib.KindExists || q[0].Existing == nil {
		t.Fatalf("exists: %+v", q)
	}
	dst, err = p.Resolve(ctx, q[0].ID, animelib.Resolution{Action: "keep-both"})
	if err != nil || filepath.Base(dst) != "Blue Box - S01E01 (1080p) [Judas].mkv" {
		t.Fatalf("keep both: %q %v", dst, err)
	}

	// The dry run compares, and the mode is kept.
	p.Record("/x/a.mkv", "/lib/Show/Season 1/Show - S01E01.mkv", "", "/lib/Show/Season 1/Show - 1.mkv", "")
	p.Record("/x/b.mkv", "/lib/Show/Season 2/Show - S02E01.mkv", "", "", "no folder")
	p.Record("/x/c.mkv", "", animelib.ByFolderName, "/lib/Old/Season 1/Old - 1.mkv", "")
	if d := p.DryRun(); len(d) != 3 || d[0].Verdict != "same" || !d[0].ByFolder || d[1].Verdict != "better" || d[2].Verdict != "same" {
		t.Fatalf("dry run: %+v", d)
	}
	if err := p.SetMode(animelib.ModeCatalog); err != nil {
		t.Fatal(err)
	}
	p2 := &animelib.Placer{Cat: im.Cat, Meta: im.Meta, Blobs: blobs, Log: im.Log, Now: im.Now}
	if p2.Mode() != animelib.ModeCatalog || len(p2.DryRun()) != 3 {
		t.Fatalf("not kept: %s %d", p2.Mode(), len(p2.DryRun()))
	}
}
