// Package parser reads release names the way Radarr and Sonarr do: title, year,
// seasons and episodes (including multi-episode, season packs, daily and anime
// absolute numbers), quality (source, resolution, remux, proper and repack),
// codec, audio, HDR, languages, editions, streaming service and release group.
//
// It never touches the network or the disk. The quality names it gives are the
// arrs' own ("WEBDL-1080p", "Bluray-2160p", "Remux-1080p"), so a name can be
// compared with what an arr decided for the same release.
package parser

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Source is where a release came from.
type Source string

const (
	SourceUnknown   Source = ""
	SourceCAM       Source = "cam"
	SourceTelesync  Source = "telesync"
	SourceTelecine  Source = "telecine"
	SourceWorkprint Source = "workprint"
	SourceScreener  Source = "dvdscr"
	SourceTV        Source = "tv"
	SourceWEBDL     Source = "webdl"
	SourceWEBRip    Source = "webrip"
	SourceDVD       Source = "dvd"
	SourceBluray    Source = "bluray"
)

// Modifier sets a release apart within its source.
type Modifier string

const (
	ModNone     Modifier = ""
	ModRemux    Modifier = "remux"
	ModBRDisk   Modifier = "brdisk"
	ModRawHD    Modifier = "rawhd"
	ModRegional Modifier = "regional"
)

// Quality is what the arrs call a release's quality, plus its revision.
type Quality struct {
	Source     Source
	Resolution int // 2160, 1080, 720, 576, 480 or 0
	Modifier   Modifier
	Version    int // 1, or 2+ for PROPER, REPACK and anime v2
	Real       int // count of REAL tags
	Repack     bool
	Proper     bool
}

// Name is the quality as Radarr names it ("Remux-1080p"); Sonarr's names are
// the same except remuxes ("Bluray-1080p Remux"), which Canonical treats as equal.
func (q Quality) Name() string {
	r := q.Resolution
	switch q.Source {
	case SourceCAM:
		return "CAM"
	case SourceTelesync:
		return "TELESYNC"
	case SourceTelecine:
		return "TELECINE"
	case SourceWorkprint:
		return "WORKPRINT"
	case SourceScreener:
		return "DVDSCR"
	case SourceDVD:
		if q.Modifier == ModRegional {
			return "REGIONAL"
		}
		return "DVD"
	case SourceTV:
		if q.Modifier == ModRawHD {
			return "Raw-HD"
		}
		switch {
		case r >= 2160:
			return "HDTV-2160p"
		case r >= 1080:
			return "HDTV-1080p"
		case r >= 720:
			return "HDTV-720p"
		}
		return "SDTV"
	case SourceWEBDL, SourceWEBRip:
		p := "WEBDL"
		if q.Source == SourceWEBRip {
			p = "WEBRip"
		}
		switch {
		case r >= 2160:
			return p + "-2160p"
		case r >= 1080:
			return p + "-1080p"
		case r >= 720:
			return p + "-720p"
		}
		return p + "-480p"
	case SourceBluray:
		if q.Modifier == ModBRDisk {
			return "BR-DISK"
		}
		if q.Modifier == ModRemux {
			if r >= 2160 {
				return "Remux-2160p"
			}
			return "Remux-1080p"
		}
		switch {
		case r >= 2160:
			return "Bluray-2160p"
		case r >= 1080:
			return "Bluray-1080p"
		case r == 576:
			return "Bluray-576p"
		case r > 0 && r < 720:
			return "Bluray-480p"
		}
		return "Bluray-720p"
	}
	switch {
	case r >= 2160:
		return "HDTV-2160p"
	case r >= 1080:
		return "HDTV-1080p"
	case r >= 720:
		return "HDTV-720p"
	case r > 0:
		return "SDTV"
	}
	return "Unknown"
}

// Canonical makes an arr's quality name comparable with Name: Sonarr's
// "Bluray-1080p Remux" is Radarr's "Remux-1080p".
func Canonical(name string) string {
	n := strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(n), " remux") {
		res := strings.TrimSuffix(strings.SplitN(n, "-", 2)[len(strings.SplitN(n, "-", 2))-1], " Remux")
		res = strings.TrimSuffix(res, " remux")
		return "Remux-" + res
	}
	return n
}

// Info is everything read from one release name.
type Info struct {
	Name  string // the name as given
	Title string // the movie or show title, cleaned
	Year  int    // 0 when the name has none

	Seasons      []int // the season(s) named; several for a multi-season pack
	Episodes     []int // episode numbers in the season, in order
	Absolute     []int // anime absolute numbers, when the name has no season
	FullSeason   bool  // a whole season (or several), no episode numbers
	Special      bool  // season 0 or an OVA/special marker
	AirDate      string
	Daily        bool
	SeasonMarked bool // the name said which season

	Quality  Quality
	Codec    string // x264, x265, AV1, XviD, VC-1, MPEG2
	Bits     int    // 10 when the name says 10bit
	HDR      []string
	Audio    []string
	Channels string
	Atmos    bool

	Languages []string // English when the name names none
	Multi     bool
	Edition   string
	Service   string // AMZN, NF, DSNP ...
	Group     string
	Hash      string
	Hardsub   bool
}

// IsTV reports whether the name reads as an episode or season.
func (i Info) IsTV() bool {
	return len(i.Episodes) > 0 || len(i.Absolute) > 0 || i.FullSeason || i.Daily
}

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".ts": true, ".wmv": true,
	".mov": true, ".mpg": true, ".mpeg": true, ".webm": true, ".m2ts": true, ".iso": true, ".nzb": true, ".torrent": true,
	".flv": true, ".divx": true, ".vob": true, ".ogm": true}

func rx(s string) *regexp.Regexp { return regexp.MustCompile(s) }

// b wraps a pattern in separator boundaries: start or a non-alphanumeric each side.
func b(p string) *regexp.Regexp { return rx(`(?i)(?:^|[^a-z0-9])(?:` + p + `)(?:$|[^a-z0-9])`) }

var (
	leadGroup = rx(`^\s*\[([^\]]+)\]\s*`)
	hashTag   = rx(`[\[\(]([0-9A-Fa-f]{8})[\]\)]`)
	trailJunk = rx(`(?i)(?:\s*\[(?:rarbg|eztv(?:\.re)?|ettv|tgx|rartv|ion10|xvid|publichd|eztv\.io|prof|torrentgalaxy)\])+\s*$`)
	webSite   = rx(`(?i)^\s*(?:www\.[a-z0-9-]+\.[a-z]{2,4}|[a-z0-9-]+\.(?:com|org|net|to))\s*[-_ ]\s*`)
	postJunk  = rx(`(?i)[-_. ](?:obfuscated|postbot|scrambled|xpost|rakuv\w*|buymore|asrequested|sample|AsRequested|WhiteRev|BUYMORE|Rakuvfinhel)$`)

	// Episodes and seasons. Each finds where the title ends.
	sxe       = rx(`(?i)(?:^|[^a-z0-9])S(\d{1,4})[ ._-]?E(\d{1,4})((?:[ ._-]{0,2}(?:-|E|EP|-E)\s?\d{1,4}(?:[^a-z0-9p]|$))*)`)
	nxm       = rx(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})((?:[ ._-]{0,2}(?:-|x|-x)\d{2,3})*)(?:[^a-z0-9]|$)`)
	seasonTo  = rx(`(?i)(?:^|[^a-z0-9])S(?:eason)?[ ._]?(\d{1,2})[ ._]?(?:-|to)[ ._]?S?(?:eason)?[ ._]?(\d{1,2})(?:[^a-z0-9]|$)`)
	seasonS   = rx(`(?i)(?:^|[^a-z0-9])S(\d{1,2})(?:[^a-z0-9e]|$)`)
	seasonW   = rx(`(?i)(?:^|[^a-z0-9])Season[ ._-]?(\d{1,2})(?:[^a-z0-9]|$)`)
	seasonOrd = rx(`(?i)(?:^|[^a-z0-9])(\d{1,2})(?:st|nd|rd|th)[ ._-]Season(?:[^a-z0-9]|$)`)
	complete  = rx(`(?i)(?:^|[^a-z0-9])(?:complete[ ._-](?:series|season|collection)|the[ ._]complete)(?:[^a-z0-9]|$)`)
	daily     = rx(`(?:^|[^0-9])((?:19|20)\d\d)[ ._-](\d\d)[ ._-](\d\d)(?:[^0-9]|$)`)
	absDash   = rx(`(?i)\s-\s(?:EP?\.?\s?)?(\d{1,4})(?:v(\d))?(?:\s?-\s?(\d{1,4})(?:v\d)?)?(?:\s|\(|\[|$)`)
	absBare   = rx(`(?i)\s(?:EP?\.?\s?)(\d{1,4})(?:v(\d))?(?:\s|\(|\[|$)`)
	special   = b(`special|specials|ova|oav|ona|sp\d{1,2}`)

	resRe = []struct {
		re *regexp.Regexp
		n  int
	}{
		{b(`2160p|4k|uhd|3840x2160`), 2160},
		{b(`1080[pi]|1920x1080|fhd|1080`), 1080},
		{b(`720p|1280x720|720`), 720},
		{b(`576[pi]`), 576},
		{b(`480[pi]|640x480|848x480|540p|360p`), 480},
	}

	remux   = b(`remux|bd[ ._-]?remux`)
	brdisk  = b(`complete[ ._-]?(?:uhd[ ._-]?)?blu[ ._-]?ray|bdmv|avc[ ._-]?(?:dts|truehd)|mpeg-?2[ ._-]?(?:dts|truehd)|iso|bd(?:25|50|66|100)|untouched`)
	bluray  = b(`blu[ ._-]?ray|bdrip|brrip|bd|bdiso|bluray\d{3,4}p|hddvd|uhd[ ._-]?bluray|bd\d{3,4}p`)
	webdl   = b(`web[ ._-]?dl|webdl|amzn[ ._-]?web|nf[ ._-]?web|web(?:[ ._-]?h\.?26[45])?|itunes(?:hd)?|web[ ._-]?hd|webhd|vod|amazonhd`)
	webrip  = b(`web[ ._-]?rip|webrip|webmux|web[ ._-]?cap`)
	hdtv    = b(`hdtv|pdtv|sdtv|tvrip|dsr|dsrip|satrip|dthrip|dvbrip|hdtvrip|ppv|tvrip|hdrip|uhdtv`)
	rawhd   = b(`raw[ ._-]?hd|mpeg[ ._-]?2[ ._-]?hdtv`)
	dvd     = b(`dvd(?:rip|5|9|r)?|ntsc|pal|xvidvd|dvd-?r|video_ts`)
	scr     = b(`dvdscr|dvd[ ._-]?screener|screener|scr|bdscr|webscreener`)
	camRe   = b(`cam|camrip|hdcam|hd[ ._-]?cam|cam[ ._-]?rip`)
	tsRe    = b(`ts|telesync|hdts|hd[ ._-]?ts|pdvd|predvdrip`)
	tcRe    = b(`tc|telecine|hdtc|hd[ ._-]?tc`)
	wpRe    = b(`workprint|wp`)
	regRe   = b(`r[1-6]|regional`)
	proper  = b(`proper`)
	repack  = b(`repack|rerip`)
	realRe  = rx(`(?:^|[^A-Za-z0-9])REAL(?:[^A-Za-z0-9]|$)`)
	version = rx(`(?i)(?:\d|[^a-z0-9])v([2-9])(?:[^a-z0-9]|$)`)

	codecs = []struct {
		re   *regexp.Regexp
		name string
	}{
		{b(`x\.?265|h\.?265|hevc`), "x265"},
		{b(`x\.?264|h\.?264|avc`), "x264"},
		{b(`av1`), "AV1"},
		{b(`xvid|divx`), "XviD"},
		{b(`vc-?1`), "VC-1"},
		{b(`mpeg-?2`), "MPEG2"},
		{b(`vp9`), "VP9"},
	}
	tenBit = b(`10[ ._-]?bit|hi10p?|10bpp`)

	hdrs = []struct {
		re   *regexp.Regexp
		name string
	}{
		{b(`dv|dovi|dolby[ ._-]?vision`), "DV"},
		{b(`hdr10\+|hdr10plus|hdr10p`), "HDR10+"},
		{b(`hdr10`), "HDR10"},
		{b(`hdr`), "HDR"},
		{b(`hlg`), "HLG"},
		{b(`sdr`), "SDR"},
	}

	audios = []struct {
		re   *regexp.Regexp
		name string
	}{
		{b(`dts[ ._-]?hd[ ._-]?ma|dts[ ._-]?ma|dtshd[ ._-]?ma`), "DTS-HD MA"},
		{b(`dts[ ._-]?x`), "DTS:X"},
		{b(`truehd`), "TrueHD"},
		{b(`dd\+|ddp|e-?ac-?3|eac3|dd[ ._-]?plus|ddp\d`), "DD+"},
		{b(`dts[ ._-]?hd|dtshd`), "DTS-HD"},
		{b(`dts`), "DTS"},
		{b(`ac-?3|dd|dd\d|dolby[ ._-]?digital`), "DD"},
		{b(`aac(?:\d\.\d)?|aac2|he-?aac`), "AAC"},
		{b(`flac`), "FLAC"},
		{b(`opus`), "Opus"},
		{b(`mp3`), "MP3"},
		{b(`l?pcm`), "PCM"},
	}
	atmos    = b(`atmos`)
	channels = rx(`(?i)(?:[^0-9]|^)([1-7][ ._][01])(?:[^0-9]|$)`)
	chanTail = rx(`(?i)(?:ddp|dd|aac|dts|truehd|flac|opus|eac3|ac3)([1-7])[ ._]?([01])(?:[^0-9]|$)`)

	editions = []struct {
		re   *regexp.Regexp
		name string
	}{
		{b(`final[ ._-]cut`), "Final Cut"},
		{b(`special[ ._-]edition`), "Special Edition"},
		{b(`collector'?s?[ ._-]edition`), "Collector's Edition"},
		{b(`director'?s?[ ._-]?cut|dc`), "Director's Cut"},
		{b(`extended(?:[ ._-](?:cut|edition))?`), "Extended"},
		{b(`theatrical(?:[ ._-](?:cut|edition))?`), "Theatrical"},
		{b(`ultimate(?:[ ._-](?:cut|edition))?`), "Ultimate"},
		{b(`criterion(?:[ ._-]collection)?`), "Criterion"},
		{b(`anniversary(?:[ ._-]edition)?`), "Anniversary"},
		{b(`open[ ._-]matte`), "Open Matte"},
		{b(`imax(?:[ ._-]enhanced)?`), "IMAX"},
		{b(`unrated`), "Unrated"},
		{b(`uncut`), "Uncut"},
		{b(`remaster(?:ed)?|4k[ ._-]remaster`), "Remastered"},
	}

	services = []struct {
		re   *regexp.Regexp
		name string
	}{
		{b(`amzn|amazon`), "AMZN"},
		{b(`nf|netflix`), "NF"},
		{b(`dsnp|dsny|disney\+?`), "DSNP"},
		{b(`atvp|aptv`), "ATVP"},
		{b(`hmax`), "HMAX"},
		{b(`max`), "MAX"},
		{b(`hulu`), "HULU"},
		{b(`pcok|peacock`), "PCOK"},
		{b(`pmtp|paramount\+?`), "PMTP"},
		{b(`it|itunes`), "iT"},
		{b(`cr|crunchyroll`), "CR"},
		{b(`funi|funimation`), "FUNI"},
		{b(`hidive`), "HIDIVE"},
		{b(`stan`), "STAN"},
		{b(`crav`), "CRAV"},
		{b(`roku`), "ROKU"},
		{b(`sho|showtime`), "SHO"},
		{b(`bbc|iplayer`), "iP"},
	}

	languages = []struct {
		re   *regexp.Regexp
		name string
	}{
		{b(`french|vff|vfq|vf2|truefrench|vostfr`), "French"},
		{b(`german|deutsch|ger`), "German"},
		{b(`ita|italian`), "Italian"},
		{b(`spanish|español|espanol|castellano|latino|spa`), "Spanish"},
		{b(`portuguese|dublado|pt-br|ptbr`), "Portuguese"},
		{b(`russian|rus`), "Russian"},
		{b(`japanese|jap|jpn`), "Japanese"},
		{b(`korean|kor`), "Korean"},
		{b(`chinese|mandarin|cantonese|chs|cht`), "Chinese"},
		{b(`hindi`), "Hindi"},
		{b(`dutch|nl`), "Dutch"},
		{b(`swedish|swe`), "Swedish"},
		{b(`norwegian|nor`), "Norwegian"},
		{b(`danish|dan`), "Danish"},
		{b(`finnish|fin`), "Finnish"},
		{b(`polish|pl|pldub`), "Polish"},
		{b(`turkish|tur`), "Turkish"},
		{b(`hungarian|hun`), "Hungarian"},
		{b(`czech|cze`), "Czech"},
		{b(`greek`), "Greek"},
		{b(`hebrew|heb`), "Hebrew"},
		{b(`arabic`), "Arabic"},
		{b(`thai`), "Thai"},
		{b(`vietnamese|vie`), "Vietnamese"},
		{b(`ukrainian|ukr`), "Ukrainian"},
	}
	multi    = b(`multi|multi[ ._-]?(?:audio|lang|subs?)|dual[ ._-]?audio|dual`)
	hardsub  = b(`hc|hardsub|hardcoded|korsub`)
	groupEnd = rx(`-([A-Za-z0-9@&$!~\.]+?)(?:\s*\[[^\]]*\])*$`)
	// After the title, these words are never a release group.
	notGroup = map[string]bool{"dl": true, "hd": true, "rip": true, "dts": true, "hdr": true, "x264": true, "x265": true,
		"h264": true, "h265": true, "1080p": true, "720p": true, "2160p": true, "480p": true, "web": true, "ray": true, "ac3": true,
		"aac": true, "hevc": true, "avc": true, "av1": true, "dv": true, "remux": true, "bluray": true, "5": true, "7": true,
		"ma": true, "x": true, "es": true, "ddp5": true, "audio": true, "sub": true, "subs": true, "dub": true, "eng": true,
		"fr": true, "en": true, "ru": true}
	seps   = rx(`[\s._]+`)
	strip  = rx(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
	edgeRe = rx(`^[\s\-–:.,_]+|[\s\-–:.,_(\[]+$`)
)

// Parse reads one release name. A name that has no title gives an Info with an
// empty Title; the quality fields are filled whenever they can be.
func Parse(name string) Info {
	info := Info{Name: name}
	s := strings.TrimSpace(name)
	if videoExt[strings.ToLower(filepath.Ext(s))] {
		s = strings.TrimSuffix(s, filepath.Ext(s))
	}
	s = trailJunk.ReplaceAllString(s, "")
	s = webSite.ReplaceAllString(s, "")
	s = postJunk.ReplaceAllString(s, "")
	if m := hashTag.FindAllStringSubmatch(s, -1); len(m) > 0 {
		info.Hash = strings.ToUpper(m[len(m)-1][1])
	}
	anime := false
	if m := leadGroup.FindStringSubmatch(s); m != nil && !looksLikeQuality(m[1]) {
		info.Group = strings.TrimSpace(m[1])
		s = s[len(m[0]):]
		anime = true
	}
	// The quality, codec and the rest are read from the whole name, tags
	// included ("[1080p HEVC]").
	info.Quality = readQuality(s)
	readExtras(&info, s)

	// The title ends where the first numbering marker, year or quality starts.
	flat := s
	if dotted(s) {
		flat = seps.ReplaceAllString(s, " ")
	}
	cut := len(flat)
	setCut := func(i int) {
		if i >= 0 && i < cut {
			cut = i
		}
	}
	if m := sxe.FindStringSubmatchIndex(flat); m != nil {
		setCut(m[0])
		sn, _ := strconv.Atoi(flat[m[2]:m[3]])
		first, _ := strconv.Atoi(flat[m[4]:m[5]])
		info.Seasons = []int{sn}
		info.SeasonMarked = true
		info.Episodes = expand(first, flat[m[6]:m[7]])
	} else if m := nxm.FindStringSubmatchIndex(flat); m != nil && !isRes(flat[m[2]:m[5]]) {
		setCut(m[0])
		sn, _ := strconv.Atoi(flat[m[2]:m[3]])
		first, _ := strconv.Atoi(flat[m[4]:m[5]])
		info.Seasons = []int{sn}
		info.SeasonMarked = true
		info.Episodes = expand(first, flat[m[6]:m[7]])
	} else if m := daily.FindStringSubmatchIndex(flat); m != nil && validDate(flat[m[2]:m[3]], flat[m[4]:m[5]], flat[m[6]:m[7]]) {
		setCut(m[0])
		info.Daily = true
		info.AirDate = flat[m[2]:m[3]] + "-" + flat[m[4]:m[5]] + "-" + flat[m[6]:m[7]]
	} else if anime && absDash.MatchString(flat) {
		m := absDash.FindStringSubmatchIndex(flat)
		setCut(m[0])
		a, _ := strconv.Atoi(flat[m[2]:m[3]])
		info.Absolute = []int{a}
		if m[6] >= 0 {
			z, _ := strconv.Atoi(flat[m[6]:m[7]])
			for n := a + 1; n <= z && n-a < 200; n++ {
				info.Absolute = append(info.Absolute, n)
			}
		}
	} else if m := seasonTo.FindStringSubmatchIndex(flat); m != nil {
		setCut(m[0])
		a, _ := strconv.Atoi(flat[m[2]:m[3]])
		z, _ := strconv.Atoi(flat[m[4]:m[5]])
		for n := a; n <= z && n-a < 50; n++ {
			info.Seasons = append(info.Seasons, n)
		}
		info.FullSeason, info.SeasonMarked = true, true
	} else if i, n := seasonOnly(flat); i >= 0 {
		setCut(i)
		info.Seasons = []int{n}
		info.FullSeason, info.SeasonMarked = true, true
	} else if anime {
		if m := absBare.FindStringSubmatchIndex(flat); m != nil {
			setCut(m[0])
			a, _ := strconv.Atoi(flat[m[2]:m[3]])
			info.Absolute = []int{a}
		}
	}
	if m := complete.FindStringIndex(flat); m != nil {
		setCut(m[0])
		if !info.IsTV() {
			info.FullSeason = true
		}
	}
	// A season marker inside an anime title ("Show S2 - 05", "Show 2nd Season - 05").
	if anime && len(info.Absolute) > 0 && len(info.Seasons) == 0 {
		head := flat[:cut]
		if i, n := seasonOnly(head); i >= 0 {
			info.Seasons = []int{n}
			info.SeasonMarked = true
			info.Episodes = info.Absolute
			info.Absolute = nil
			cut = i
		}
	}
	// The year: the last plausible year before the cut (or, for movies, before
	// the first quality token), so "Blade Runner 2049 2017" keeps 2049 in the title.
	qcut := qualityStart(flat)
	if !info.IsTV() {
		setCut(qcut)
	}
	head := flat[:cut]
	for _, y := range years(head) {
		if strings.TrimSpace(edgeRe.ReplaceAllString(strip.ReplaceAllString(head[:y[0]], ""), "")) == "" {
			continue // the year is the whole title ("1917", "2012")
		}
		info.Year, _ = strconv.Atoi(head[y[0]:y[1]])
		cut = y[0]
		if y[0] > 0 && (head[y[0]-1] == '(' || head[y[0]-1] == '[') {
			cut = y[0] - 1
		}
		break
	}
	title := flat[:cut]
	if !dotted(s) {
		title = seps.ReplaceAllString(title, " ")
	}
	title = strip.ReplaceAllString(title, " ")
	title = strings.TrimSpace(seps.ReplaceAllString(title, " "))
	title = edgeRe.ReplaceAllString(title, "")
	info.Title = title
	if len(info.Seasons) == 1 && info.Seasons[0] == 0 {
		info.Special = true
	}
	if special.MatchString(flat[cut:]) && info.IsTV() && len(info.Episodes) == 0 && len(info.Absolute) == 0 {
		info.Special = true
	}
	if len(info.Absolute) > 0 && info.Quality.Version < 2 {
		if m := absDash.FindStringSubmatch(flat); m != nil && m[2] != "" {
			info.Quality.Version, _ = strconv.Atoi(m[2])
		}
	}
	if info.Group == "" {
		info.Group = sceneGroup(s)
	}
	if len(info.Languages) == 0 {
		info.Languages = []string{"English"}
	}
	return info
}

// years lists plausible years standing as their own word, last first.
func years(s string) [][2]int {
	var out [][2]int
	max := time.Now().Year() + 2
	for i := 0; i+4 <= len(s); i++ {
		if i > 0 && isAlnum(s[i-1]) {
			continue
		}
		if i+4 < len(s) && isAlnum(s[i+4]) {
			continue
		}
		n, err := strconv.Atoi(s[i : i+4])
		if err != nil || n < 1900 || n > max {
			continue
		}
		out = append([][2]int{{i, i + 4}}, out...)
	}
	return out
}

func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// dotted reports whether a name uses dots or underscores between words.
func dotted(s string) bool {
	return strings.Count(s, ".")+strings.Count(s, "_") > strings.Count(s, " ")
}

func isRes(s string) bool {
	l := strings.ToLower(s)
	return l == "1920x1080" || l == "1280x720" || l == "3840x2160" || strings.HasPrefix(l, "1920x") || strings.HasPrefix(l, "1280x")
}

func validDate(y, m, d string) bool {
	_, err := time.Parse("2006-01-02", y+"-"+m+"-"+d)
	return err == nil
}

// expand reads the rest of an episode run: "E02E03", "-03", "-E05" or "x02".
func expand(first int, rest string) []int {
	eps := []int{first}
	nums := rx(`\d{1,4}`).FindAllString(rest, -1)
	if len(nums) == 0 {
		return eps
	}
	ranged := strings.Contains(rest, "-") && len(nums) == 1
	for _, n := range nums {
		v, _ := strconv.Atoi(n)
		if v <= eps[len(eps)-1] || v-first > 100 {
			break
		}
		if ranged {
			for k := eps[len(eps)-1] + 1; k <= v; k++ {
				eps = append(eps, k)
			}
		} else {
			eps = append(eps, v)
		}
	}
	return eps
}

// seasonOnly finds a season-pack marker: "S02", "Season 2", "2nd Season".
func seasonOnly(s string) (int, int) {
	best, num := -1, 0
	for _, re := range []*regexp.Regexp{seasonS, seasonW, seasonOrd} {
		if m := re.FindStringSubmatchIndex(s); m != nil && (best < 0 || m[0] < best) {
			best = m[0]
			num, _ = strconv.Atoi(s[m[2]:m[3]])
		}
	}
	return best, num
}

// qualityStart is where the first quality, codec or source word begins.
func qualityStart(s string) int {
	first := -1
	for _, re := range []*regexp.Regexp{resRe[0].re, resRe[1].re, resRe[2].re, resRe[3].re, resRe[4].re, remux, bluray, webdl, webrip, hdtv,
		rx(`(?i)(?:^|[^a-z0-9])(?:dvdrip|dvd5|dvd9|xvid|x264|x265|h\.?264|h\.?265|hevc|10bit|hdr|proper|repack|multi|dual|internal|limited|unrated|extended|remastered|imax|directors[ ._]cut)(?:[^a-z0-9]|$)`)} {
		if m := re.FindStringIndex(s); m != nil && m[0] > 0 && (first < 0 || m[0] < first) {
			first = m[0]
		}
	}
	return first
}

func looksLikeQuality(tag string) bool {
	q := readQuality(tag)
	return q.Source != SourceUnknown || q.Resolution > 0
}

func readQuality(s string) Quality {
	q := Quality{Version: 1}
	for _, r := range resRe {
		if r.re.MatchString(s) {
			q.Resolution = r.n
			break
		}
	}
	// A bare "1080" or "720" counts only next to other quality words.
	if q.Resolution == 1080 && !rx(`(?i)1080[pi]|1920x1080|fhd`).MatchString(s) && !bluray.MatchString(s) && !webdl.MatchString(s) {
		q.Resolution = 0
		if rx(`(?i)720p|1280x720`).MatchString(s) {
			q.Resolution = 720
		}
	}
	if q.Resolution == 720 && !rx(`(?i)720p|1280x720`).MatchString(s) && !bluray.MatchString(s) && !webdl.MatchString(s) {
		q.Resolution = 0
	}
	switch {
	case remux.MatchString(s):
		q.Source, q.Modifier = SourceBluray, ModRemux
		if q.Resolution == 0 {
			q.Resolution = 1080
		}
	case brdisk.MatchString(s) && (bluray.MatchString(s) || rx(`(?i)bdmv|complete[ ._-]?(?:uhd[ ._-]?)?blu`).MatchString(s)) &&
		!rx(`(?i)x26[45]|h\.?26[45]|hevc|xvid|bdrip|brrip|web`).MatchString(s):
		q.Source, q.Modifier = SourceBluray, ModBRDisk
	case webrip.MatchString(s):
		q.Source = SourceWEBRip
	case bluray.MatchString(s):
		q.Source = SourceBluray
		if rx(`(?i)bdrip|brrip`).MatchString(s) && q.Resolution == 0 {
			q.Resolution = 480
		}
	case webdl.MatchString(s):
		q.Source = SourceWEBDL
	case rawhd.MatchString(s):
		q.Source, q.Modifier = SourceTV, ModRawHD
	case hdtv.MatchString(s):
		q.Source = SourceTV
	case scr.MatchString(s):
		q.Source = SourceScreener
	case dvd.MatchString(s):
		q.Source = SourceDVD
		if regRe.MatchString(s) && rx(`(?i)(?:^|[^a-z0-9])R5(?:[^a-z0-9]|$)`).MatchString(s) {
			q.Modifier = ModRegional
		}
		if q.Resolution >= 720 {
			q.Source = SourceBluray // an upscale named DVD with an HD size reads as Bluray in the arrs
		}
	case camRe.MatchString(s):
		q.Source = SourceCAM
	case tcRe.MatchString(s):
		q.Source = SourceTelecine
	case tsRe.MatchString(s) && !rx(`(?i)\.ts$`).MatchString(s):
		q.Source = SourceTelesync
	case wpRe.MatchString(s):
		q.Source = SourceWorkprint
	case regRe.MatchString(s) && rx(`(?i)(?:^|[^a-z0-9])R5(?:[^a-z0-9]|$)`).MatchString(s):
		q.Source, q.Modifier = SourceDVD, ModRegional
	case rx(`(?i)(?:^|[^a-z0-9])(?:xvid|divx)(?:[^a-z0-9]|$)`).MatchString(s) && q.Resolution == 0:
		q.Source = SourceTV // an XviD with nothing else is SDTV in the arrs
	}
	if q.Source == SourceWEBDL && q.Resolution == 0 && rx(`(?i)(?:^|[^a-z0-9])(?:4k|uhd)(?:[^a-z0-9]|$)`).MatchString(s) {
		q.Resolution = 2160
	}
	if proper.MatchString(s) {
		q.Proper = true
		q.Version = 2
	}
	if repack.MatchString(s) {
		q.Repack = true
		q.Version = 2
		if m := rx(`(?i)repack([2-9])`).FindStringSubmatch(s); m != nil {
			q.Version, _ = strconv.Atoi(m[1])
		}
	}
	if m := version.FindStringSubmatch(s); m != nil && q.Version < 2 {
		q.Version, _ = strconv.Atoi(m[1])
	}
	q.Real = len(realRe.FindAllString(s, -1))
	return q
}

func readExtras(info *Info, s string) {
	for _, c := range codecs {
		if c.re.MatchString(s) {
			info.Codec = c.name
			break
		}
	}
	if tenBit.MatchString(s) {
		info.Bits = 10
	}
	for _, h := range hdrs {
		if h.re.MatchString(s) && !(h.name == "HDR" && len(info.HDR) > 0 && info.HDR[len(info.HDR)-1] != "DV") && !(h.name == "HDR10" && has(info.HDR, "HDR10+")) {
			if h.name == "DV" && !rx(`(?i)(?:^|[^a-z0-9])(?:dv|dovi|dolby[ ._-]?vision)(?:[^a-z0-9]|$)`).MatchString(s) {
				continue
			}
			info.HDR = append(info.HDR, h.name)
		}
	}
	if has(info.HDR, "HDR") && (has(info.HDR, "HDR10") || has(info.HDR, "HDR10+")) {
		info.HDR = without(info.HDR, "HDR")
	}
	// "DV" in a name with no other video words is too weak (a title word): keep it
	// only next to a resolution.
	if has(info.HDR, "DV") && info.Quality.Resolution < 1080 {
		info.HDR = without(info.HDR, "DV")
	}
	for _, a := range audios {
		if a.re.MatchString(s) {
			if a.name == "DTS" && (has(info.Audio, "DTS-HD MA") || has(info.Audio, "DTS-HD") || has(info.Audio, "DTS:X")) {
				continue
			}
			if a.name == "DD" && has(info.Audio, "DD+") {
				continue
			}
			if a.name == "DTS-HD" && has(info.Audio, "DTS-HD MA") {
				continue
			}
			info.Audio = append(info.Audio, a.name)
		}
	}
	if atmos.MatchString(s) {
		info.Atmos = true
	}
	if m := chanTail.FindStringSubmatch(s); m != nil {
		info.Channels = m[1] + "." + m[2]
	} else if m := channels.FindStringSubmatch(s); m != nil {
		info.Channels = strings.NewReplacer(" ", ".", "_", ".").Replace(m[1])
	}
	for _, e := range editions {
		if e.re.MatchString(s) {
			if e.name == "Director's Cut" && !rx(`(?i)director`).MatchString(s) && !rx(`(?:^|[^A-Za-z0-9])DC(?:[^A-Za-z0-9]|$)`).MatchString(s) {
				continue
			}
			info.Edition = e.name
			break
		}
	}
	for _, sv := range services {
		if sv.re.MatchString(s) {
			// Short codes count only in the scene form, next to WEB.
			if len(sv.name) <= 3 && sv.name != "NF" && sv.name != "iT" && sv.name != "CR" && sv.name != "iP" && sv.name != "MAX" {
				info.Service = sv.name
				break
			}
			if rx(`(?i)web`).MatchString(s) && rx(`(?:^|[^A-Za-z0-9])(?:`+regexp.QuoteMeta(strings.ToUpper(sv.name))+`|`+regexp.QuoteMeta(sv.name)+`)(?:[^A-Za-z0-9]|$)`).MatchString(s) {
				info.Service = sv.name
				break
			}
		}
	}
	for _, l := range languages {
		if l.re.MatchString(s) {
			if len(l.name) > 0 && shortLang(l.re, s) {
				continue
			}
			info.Languages = append(info.Languages, l.name)
		}
	}
	if multi.MatchString(s) {
		info.Multi = true
		if !has(info.Languages, "English") {
			info.Languages = append([]string{"English"}, info.Languages...)
		}
	}
	if hardsub.MatchString(s) && rx(`(?:^|[^A-Za-z0-9])(?:HC|HARDSUB|hardsub|Hardsub|KORSUB|HardSub)(?:[^A-Za-z0-9]|$)`).MatchString(s) {
		info.Hardsub = true
	}
}

// shortLang drops three-letter language codes that appear only as part of the
// title ("Ita" or "Fin" as a word before any quality word).
func shortLang(re *regexp.Regexp, s string) bool {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return false
	}
	word := strings.Trim(s[loc[0]:loc[1]], " ._-[]()")
	if len(word) > 3 {
		return false
	}
	q := qualityStart(s)
	if q < 0 {
		return true
	}
	return loc[0] < q || strings.ToUpper(word) != word
}

func has(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func without(l []string, v string) []string {
	var out []string
	for _, x := range l {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// sceneGroup reads the "-GROUP" at the end of a scene name.
func sceneGroup(s string) string {
	t := strings.TrimSpace(s)
	t = rx(`\s*\[[^\]]*\]\s*$`).ReplaceAllString(t, "")
	t = rx(`\s*\([^\)]*\)\s*$`).ReplaceAllString(t, "")
	m := groupEnd.FindStringSubmatch(t)
	if m == nil {
		return ""
	}
	g := strings.Trim(m[1], ".")
	if g == "" || notGroup[strings.ToLower(g)] || strings.ContainsAny(g, " ") {
		return ""
	}
	if _, err := strconv.Atoi(g); err == nil {
		return "" // "Show - 05" is an episode, not a group
	}
	if rx(`(?i)^(?:dl|rip|hd|ray|tv|s\d+e\d+|e\d+|\d+p)$`).MatchString(g) {
		return ""
	}
	return g
}

// Episodes lists the episode numbers of a run as "1-3" style text, for display.
func Episodes(eps []int) string {
	if len(eps) == 0 {
		return ""
	}
	sort.Ints(eps)
	if len(eps) == 1 {
		return strconv.Itoa(eps[0])
	}
	return strconv.Itoa(eps[0]) + "-" + strconv.Itoa(eps[len(eps)-1])
}
