package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/job"
)

var videoExt = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".m2ts": true, ".wmv": true, ".webm": true,
}

func isVideo(p string) bool { return videoExt[strings.ToLower(filepath.Ext(p))] }

func stem(p string) string { return strings.TrimSuffix(p, filepath.Ext(p)) }

func (e *Engine) tdarrIn(id string) string  { return filepath.Join(e.Cfg.Get().Tdarr.InDir, id) }
func (e *Engine) tdarrOut(id string) string { return filepath.Join(e.Cfg.Get().Tdarr.OutDir, id) }

// sendToTdarr moves the staged videos into the Tdarr input folder. Sidecar files
// (subtitles) stay in staging. It returns false when there is nothing for Tdarr to do.
func (e *Engine) sendToTdarr(j job.Job) bool {
	var videos []string
	for _, f := range j.Files {
		if isVideo(f) {
			videos = append(videos, f)
		}
	}
	if len(videos) == 0 {
		return false
	}
	for _, f := range videos {
		if err := moveFile(filepath.Join(j.StagingDir, f), filepath.Join(e.tdarrIn(j.ID), f)); err != nil {
			e.retry(j, err, 6, "move to Tdarr input")
			return true
		}
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
		c.TranscodeSig = ""
		c.LastProgress = e.Now()
		c.Move(job.QueuedTranscode, fmt.Sprintf("%d video(s) waiting in the Tdarr input folder", len(videos)), e.Now())
		return nil
	})
	e.Log.Printf("job %s sent to Tdarr", short(j.ID))
	return true
}

// findOutput looks for Tdarr's result for one input video: same folder, same name, any video extension.
func (e *Engine) findOutput(id, rel string) (string, os.FileInfo) {
	dir := filepath.Join(e.tdarrOut(id), filepath.Dir(rel))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	want := filepath.Base(stem(rel))
	for _, en := range ents {
		if en.IsDir() || !isVideo(en.Name()) || strings.HasPrefix(en.Name(), ".") {
			continue
		}
		if stem(en.Name()) == want {
			if fi, err := en.Info(); err == nil {
				return filepath.Join(dir, en.Name()), fi
			}
		}
	}
	return "", nil
}

// doTranscode waits for every video's output to appear and stop growing.
func (e *Engine) doTranscode(ctx context.Context, j job.Job) {
	now := e.Now()
	s := e.Cfg.Get()
	next := now.Add(time.Duration(s.ReconcileSeconds) * time.Second)
	found, total := 0, 0
	var sig strings.Builder
	for _, f := range j.Files {
		if !isVideo(f) {
			continue
		}
		total++
		if _, fi := e.findOutput(j.ID, f); fi != nil {
			found++
			fmt.Fprintf(&sig, "%d,", fi.Size())
		}
	}
	timeout := time.Duration(s.Tdarr.TimeoutMinutes) * time.Minute
	if found == 0 {
		if now.Sub(j.StateChanged) > timeout {
			e.fail(j, fmt.Sprintf("Tdarr produced no output within %d minutes", s.Tdarr.TimeoutMinutes))
			return
		}
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error { c.NextRun = next; return nil })
		return
	}
	if j.State == job.QueuedTranscode {
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			c.Move(job.Transcoding, "Tdarr is producing output", now)
			c.TranscodeSig, c.NextRun = sig.String(), next
			return nil
		})
		return
	}
	if found == total && sig.String() == j.TranscodeSig {
		_, _ = e.Store.Update(j.ID, func(c *job.Job) error {
			c.Move(job.Verifying, "Tdarr output complete", now)
			return nil
		})
		return
	}
	if now.Sub(j.StateChanged) > timeout {
		e.fail(j, fmt.Sprintf("Tdarr did not finish within %d minutes", s.Tdarr.TimeoutMinutes))
		return
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error { c.TranscodeSig, c.NextRun = sig.String(), next; return nil })
}

// validContainer checks the file starts like the container its extension claims.
func validContainer(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := make([]byte, 12)
	if n, _ := f.Read(h); n < 12 {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".webm":
		return h[0] == 0x1A && h[1] == 0x45 && h[2] == 0xDF && h[3] == 0xA3
	case ".mp4", ".m4v", ".mov":
		return string(h[4:8]) == "ftyp" || string(h[4:8]) == "moov" || string(h[4:8]) == "mdat" || string(h[4:8]) == "free"
	}
	return true
}

// doVerify checks each output, swaps it into staging in place of the original, clears the Tdarr folders and hands back.
func (e *Engine) doVerify(ctx context.Context, j job.Job) {
	type swap struct{ rel, out string }
	var swaps []swap
	for _, f := range j.Files {
		if !isVideo(f) {
			continue
		}
		out, fi := e.findOutput(j.ID, f)
		if fi == nil {
			e.fail(j, "Tdarr output for "+f+" vanished")
			return
		}
		in, err := os.Stat(filepath.Join(e.tdarrIn(j.ID), f))
		if fi.Size() == 0 || (err == nil && in.Size() > 0 && fi.Size()*20 < in.Size()) {
			e.fail(j, "Tdarr output for "+f+" failed the size check")
			return
		}
		if !validContainer(out) {
			e.fail(j, "Tdarr output for "+f+" is not a valid media container")
			return
		}
		swaps = append(swaps, swap{f, out})
	}
	newFiles := make([]string, 0, len(j.Files))
	repl := map[string]string{}
	for _, sw := range swaps {
		dstRel := filepath.Join(filepath.Dir(sw.rel), filepath.Base(sw.out))
		if err := moveFile(sw.out, filepath.Join(j.StagingDir, dstRel)); err != nil {
			e.retry(j, err, 6, "move Tdarr output to staging")
			return
		}
		repl[sw.rel] = dstRel
	}
	for _, f := range j.Files {
		if r, ok := repl[f]; ok {
			newFiles = append(newFiles, r)
		} else {
			newFiles = append(newFiles, f)
		}
	}
	if err := e.removeTdarrDirs(j); err != nil {
		e.retry(j, err, 10, "clear Tdarr folders")
		return
	}
	_, _ = e.Store.Update(j.ID, func(c *job.Job) error { c.Files, c.TranscodeSig = newFiles, ""; return nil })
	j.Files = newFiles
	e.handBack(j)
}

func (e *Engine) removeTdarrDirs(j job.Job) error {
	s := e.Cfg.Get().Tdarr
	if err := e.removeTree(filepath.Join(s.InDir, j.ID), s.InDir); err != nil {
		return err
	}
	return e.removeTree(filepath.Join(s.OutDir, j.ID), s.OutDir)
}
