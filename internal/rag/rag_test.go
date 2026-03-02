package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- ChunkText tests ----

func TestChunkText_Empty(t *testing.T) {
	chunks := ChunkText("", 100, 20)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty input, got %d", len(chunks))
	}
}

func TestChunkText_FitsInOne(t *testing.T) {
	chunks := ChunkText("hello world", 100, 20)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "hello world" {
		t.Errorf("unexpected chunk content: %q", chunks[0])
	}
}

func TestChunkText_Overlap(t *testing.T) {
	// "abcdefghij" (10 chars), size=4, overlap=2:
	// start=0 end=4: "abcd"; start=2 end=6: "cdef"; start=4 end=8: "efgh"; start=6 end=10: "ghij"
	text := "abcdefghij"
	chunks := ChunkText(text, 4, 2)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d: %v", len(chunks), chunks)
	}
	want := []string{"abcd", "cdef", "efgh", "ghij"}
	for i, w := range want {
		if chunks[i] != w {
			t.Errorf("chunk[%d]: want %q, got %q", i, w, chunks[i])
		}
	}
}

func TestChunkText_NoOverlap(t *testing.T) {
	chunks := ChunkText("abcdef", 2, 0)
	// [0:2]="ab", [2:4]="cd", [4:6]="ef"
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d: %v", len(chunks), chunks)
	}
}

// ---- FormatContext tests ----

func TestFormatContext_Empty(t *testing.T) {
	r := &Retriever{}
	if out := r.FormatContext(nil); out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
	if out := r.FormatContext([]SearchResult{}); out != "" {
		t.Errorf("expected empty string for empty slice, got %q", out)
	}
}

func TestFormatContext_Header(t *testing.T) {
	results := []SearchResult{
		{Score: 0.9, Payload: map[string]string{"source": "docs/test.md", "content": "hello"}},
	}
	r := &Retriever{}
	out := r.FormatContext(results)
	if !strings.Contains(out, "## Relevant Documentation") {
		t.Error("expected '## Relevant Documentation' header in output")
	}
	if !strings.Contains(out, "=== Source: docs/test.md ===") {
		t.Error("expected source header in output")
	}
	if !strings.Contains(out, "hello") {
		t.Error("expected content in output")
	}
}

func TestFormatContext_Truncation(t *testing.T) {
	long := strings.Repeat("x", 900)
	results := []SearchResult{
		{Score: 0.9, Payload: map[string]string{"source": "test.md", "content": long}},
	}
	r := &Retriever{}
	out := r.FormatContext(results)
	// The content section should not contain more than maxChunkDisplay x's
	count := strings.Count(out, "x")
	if count > maxChunkDisplay {
		t.Errorf("content not truncated: %d chars of 'x', max %d", count, maxChunkDisplay)
	}
}

// ---- EnsureCollection idempotency test ----

func TestEnsureCollection_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"result": true, "status": "ok"})
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-collection")
	if err := c.EnsureCollection(context.Background(), 768); err != nil {
		t.Errorf("expected no error on 200, got: %v", err)
	}
}

func TestEnsureCollection_AlreadyExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  "Collection `test-collection` already exists!",
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-collection")
	// 409 should be treated as success (idempotent).
	if err := c.EnsureCollection(context.Background(), 768); err != nil {
		t.Errorf("EnsureCollection should be idempotent on 409, got: %v", err)
	}
}

// ---- Search response parsing test ----

func TestSearch_ParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"id":      "abc-123",
					"score":   0.92,
					"payload": map[string]string{"source": "test.yaml", "content": "some content"},
				},
			},
			"status": "ok",
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-collection")
	results, err := c.Search(context.Background(), []float32{0.1, 0.2, 0.3}, 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Score < 0.9 {
		t.Errorf("expected score ~0.92, got %f", results[0].Score)
	}
	if results[0].Payload["source"] != "test.yaml" {
		t.Errorf("expected source 'test.yaml', got %q", results[0].Payload["source"])
	}
}

// ---- pointID determinism test ----

func TestPointID_Deterministic(t *testing.T) {
	id1 := pointID("/opt/postal/config.yaml", 0)
	id2 := pointID("/opt/postal/config.yaml", 0)
	if id1 != id2 {
		t.Errorf("pointID not deterministic: %q != %q", id1, id2)
	}
	// Different inputs must produce different IDs.
	id3 := pointID("/opt/postal/config.yaml", 1)
	if id1 == id3 {
		t.Error("pointID collision: different chunk_index produced same ID")
	}
	// Must be UUID format: 8-4-4-4-12
	parts := strings.Split(id1, "-")
	if len(parts) != 5 {
		t.Errorf("expected UUID format with 5 parts, got %d: %q", len(parts), id1)
	}
	if len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Errorf("UUID segment lengths wrong: %v", parts)
	}
}
