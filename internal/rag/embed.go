package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// DefaultEmbedModel is the default Ollama embedding model.
// nomic-embed-text produces 768-dimensional L2-normalized vectors.
const DefaultEmbedModel = "nomic-embed-text"

// DefaultEmbedVectorSize is the vector dimension for nomic-embed-text.
const DefaultEmbedVectorSize = 768

// Embedder calls the Ollama /api/embed endpoint to produce embeddings.
type Embedder struct {
	OllamaURL  string
	Model      string
	httpClient *http.Client
}

// NewEmbedder creates an Embedder targeting the given Ollama URL.
// If model is empty, DefaultEmbedModel ("nomic-embed-text") is used.
func NewEmbedder(ollamaURL, model string) *Embedder {
	if model == "" {
		model = DefaultEmbedModel
	}
	return &Embedder{
		OllamaURL:  ollamaURL,
		Model:      model,
		httpClient: &http.Client{},
	}
}

// embedResponse mirrors the Ollama /api/embed response shape.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns the embedding vector for a single text string.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ollama embed: empty response for model %s", e.Model)
	}
	return vecs[0], nil
}

// EmbedBatch returns embeddings for a batch of texts in a single API call.
// Available since Ollama 0.2.0+. Results are in the same order as the input.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]interface{}{
		"model": e.Model,
		"input": texts,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.OllamaURL+"/api/embed", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: unreachable at %s (%w)", e.OllamaURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: status %d from %s", resp.StatusCode, e.OllamaURL)
	}

	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("ollama embed: decode: %w", err)
	}
	return er.Embeddings, nil
}
