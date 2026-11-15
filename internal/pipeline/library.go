package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/library"
)

// placeInLibrary moves each finished episode into its show's season folder, named
// like the rest of the library. An episode it cannot place with certainty is left
// where it was delivered, and nothing is ever overwritten. The job ends either way.
func (e *Engine) placeInLibrary(ctx context.Context, j job.Job, cat config.Category) {
	now := e.Now()
	root := cat.LibraryDir
	folders, err := library.Folders(root)
	if err != nil {
		e.retry(j, fmt.Errorf("library folder %s: %w", root, err), 10, "library")
		return
	}
	var left []string
	placed := 0
	for _, f := range j.Files {
		if !isVideo(f) {
			continue
		}
		src := filepath.Join(j.CompleteDir, f)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			continue // placed on an earlier pass
		}
		base := filepath.Base(f)
		rel, ok := library.Parse(base)
		if !ok {
			left = append(left, base+": the name has no episode number")
			continue
		}
		folder, cands := library.Match(rel.Title, folders)
		if folder == "" {
			msg := fmt.Sprintf("no folder named %q", rel.Title)
			if len(cands) > 0 {
				msg += " (closest: " + strings.Join(cands, ", ") + ")"
			}
			left = append(left, msg)
			continue
		}
		dst := filepath.Join(library.SeasonDir(filepath.Join(root, folder), rel.Season), library.Clean(base))
		if _, err := os.Stat(dst); err == nil {
			left = append(left, fmt.Sprintf("%s already exists in %s", filepath.Base(dst), folder))
			continue
		}
		if err := moveFile(src, dst); err != nil {
			e.retry(j, err, 10, "place in library")
			return
		}
		placed++
		e.Log.Printf("job %s placed %s", short(j.ID), dst)
	}
	if len(left) == 0 {
		if err := e.cleanupExcept(ctx, j, false); err != nil {
			e.retry2(j, err)
			return
		}
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			c.Flag = ""
			c.Move(job.Cleaned, fmt.Sprintf("placed %d file(s) in %s", placed, root), now)
			return nil
		})
		return
	}
	// Some files could not be placed: keep everything in the complete folder as it is.
	if err := e.cleanupExcept(ctx, j, true); err != nil {
		e.retry2(j, err)
		return
	}
	why := fmt.Sprintf("placed %d, left in %s: %s", placed, j.CompleteDir, strings.Join(dedupe(left), "; "))
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.Move(job.Cleaned, why, now)
		return nil
	})
	e.Log.Printf("job %s %s", short(j.ID), why)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
