package metadata

import (
	"database/sql"
	"strconv"

	"github.com/z354prance/undermynd/internal/db"
	"github.com/z354prance/undermynd/internal/library"
)

const normKey = "title-norm-version"

// Renormalize works out the stored comparison form of every list title and
// alias again after the way titles are compared changed. It runs once per
// change and returns how many rows changed.
func Renormalize(h *sql.DB) (int, error) {
	kv := db.Blobs{DB: h}
	if b, ok, err := kv.Load(normKey); err != nil {
		return 0, err
	} else if ok && string(b) == strconv.Itoa(library.NormalizeVersion) {
		return 0, nil
	}
	n := 0
	for _, table := range []string{"anime_titles", "aliases"} {
		tx, err := h.Begin()
		if err != nil {
			return n, err
		}
		rows, err := tx.Query(`SELECT rowid, title, norm FROM ` + table)
		if err != nil {
			tx.Rollback()
			return n, err
		}
		type fix struct {
			id   int64
			norm string
		}
		var fixes []fix
		for rows.Next() {
			var id int64
			var title, norm string
			if err := rows.Scan(&id, &title, &norm); err != nil {
				rows.Close()
				tx.Rollback()
				return n, err
			}
			if nn := library.Normalize(title); nn != norm && nn != "" {
				fixes = append(fixes, fix{id, nn})
			}
		}
		rows.Close()
		for _, f := range fixes {
			if _, err := tx.Exec(`UPDATE OR IGNORE `+table+` SET norm = ? WHERE rowid = ?`, f.norm, f.id); err != nil {
				tx.Rollback()
				return n, err
			}
		}
		if err := tx.Commit(); err != nil {
			return n, err
		}
		n += len(fixes)
	}
	return n, kv.Save(normKey, []byte(strconv.Itoa(library.NormalizeVersion)))
}
