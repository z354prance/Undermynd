// Package state names the small blobs of saved state that modules keep (RSS items
// already seen, tracked shows, access requests), and where they are kept: in files
// under the config folder, or in the database.
package state

import (
	"errors"
	"os"
	"path/filepath"
)

// Blobs keeps named blobs of saved state.
type Blobs interface {
	// Load returns the blob and true, or false when nothing has been saved yet.
	Load(name string) ([]byte, bool, error)
	// Save replaces the blob.
	Save(name string, b []byte) error
}

// Files keeps each blob as a file in Dir, written atomically and private (0600).
type Files struct{ Dir string }

func (f Files) Load(name string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(f.Dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func (f Files) Save(name string, b []byte) error {
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(f.Dir, name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
