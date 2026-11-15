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
	q.Set("downloadId", downloadID)
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

// Check proves the address and key work by reading the arr's status, and returns
// its name and version. It also reads the history, the call the import check
// makes, so a key that can read status but not history is caught too.
func (c *Client) Check(ctx context.Context) (name, version string, err error) {
	get := func(path string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+c.prefix()+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Api-Key", c.APIKey)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return fmt.Errorf("%s refused the API key (HTTP %d)", c.Kind, resp.StatusCode)
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("%s answered HTTP %d for %s", c.Kind, resp.StatusCode, path)
		}
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	var st struct {
		AppName string `json:"appName"`
		Version string `json:"version"`
	}
	if err = get("/system/status", &st); err != nil {
		return "", "", err
	}
	if err = get("/history?page=1&pageSize=1", nil); err != nil {
		return "", "", err
	}
	return st.AppName, st.Version, nil
}
