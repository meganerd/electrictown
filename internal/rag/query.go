package rag

import (
	"context"
	"fmt"
	"strings"
)

const defaultRetrieveLimit = 3
const maxChunkDisplay      = 800

// Retriever retrieves relevant document chunks from Qdrant using semantic search.
type Retriever struct {
	Client   *Client
	Embedder *Embedder
}

// NewRetriever creates a Retriever backed by the given Qdrant client and Embedder.
func NewRetriever(client *Client, embedder *Embedder) *Retriever {
	return &Retriever{Client: client, Embedder: embedder}
}

// Retrieve embeds the query string and returns the top-N most relevant chunks
// from Qdrant. If limit <= 0, defaultRetrieveLimit (3) is used.
func (r *Retriever) Retrieve(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = defaultRetrieveLimit
	}
	vec, err := r.Embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("retriever embed query: %w", err)
	}
	return r.Client.Search(ctx, vec, limit)
}

// FormatContext formats retrieved search results as a documentation context block
// suitable for prepending to an LLM prompt. Each chunk's content is truncated
// to maxChunkDisplay (800) characters to stay within worker context budgets.
// Returns an empty string if results is nil or empty.
func (r *Retriever) FormatContext(results []SearchResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Relevant Documentation\n\n")
	for _, result := range results {
		source := result.Payload["source"]
		content := result.Payload["content"]
		if len(content) > maxChunkDisplay {
			content = content[:maxChunkDisplay]
		}
		fmt.Fprintf(&sb, "=== Source: %s ===\n%s\n\n", source, content)
	}
	return sb.String()
}
