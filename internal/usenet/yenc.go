package usenet

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// part is one decoded yEnc article.
type part struct {
	Name  string
	Begin int64 // 1-based offset of the first byte in the file
	Size  int64 // the whole file's size
	Data  []byte
	CRCOK bool // false when the article's CRC did not match (damaged)
}

var errNotYEnc = errors.New("not a yEnc article")

// decodeYEnc decodes one article body.
func decodeYEnc(body []byte) (part, error) {
	var p part
	lines := bytes.Split(body, []byte("\n"))
	i := 0
	for ; i < len(lines); i++ {
		if bytes.HasPrefix(lines[i], []byte("=ybegin ")) {
			break
		}
	}
	if i == len(lines) {
		return p, errNotYEnc
	}
	hdr := yfields(lines[i])
	p.Name = hdr["name"]
	p.Size, _ = strconv.ParseInt(hdr["size"], 10, 64)
	p.Begin = 1
	i++
	if i < len(lines) && bytes.HasPrefix(lines[i], []byte("=ypart ")) {
		pf := yfields(lines[i])
		p.Begin, _ = strconv.ParseInt(pf["begin"], 10, 64)
		i++
	}
	out := make([]byte, 0, len(body))
	var trailer map[string]string
	for ; i < len(lines); i++ {
		l := bytes.TrimRight(lines[i], "\r")
		if bytes.HasPrefix(l, []byte("=yend")) {
			trailer = yfields(l)
			break
		}
		esc := false
		for _, b := range l {
			if esc {
				out = append(out, b-64-42)
				esc = false
				continue
			}
			if b == '=' {
				esc = true
				continue
			}
			out = append(out, b-42)
		}
	}
	p.Data = out
	p.CRCOK = trailer != nil
	if trailer != nil {
		want := trailer["pcrc32"]
		if want == "" && p.Begin <= 1 {
			want = trailer["crc32"]
		}
		if want != "" {
			v, err := strconv.ParseUint(strings.TrimSpace(want), 16, 32)
			p.CRCOK = err == nil && uint32(v) == crc32.ChecksumIEEE(out)
		}
		if n, err := strconv.Atoi(trailer["size"]); err == nil && n != len(out) {
			p.CRCOK = false
		}
	}
	if p.Begin < 1 {
		p.Begin = 1
	}
	return p, nil
}

// yfields reads "=ybegin part=1 line=128 size=123 name=a b.rar": name runs to
// the end of the line.
func yfields(l []byte) map[string]string {
	s := strings.TrimRight(string(l), "\r\n")
	m := map[string]string{}
	if i := strings.Index(s, " name="); i >= 0 {
		m["name"] = strings.TrimSpace(s[i+6:])
		s = s[:i]
	}
	for _, f := range strings.Fields(s)[1:] {
		if k, v, ok := strings.Cut(f, "="); ok {
			m[k] = v
		}
	}
	return m
}

// NZB is a parsed NZB file.
type NZB struct {
	Files    []NZBFile
	Password string // from <meta type="password">
}

// NZBFile is one file of a release.
type NZBFile struct {
	Subject  string
	Name     string // from the subject; may be scrambled
	Groups   []string
	Segments []Segment
	Bytes    int64 // the sum of the segments' sizes (encoded, so a little over the file)
}

// Segment is one article.
type Segment struct {
	Number int
	Bytes  int64
	ID     string
}

// ParseNZB reads an NZB.
func ParseNZB(r io.Reader) (NZB, error) {
	var x struct {
		Head struct {
			Meta []struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"meta"`
		} `xml:"head"`
		Files []struct {
			Subject string   `xml:"subject,attr"`
			Groups  []string `xml:"groups>group"`
			Segs    []struct {
				Bytes  int64  `xml:"bytes,attr"`
				Number int    `xml:"number,attr"`
				ID     string `xml:",chardata"`
			} `xml:"segments>segment"`
		} `xml:"file"`
	}
	d := xml.NewDecoder(r)
	d.Strict = false
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := d.Decode(&x); err != nil {
		return NZB{}, fmt.Errorf("the NZB could not be read: %w", err)
	}
	var n NZB
	for _, m := range x.Head.Meta {
		if strings.EqualFold(m.Type, "password") {
			n.Password = strings.TrimSpace(m.Value)
		}
	}
	for _, f := range x.Files {
		nf := NZBFile{Subject: f.Subject, Groups: f.Groups, Name: subjectName(f.Subject)}
		seen := map[int]bool{}
		for _, s := range f.Segs {
			id := strings.Trim(strings.TrimSpace(s.ID), "<>")
			if id == "" || seen[s.Number] {
				continue
			}
			seen[s.Number] = true
			nf.Segments = append(nf.Segments, Segment{Number: s.Number, Bytes: s.Bytes, ID: id})
			nf.Bytes += s.Bytes
		}
		sort.Slice(nf.Segments, func(i, j int) bool { return nf.Segments[i].Number < nf.Segments[j].Number })
		if len(nf.Segments) > 0 {
			n.Files = append(n.Files, nf)
		}
	}
	if len(n.Files) == 0 {
		return NZB{}, errors.New("the NZB lists no files")
	}
	return n, nil
}

var quoted = regexp.MustCompile(`"([^"]+)"`)

// subjectName takes the file name from a subject such as
// `[01/15] - "Show.S01E01.part01.rar" yEnc (1/50)`.
func subjectName(s string) string {
	if m := quoted.FindStringSubmatch(s); m != nil {
		return safeName(m[1])
	}
	// No quotes: the word before "yEnc" that looks like a file.
	for _, w := range strings.Fields(s) {
		if strings.Contains(w, ".") && !strings.HasPrefix(w, "(") {
			return safeName(strings.Trim(w, "[]()"))
		}
	}
	return ""
}

// safeName keeps a name inside the job's folder.
func safeName(n string) string {
	n = strings.ReplaceAll(n, "\\", "/")
	n = path.Base(path.Clean("/" + n))
	n = strings.Map(func(r rune) rune {
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, n)
	if n == "/" || n == "." || n == ".." {
		return ""
	}
	return n
}

var repairVol = regexp.MustCompile(`(?i)\.vol\d+[+-]\d+\.par2$`)

// isRepairVolume says whether a file holds only par2 repair data (fetched
// only when a repair is needed).
func isRepairVolume(name string) bool { return repairVol.MatchString(name) }
