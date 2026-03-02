// Package rag provides a minimal Qdrant REST client and Ollama embedding
// integration for retrieval-augmented generation in electrictown.
package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Point is a vector with associated metadata to store in Qdrant.
type Point struct {
	ID      string            // UUID-format string identifier
	Vector  []float32         // embedding vector
	Payload map[string]string // metadata (source, chunk_index, content)
}

// SearchResult is a scored document chunk returned from Qdrant search.
type SearchResult struct {
	Score   float32
	Payload map[string]string
}

// CollectionStats holds basic info about a Qdrant collection.
type CollectionStats struct {
	PointsCount int
	Status      string
}

// Client is a minimal Qdrant REST client using only stdlib net/http.
type Client struct {
	BaseURL        string
	CollectionName string
	httpClient     *http.Client
}

// NewClient creates a Qdrant client for the given server URL and collection name.
func NewClient(baseURL, collection string) *Client {
	return &Client{
		BaseURL:        baseURL,
		CollectionName: collection,
		httpClient:     &http.Client{},
	}
}

// EnsureCollection creates the collection if it does not already exist.
// Idempotent: a 409 Conflict (collection already exists) is treated as success.
func (c *Client) EnsureCollection(ctx context.Context, vectorSize int) error {
	body := map[string]interface{}{
		"vectors": map[string]interface{}{
			"size":     vectorSize,
			"distance": "Cosine",
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.BaseURL+"/collections/"+c.CollectionName, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant EnsureCollection: %w", err)
	}
	defer resp.Body.Close()

	// 200 = created; 409 = already exists — both are success for idempotency.
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("qdrant EnsureCollection: unexpected status %d", resp.StatusCode)
}

// qdrantPoint mirrors the JSON shape Qdrant expects for upsert.
type qdrantPoint struct {
	ID      string            `json:"id"`
	Vector  []float32         `json:"vector"`
	Payload map[string]string `json:"payload"`
}

// Upsert batch-upserts points into the collection via PUT /points.
func (c *Client) Upsert(ctx context.Context, points []Point) error {
	ps := make([]qdrantPoint, len(points))
	for i, p := range points {
		ps[i] = qdrantPoint{ID: p.ID, Vector: p.Vector, Payload: p.Payload}
	}
	body := map[string]interface{}{"points": ps}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.BaseURL+"/collections/"+c.CollectionName+"/points", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant Upsert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant Upsert: status %d", resp.StatusCode)
	}
	return nil
}

// searchRequest is the Qdrant POST /points/search request body.
type searchRequest struct {
	Vector      []float32 `json:"vector"`
	Limit       int       `json:"limit"`
	WithPayload bool      `json:"with_payload"`
}

// searchResponseItem represents one result in the Qdrant search response.
type searchResponseItem struct {
	Score   float32           `json:"score"`
	Payload map[string]string `json:"payload"`
}

// searchResponse is the top-level Qdrant search response envelope.
type searchResponse struct {
	Result []searchResponseItem `json:"result"`
	Status string               `json:"status"`
}

// Search returns the top-N most similar points to the given vector.
// Results are ordered by descending similarity score.
func (c *Client) Search(ctx context.Context, vector []float32, limit int) ([]SearchResult, error) {
	sr := searchRequest{Vector: vector, Limit: limit, WithPayload: true}
	data, err := json.Marshal(sr)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/collections/"+c.CollectionName+"/points/search", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant Search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qdrant Search: status %d", resp.StatusCode)
	}

	var sr2 searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr2); err != nil {
		return nil, fmt.Errorf("qdrant Search: decode: %w", err)
	}

	results := make([]SearchResult, len(sr2.Result))
	for i, item := range sr2.Result {
		results[i] = SearchResult{Score: item.Score, Payload: item.Payload}
	}
	return results, nil
}

// Stats returns basic statistics for the collection.
func (c *Client) Stats(ctx context.Context) (*CollectionStats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/collections/"+c.CollectionName, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant Stats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qdrant Stats: status %d", resp.StatusCode)
	}

	var body struct {
		Result struct {
			Status      string `json:"status"`
			PointsCount int    `json:"points_count"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("qdrant Stats: decode: %w", err)
	}
	return &CollectionStats{
		PointsCount: body.Result.PointsCount,
		Status:      body.Result.Status,
	}, nil
}
