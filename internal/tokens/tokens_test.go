package tokens_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/tokens"
)

func TestTokens(t *testing.T) {
	h, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := db.Migrate(h); err != nil {
		t.Fatal(err)
	}
	s := &tokens.Store{DB: h, Now: time.Now}
	if _, _, err := s.Create(" "); err == nil {
		t.Fatal("a token without a name")
	}
	v, tok, err := s.Create("home assistant")
	if err != nil || len(v) < 40 || tok.Hint != v[len(v)-4:] {
		t.Fatalf("%q %+v %v", v, tok, err)
	}
	if !s.Valid(v) || s.Valid(v+"x") || s.Valid("um_short") || s.Valid("") {
		t.Fatal("validity")
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].LastUsed.IsZero() {
		t.Fatalf("%+v", list)
	}
	s.Delete(tok.ID)
	if s.Valid(v) {
		t.Fatal("a revoked token still works")
	}
	var none *tokens.Store
	if none.Valid(v) {
		t.Fatal("nil store")
	}
}
