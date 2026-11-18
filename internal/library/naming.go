package library

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// Style is how the files in a show folder are named, read from the files
// already there, so a new file can be named the same way.
type Style struct {
	Marked bool `json:"marked"` // "Show - S01E05": the season is in the name; else "Show - 5"
	Pad    int  `json:"pad"`    // digits the episode number is padded to (1: no padding)
	Tags   bool `json:"tags"`   // names end with a tag such as "[SubsPlease 1080p]"
	Res    bool `json:"res"`    // names end with the resolution in parentheses: "(1080p)"
	Files  int  `json:"files"`  // video files the style was read from (0: a new folder)
}

var (
	styleMarked = regexp.MustCompile(`(?i)\bS(\d{1,2})E(\d{1,4})\b`)
	styleAbs    = regexp.MustCompile(` - (\d{1,4})(?:v\d)?(?:\s|\.|\[|\(|$)`)
	styleTag    = regexp.MustCompile(`\]\s*$`)
	styleRes    = regexp.MustCompile(`(?i)\(\d{3,4}p\)\s*$`)
	videoExts   = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".ts": true, ".webm": true, ".wmv": true, ".mov": true}
)

// IsVideo says whether a file name is a video by its extension.
func IsVideo(name string) bool { return videoExts[strings.ToLower(filepath.Ext(name))] }

// NameStyle reads one file name: whether it carries SxxEyy, the episode digits
// as written, and whether it ends with a tag. ok is false when the name shows
// no episode number in either form.
func NameStyle(name string) (marked bool, digits string, tags bool, ok bool) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	tags = styleTag.MatchString(stem)
	if styleRes.MatchString(stem) {
		tags = false
	}
	if m := styleMarked.FindStringSubmatch(stem); m != nil {
		return true, m[2], tags, true
	}
	if m := styleAbs.FindStringSubmatch(stem); m != nil {
		return false, m[1], tags, true
	}
	return false, "", tags, false
}

// FolderStyle reads a show folder's naming from its video files: the form most
// files use. Files is 0 for a folder with no readable files; FolderName then
// follows the release's own form (see ReleaseStyle).
func FolderStyle(showDir string) Style {
	var marked, abs, tagged, resd, padded, width, files int
	_ = filepath.WalkDir(showDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == showDir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if p != showDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !IsVideo(d.Name()) {
			return nil
		}
		m, digits, t, ok := NameStyle(d.Name())
		if !ok {
			return nil
		}
		files++
		if m {
			marked++
		} else {
			abs++
		}
		if t {
			tagged++
		}
		if styleRes.MatchString(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))) {
			resd++
		}
		if len(digits) > 1 && digits[0] == '0' {
			padded++
			width = max(width, len(digits))
		}
		return nil
	})
	st := Style{Pad: 1, Files: files}
	if files == 0 {
		return st
	}
	st.Marked = marked > abs
	st.Tags = tagged*2 > files
	st.Res = resd*2 > files
	if padded*2 > files || (st.Marked && padded > 0) {
		st.Pad = max(width, 2)
	}
	return st
}

// ReleaseStyle is the style for a show folder with no files yet: Rename.ps1's
// (what Clean gives): the release's own numbering, unpadded, with the
// resolution in parentheses when the release has one.
func ReleaseStyle(r Release) Style {
	return Style{Marked: r.Marked, Pad: 1, Res: r.Res != ""}
}

// unsafeName are characters a file name cannot carry on common shares.
var unsafeName = strings.NewReplacer("/", " ", "\\", " ", ":", " -", "*", "", "?", "", "\"", "'", "<", "", ">", "", "|", "")

// FolderName is the file name a release gets in a show folder: the folder's
// name as the title, the episode in the folder's form, and the group and
// resolution as a tag when the folder's files carry tags. A release that names
// a season past 1 keeps SxxEyy even in a folder of bare numbers, since its
// number counts from that season, not from the show's first episode.
func FolderName(folder string, st Style, r Release, ext string) string {
	if st.Files == 0 {
		st = ReleaseStyle(r)
	}
	title := strings.Join(strings.Fields(unsafeName.Replace(folder)), " ")
	pad := max(st.Pad, 1)
	var num string
	if st.Marked || (r.Marked && r.Season > 1) {
		num = fmt.Sprintf("S%02dE%0*d", r.Season, max(pad, 2), r.Episode)
	} else {
		num = fmt.Sprintf("%0*d", pad, r.Episode)
	}
	name := title + " - " + num
	if st.Res && r.Res != "" {
		name += " (" + r.Res + ")"
	} else if st.Tags {
		var tag []string
		if r.Group != "" {
			tag = append(tag, r.Group)
		}
		if r.Res != "" {
			tag = append(tag, r.Res)
		}
		if len(tag) > 0 {
			name += " [" + strings.Join(tag, " ") + "]"
		}
	}
	return name + strings.ToLower(ext)
}
