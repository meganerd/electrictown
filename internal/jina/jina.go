// Package jina provides a client for Jina AI Reader (r.jina.ai).
// It fetches a URL and returns the page content as clean markdown,
// stripping navigation, ads, and boilerplate automatically.
package jina

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	readerBaseURL = "https://r.jina.ai/"
	maxBodyBytes  = 64 * 1024 // 64KB hard cap on response body
)

// Client fetches URLs through Jina AI Reader.
type Client struct {
	APIKey     string
	httpClient *http.Client
}

// New creates a Client. apiKey may be empty (anonymous tier, 20 req/min).
// With a free Jina API key the limit rises to 100 req/min.
func New(apiKey string) *Client {
	return &Client{
		APIKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchURL fetches targetURL through Jina Reader and returns clean markdown.
// targetURL must include the scheme (e.g. "https://docs.example.com/page").
func (c *Client) FetchURL(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readerBaseURL+targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("jina: build request for %s: %w", targetURL, err)
	}
	req.Header.Set("Accept", "text/markdown")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("jina: fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jina: fetch %s: HTTP %d", targetURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("jina: read body for %s: %w", targetURL, err)
	}
	return string(body), nil
}
