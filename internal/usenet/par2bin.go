package usenet

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// par2Path finds par2cmdline: HYVEMYND_PAR2, the copy in the image (/par2),
// or one on the PATH. "" when there is none (then the built-in repair, which
// holds the release in memory, is used).
var par2Path = sync.OnceValue(func() string {
	for _, p := range []string{os.Getenv("HYVEMYND_PAR2"), "/par2"} {
		if p != "" {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if p, err := exec.LookPath("par2"); err == nil {
		return p
	}
	return ""
})

var (
	par2Pct     = regexp.MustCompile(`^(Loading|Scanning|Repairing|Constructing|Solving|Verifying): ?(\d{1,3}(?:\.\d+)?)%`)
	par2Opening = regexp.MustCompile(`^Opening: "(.+)"`)
)

// repairPar2 verifies and repairs a release with par2cmdline. It reads the
// files from disk as it goes, so a release of any size can be repaired; it
// ends with ctx.
func repairPar2(ctx context.Context, bin, dir, idx string, r reporter) error {
	cmd := exec.CommandContext(ctx, bin, "r", "-p", "--", idx)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting par2: %w", err)
	}
	var tail bytes.Buffer
	var size int64 // the file being scanned
	last := 0.0
	repairing := false
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == '\n' || b == '\r' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if m := par2Opening.FindStringSubmatch(line); m != nil {
			size, last = 0, 0
			if st, err := os.Stat(filepath.Join(dir, m[1])); err == nil {
				size = st.Size()
			}
			r.Work(1)
			continue
		}
		if m := par2Pct.FindStringSubmatch(line); m != nil {
			p, _ := strconv.ParseFloat(m[2], 64)
			switch m[1] {
			case "Scanning":
				if p > last {
					r.Work(int64((p-last)/100*float64(size)) + 1)
					last = p
				}
			case "Repairing":
				if !repairing {
					repairing = true
					r.Note("par2: rebuilding")
				}
				r.Work(1)
			default:
				r.Work(1)
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "Target:") && !strings.HasSuffix(line, "found."),
			strings.HasPrefix(line, "You have"),
			strings.HasPrefix(line, "Repair is"),
			strings.HasPrefix(line, "Repair complete"),
			strings.HasPrefix(line, "All files are correct"),
			strings.HasPrefix(line, "Wrote "):
			r.Note("par2: %s", line)
		}
		tail.WriteString(line + "\n")
		if tail.Len() > 4096 {
			b := tail.Bytes()
			tail.Reset()
			tail.Write(b[len(b)-2048:])
		}
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if werr == nil {
		return nil
	}
	msg := tail.String()
	var ee *exec.ExitError
	if errors.As(werr, &ee) && ee.ExitCode() == 2 {
		return fmt.Errorf("too much is missing to repair: %s", par2Lines(msg, "You have"))
	}
	return fmt.Errorf("par2 repair failed: %s", lastLines(msg, 3))
}

// par2Lines picks the lines that start with prefix, or the last few.
func par2Lines(s, prefix string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	if len(keep) == 0 {
		return lastLines(s, 3)
	}
	return strings.Join(keep, " / ")
}
