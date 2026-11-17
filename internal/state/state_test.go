package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/z354prance/undermynd/internal/state"
)

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	f := state.Files{Dir: dir}
	if _, ok, err := f.Load("x.json"); ok || err != nil {
		t.Fatalf("nothing saved yet: %v %v", ok, err)
	}
	if err := f.Save("x.json", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	b, ok, err := f.Load("x.json")
	if !ok || err != nil || string(b) != `{"a":1}` {
		t.Fatalf("%q %v %v", b, ok, err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "x.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, "x.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("no temporary file may be left")
	}
}
