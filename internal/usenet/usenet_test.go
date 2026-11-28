package usenet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akalin/gopar/par2"

	"github.com/z354prance/undermynd/internal/backend"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/secret"
)

// ---- yEnc and NZB making

func yencode(name string, data []byte, begin, total int64, part, parts int) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "=ybegin part=%d total=%d line=128 size=%d name=%s\r\n", part, parts, total, name)
	fmt.Fprintf(&b, "=ypart begin=%d end=%d\r\n", begin, begin+int64(len(data))-1)
	col := 0
	for _, c := range data {
		e := c + 42
		if e == 0 || e == '\n' || e == '\r' || e == '=' || (col == 0 && (e == '.' || e == '\t' || e == ' ')) {
			b.WriteByte('=')
			e += 64
			col++
		}
		b.WriteByte(e)
		col++
		if col >= 128 {
			b.WriteString("\r\n")
			col = 0
		}
	}
	if col > 0 {
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "=yend size=%d part=%d pcrc32=%08x\r\n", len(data), part, crc32.ChecksumIEEE(data))
	return b.Bytes()
}

type release struct {
	articles map[string][]byte // id -> body
	nzb      string
}

// makeRelease turns files into articles and an NZB; subjectName maps a file
// to the name in its subject (to scramble it).
func makeRelease(files map[string][]byte, partSize int, subjectName func(string) string) release {
	r := release{articles: map[string][]byte{}}
	var nzb strings.Builder
	nzb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="title">x</meta></head>`)
	k := 0
	for name, data := range files {
		parts := (len(data) + partSize - 1) / partSize
		sn := subjectName(name)
		fmt.Fprintf(&nzb, `<file poster="a" date="1" subject="[1/1] - &quot;%s&quot; yEnc (1/%d)"><groups><group>alt.binaries.x</group></groups><segments>`, sn, parts)
		for p := 0; p < parts; p++ {
			lo, hi := p*partSize, (p+1)*partSize
			if hi > len(data) {
				hi = len(data)
			}
			k++
			id := fmt.Sprintf("part%d.%d@test", k, p)
			body := yencode(sn, data[lo:hi], int64(lo+1), int64(len(data)), p+1, parts)
			r.articles[id] = body
			fmt.Fprintf(&nzb, `<segment bytes="%d" number="%d">%s</segment>`, len(body), p+1, id)
		}
		nzb.WriteString(`</segments></file>`)
	}
	nzb.WriteString(`</nzb>`)
	r.nzb = nzb.String()
	return r
}

// ---- a fake NNTP server

type newsServer struct {
	ln       net.Listener
	articles map[string][]byte
	user     string
	served   atomic.Int32
	conns    atomic.Int32
	mu       sync.Mutex
}

func startNews(t *testing.T, articles map[string][]byte, user string) *newsServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &newsServer{ln: ln, articles: articles, user: user}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.conns.Add(1)
			go s.serve(c)
		}
	}()
	return s
}

func (s *newsServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	fmt.Fprint(c, "200 welcome\r\n")
	authed := s.user == ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "AUTHINFO":
			if strings.EqualFold(f[1], "USER") {
				fmt.Fprint(c, "381 more\r\n")
			} else if len(f) > 2 && f[2] == "secret" {
				authed = true
				fmt.Fprint(c, "281 ok\r\n")
			} else {
				fmt.Fprint(c, "481 no\r\n")
			}
		case "BODY":
			if !authed {
				fmt.Fprint(c, "480 auth\r\n")
				continue
			}
			id := strings.Trim(f[1], "<>")
			s.mu.Lock()
			body, ok := s.articles[id]
			s.mu.Unlock()
			if !ok {
				fmt.Fprint(c, "430 no such article\r\n")
				continue
			}
			s.served.Add(1)
			var b bytes.Buffer
			fmt.Fprintf(&b, "222 0 <%s>\r\n", id)
			for _, l := range bytes.SplitAfter(body, []byte("\r\n")) {
				if len(l) == 0 {
					continue
				}
				if l[0] == '.' {
					b.WriteByte('.')
				}
				b.Write(l)
			}
			b.WriteString(".\r\n")
			c.Write(b.Bytes())
		case "STAT":
			if !authed {
				fmt.Fprint(c, "480 auth\r\n")
				continue
			}
			id := strings.Trim(f[1], "<>")
			s.mu.Lock()
			_, ok := s.articles[id]
			s.mu.Unlock()
			if ok {
				fmt.Fprintf(c, "223 0 <%s>\r\n", id)
			} else {
				fmt.Fprint(c, "430 no such article\r\n")
			}
		case "QUIT":
			fmt.Fprint(c, "205 bye\r\n")
			return
		default:
			fmt.Fprint(c, "500 what\r\n")
		}
	}
}

func (s *newsServer) cfg(name string, prio int) config.NewsServer {
	h, p, _ := net.SplitHostPort(s.ln.Addr().String())
	var port int
	fmt.Sscan(p, &port)
	ns := config.NewsServer{Name: name, Host: h, Port: port, Conns: 4, Priority: prio, On: true}
	if s.user != "" {
		ns.User, ns.Pass = s.user, secret.String("secret")
	}
	return ns
}

// ---- RAR (version 4, stored) for tests

func rarHeader(typ byte, flags uint16, body []byte) []byte {
	h := make([]byte, 7, 7+len(body))
	h[2] = typ
	binary.LittleEndian.PutUint16(h[3:], flags)
	binary.LittleEndian.PutUint16(h[5:], uint16(7+len(body)))
	h = append(h, body...)
	binary.LittleEndian.PutUint16(h[0:], uint16(crc32.ChecksumIEEE(h[2:])))
	return h
}

func makeRAR(name string, data []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0x00})
	b.Write(rarHeader(0x73, 0, make([]byte, 6)))
	fb := make([]byte, 25, 25+len(name))
	binary.LittleEndian.PutUint32(fb[0:], uint32(len(data))) // packed
	binary.LittleEndian.PutUint32(fb[4:], uint32(len(data))) // unpacked
	fb[8] = 3                                                // unix
	binary.LittleEndian.PutUint32(fb[9:], crc32.ChecksumIEEE(data))
	binary.LittleEndian.PutUint32(fb[13:], 0x5a000000) // time
	fb[17] = 20                                        // version
	fb[18] = 0x30                                      // stored
	binary.LittleEndian.PutUint16(fb[19:], uint16(len(name)))
	binary.LittleEndian.PutUint32(fb[21:], 0x81a4)
	fb = append(fb, name...)
	b.Write(rarHeader(0x74, 0x8000, fb))
	b.Write(data)
	b.Write(rarHeader(0x7b, 0x4000, nil))
	return b.Bytes()
}

// ---- helpers

type blobs struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (b *blobs) Load(n string) ([]byte, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[n]
	return v, ok, nil
}
func (b *blobs) Save(n string, v []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string][]byte{}
	}
	b.m[n] = append([]byte(nil), v...)
	return nil
}

func randBytes(n int) []byte { b := make([]byte, n); rand.Read(b); return b }

func run(t *testing.T, d *Downloader) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

func waitDone(t *testing.T, d *Downloader, id string) backend.Item {
	t.Helper()
	end := time.Now().Add(30 * time.Second)
	for time.Now().Before(end) {
		it, err := d.Status(context.Background(), id)
		if err == nil && (it.Status == backend.Complete || it.Status == backend.Errored) {
			return it
		}
		time.Sleep(20 * time.Millisecond)
	}
	it, err := d.Status(context.Background(), id)
	t.Fatalf("job %s did not finish: %+v %v", id, it, err)
	return backend.Item{}
}

func addJob(t *testing.T, d *Downloader, id, nzb string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), id+".nzb")
	os.WriteFile(p, []byte(nzb), 0o644)
	if _, _, err := d.Add(context.Background(), job.Job{ID: id, Kind: "usenet", NZBPath: p}); err != nil {
		t.Fatal(err)
	}
}

// ---- tests

func TestYEncRoundTrip(t *testing.T) {
	data := randBytes(5000)
	p, err := decodeYEnc(yencode("a b.mkv", data, 101, 9000, 2, 3))
	if err != nil || !bytes.Equal(p.Data, data) || !p.CRCOK || p.Begin != 101 || p.Size != 9000 || p.Name != "a b.mkv" {
		t.Fatalf("decode: %v crc=%v begin=%d name=%q", err, p.CRCOK, p.Begin, p.Name)
	}
	bad := yencode("x", data, 1, 5000, 1, 1)
	bad[len(bad)/2] ^= 1
	if p, _ := decodeYEnc(bad); p.CRCOK {
		t.Fatal("damage not noticed")
	}
}

// Main provider lacks some articles; the backup has them. A RAR is unpacked.
func TestDownloadWithBackupProviderAndUnpack(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	video := randBytes(300 << 10)
	files := map[string][]byte{"Show.S01E01.rar": makeRAR("Show.S01E01.mkv", video), "Show.S01E01.nfo": []byte("about")}
	rel := makeRelease(files, 20<<10, func(n string) string { return n })
	mainArts := map[string][]byte{}
	i := 0
	for id, b := range rel.articles {
		if i%3 != 0 {
			mainArts[id] = b // the main provider lacks every third article
		}
		i++
	}
	main := startNews(t, mainArts, "me")
	backup := startNews(t, rel.articles, "")
	cfg := config.Usenet{Servers: []config.NewsServer{main.cfg("Main", 0), backup.cfg("Backup", 1)}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	stop := run(t, d)
	defer stop()
	addJob(t, d, "j1", rel.nzb)
	it := waitDone(t, d, "j1")
	if it.Status != backend.Complete {
		st, _ := d.Status(context.Background(), "j1")
		t.Fatalf("status %+v %+v", it, st)
	}
	got, err := os.ReadFile(filepath.Join(d.Dir, "j1", "Show.S01E01.mkv"))
	if err != nil || !bytes.Equal(got, video) {
		t.Fatalf("unpacked video wrong: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "j1", "Show.S01E01.rar")); err == nil {
		t.Fatal("archive left behind")
	}
	if main.served.Load() == 0 || backup.served.Load() == 0 {
		t.Fatalf("served main %d backup %d", main.served.Load(), backup.served.Load())
	}
	if int(backup.served.Load()) > len(rel.articles)-len(mainArts)+2 {
		t.Fatalf("backup served %d, want only what main lacks (%d)", backup.served.Load(), len(rel.articles)-len(mainArts))
	}
	fs, _ := d.Files(context.Background(), "j1")
	names := []string{}
	for _, f := range fs {
		names = append(names, f.Path)
	}
	if strings.Join(names, ",") != "Show.S01E01.mkv,Show.S01E01.nfo" {
		t.Fatalf("files %v", names)
	}
}

// Scrambled names, some articles gone everywhere: names come back from par2
// and the files are repaired with the repair volumes.
func TestScrambledNamesAndPar2Repair(t *testing.T) {
	dir := t.TempDir()
	a, b := randBytes(200<<10), randBytes(150<<10)
	os.WriteFile(filepath.Join(dir, "Movie.2020.mkv"), a, 0o644)
	os.WriteFile(filepath.Join(dir, "Movie.2020.srt"), b, 0o644)
	if err := par2.Create(filepath.Join(dir, "Movie.2020.par2"), []string{filepath.Join(dir, "Movie.2020.mkv"), filepath.Join(dir, "Movie.2020.srt")},
		par2.CreateOptions{SliceByteCount: 4096, NumParityShards: 30}); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		files[e.Name()], _ = os.ReadFile(filepath.Join(dir, e.Name()))
	}
	scramble := map[string]string{"Movie.2020.mkv": "a8f3k2j1", "Movie.2020.srt": "zz91kd0q"}
	rel := makeRelease(files, 16<<10, func(n string) string {
		if s, ok := scramble[n]; ok {
			return s
		}
		return n
	})
	// Drop one article of the video everywhere.
	dropped := ""
	for id, body := range rel.articles {
		if strings.Contains(string(body[:80]), "a8f3k2j1") && strings.Contains(string(body[:60]), "part=2 ") {
			dropped = id
			delete(rel.articles, id)
			break
		}
	}
	if dropped == "" {
		t.Fatal("no article dropped")
	}
	srv := startNews(t, rel.articles, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("Only", 0)}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	stop := run(t, d)
	defer stop()
	addJob(t, d, "j2", rel.nzb)
	if it := waitDone(t, d, "j2"); it.Status != backend.Complete {
		t.Fatalf("status %+v", it)
	}
	got, _ := os.ReadFile(filepath.Join(d.Dir, "j2", "Movie.2020.mkv"))
	if !bytes.Equal(got, a) {
		t.Fatal("video not repaired or not renamed")
	}
	if got, _ := os.ReadFile(filepath.Join(d.Dir, "j2", "Movie.2020.srt")); !bytes.Equal(got, b) {
		t.Fatal("subtitle not renamed")
	}
	left, _ := os.ReadDir(filepath.Join(d.Dir, "j2"))
	for _, e := range left {
		if strings.HasSuffix(e.Name(), ".par2") || strings.HasPrefix(e.Name(), ".hyvemynd") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

func TestTooMuchMissingFails(t *testing.T) {
	files := map[string][]byte{"x.mkv": randBytes(100 << 10)}
	rel := makeRelease(files, 10<<10, func(n string) string { return n })
	n := 0
	for id := range rel.articles {
		if n < 3 {
			delete(rel.articles, id)
		}
		n++
	}
	srv := startNews(t, rel.articles, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("Only", 0)}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	stop := run(t, d)
	defer stop()
	addJob(t, d, "j3", rel.nzb)
	it := waitDone(t, d, "j3")
	if it.Status != backend.Errored || !(strings.Contains(it.Error, "no par2") || strings.Contains(it.Error, "gone from your providers")) {
		t.Fatalf("status %+v", it)
	}
}

// Providers unreachable: the job waits (busy), then finishes when one is back.
func TestProvidersDownWaitThenResume(t *testing.T) {
	files := map[string][]byte{"y.mkv": randBytes(60 << 10)}
	rel := makeRelease(files, 10<<10, func(n string) string { return n })
	srv := startNews(t, rel.articles, "")
	good := srv.cfg("Only", 0)
	bad := good
	bad.Port = 1 // nothing listens
	var mu sync.Mutex
	cfg := config.Usenet{Servers: []config.NewsServer{bad}}
	d := &Downloader{Cfg: func() config.Usenet { mu.Lock(); defer mu.Unlock(); return cfg }, Dir: t.TempDir(), State: &blobs{}, RetryWait: 100 * time.Millisecond}
	stop := run(t, d)
	defer stop()
	addJob(t, d, "j4", rel.nzb)
	time.Sleep(400 * time.Millisecond)
	if _, err := d.Status(context.Background(), "j4"); !errors.Is(err, backend.ErrBusy) || !strings.Contains(err.Error(), "could be reached") {
		t.Fatalf("while down: %v", err)
	}
	mu.Lock()
	cfg = config.Usenet{Servers: []config.NewsServer{good}}
	mu.Unlock()
	if it := waitDone(t, d, "j4"); it.Status != backend.Complete {
		t.Fatalf("after: %+v", it)
	}
}

func TestBadLoginReported(t *testing.T) {
	srv := startNews(t, nil, "me")
	c := srv.cfg("P", 0)
	c.Pass = "wrong"
	err := Test(context.Background(), c, nil)
	if err == nil || !strings.Contains(err.Error(), "refused the login") {
		t.Fatalf("err = %v", err)
	}
	if err := Test(context.Background(), srv.cfg("P", 0), nil); err != nil {
		t.Fatal(err)
	}
}

func TestRestartCarriesOn(t *testing.T) {
	files := map[string][]byte{"z.mkv": randBytes(400 << 10)}
	rel := makeRelease(files, 8<<10, func(n string) string { return n })
	srv := startNews(t, rel.articles, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("Only", 0)}}
	st := &blobs{}
	dir := t.TempDir()
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: dir, State: st}
	stop := run(t, d)
	addJob(t, d, "j5", rel.nzb)
	for srv.served.Load() < int32(len(rel.articles)/2) {
		time.Sleep(time.Millisecond)
	}
	stop() // HyveMynd stops halfway
	time.Sleep(5100 * time.Millisecond)
	before := srv.served.Load()
	d2 := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: dir, State: st}
	stop2 := run(t, d2)
	defer stop2()
	if it := waitDone(t, d2, "j5"); it.Status != backend.Complete {
		t.Fatalf("after restart: %+v", it)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "j5", "z.mkv"))
	if !bytes.Equal(got, files["z.mkv"]) {
		t.Fatal("file wrong after restart")
	}
	if again := srv.served.Load() - before; int(again) >= len(rel.articles) {
		t.Fatalf("fetched everything again (%d)", again)
	}
}

func TestPauseResumeAndQueueOrder(t *testing.T) {
	big := map[string][]byte{"a.mkv": randBytes(600 << 10)}
	relA := makeRelease(big, 4<<10, func(n string) string { return n })
	relB := makeRelease(map[string][]byte{"b.mkv": randBytes(20 << 10)}, 4<<10, func(n string) string { return n })
	relC := makeRelease(map[string][]byte{"c.mkv": randBytes(20 << 10)}, 4<<10, func(n string) string { return n })
	all := map[string][]byte{}
	for _, r := range []release{relA, relB, relC} {
		for k, v := range r.articles {
			all[k] = v
		}
	}
	srv := startNews(t, all, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("Only", 0)}}
	var limit atomic.Int32
	limit.Store(200) // KB/s
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}, Limit: func() int { return int(limit.Load()) }}
	stop := run(t, d)
	defer stop()
	addJob(t, d, "a", relA.nzb)
	time.Sleep(50 * time.Millisecond)
	addJob(t, d, "b", relB.nzb)
	time.Sleep(10 * time.Millisecond)
	addJob(t, d, "c", relC.nzb)
	// a is running (slowly, under the limit); c jumps ahead of b.
	if err := d.Top("c"); err != nil {
		t.Fatal(err)
	}
	if det := d.Details(); det["c"].Position != 1 || det["b"].Position != 2 || det["a"].Stage != Downloading {
		t.Fatalf("details %+v", det)
	}
	// Pausing a stops it and lets c run.
	if err := d.Pause("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Status(context.Background(), "a"); !errors.Is(err, backend.ErrBusy) || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("paused status: %v", err)
	}
	if it := waitDone(t, d, "c"); it.Status != backend.Complete {
		t.Fatalf("c: %+v", it)
	}
	if it, _ := d.Status(context.Background(), "a"); it.Status == backend.Complete {
		t.Fatal("paused download finished")
	}
	limit.Store(0)
	d.Resume("a")
	if it := waitDone(t, d, "a"); it.Status != backend.Complete {
		t.Fatalf("a: %+v", it)
	}
	if det := d.Details(); det["a"].FromMain == 0 {
		t.Fatalf("no article counted: %+v", det["a"])
	}
}

// An unpack that hangs does not hold up the queue: the next download goes
// ahead, the stuck one is given up with a clear message, and the log says
// what happened.
func TestStuckUnpackDoesNotBlockTheQueue(t *testing.T) {
	relA := makeRelease(map[string][]byte{"a.mkv": randBytes(30 << 10)}, 8<<10, func(n string) string { return n })
	relB := makeRelease(map[string][]byte{"b.mkv": randBytes(30 << 10)}, 8<<10, func(n string) string { return n })
	all := map[string][]byte{}
	for _, r := range []release{relA, relB} {
		for k, v := range r.articles {
			all[k] = v
		}
	}
	srv := startNews(t, all, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("Only", 0)}}

	release := make(chan struct{})
	defer close(release)
	orig := unpackFn
	unpackFn = func(ctx context.Context, dir, pw string, r reporter) (int, error) {
		if strings.HasSuffix(dir, "ja") {
			r.Note("unpacking a.part01.rar")
			<-release // hangs, like a stuck archive
			return 0, nil
		}
		return orig(ctx, dir, pw, r)
	}
	defer func() { unpackFn = orig }()

	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}, UnpackStall: 300 * time.Millisecond}
	stop := run(t, d)
	defer stop()
	addJob(t, d, "ja", relA.nzb)
	time.Sleep(20 * time.Millisecond)
	addJob(t, d, "jb", relB.nzb)

	// b downloads while a is stuck unpacking; then a is given up and b finishes.
	itA := waitDone(t, d, "ja")
	if itA.Status != backend.Errored || !strings.Contains(itA.Error, "stopped making progress") {
		t.Fatalf("a: %+v", itA)
	}
	if itB := waitDone(t, d, "jb"); itB.Status != backend.Complete {
		t.Fatalf("b: %+v", itB)
	}
	var lines []string
	for _, l := range d.LogOf("ja") {
		lines = append(lines, l.Msg)
	}
	log := strings.Join(lines, "\n")
	for _, want := range []string{"added:", "downloading 1 files", "downloaded: 0 articles missing", "unpacking a.part01.rar", "failed: unpacking stopped making progress"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log lacks %q:\n%s", want, log)
		}
	}
}

// Unpack progress is reported while it writes.
func TestUnpackReportsProgress(t *testing.T) {
	dir := t.TempDir()
	data := randBytes(200 << 10)
	os.WriteFile(filepath.Join(dir, "x.rar"), makeRAR("x.mkv", data), 0o644)
	var rep recReporter
	if n, err := unpack(context.Background(), dir, "", &rep); err != nil || n != 1 {
		t.Fatalf("unpack: %d %v", n, err)
	}
	if rep.work < int64(len(data)) || len(rep.notes) < 2 {
		t.Fatalf("work %d notes %v", rep.work, rep.notes)
	}
}

type recReporter struct {
	mu    sync.Mutex
	work  int64
	notes []string
}

func (r *recReporter) Note(f string, a ...any) {
	r.mu.Lock()
	r.notes = append(r.notes, fmt.Sprintf(f, a...))
	r.mu.Unlock()
}
func (r *recReporter) Work(n int64) { r.mu.Lock(); r.work += n; r.mu.Unlock() }

// More missing than the repair files can rebuild: fail at once, without
// fetching the repair files.
func TestUnrepairableFailsEarly(t *testing.T) {
	dir := t.TempDir()
	a := randBytes(200 << 10)
	os.WriteFile(filepath.Join(dir, "v.mkv"), a, 0o644)
	if err := par2.Create(filepath.Join(dir, "v.par2"), []string{filepath.Join(dir, "v.mkv")}, par2.CreateOptions{SliceByteCount: 4096, NumParityShards: 4}); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		files[e.Name()], _ = os.ReadFile(filepath.Join(dir, e.Name()))
	}
	rel := makeRelease(files, 16<<10, func(n string) string { return n })
	n := 0
	for id, body := range rel.articles {
		if strings.Contains(string(body[:80]), "v.mkv") && n < 6 {
			delete(rel.articles, id) // ~96 KB gone, the repair holds ~16 KB
			n++
		}
	}
	srv := startNews(t, rel.articles, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("Only", 0)}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	stop := run(t, d)
	defer stop()
	addJob(t, d, "ju", rel.nzb)
	it := waitDone(t, d, "ju")
	if it.Status != backend.Errored || !(strings.Contains(it.Error, "too much of this release is gone") || strings.Contains(it.Error, "gone from your providers")) {
		t.Fatalf("status %+v", it)
	}
}

// 7-Zip, when present, unpacks the archive (CI installs it).
func TestUnpackWith7Zip(t *testing.T) {
	if sevenZipPath() == "" {
		t.Skip("7-Zip not installed")
	}
	dir := t.TempDir()
	data := randBytes(300 << 10)
	os.WriteFile(filepath.Join(dir, "Show.S01E01.rar"), makeRAR("Show.S01E01.mkv", data), 0o644)
	var rep recReporter
	n, err := unpack(context.Background(), dir, "", &rep)
	if err != nil || n != 1 {
		t.Fatalf("unpack: %d %v %v", n, err, rep.notes)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "Show.S01E01.mkv"))
	if !bytes.Equal(got, data) {
		t.Fatal("content differs")
	}
	if _, err := os.Stat(filepath.Join(dir, "Show.S01E01.rar")); err == nil {
		t.Fatal("archive left behind")
	}
	if !strings.Contains(strings.Join(rep.notes, "\n"), "with 7-Zip") {
		t.Fatalf("notes %v", rep.notes)
	}
}

// A stuck 7-Zip is stopped when the work is given up.
func TestStuck7ZipIsStopped(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no shell")
	}
	dir := t.TempDir()
	fake := filepath.Join(t.TempDir(), "7zfake")
	os.WriteFile(fake, []byte("#!"+sh+"\necho '  0%'\nsleep 60\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "x.rar"), []byte("Rar!"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = extract7z(ctx, fake, filepath.Join(dir, "x.rar"), dir, "", 100, &recReporter{})
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(start))
	}
}

// TestWriteSampleRAR writes a small archive and its content for CI to unpack
// with the image's own 7-Zip (only when HYVEMYND_SAMPLE_RAR names a folder).
func TestWriteSampleRAR(t *testing.T) {
	dir := os.Getenv("HYVEMYND_SAMPLE_RAR")
	if dir == "" {
		t.Skip("only for the container check")
	}
	data := randBytes(500 << 10)
	if err := os.WriteFile(filepath.Join(dir, "sample.rar"), makeRAR("sample.mkv", data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.mkv"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fetchTask(t *testing.T, d *Downloader, nzb string) *task {
	t.Helper()
	tk := &task{ID: "f", Save: filepath.Join(d.Dir, "f")}
	os.MkdirAll(tk.Save, 0o755)
	os.WriteFile(filepath.Join(tk.Save, nzbFile), []byte(nzb), 0o644)
	return tk
}

// Articles missing everywhere are asked for again when a provider is added.
func TestAddedProviderFillsTheGaps(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	data := randBytes(100 << 10)
	rel := makeRelease(map[string][]byte{"x.mkv": data}, 10<<10, func(n string) string { return n })
	partial := map[string][]byte{}
	n := 0
	for id, b := range rel.articles {
		if n%10 != 0 { // one in ten gone: past the dead-release check
			partial[id] = b
		}
		n++
	}
	a := startNews(t, partial, "")
	b := startNews(t, rel.articles, "")
	var mu sync.Mutex
	cfg := config.Usenet{Servers: []config.NewsServer{a.cfg("A", 0)}}
	d := &Downloader{Cfg: func() config.Usenet { mu.Lock(); defer mu.Unlock(); return cfg }, Dir: t.TempDir(), State: &blobs{}}
	defer d.closePools()
	tk := fetchTask(t, d, rel.nzb)
	if _, err := d.fetch(context.Background(), tk); err == nil || !strings.Contains(err.Error(), "no par2") {
		t.Fatalf("first: %v", err)
	}
	mu.Lock()
	cfg = config.Usenet{Servers: []config.NewsServer{a.cfg("A", 0), b.cfg("B", 1)}}
	mu.Unlock()
	pr, err := d.fetch(context.Background(), tk)
	if err != nil || pr.Missing != 0 || len(pr.Lost) != 0 {
		t.Fatalf("second: %v %+v", err, pr)
	}
	got, _ := os.ReadFile(filepath.Join(tk.Save, "x.mkv"))
	if !bytes.Equal(got, data) {
		t.Fatal("gaps not filled")
	}
	if !strings.Contains(fmt.Sprint(tk.Log), "asking B for the") {
		t.Fatalf("log %v", tk.Log)
	}
}

// A job an older version left mid-repair, with far more missing than par2
// can rebuild, fails when it is picked up again.
func TestOldUnrepairableJobFails(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	rel := makeRelease(map[string][]byte{"x.mkv": randBytes(100 << 10), "x.vol00+01.par2": randBytes(5 << 10)}, 10<<10, func(n string) string { return n })
	srv := startNews(t, rel.articles, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("A", 0)}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	defer d.closePools()
	tk := fetchTask(t, d, rel.nzb)
	pr := &progress{Done: map[string]bool{}, Names: map[int]string{}, Missing: 6, Repair: true}
	for id := range rel.articles {
		pr.Done[id] = true
	}
	d.saveProgress(tk, pr)
	if _, err := d.fetch(context.Background(), tk); err == nil || !strings.Contains(err.Error(), "too much of this release is gone") {
		t.Fatalf("err %v", err)
	}
}

// par2cmdline, when present, repairs from disk (CI installs it).
func TestRepairWithPar2(t *testing.T) {
	bin := par2Path()
	if bin == "" {
		t.Skip("par2 not installed")
	}
	dir := t.TempDir()
	data := randBytes(400 << 10)
	os.WriteFile(filepath.Join(dir, "v.mkv"), data, 0o644)
	if out, err := exec.Command(bin, "c", "-q", "-s4096", "-r10", filepath.Join(dir, "v.par2"), filepath.Join(dir, "v.mkv")).CombinedOutput(); err != nil {
		t.Fatalf("create: %v %s", err, out)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "v.mkv"), os.O_RDWR, 0)
	f.WriteAt(make([]byte, 20000), 50000)
	f.Close()
	var rep recReporter
	if err := repair(context.Background(), dir, &rep); err != nil {
		t.Fatalf("repair: %v %v", err, rep.notes)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "v.mkv")); !bytes.Equal(got, data) {
		t.Fatalf("not repaired: %v", rep.notes)
	}
	if rep.work == 0 || !strings.Contains(strings.Join(rep.notes, "\n"), "with par2") {
		t.Fatalf("progress %d notes %v", rep.work, rep.notes)
	}
	// Too much gone: a clear error.
	exec.Command(bin, "c", "-q", "-s4096", "-r1", filepath.Join(dir, "v.par2"), filepath.Join(dir, "v.mkv")).Run()
	f, _ = os.OpenFile(filepath.Join(dir, "v.mkv"), os.O_RDWR, 0)
	f.WriteAt(make([]byte, 200000), 50000)
	f.Close()
	if err := repair(context.Background(), dir, &recReporter{}); err == nil || !strings.Contains(err.Error(), "too much is missing") {
		t.Fatalf("err %v", err)
	}
}

// A stuck par2 is stopped when the work is given up.
func TestStuckPar2IsStopped(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no shell")
	}
	dir := t.TempDir()
	fake := filepath.Join(t.TempDir(), "par2fake")
	os.WriteFile(fake, []byte("#!"+sh+"\necho 'Scanning: 1.0%'\nsleep 60\n"), 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := repairPar2(ctx, fake, dir, filepath.Join(dir, "x.par2"), &recReporter{}); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(start))
	}
}

// Providers are listed before any download has opened a connection.
func TestStatsListsIdleProviders(t *testing.T) {
	cfg := config.Usenet{Servers: []config.NewsServer{{Name: "A", Host: "a.example", On: true}, {Name: "B", Host: "b.example"}}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	s := d.Stats()
	if len(s.Servers) != 1 || s.Servers[0].Name != "A" || s.Servers[0].Problem != "" {
		t.Fatalf("servers %+v", s.Servers)
	}
}

// A release gone everywhere fails at the sample check, before downloading.
func TestDeadReleaseFailsFast(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	rel := makeRelease(map[string][]byte{"x.mkv": randBytes(200 << 10)}, 10<<10, func(n string) string { return n })
	srv := startNews(t, map[string][]byte{}, "")
	cfg := config.Usenet{Servers: []config.NewsServer{srv.cfg("A", 0)}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	defer d.closePools()
	tk := fetchTask(t, d, rel.nzb)
	_, err := d.fetch(context.Background(), tk)
	if err == nil || !strings.Contains(err.Error(), "gone from your providers") {
		t.Fatalf("err %v", err)
	}
	if srv.served.Load() != 0 {
		t.Fatalf("%d articles downloaded; none should be", srv.served.Load())
	}
}

// The report card counts, per provider, what it served, what it lacked and
// what only it had.
func TestProviderReports(t *testing.T) {
	secret.SetKeyForTest(make([]byte, 32))
	data := randBytes(100 << 10)
	rel := makeRelease(map[string][]byte{"x.mkv": data}, 10<<10, func(n string) string { return n })
	partial := map[string][]byte{}
	n := 0
	for id, b := range rel.articles {
		if n%10 != 0 {
			partial[id] = b
		}
		n++
	}
	a := startNews(t, partial, "")
	b := startNews(t, rel.articles, "")
	cfg := config.Usenet{Servers: []config.NewsServer{a.cfg("A", 0), b.cfg("B", 1)}}
	d := &Downloader{Cfg: func() config.Usenet { return cfg }, Dir: t.TempDir(), State: &blobs{}}
	defer d.closePools()
	tk := fetchTask(t, d, rel.nzb)
	if _, err := d.fetch(context.Background(), tk); err != nil {
		t.Fatal(err)
	}
	reps := d.ProviderReports(30)
	byName := map[string]ProviderReport{}
	for _, r := range reps {
		byName[r.Name] = r
	}
	ra, rb := byName[a.cfg("A", 0).Label()], byName[b.cfg("B", 1).Label()]
	if ra.Missing == 0 || rb.Rescued == 0 || rb.Rescued != ra.Missing || ra.Articles == 0 || ra.Share <= rb.Share {
		t.Fatalf("A %+v B %+v", ra, rb)
	}
	d.saveProvStats()
	d2 := &Downloader{Cfg: d.Cfg, Dir: d.Dir, State: d.State}
	if r := d2.ProviderReports(30); len(r) != 2 || r[0].Articles == 0 {
		t.Fatalf("after a restart: %+v", r)
	}
}
