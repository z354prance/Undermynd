package web

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/config"
	"github.com/z354prance/undermynd/internal/mediaserver"
)

// Re-download or re-encode (Downloads › Tdarr › What Tdarr could still save).
// Every download goes through Tdarr anyway, so the question is only what
// Tdarr starts from. A file that is already someone's encode (a WEBRip, an
// HDTV capture, an x264 encode) loses quality again when converted, so it is
// better downloaded again from the WEB-DL or Blu-ray: that goes to Upgrades,
// which moves the title to the upgrade profile and searches (adding a movie
// Radarr lacks at its folder). A file that is already the source (an
// untouched WEB-DL, a remux), anime, and anything the arrs and the media
// server cannot name stay on re-encode.

var (
	encodeTag = regexp.MustCompile(`(?i)(^|[^a-z0-9])(web-?rip|hdtv|pdtv|sdtv|dsr|tv-?rip|dvd-?rip|bd-?rip|br-?rip|hd-?rip|xvid|divx|x264)([^a-z0-9]|$)`)
	sourceTag = regexp.MustCompile(`(?i)(^|[^a-z0-9])(web-?dl|remux|amzn|nf|dsnp|hmax|atvp|pcok|hulu)([^a-z0-9]|$)`)
	webTag    = regexp.MustCompile(`(?i)(^|[^a-z0-9])web([^a-z0-9]|$)`)
)

// sourceOf says what a file was made from: "encode", "source", or "" when
// neither its name nor the arr says. The arr's record (Radarr's quality
// source) is used when the name has no tags.
func sourceOf(name, arrSource string, remux bool) (string, string) {
	if m := encodeTag.FindStringSubmatch(name); m != nil {
		return "encode", tagName(m[2]) + " in its name"
	}
	if remux || strings.Contains(strings.ToLower(name), "remux") {
		return "source", "a remux"
	}
	if sourceTag.MatchString(name) || webTag.MatchString(name) {
		return "source", "a WEB-DL"
	}
	switch arrSource {
	case "webrip":
		return "encode", "Radarr: WEBRip"
	case "tv", "television":
		return "encode", "Radarr: HDTV"
	case "dvd":
		return "encode", "Radarr: DVD rip"
	case "bluray":
		return "encode", "Radarr: Blu-ray encode"
	case "webdl":
		return "source", "Radarr: WEB-DL"
	}
	return "", ""
}

// idIndex names library files: movies by their folder (Radarr) or file (the
// media server), series by their folder (Sonarr).
type idIndex struct {
	movies   map[string]arr.Entry // folder name -> Radarr movie
	series   map[string]arr.Entry // folder name -> Sonarr series
	server   map[string]mediaserver.Movie
	notes    []string
	upMovies bool
	upShows  bool
}

func folderKey(p string) string { return strings.ToLower(filepath.Base(filepath.Clean(p))) }

func fileKey(p string) string {
	p = filepath.ToSlash(p)
	return strings.ToLower(filepath.Base(filepath.Dir(p)) + "/" + filepath.Base(p))
}

func (s *Server) buildIDIndex(ctx context.Context, set config.Settings) *idIndex {
	ix := &idIndex{movies: map[string]arr.Entry{}, series: map[string]arr.Entry{}, server: map[string]mediaserver.Movie{}}
	at := set.Attention
	ix.upMovies = at.On && strings.TrimSpace(at.MovieProfile) != ""
	ix.upShows = at.On && strings.TrimSpace(at.TVProfile) != ""
	for _, c := range set.Categories {
		if (c.Kind != "radarr" && c.Kind != "sonarr") || c.ArrURL == "" {
			continue
		}
		cx, cancel := context.WithTimeout(ctx, 90*time.Second)
		es, err := arr.New(c.Kind, c.ArrURL, string(c.ArrAPIKey)).Entries(cx)
		cancel()
		if err != nil {
			ix.notes = append(ix.notes, fmt.Sprintf("%s (%s) could not be read: %v", c.Name, c.Kind, err))
			continue
		}
		for _, e := range es {
			if e.Path == "" {
				continue
			}
			if c.Kind == "radarr" {
				ix.movies[folderKey(e.Path)] = e
			} else {
				ix.series[folderKey(e.Path)] = e
			}
		}
	}
	if set.Playback.EmbyURL != "" && set.Playback.EmbyKey != "" {
		ms := mediaserver.Server{Kind: set.Access.Provider, URL: set.Playback.EmbyURL, Key: string(set.Playback.EmbyKey)}
		cx, cancel := context.WithTimeout(ctx, 90*time.Second)
		list, err := ms.Movies(cx)
		cancel()
		if err != nil {
			ix.notes = append(ix.notes, "the media server's movies could not be read: "+err.Error())
		}
		for _, m := range list {
			if m.Path != "" && m.TMDB != 0 {
				ix.server[fileKey(m.Path)] = m
			}
		}
	}
	return ix
}

// classify decides re-download or re-encode for one older-format file.
func (ix *idIndex) classify(f *potFile) {
	f.Way, f.Why, f.Kind = "reencode", "", ""
	if animeSegmentPath(f.Path) {
		f.Why = "anime: kept on re-encode"
		return
	}
	parts := strings.Split(filepath.ToSlash(f.Path), "/")
	name := filepath.Base(f.Path)
	// A movie: Radarr knows its folder, or the media server its file.
	if len(parts) >= 2 {
		if e, ok := ix.movies[strings.ToLower(parts[len(parts)-2])]; ok {
			f.Kind, f.TMDB, f.Title, f.Year, f.InArr = "movie", e.TMDB, e.Title, e.Year, true
			f.Source, f.Why = sourceOf(name+" "+e.Scene, e.Source, e.Remux)
		}
	}
	if f.Kind == "" {
		if m, ok := ix.server[fileKey(f.Path)]; ok {
			f.Kind, f.TMDB, f.Title, f.Year = "movie", m.TMDB, m.Title, m.Year
			f.Source, f.Why = sourceOf(name, "", false)
		}
	}
	// A series: Sonarr knows one of the folders it sits in.
	if f.Kind == "" {
		for i := len(parts) - 2; i >= 0; i-- {
			if e, ok := ix.series[strings.ToLower(parts[i])]; ok {
				if e.Anime {
					f.Why = "anime: kept on re-encode"
					return
				}
				f.Kind, f.TVDB, f.Title, f.Year, f.InArr = "show", e.TVDB, e.Title, e.Year, true
				f.Source, f.Why = sourceOf(name, "", false)
				break
			}
		}
	}
	switch {
	case f.Kind == "":
		f.Why = "not found in the arrs or the media server"
	case f.Source == "encode":
		f.Way = "redownload"
	case f.Source == "source":
		f.Why = "already the source (" + strings.TrimPrefix(f.Why, "Radarr: ") + "): re-encoding loses nothing more"
	default:
		f.Why = "what it was made from is not known"
	}
}

// animeSegmentPath: a folder named anime anywhere in the path.
func animeSegmentPath(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if strings.Contains(strings.ToLower(seg), "anime") {
			return true
		}
	}
	return false
}

// tagName writes a release tag the usual way.
func tagName(t string) string {
	k := strings.ToLower(strings.ReplaceAll(t, "-", ""))
	if n, ok := map[string]string{"webrip": "WEBRip", "hdtv": "HDTV", "pdtv": "PDTV", "sdtv": "SDTV", "dsr": "DSR", "tvrip": "TVRip",
		"dvdrip": "DVDRip", "bdrip": "BDRip", "brrip": "BRRip", "hdrip": "HDRip", "xvid": "XviD", "divx": "DivX", "x264": "x264"}[k]; ok {
		return n
	}
	return t
}
