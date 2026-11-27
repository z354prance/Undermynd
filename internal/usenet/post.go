package usenet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/akalin/gopar/par2"
	"github.com/nwaples/rardecode/v2"
)

// par2Entry is one file a par2 set describes.
type par2Entry struct {
	Name   string
	Hash16 [16]byte // MD5 of the first 16 KiB
	Length int64
}

var (
	par2Magic    = []byte("PAR2\x00PKT")
	fileDescType = []byte("PAR 2.0\x00FileDesc")
)

// readPar2Names lists the files a .par2 file describes, reading only its
// file description packets.
func readPar2Names(p string) ([]par2Entry, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	var out []par2Entry
	seen := map[[16]byte]bool{}
	hdr := make([]byte, 64)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			break
		}
		if !bytes.Equal(hdr[:8], par2Magic) {
			return out, errors.New("not a par2 file")
		}
		n := int64(binary.LittleEndian.Uint64(hdr[8:16]))
		if n < 64 || n > 1<<30 || n%4 != 0 {
			return out, errors.New("damaged par2 packet")
		}
		body := n - 64
		var typ [16]byte
		copy(typ[:], hdr[48:64])
		if !bytes.Equal(typ[:], fileDescType) || body > 64<<10 {
			if _, err := io.CopyN(io.Discard, r, body); err != nil {
				break
			}
			continue
		}
		b := make([]byte, body)
		if _, err := io.ReadFull(r, b); err != nil {
			break
		}
		if len(b) < 56 {
			continue
		}
		var id [16]byte
		copy(id[:], b[:16])
		if seen[id] {
			continue
		}
		seen[id] = true
		var e par2Entry
		copy(e.Hash16[:], b[32:48])
		e.Length = int64(binary.LittleEndian.Uint64(b[48:56]))
		e.Name = safeName(string(bytes.TrimRight(b[56:], "\x00")))
		if e.Name != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

func hash16k(p string) ([16]byte, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return [16]byte{}, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return [16]byte{}, 0, err
	}
	h := md5.New()
	if _, err := io.CopyN(h, f, 16<<10); err != nil && err != io.EOF {
		return [16]byte{}, 0, err
	}
	var s [16]byte
	copy(s[:], h.Sum(nil))
	return s, st.Size(), nil
}

// mainPar2 finds the par2 index file in dir (the smallest .par2, which is
// the one without repair data), or "".
func mainPar2(dir string) string {
	ents, _ := os.ReadDir(dir)
	best, size := "", int64(-1)
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".par2") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !isRepairVolume(e.Name()) && (size < 0 || info.Size() < size) {
			best, size = filepath.Join(dir, e.Name()), info.Size()
		}
	}
	if best == "" {
		for _, e := range ents { // only repair volumes: any of them describes the set
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".par2") {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return best
}

// restoreNames renames files whose names are scrambled to the names the par2
// set gives them, matched by the MD5 of their first 16 KiB. It returns how
// many it renamed.
func restoreNames(dir string) int {
	idx := mainPar2(dir)
	if idx == "" {
		return 0
	}
	entries, err := readPar2Names(idx)
	if err != nil || len(entries) == 0 {
		return 0
	}
	byHash := map[[16]byte]par2Entry{}
	want := map[string]bool{}
	for _, e := range entries {
		byHash[e.Hash16] = e
		want[e.Name] = true
	}
	ents, _ := os.ReadDir(dir)
	n := 0
	for _, de := range ents {
		name := de.Name()
		if de.IsDir() || want[name] || strings.HasPrefix(name, ".") || strings.EqualFold(filepath.Ext(name), ".par2") {
			continue
		}
		p := filepath.Join(dir, name)
		h, size, err := hash16k(p)
		if err != nil {
			continue
		}
		e, ok := byHash[h]
		if !ok || (e.Length != size && size > 16<<10) {
			continue
		}
		dst := filepath.Join(dir, e.Name)
		if _, err := os.Stat(dst); err == nil {
			continue // never overwrite
		}
		if os.Rename(p, dst) == nil {
			n++
		}
	}
	return n
}

// reporter receives what the post-download steps are doing: Note for a
// line in the download's log, Work for bytes written (or a step done), so a
// watchdog can tell slow from stuck.
type reporter interface {
	Note(format string, a ...any)
	Work(n int64)
}

type nopReporter struct{}

func (nopReporter) Note(string, ...any) {}
func (nopReporter) Work(int64)          {}

// par2Delegate turns the par2 library's callbacks into progress.
type par2Delegate struct {
	par2.DoNothingDecoderDelegate
	r reporter
}

func (p par2Delegate) OnDataFileLoad(i, n int, path string, byteCount, hits, misses int, err error) {
	p.r.Work(int64(byteCount) + 1)
	if misses > 0 || err != nil {
		p.r.Note("par2: %s has %d damaged or missing blocks", filepath.Base(path), misses)
	}
}
func (p par2Delegate) OnParityFileLoad(i int, path string, err error) { p.r.Work(1) }
func (p par2Delegate) OnDataFileWrite(i, n int, path string, byteCount int, err error) {
	p.r.Work(int64(byteCount) + 1)
	if err == nil {
		p.r.Note("par2: repaired %s", filepath.Base(path))
	}
}

// repair verifies the release against its par2 set and repairs it.
func repair(ctx context.Context, dir string, r reporter) (err error) {
	defer func() {
		if r := recover(); r != nil { // the par2 library panics on some damaged sets
			err = fmt.Errorf("par2 repair failed: %v", r)
		}
	}()
	idx := mainPar2(dir)
	if idx == "" {
		return errors.New("some articles are missing or damaged and the release has no par2 repair files")
	}
	if bin := par2Path(); bin != "" {
		r.Note("par2: checking the release against %s with par2", filepath.Base(idx))
		return repairPar2(ctx, bin, dir, idx, r)
	}
	// The par2 library holds the whole release in memory while it repairs.
	var size int64
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			size += info.Size()
		}
	}
	if free := memAvailable(); free > 0 && size > free*7/10 {
		return fmt.Errorf("the release needs repair but is too large to repair in the memory available (%d MB needed, %d MB free)", size>>20, free>>20)
	}
	r.Note("par2: checking %d MB against %s", size>>20, filepath.Base(idx))
	dec, err := par2.NewDecoder(par2Delegate{r: r}, idx, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("reading the par2 files: %w", err)
	}
	if err := dec.LoadFileData(); err != nil {
		return fmt.Errorf("checking the files: %w", err)
	}
	if err := dec.LoadParityData(); err != nil {
		return fmt.Errorf("reading the repair data: %w", err)
	}
	sc := dec.ShardCounts()
	if !sc.RepairNeeded() {
		r.Note("par2: nothing needs repair")
		return nil
	}
	r.Note("par2: %d blocks damaged, %d repair blocks available: repairing", sc.UnusableDataShardCount, sc.UsableParityShardCount)
	if !sc.RepairPossible() {
		return fmt.Errorf("too much is missing to repair (%d blocks damaged, %d repair blocks)", sc.UnusableDataShardCount, sc.UsableParityShardCount)
	}
	if _, err := dec.Repair(true); err != nil {
		return fmt.Errorf("par2 repair failed: %w", err)
	}
	return nil
}

var (
	rarPart  = regexp.MustCompile(`(?i)\.part0*(\d+)\.rar$`)
	rarOld   = regexp.MustCompile(`(?i)\.r\d{2,3}$`)
	splitNum = regexp.MustCompile(`(?i)\.(\d{3})$`)
)

// firstVolumes finds the first volume of each RAR archive in dir.
func firstVolumes(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") {
			continue
		}
		if m := rarPart.FindStringSubmatch(n); m != nil {
			if v, _ := strconv.Atoi(m[1]); v == 1 {
				out = append(out, filepath.Join(dir, n))
			}
			continue
		}
		if strings.EqualFold(filepath.Ext(n), ".rar") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out
}

// errPassword means an archive needs a password the NZB did not give.
var errPassword = errors.New("the archive is password protected")

// unpack extracts every RAR archive in dir into dir and deletes the
// archives it extracted. It goes one level deep into archives inside
// archives.
func unpack(ctx context.Context, dir, password string, r reporter) (int, error) {
	total := 0
	bin := sevenZipPath()
	for round := 0; round < 2; round++ {
		vols := firstVolumes(dir)
		if len(vols) == 0 {
			break
		}
		for _, v := range vols {
			var used []string
			var n int
			var err error
			if bin != "" {
				size := int64(0)
				for _, f := range volumesOf(v) {
					if st, e := os.Stat(f); e == nil {
						size += st.Size()
					}
				}
				r.Note("unpacking %s with 7-Zip", filepath.Base(v))
				used, n, err = extract7z(ctx, bin, v, dir, password, size, r)
			} else {
				r.Note("unpacking %s", filepath.Base(v))
				used, n, err = extract(v, dir, password, r)
			}
			if err != nil {
				r.Note("unpacking %s failed: %v", filepath.Base(v), err)
				return total, err
			}
			r.Note("unpacked %d file(s) from %s (%d volume(s))", n, filepath.Base(v), len(used))
			total += n
			for _, u := range used {
				os.Remove(u)
			}
		}
	}
	// Leftover split parts of archives that were extracted (.r00 style).
	if total > 0 {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if rarOld.MatchString(e.Name()) {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	return total, nil
}

func extract(first, dir, password string, r reporter) (used []string, n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			used, err = nil, fmt.Errorf("unpacking %s failed: %v", filepath.Base(first), r)
		}
	}()
	var opts []rardecode.Option
	if password != "" {
		opts = append(opts, rardecode.Password(password))
	}
	rc, err := rardecode.OpenReader(first, opts...)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "password") {
			return nil, 0, errPassword
		}
		return nil, 0, fmt.Errorf("opening %s: %w", filepath.Base(first), err)
	}
	defer rc.Close()
	for {
		h, err := rc.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "password") {
				return nil, n, errPassword
			}
			return nil, n, fmt.Errorf("unpacking %s: %w", filepath.Base(first), err)
		}
		if h.IsDir {
			continue
		}
		if h.Encrypted && password == "" {
			return nil, n, errPassword
		}
		rel := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(h.Name, "\\", "/")))
		if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, n, err
		}
		tmp := dst + ".unpacking"
		f, err := os.Create(tmp)
		if err != nil {
			return nil, n, err
		}
		r.Note("  writing %s (%d MB)", rel, h.UnPackedSize>>20)
		_, cerr := io.Copy(workWriter{f, r}, rc)
		f.Close()
		if cerr != nil {
			os.Remove(tmp)
			if h.Encrypted {
				return nil, n, errPassword // a wrong password shows as bad data
			}
			return nil, n, fmt.Errorf("unpacking %s: %w", h.Name, cerr)
		}
		if err := os.Rename(tmp, dst); err != nil {
			return nil, n, err
		}
		n++
	}
	used = append([]string{first}, rc.Volumes()...)
	for i, u := range used {
		if !filepath.IsAbs(u) {
			used[i] = filepath.Join(dir, u)
		}
	}
	return used, n, nil
}

// memAvailable reads the memory free for use (Linux), or 0 if unknown.
func memAvailable() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "MemAvailable:"); ok {
			f := strings.Fields(v)
			if len(f) > 0 {
				kb, _ := strconv.ParseInt(f[0], 10, 64)
				return kb << 10
			}
		}
	}
	return 0
}

// workWriter reports every write as progress.
type workWriter struct {
	w io.Writer
	r reporter
}

func (w workWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.r.Work(int64(n))
	return n, err
}

// archiveBytes is the size of the archives in dir, roughly what unpacking writes.
func archiveBytes(dir string) int64 {
	var n int64
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		nm := strings.ToLower(e.Name())
		if strings.HasSuffix(nm, ".rar") || rarOld.MatchString(nm) {
			if info, err := e.Info(); err == nil {
				n += info.Size()
			}
		}
	}
	return n
}
