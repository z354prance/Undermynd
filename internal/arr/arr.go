// Package arr talks to Radarr, Sonarr and Lidarr to confirm imports.
package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	Kind    string
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(kind, baseURL, apiKey string) *Client {
	return &Client{Kind: kind, BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey,
		HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) prefix() string {
	if c.Kind == "lidarr" {
		return "/api/v1"
	}
	return "/api/v3"
}

type historyResp struct {
	Records []struct {
		EventType  string `json:"eventType"`
		DownloadID string `json:"downloadId"`
	} `json:"records"`
}

// Imported reports whether the arr's history shows an import for the download ID.
func (c *Client) Imported(ctx context.Context, downloadID string) (bool, error) {
	q := url.Values{}
	q.Set("page", "1")
	q.Set("pageSize", "100")
	q.Set("downloadId", strings.ToUpper(downloadID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+c.prefix()+"/history?"+q.Encode(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s history: HTTP %d", c.Kind, resp.StatusCode)
	}
	var h historyResp
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return false, err
	}
	for _, r := range h.Records {
		if !strings.EqualFold(r.DownloadID, downloadID) {
			continue // the endpoint may ignore the filter on older versions
		}
		e := strings.ToLower(r.EventType)
		if strings.Contains(e, "imported") && !strings.Contains(e, "fail") {
			return true, nil
		}
	}
	return false, nil
}
