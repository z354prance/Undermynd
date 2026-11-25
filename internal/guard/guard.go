// Package guard spots downloads that carry something other than media:
// programs and scripts (by name), and programs or archives dressed up as
// video or audio (by their first bytes). Fake releases that hide malware
// look like this on public trackers and usenet alike.
package guard

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// dangerous are file types no media release has a reason to carry.
var dangerous = map[string]bool{
	".exe": true, ".scr": true, ".com": true, ".pif": true, ".bat": true, ".cmd": true,
	".msi": true, ".msp": true, ".lnk": true, ".vbs": true, ".vbe": true,
	".js": true, ".jse": true, ".wsf": true, ".wsh": true, ".ps1": true, ".psm1": true,
	".hta": true, ".cpl": true, ".jar": true, ".dll": true, ".sys": true, ".reg": true,
	".scf": true, ".inf": true, ".application": true, ".gadget": true, ".appx": true,
	".msix": true, ".apk": true, ".dmg": true, ".pkg": true, ".app": true, ".deb": true,
	".rpm": true, ".sh": true, ".run": true, ".bin": true, ".elf": true,
	".docm": true, ".xlsm": true, ".pptm": true,
}

// media are the types whose contents are checked.
var media = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".m2ts": true,
	".wmv": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true, ".wav": true,
}

// Name says why a file name is not allowed, or "".
func Name(name string) string {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(name)))
	ext := filepath.Ext(base)
	if dangerous[ext] {
		return fmt.Sprintf("it contains a program or script (%s)", filepath.Base(name))
	}
	// "Movie.mkv .exe" style padding, or a right-to-left override that
	// makes "xyz‮vkm.exe" read as "xyzexe.mkv".
	if strings.ContainsRune(base, '‮') {
		return fmt.Sprintf("a file name hides its real type (%s)", filepath.Base(name))
	}
	return ""
}

// signatures of things that are never media.
var signatures = []struct {
	at   int
	sig  []byte
	what string
}{
	{0, []byte("MZ"), "a Windows program"},
	{0, []byte("\x7fELF"), "a Linux program"},
	{0, []byte{0xcf, 0xfa, 0xed, 0xfe}, "a Mac program"},
	{0, []byte{0xca, 0xfe, 0xba, 0xbe}, "a Mac or Java program"},
	{0, []byte("#!"), "a script"},
	{0, []byte("PK\x03\x04"), "a zip archive"},
	{0, []byte("Rar!\x1a\x07"), "a RAR archive"},
	{0, []byte("7z\xbc\xaf\x27\x1c"), "a 7-Zip archive"},
	{0, []byte("%PDF"), "a PDF"},
	{0, []byte("<html"), "a web page"},
	{0, []byte("<!DOCTYPE"), "a web page"},
	{0, []byte{0xd0, 0xcf, 0x11, 0xe0}, "an Office document or installer"},
}

// Content says why a media file's contents are not media, or "".
func Content(path string) string {
	if !media[strings.ToLower(filepath.Ext(path))] {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	trim := bytes.TrimLeft(head, " \t\r\n\xef\xbb\xbf")
	for _, s := range signatures {
		h := head
		if s.sig[0] == '<' {
			h = bytes.ToLower(trim)
			if !bytes.HasPrefix(h, bytes.ToLower(s.sig)) {
				continue
			}
			return fmt.Sprintf("%s is really %s", filepath.Base(path), s.what)
		}
		if len(h) >= s.at+len(s.sig) && bytes.Equal(h[s.at:s.at+len(s.sig)], s.sig) {
			return fmt.Sprintf("%s is really %s", filepath.Base(path), s.what)
		}
	}
	return ""
}
