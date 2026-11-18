package parser

import (
	"fmt"
	"strings"
	"testing"
)

type want struct {
	title   string
	year    int
	seasons string // "1" or "1,2"
	eps     string // "1,2"
	abs     string
	full    bool
	quality string
	group   string
	extra   string // checks of the form key=value;key=value
}

func ints(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ",")
}

var corpus = map[string]want{
	// Movies, scene style.
	"The.Matrix.1999.1080p.BluRay.x264-SPARKS":                             {title: "The Matrix", year: 1999, quality: "Bluray-1080p", group: "SPARKS", extra: "codec=x264"},
	"Blade.Runner.2049.2017.2160p.UHD.BluRay.REMUX.HDR.HEVC.Atmos-EPSiLON": {title: "Blade Runner 2049", year: 2017, quality: "Remux-2160p", group: "EPSiLON", extra: "codec=x265;hdr=HDR;atmos=true"},
	"2001.A.Space.Odyssey.1968.1080p.BluRay.x264-AMIABLE":                  {title: "2001 A Space Odyssey", year: 1968, quality: "Bluray-1080p", group: "AMIABLE"},
	"1917.2019.2160p.WEB-DL.DDP5.1.Atmos.HDR.HEVC-EVO":                     {title: "1917", year: 2019, quality: "WEBDL-2160p", group: "EVO", extra: "audio=DD+;channels=5.1;atmos=true"},
	"Dune.Part.Two.2024.1080p.AMZN.WEB-DL.DDP5.1.H.264-FLUX":               {title: "Dune Part Two", year: 2024, quality: "WEBDL-1080p", group: "FLUX", extra: "service=AMZN;codec=x264"},
	"Oppenheimer.2023.IMAX.2160p.WEB-DL.DDP5.1.DV.HDR.H.265-FLUX":          {title: "Oppenheimer", year: 2023, quality: "WEBDL-2160p", group: "FLUX", extra: "edition=IMAX;hdr=DV,HDR"},
	"Aliens.1986.Special.Edition.1080p.BluRay.x264-GROUP":                  {title: "Aliens", year: 1986, quality: "Bluray-1080p", group: "GROUP", extra: "edition=Special Edition"},
	"Blade.Runner.1982.The.Final.Cut.REMASTERED.1080p.BluRay.x264-PSYCHD":  {title: "Blade Runner", year: 1982, quality: "Bluray-1080p", group: "PSYCHD", extra: "edition=Final Cut"},
	"The.Lord.of.the.Rings.2001.EXTENDED.720p.BluRay.x264-SiNNERS":         {title: "The Lord of the Rings", year: 2001, quality: "Bluray-720p", group: "SiNNERS", extra: "edition=Extended"},
	"Heat.1995.Directors.Cut.1080p.BluRay.DTS-HD.MA.5.1.x264-FGT":          {title: "Heat", year: 1995, quality: "Bluray-1080p", group: "FGT", extra: "edition=Director's Cut;audio=DTS-HD MA;channels=5.1"},
	"Some.Movie.2020.720p.WEBRip.x264.AAC-YTS.MX":                          {title: "Some Movie", year: 2020, quality: "WEBRip-720p", group: "YTS.MX"},
	"Some Movie (2021) [1080p] [WEBRip] [5.1] [YTS.MX]":                    {title: "Some Movie", year: 2021, quality: "WEBRip-1080p", extra: "channels=5.1"},
	"Movie.Title.2019.HDCAM.x264-GROUP":                                    {title: "Movie Title", year: 2019, quality: "CAM", group: "GROUP"},
	"Movie.Title.2019.HDTS.x264-GROUP":                                     {title: "Movie Title", year: 2019, quality: "TELESYNC", group: "GROUP"},
	"Movie.Title.2019.DVDSCR.XviD-GROUP":                                   {title: "Movie Title", year: 2019, quality: "DVDSCR", group: "GROUP"},
	"Movie.Title.2004.DVDRip.XviD-GROUP":                                   {title: "Movie Title", year: 2004, quality: "DVD", group: "GROUP", extra: "codec=XviD"},
	"Movie.Title.2010.COMPLETE.BLURAY-GROUP":                               {title: "Movie Title", year: 2010, quality: "BR-DISK", group: "GROUP"},
	"Movie.Title.2010.1080p.BluRay.AVC.DTS-HD.MA.5.1-GROUP":                {title: "Movie Title", year: 2010, quality: "BR-DISK", group: "GROUP"},
	"Movie.Title.2010.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GROUP":          {title: "Movie Title", year: 2010, quality: "Remux-1080p", group: "GROUP"},
	"Movie.Title.2010.BDRip.x264-GROUP":                                    {title: "Movie Title", year: 2010, quality: "Bluray-480p", group: "GROUP"},
	"Movie.Title.2010.PROPER.1080p.BluRay.x264-GROUP":                      {title: "Movie Title", year: 2010, quality: "Bluray-1080p", group: "GROUP", extra: "version=2;proper=true"},
	"Movie.Title.2010.REPACK.1080p.WEB.H264-GROUP":                         {title: "Movie Title", year: 2010, quality: "WEBDL-1080p", group: "GROUP", extra: "version=2;repack=true"},
	"Movie.Title.2010.MULTi.1080p.BluRay.x264-GROUP":                       {title: "Movie Title", year: 2010, quality: "Bluray-1080p", group: "GROUP", extra: "multi=true"},
	"Movie.Title.2010.FRENCH.1080p.BluRay.x264-GROUP":                      {title: "Movie Title", year: 2010, quality: "Bluray-1080p", group: "GROUP", extra: "lang=French"},
	"Movie.Title.2010.GERMAN.DL.1080p.BluRay.x264-GROUP":                   {title: "Movie Title", year: 2010, quality: "Bluray-1080p", group: "GROUP", extra: "lang=German"},
	"Movie Title 2010 1080p BluRay x265 10bit-Tigole":                      {title: "Movie Title", year: 2010, quality: "Bluray-1080p", group: "Tigole", extra: "bits=10;codec=x265"},
	"Movie.Title.2010.1080p.BluRay.x264-GROUP.mkv":                         {title: "Movie Title", year: 2010, quality: "Bluray-1080p", group: "GROUP"},
	"Movie.Title.2010.1080p.BluRay.x264-GROUP[rarbg]":                      {title: "Movie Title", year: 2010, quality: "Bluray-1080p", group: "GROUP"},
	"www.Torrenting.com - Movie.Title.2010.1080p.WEBRip.x264-GROUP":        {title: "Movie Title", year: 2010, quality: "WEBRip-1080p", group: "GROUP"},
	"Movie.Title.2010.720p.HDTV.x264-GROUP":                                {title: "Movie Title", year: 2010, quality: "HDTV-720p", group: "GROUP"},
	"2012.2009.1080p.BluRay.x264-GROUP":                                    {title: "2012", year: 2009, quality: "Bluray-1080p", group: "GROUP"},
	"Movie.Title.2010.1080p.WEB-DL.DD5.1.H264-GROUP-Obfuscated":            {title: "Movie Title", year: 2010, quality: "WEBDL-1080p", group: "GROUP", extra: "audio=DD;channels=5.1"},

	// TV, scene style.
	"The.Last.of.Us.S01E01.1080p.WEB.H264-CAKES":                                             {title: "The Last of Us", seasons: "1", eps: "1", quality: "WEBDL-1080p", group: "CAKES"},
	"The.Last.of.Us.S01E01.When.Youre.Lost.2160p.HMAX.WEB-DL.DDP5.1.Atmos.DV.HDR.H.265-FLUX": {title: "The Last of Us", seasons: "1", eps: "1", quality: "WEBDL-2160p", group: "FLUX", extra: "service=HMAX"},
	"Severance.S02E03.720p.ATVP.WEB-DL.DDP5.1.H.264-NTb":                                     {title: "Severance", seasons: "2", eps: "3", quality: "WEBDL-720p", group: "NTb", extra: "service=ATVP"},
	"Show.Name.S01E01E02.720p.HDTV.x264-GROUP":                                               {title: "Show Name", seasons: "1", eps: "1,2", quality: "HDTV-720p", group: "GROUP"},
	"Show.Name.S01E01-E03.1080p.WEB-DL-GROUP":                                                {title: "Show Name", seasons: "1", eps: "1,2,3", quality: "WEBDL-1080p", group: "GROUP"},
	"Show.Name.S01E01-03.1080p.WEB-DL-GROUP":                                                 {title: "Show Name", seasons: "1", eps: "1,2,3", quality: "WEBDL-1080p", group: "GROUP"},
	"Show Name - 1x05 - Episode Title":                                                       {title: "Show Name", seasons: "1", eps: "5", quality: "Unknown"},
	"Show.Name.S03.1080p.BluRay.x264-GROUP":                                                  {title: "Show Name", seasons: "3", full: true, quality: "Bluray-1080p", group: "GROUP"},
	"Show.Name.Season.2.720p.WEB-DL-GROUP":                                                   {title: "Show Name", seasons: "2", full: true, quality: "WEBDL-720p", group: "GROUP"},
	"Show.Name.S01-S03.1080p.BluRay.x265-GROUP":                                              {title: "Show Name", seasons: "1,2,3", full: true, quality: "Bluray-1080p", group: "GROUP"},
	"Show Name Complete Series 1080p BluRay x264-GROUP":                                      {title: "Show Name", full: true, quality: "Bluray-1080p", group: "GROUP"},
	"The.Daily.Show.2024.03.14.Guest.Name.720p.WEB.h264-EDITH":                               {title: "The Daily Show", extra: "daily=2024-03-14", quality: "WEBDL-720p", group: "EDITH"},
	"Show.Name.2019.S02E05.1080p.WEB.h264-GROUP":                                             {title: "Show Name", year: 2019, seasons: "2", eps: "5", quality: "WEBDL-1080p", group: "GROUP"},
	"Show.Name.S00E01.Special.720p.WEB-DL-GROUP":                                             {title: "Show Name", seasons: "0", eps: "1", quality: "WEBDL-720p", group: "GROUP", extra: "special=true"},
	"Show.Name.S01E05.REPACK.1080p.WEB.H264-GROUP":                                           {title: "Show Name", seasons: "1", eps: "5", quality: "WEBDL-1080p", group: "GROUP", extra: "version=2"},
	"Show.Name.S01E05.PROPER.REAL.720p.HDTV.x264-GROUP":                                      {title: "Show Name", seasons: "1", eps: "5", quality: "HDTV-720p", group: "GROUP", extra: "real=1"},
	"Show.Name.S01E05.HDTV.x264-LOL":                                                         {title: "Show Name", seasons: "1", eps: "5", quality: "SDTV", group: "LOL"},
	"Show.Name.S01E05.XviD-LOL":                                                              {title: "Show Name", seasons: "1", eps: "5", quality: "SDTV", group: "LOL"},
	"Show.Name.S01E05.1080p.WEBRip.x264-GROUP":                                               {title: "Show Name", seasons: "1", eps: "5", quality: "WEBRip-1080p", group: "GROUP"},
	"Show.Name.S01E05.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GROUP":                            {title: "Show Name", seasons: "1", eps: "5", quality: "Remux-1080p", group: "GROUP"},
	"Show.Name.S01E05.480p.WEB-DL-GROUP":                                                     {title: "Show Name", seasons: "1", eps: "5", quality: "WEBDL-480p", group: "GROUP"},
	"Show.Name.S01E05.DVDRip.x264-GROUP":                                                     {title: "Show Name", seasons: "1", eps: "5", quality: "DVD", group: "GROUP"},
	"Show.Name.US.S01E05.720p.HDTV.x264-GROUP":                                               {title: "Show Name US", seasons: "1", eps: "5", quality: "HDTV-720p", group: "GROUP"},
	"Marvels.Agents.of.S.H.I.E.L.D.S05E01.720p.HDTV.x264-AVS":                                {title: "Marvels Agents of S H I E L D", seasons: "5", eps: "1", quality: "HDTV-720p", group: "AVS"},
	"Show Name S01E05 1080p NF WEB-DL DDP5.1 x264-GROUP":                                     {title: "Show Name", seasons: "1", eps: "5", quality: "WEBDL-1080p", group: "GROUP", extra: "service=NF"},
	"Show.Name.S2024E05.1080p.WEB.h264-GROUP":                                                {title: "Show Name", seasons: "2024", eps: "5", quality: "WEBDL-1080p", group: "GROUP"},
	"Show.Name.S01E05.1080p.DSNP.WEB-DL.DDP5.1.H.264-GROUP [eztv]":                           {title: "Show Name", seasons: "1", eps: "5", quality: "WEBDL-1080p", group: "GROUP", extra: "service=DSNP"},
	"Show.Name.S01E05.2160p.WEB.H265-GROUP":                                                  {title: "Show Name", seasons: "1", eps: "5", quality: "WEBDL-2160p", group: "GROUP", extra: "codec=x265"},

	// Anime.
	"[SubsPlease] Sousou no Frieren - 28 (1080p) [1A2B3C4D].mkv":                   {title: "Sousou no Frieren", abs: "28", quality: "HDTV-1080p", group: "SubsPlease", extra: "hash=1A2B3C4D"},
	"[SubsPlease] One Piece - 1071 (1080p) [ABCDEF12].mkv":                         {title: "One Piece", abs: "1071", quality: "HDTV-1080p", group: "SubsPlease"},
	"[SubsPlease] Dungeon Meshi - 05v2 (720p) [ABCDEF12].mkv":                      {title: "Dungeon Meshi", abs: "5", quality: "HDTV-720p", group: "SubsPlease", extra: "version=2"},
	"[Erai-raws] Shingeki no Kyojin - 01 [1080p][Multiple Subtitle].mkv":           {title: "Shingeki no Kyojin", abs: "1", quality: "HDTV-1080p", group: "Erai-raws"},
	"[Judas] Boku no Hero Academia S5 - 02 [1080p][HEVC x265 10bit][Eng-Subs].mkv": {title: "Boku no Hero Academia", seasons: "5", eps: "2", quality: "HDTV-1080p", group: "Judas", extra: "codec=x265;bits=10"},
	"[Trix] Ao no Hako S01E07 (1080p AV1 E-AC3)[Multi Subs].mkv":                   {title: "Ao no Hako", seasons: "1", eps: "7", quality: "HDTV-1080p", group: "Trix", extra: "codec=AV1;audio=DD+"},
	"[Some-Group] Show Name 2nd Season - 05 [1080p].mkv":                           {title: "Show Name", seasons: "2", eps: "5", quality: "HDTV-1080p", group: "Some-Group"},
	"[Group] Show Name - 01-12 (BD 1080p) [Batch]":                                 {title: "Show Name", abs: "1,2,3,4,5,6,7,8,9,10,11,12", quality: "Bluray-1080p", group: "Group"},
	"[Group] Show Name (2019) - 03 [WEB 1080p]":                                    {title: "Show Name", year: 2019, abs: "3", quality: "WEBDL-1080p", group: "Group"},
	"Show.Name.S01E05.1080p.CR.WEB-DL.AAC2.0.H.264-VARYG":                          {title: "Show Name", seasons: "1", eps: "5", quality: "WEBDL-1080p", group: "VARYG", extra: "service=CR;audio=AAC;channels=2.0"},
}

func check(t *testing.T, name string, w want, got Info) {
	t.Helper()
	bad := func(field string, g, e any) { t.Errorf("%s\n  %s: got %v, want %v", name, field, g, e) }
	if got.Title != w.title {
		bad("title", got.Title, w.title)
	}
	if got.Year != w.year {
		bad("year", got.Year, w.year)
	}
	if ints(got.Seasons) != w.seasons {
		bad("seasons", ints(got.Seasons), w.seasons)
	}
	if ints(got.Episodes) != w.eps {
		bad("episodes", ints(got.Episodes), w.eps)
	}
	if ints(got.Absolute) != w.abs {
		bad("absolute", ints(got.Absolute), w.abs)
	}
	if got.FullSeason != w.full {
		bad("full season", got.FullSeason, w.full)
	}
	if got.Quality.Name() != w.quality {
		bad("quality", got.Quality.Name(), w.quality)
	}
	if got.Group != w.group {
		bad("group", got.Group, w.group)
	}
	if w.extra == "" {
		return
	}
	for _, kv := range strings.Split(w.extra, ";") {
		k, v, _ := strings.Cut(kv, "=")
		var g string
		switch k {
		case "codec":
			g = got.Codec
		case "bits":
			g = fmt.Sprint(got.Bits)
		case "hdr":
			g = strings.Join(got.HDR, ",")
		case "audio":
			g = strings.Join(got.Audio, ",")
		case "channels":
			g = got.Channels
		case "atmos":
			g = fmt.Sprint(got.Atmos)
		case "service":
			g = got.Service
		case "edition":
			g = got.Edition
		case "version":
			g = fmt.Sprint(got.Quality.Version)
		case "proper":
			g = fmt.Sprint(got.Quality.Proper)
		case "repack":
			g = fmt.Sprint(got.Quality.Repack)
		case "real":
			g = fmt.Sprint(got.Quality.Real)
		case "multi":
			g = fmt.Sprint(got.Multi)
		case "lang":
			g = strings.Join(got.Languages, ",")
		case "daily":
			g = got.AirDate
		case "special":
			g = fmt.Sprint(got.Special)
		case "hash":
			g = got.Hash
		}
		if g != v {
			bad(k, g, v)
		}
	}
}

func TestCorpus(t *testing.T) {
	for name, w := range corpus {
		check(t, name, w, Parse(name))
	}
}

func TestCanonical(t *testing.T) {
	for in, out := range map[string]string{"Bluray-1080p Remux": "Remux-1080p", "Bluray-2160p Remux": "Remux-2160p", "WEBDL-1080p": "WEBDL-1080p"} {
		if g := Canonical(in); g != out {
			t.Errorf("%s: %s", in, g)
		}
	}
}

// Every prefix and suffix of every corpus name parses without a panic.
func TestNoPanics(t *testing.T) {
	for name := range corpus {
		for i := 0; i <= len(name); i++ {
			Parse(name[:i])
			Parse(name[i:])
		}
	}
	for _, s := range []string{"", " ", "-", "[", "[]", "()", "S01", "E01", "2020", "- 01", "[G] - 01", "1x", "x264", "1080p"} {
		Parse(s)
	}
}

func FuzzParse(f *testing.F) {
	for name := range corpus {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, s string) { Parse(s) })
}
