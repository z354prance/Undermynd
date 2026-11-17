package db_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/db"
)

func TestMigrateIsIdempotentAndRefusesNewer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u.db")
	h, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for i := 0; i < 2; i++ {
		if err := db.Migrate(h); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := db.Applied(h); v != db.Version() {
		t.Fatalf("version %d, want %d", v, db.Version())
	}
	if _, err := h.Exec(`INSERT INTO schema_migrations (version, applied) VALUES (?, 'x')`, db.Version()+1); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(h); err == nil {
		t.Fatal("a database newer than the build must be refused")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("database must be private, got %v", fi.Mode().Perm())
	}
}

func TestBlobsImportLegacyFileOnce(t *testing.T) {
	dir := t.TempDir()
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rss-seen.json"), []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	b := db.Blobs{DB: h, LegacyDir: dir}
	v, ok, err := b.Load("rss-seen.json")
	if err != nil || !ok || string(v) != `{"k":"v"}` {
		t.Fatalf("import: %q %v %v", v, ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rss-seen.json")); !os.IsNotExist(err) {
		t.Fatal("the legacy file must be renamed after import")
	}
	if _, err := os.Stat(filepath.Join(dir, "rss-seen.json.bak")); err != nil {
		t.Fatal("the legacy file must be kept as .bak")
	}
	if err := b.Save("rss-seen.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := b.Load("rss-seen.json"); !ok || string(v) != `{}` {
		t.Fatalf("after save: %q", v)
	}
	if _, ok, _ := b.Load("missing.json"); ok {
		t.Fatal("a missing blob must report not found")
	}
}

func TestBackupKeepsNewestAndOpens(t *testing.T) {
	dir := t.TempDir()
	h, err := db.Open(filepath.Join(dir, "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	if err := (db.Blobs{DB: h}).Save("a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	bdir := filepath.Join(dir, "backups")
	t0 := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	var last string
	for i := 0; i < 5; i++ {
		p, err := db.Backup(h, bdir, 3, t0.Add(time.Duration(i)*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		last = p
	}
	list, _ := db.Backups(bdir)
	if len(list) != 3 || list[2] != last {
		t.Fatalf("want the newest 3 backups, got %v", list)
	}
	// Restoring is opening the copy: it must hold the data and the schema version.
	r, err := db.Open(last)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, _ := db.Applied(r); v != db.Version() {
		t.Fatalf("backup schema version %d", v)
	}
	if v, ok, _ := (db.Blobs{DB: r}).Load("a"); !ok || string(v) != "1" {
		t.Fatalf("backup content %q %v", v, ok)
	}
}
