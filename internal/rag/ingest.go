package rag

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultChunkSize    = 1000
const defaultChunkOverlap = 200

// Ingestor reads documents, chunks them, embeds them, and upserts into Qdrant.
type Ingestor struct {
	Client       *Client
	Embedder     *Embedder
	ChunkSize    int
	ChunkOverlap int
}

// NewIngestor creates an Ingestor with default chunk settings
// (ChunkSize=1000, ChunkOverlap=200).
func NewIngestor(client *Client, embedder *Embedder) *Ingestor {
	return &Ingestor{
		Client:       client,
		Embedder:     embedder,
		ChunkSize:    defaultChunkSize,
		ChunkOverlap: defaultChunkOverlap,
	}
}

// ChunkText splits text into overlapping windows of chunkSize characters
// with chunkOverlap characters of overlap between consecutive chunks.
// Returns nil for empty input.
func ChunkText(text string, chunkSize, overlap int) []string {
	if len(text) == 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	if overlap < 0 || overlap >= chunkSize {
		overlap = 0
	}

	var chunks []string
	start := 0
	for start < len(text) {
		end := start + chunkSize
		if end > len(text) {
			end = len(text)
		}
		chunk := strings.TrimSpace(text[start:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(text) {
			break
		}
		start += chunkSize - overlap
	}
	return chunks
}

// pointID generates a deterministic UUID-format string from source path + chunk index.
// Uses the first 32 hex characters of SHA-256(source:index) formatted as UUID.
func pointID(source string, idx int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", source, idx)))
	hex := fmt.Sprintf("%x", h)
	// Format as 8-4-4-4-12 UUID from the first 32 hex chars.
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
}

// IngestFile reads a file, chunks it into overlapping windows, embeds each chunk,
// and upserts all points into Qdrant. Returns the number of chunks ingested.
func (ing *Ingestor) IngestFile(ctx context.Context, path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("ingest %s: %w", path, err)
	}

	chunks := ChunkText(string(data), ing.ChunkSize, ing.ChunkOverlap)
	if len(chunks) == 0 {
		return 0, nil
	}

	vectors, err := ing.Embedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("embed %s: %w", path, err)
	}
	if len(vectors) != len(chunks) {
		return 0, fmt.Errorf("embed %s: expected %d vectors, got %d", path, len(chunks), len(vectors))
	}

	points := make([]Point, len(chunks))
	for i, chunk := range chunks {
		points[i] = Point{
			ID:     pointID(path, i),
			Vector: vectors[i],
			Payload: map[string]string{
				"source":      path,
				"chunk_index": fmt.Sprintf("%d", i),
				"content":     chunk,
			},
		}
	}

	if err := ing.Client.Upsert(ctx, points); err != nil {
		return 0, fmt.Errorf("upsert %s: %w", path, err)
	}
	return len(chunks), nil
}

// ingestExtensions is the set of file extensions supported by IngestDir.
var ingestExtensions = map[string]bool{
	".md":   true,
	".txt":  true,
	".yaml": true,
	".yml":  true,
}

// IngestDir walks dir recursively and ingests all supported files
// (.md, .txt, .yaml, .yml). Hidden files and directories (names starting
// with ".") are skipped. File-level errors print a warning and continue.
// Returns total chunks ingested across all files.
func (ing *Ingestor) IngestDir(ctx context.Context, dir string) (int, error) {
	total := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if strings.HasPrefix(name, ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !ingestExtensions[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		n, err := ing.IngestFile(ctx, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
			return nil // continue on file-level errors
		}
		total += n
		return nil
	})
	return total, err
}
