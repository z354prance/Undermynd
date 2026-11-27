package arr

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// Artist is an artist as Lidarr's lookup gives it.
type Artist struct {
	ID             int64      `json:"id"` // > 0 when already in Lidarr
	Name           string     `json:"artistName"`
	ForeignID      string     `json:"foreignArtistId"` // MusicBrainz id
	Overview       string     `json:"overview"`
	Disambiguation string     `json:"disambiguation"`
	Type           string     `json:"artistType"`
	Images         []arrImage `json:"images"`
}

// Image is the artist's picture (a remote address), or "".
func (a Artist) Image() string { return pickImage(a.Images, "poster", "fanart", "banner") }

// LookupArtists searches Lidarr's metadata for artists.
func (c *Client) LookupArtists(ctx context.Context, term string) ([]Artist, error) {
	var out []Artist
	return out, c.get(ctx, "/artist/lookup?term="+url.QueryEscape(term), &out)
}

// ArtistByMBID finds one artist by MusicBrainz id through Lidarr's lookup.
func (c *Client) ArtistByMBID(ctx context.Context, mbid string) (Artist, error) {
	list, err := c.LookupArtists(ctx, "lidarr:"+mbid)
	if err != nil {
		return Artist{}, err
	}
	for _, a := range list {
		if strings.EqualFold(a.ForeignID, mbid) {
			return a, nil
		}
	}
	return Artist{}, errors.New("Lidarr does not know this artist")
}

type musicRoot struct {
	Path            string `json:"path"`
	DefaultQuality  int64  `json:"defaultQualityProfileId"`
	DefaultMetadata int64  `json:"defaultMetadataProfileId"`
}

// AddArtist adds an artist the way adding one in Lidarr does: every album
// monitored and searched, into Lidarr's first root folder with its default
// quality and metadata profiles (the first of each when it has no default).
func (c *Client) AddArtist(ctx context.Context, a Artist) (Artist, error) {
	var roots []musicRoot
	if err := c.get(ctx, "/rootfolder", &roots); err != nil {
		return Artist{}, err
	}
	if len(roots) == 0 {
		return Artist{}, errors.New("Lidarr has no root folder: add one in Lidarr › Settings › Media Management")
	}
	root := roots[0]
	qp, mp := root.DefaultQuality, root.DefaultMetadata
	if qp == 0 {
		ps, err := c.Profiles(ctx)
		if err != nil || len(ps) == 0 {
			return Artist{}, errors.New("Lidarr has no quality profile")
		}
		qp = ps[0].ID
	}
	if mp == 0 {
		var ms []Profile
		if err := c.get(ctx, "/metadataprofile", &ms); err != nil || len(ms) == 0 {
			return Artist{}, errors.New("Lidarr has no metadata profile")
		}
		mp = ms[0].ID
	}
	body := map[string]any{
		"artistName": a.Name, "foreignArtistId": a.ForeignID, "qualityProfileId": qp, "metadataProfileId": mp,
		"rootFolderPath": root.Path, "monitored": true, "monitorNewItems": "all",
		"addOptions": map[string]any{"monitor": "all", "searchForMissingAlbums": true},
	}
	var out Artist
	return out, c.send(ctx, "POST", "/artist", body, &out)
}
