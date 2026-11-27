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

// sevenZipPath finds 7-Zip: HYVEMYND_7Z, the copy in the image (/7zzs), or
// one on the PATH. "" when there is none (then the built-in RAR reader is
// used).
var sevenZipPath = sync.OnceValue(func() string {
	for _, p := range []string{os.Getenv("HYVEMYND_7Z"), "/7zzs"} {
		if p != "" {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	for _, n := range []string{"7zzs", "7zz"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
})

var sevenPct = regexp.MustCompile(`(\d{1,3})%`)

// extract7z unpacks an archive (all its volumes) with 7-Zip into dir. The
// process ends with ctx, so a stuck one can be stopped.
func extract7z(ctx context.Context, bin, first, dir, password string, total int64, r reporter) (used []string, n int, err error) {
	args := []string{"x", "-y", "-aoa", "-bb1", "-bsp1", "-bso1", "-bse1", "-o" + dir, "-p" + password, first}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// Stopping it stops everything it started, and its output closes, so a
	// stuck unpack can always be given up.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("starting 7-Zip: %w", err)
	}
	var tail bytes.Buffer // the last lines, for the error message
	last := 0
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	// 7-Zip redraws its progress with carriage returns.
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == '\n' || b == '\r' || b == '\b' {
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
		if m := sevenPct.FindStringSubmatch(line); m != nil {
			if p, _ := strconv.Atoi(m[1]); p > last && p <= 100 {
				if total > 0 {
					r.Work(int64(p-last) * total / 100)
				} else {
					r.Work(1)
				}
				last = p
			}
			continue
		}
		if strings.HasPrefix(line, "- ") {
			n++
			r.Note("  writing %s", strings.TrimPrefix(line, "- "))
			r.Work(1)
			continue
		}
		tail.WriteString(line + "\n")
		if tail.Len() > 4096 {
			b := tail.Bytes()
			tail.Reset()
			tail.Write(b[len(b)-2048:])
		}
	}
	werr := cmd.Wait()
	msg := tail.String()
	if ctx.Err() != nil {
		return nil, n, ctx.Err()
	}
	low := strings.ToLower(msg)
	if strings.Contains(low, "wrong password") || strings.Contains(low, "can not open encrypted archive") {
		return nil, n, errPassword
	}
	if werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) && ee.ExitCode() == 1 {
			r.Note("7-Zip warnings: %s", lastLines(msg, 3))
		} else {
			return nil, n, fmt.Errorf("7-Zip could not unpack %s: %s", filepath.Base(first), lastLines(msg, 4))
		}
	}
	if total > 0 && last < 100 {
		r.Work(int64(100-last) * total / 100) // finished: the rest is done
	}
	return volumesOf(first), n, nil
}

func lastLines(s string, k int) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "7-Zip") && !strings.HasPrefix(l, "Scanning") {
			keep = append(keep, l)
		}
	}
	if len(keep) > k {
		keep = keep[len(keep)-k:]
	}
	if len(keep) == 0 {
		return "no details"
	}
	return strings.Join(keep, " / ")
}

// volumesOf lists the files of a RAR archive from its first volume's name:
// name.partNN.rar volumes, or name.rar with name.rNN parts.
func volumesOf(first string) []string {
	dir, base := filepath.Dir(first), filepath.Base(first)
	ents, _ := os.ReadDir(dir)
	var out []string
	if m := rarPart.FindStringSubmatchIndex(base); m != nil {
		stem := strings.ToLower(base[:m[0]])
		for _, e := range ents {
			n := e.Name()
			if mm := rarPart.FindStringSubmatchIndex(n); mm != nil && strings.ToLower(n[:mm[0]]) == stem {
				out = append(out, filepath.Join(dir, n))
			}
		}
		return out
	}
	stem := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	for _, e := range ents {
		n := e.Name()
		low := strings.ToLower(n)
		if low == stem+".rar" || (rarOld.MatchString(n) && strings.TrimSuffix(low, filepath.Ext(low)) == stem) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}
