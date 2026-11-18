// Package tokens keeps API tokens (roadmap I1). A token stands for the admin:
// a script or another app sends it as an X-Api-Key header (or a Bearer
// token) instead of signing in. Only a hash is stored; the token is shown once.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/db"
)

// Token is a stored token, without its value.
type Token struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Hint     string    `json:"hint"` // the last four characters
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed"`
}

// Store keeps tokens.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func hash(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// Create makes a token and returns its value, which is not kept.
func (s *Store) Create(name string) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return "", Token{}, errors.New("give the token a name (up to 80 characters)")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", Token{}, err
	}
	v := "um_" + base64.RawURLEncoding.EncodeToString(b)
	now := s.Now()
	r, err := s.DB.Exec(`INSERT INTO api_tokens (name, hash, hint, created) VALUES (?, ?, ?, ?)`, name, hash(v), v[len(v)-4:], db.Stamp(now))
	if err != nil {
		return "", Token{}, err
	}
	id, _ := r.LastInsertId()
	return v, Token{ID: id, Name: name, Hint: v[len(v)-4:], Created: now}, nil
}

// List lists the tokens.
func (s *Store) List() ([]Token, error) {
	rows, err := s.DB.Query(`SELECT id, name, hint, created, last_used FROM api_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var c, u string
		if err := rows.Scan(&t.ID, &t.Name, &t.Hint, &c, &u); err != nil {
			return nil, err
		}
		t.Created, _ = time.Parse(db.TimeFormat, c)
		if u != "" {
			t.LastUsed, _ = time.Parse(db.TimeFormat, u)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Delete revokes a token.
func (s *Store) Delete(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}

// Valid reports whether a token value is one of the stored tokens.
func (s *Store) Valid(v string) bool {
	if s == nil || !strings.HasPrefix(v, "um_") || len(v) < 20 {
		return false
	}
	r, err := s.DB.Exec(`UPDATE api_tokens SET last_used = ? WHERE hash = ?`, db.Stamp(s.Now()), hash(v))
	if err != nil {
		return false
	}
	n, _ := r.RowsAffected()
	return n == 1
}
