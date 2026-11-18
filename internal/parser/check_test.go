package parser

import "testing"

func TestCompare(t *testing.T) {
	r := Compare([]Sample{
		{Title: "Show.S01E01E02.1080p.WEB.h264-GRP", Quality: "WEBDL-1080p", Season: 1, Episodes: []int{1, 2}},
		{Title: "Show.S02.1080p.BluRay.x264-GRP", Quality: "Bluray-1080p", Season: 2, Episodes: []int{1, 2, 3}},
		{Title: "[SubsPlease] Show - 1071 (1080p) [ABCDEF12].mkv", Quality: "HDTV-1080p", Season: 21, Episodes: []int{1071}, Absolute: []int{1071}},
		{Title: "Movie.2010.1080p.BluRay.REMUX.AVC-GRP", Quality: "Bluray-1080p Remux", Season: -1, Year: 2010},
		{Title: "Movie.2010.720p.WEB.h264-GRP", Quality: "WEBRip-720p", Season: -1, Year: 2010},
		{Title: "Show.S01E05.720p.HDTV.x264-GRP", Quality: "HDTV-720p", Season: 1, Episodes: []int{6}},
		{Title: "Movie.1999.1080p.BluRay.x264-GRP", Quality: "Bluray-1080p", Season: -1, Year: 2004},
	})
	if r.Total != 7 || r.Agree != 4 || r.ByField["quality"] != 1 || r.ByField["episodes"] != 1 || r.ByField["year"] != 1 {
		t.Fatalf("%+v", r)
	}
}
