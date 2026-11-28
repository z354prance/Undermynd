package web

import (
	"testing"

	"github.com/z354prance/undermynd/internal/arr"
	"github.com/z354prance/undermynd/internal/mediaserver"
)

func TestSourceOf(t *testing.T) {
	cases := []struct {
		name, arrSrc string
		remux        bool
		want         string
	}{
		{"Chicago.Fire.S08E16.1080p.AMZN.WEB-DL.DDP5.1.H.264-KiNGS.mkv", "", false, "source"},
		{"Movie.2010.1080p.BluRay.x264-SPARKS.mkv", "", false, "encode"},
		{"Movie.2010.720p.BRRip.XviD.avi", "", false, "encode"},
		{"Show.S01E01.720p.HDTV.x264-KILLERS.mkv", "", false, "encode"},
		{"Show.S02E03.1080p.WEBRip.x264-ION10.mp4", "", false, "encode"},
		{"Show.S02E03.1080p.WEB.H264-GGEZ.mkv", "", false, "source"},
		{"Movie.2001.1080p.BluRay.REMUX.AVC.DTS-HD.MA-FGT.mkv", "", false, "source"},
		{"Movie (2004).mkv", "webrip", false, "encode"},
		{"Movie (2004).mkv", "bluray", true, "source"},
		{"Movie (2004).mkv", "bluray", false, "encode"},
		{"Movie (2004).mkv", "", false, ""},
		{"Netflix Docs S01E01.mkv", "", false, ""}, // "nf" only as its own word
	}
	for _, c := range cases {
		if got, _ := sourceOf(c.name, c.arrSrc, c.remux); got != c.want {
			t.Errorf("%s (%s): %q, want %q", c.name, c.arrSrc, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	ix := &idIndex{
		movies: map[string]arr.Entry{"dune (2021)": {TMDB: 438631, Title: "Dune", Year: 2021, Source: "webrip"}},
		series: map[string]arr.Entry{"suits": {TVDB: 247808, Title: "Suits"}, "frieren": {TVDB: 1, Anime: true}},
		server: map[string]mediaserver.Movie{"heat (1995)/heat.mkv": {TMDB: 949, Title: "Heat", Year: 1995}},
	}
	cases := []struct{ path, way, kind string }{
		{"/media/Movies/Dune (2021)/Dune.mkv", "redownload", "movie"},
		{"/media/TV/Suits/Season 1/Suits.S01E01.720p.HDTV.x264-ASAP.mkv", "redownload", "show"},
		{"/media/TV/Suits/Season 2/Suits.S02E01.1080p.WEB-DL.H264.mkv", "reencode", "show"},
		{"/media/TV/Frieren/Frieren - 01.mkv", "reencode", ""},
		{"/media/Anime/Show/Show - 01 [x264].mkv", "reencode", ""},
		{"/media/Movies/Heat (1995)/Heat.mkv", "reencode", "movie"}, // not known: no tags, not in Radarr
		{"/media/Movies/Unknown (1990)/x264 file.mkv", "reencode", ""},
	}
	for _, c := range cases {
		f := potFile{Path: c.path}
		ix.classify(&f)
		if f.Way != c.way || f.Kind != c.kind {
			t.Errorf("%s: %s %s (%s), want %s %s", c.path, f.Way, f.Kind, f.Why, c.way, c.kind)
		}
	}
}
