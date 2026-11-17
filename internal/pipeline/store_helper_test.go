package pipeline_test

import (
	"path/filepath"
	"testing"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/store"
)

// openStore opens the job store the way the app does: SQLite in the config
// folder. Opening the same folder again is a restart.
func openStore(t *testing.T, configDir string) (*store.Store, error) {
	t.Helper()
	h, err := db.Open(filepath.Join(configDir, "undermynd.db"))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { h.Close() })
	if err := db.Migrate(h); err != nil {
		return nil, err
	}
	return store.OpenDB(h, filepath.Join(configDir, "jobs.json"))
}
