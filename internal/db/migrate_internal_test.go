package db

import (
	"path/filepath"
	"testing"
)

// Each schema version must upgrade to the newest without losing data. This
// stops at every earlier version, stores a job there, and migrates up.
func TestMigrateUpFromEveryVersion(t *testing.T) {
	for from := 1; from < len(migrations); from++ {
		h, err := Open(filepath.Join(t.TempDir(), "u.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Applied(h); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < from; i++ {
			if _, err := h.Exec(migrations[i]); err != nil {
				t.Fatalf("migration %d: %v", i+1, err)
			}
			if _, err := h.Exec(`INSERT INTO schema_migrations (version, applied) VALUES (?, 'x')`, i+1); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.Exec(`INSERT INTO jobs (id, state, created, updated, data) VALUES ('j1', 'downloading', 'a', 'a', '{"id":"j1"}')`); err != nil {
			t.Fatal(err)
		}
		if err := Migrate(h); err != nil {
			t.Fatalf("from version %d: %v", from, err)
		}
		var n int
		if err := h.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id = 'j1'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("from version %d: job lost (%d, %v)", from, n, err)
		}
		if v, _ := Applied(h); v != Version() {
			t.Fatalf("from version %d: at %d, want %d", from, v, Version())
		}
		h.Close()
	}
}
