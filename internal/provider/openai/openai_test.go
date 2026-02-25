package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meganerd/electrictown/internal/provider"
)

// newTestServer creates an httptest.Server and an OpenAIProvider pointed at it.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *OpenAIProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("test-key", WithBaseURL(srv.URL))
	return srv, p
}

func TestName(t *testing.T) {
	p := New("key")
	if p.Name() != "openai" {
		t.Fatalf("expected Name() = %q, got %q", "openai", p.Name())
	}
}

func TestChatCompletion(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Validate request.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		var req oaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Model != "gpt-4" {
			t.Errorf("expected model gpt-4, got %s", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "Hello" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}
		if req.Stream {
			t.Error("expected stream=false for ChatCompletion")
		}

		resp := oaiResponse{
			ID:    "chatcmpl-123",
			Model: "gpt-4",
			Choices: []oaiChoice{
				{
					Index: 0,
					Message: oaiMessage{
						Role:    provider.RoleAssistant,
						Content: "Hello there!",
					},
				},
			},
			Usage: &oaiUsage{
				PromptTokens:     5,
				CompletionTokens: 3,
				TotalTokens:      8,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "gpt-4",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "chatcmpl-123" {
		t.Errorf("expected ID chatcmpl-123, got %s", resp.ID)
	}
	if resp.Message.Content != "Hello there!" {
		t.Errorf("expected content 'Hello there!', got %q", resp.Message.Content)
	}
	if resp.Message.Role != provider.RoleAssistant {
		t.Errorf("expected role assistant, got %s", resp.Message.Role)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Errorf("expected 8 total tokens, got %d", resp.Usage.TotalTokens)
	}
	if !resp.Done {
		t.Error("expected Done=true")
	}
}

func TestChatCompletionWithToolCalls(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req oaiRequest
		json.NewDecoder(r.Body).Decode(&req)

		if len(req.Tools) != 1 || req.Tools[0].Function.Name != "get_weather" {
			t.Errorf("unexpected tools: %+v", req.Tools)
		}

		resp := oaiResponse{
			ID:    "chatcmpl-456",
			Model: "gpt-4",
			Choices: []oaiChoice{
				{
					Message: oaiMessage{
						Role: provider.RoleAssistant,
						ToolCalls: []provider.ToolCall{
							{
								ID:   "call_abc",
								Type: "function",
								Function: provider.FunctionCall{
									Name:      "get_weather",
									Arguments: `{"location":"NYC"}`,
								},
							},
						},
					},
				},
			},
			Usage: &oaiUsage{TotalTokens: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "gpt-4",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "What's the weather in NYC?"},
		},
		Tools: []provider.Tool{
			{
				Type: "function",
				Function: provider.ToolFunction{
					Name:        "get_weather",
					Description: "Get weather for a location",
					Parameters:  map[string]any{"type": "object"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_abc" || tc.Function.Name != "get_weather" {
		t.Errorf("unexpected tool call: %+v", tc)
	}
}

func TestChatCompletionAPIError(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Rate limit exceeded",
				"type":    "tokens",
				"code":    "rate_limit_exceeded",
			},
		})
	})

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gpt-4",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*provider.APIError)
	if !ok {
		t.Fatalf("expected *provider.APIError, got %T", err)
	}
	if apiErr.Status != 429 {
		t.Errorf("expected status 429, got %d", apiErr.Status)
	}
	if apiErr.Code != "rate_limit_exceeded" {
		t.Errorf("expected code rate_limit_exceeded, got %s", apiErr.Code)
	}
	if apiErr.Message != "Rate limit exceeded" {
		t.Errorf("expected message 'Rate limit exceeded', got %q", apiErr.Message)
	}

	// Verify error classification works.
	if provider.ClassifyError(apiErr) != provider.ErrRateLimit {
		t.Errorf("expected ErrRateLimit classification, got %v", provider.ClassifyError(apiErr))
	}
}

func TestStreamChatCompletion(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req oaiRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("expected stream=true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("server does not support flushing")
		}

		chunks := []string{
			`{"id":"chatcmpl-s1","model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"id":"chatcmpl-s1","model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-s1","model":"gpt-4","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-s1","model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	stream, err := p.StreamChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gpt-4",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()

	var chunks []*provider.ChatStreamChunk
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}

	// First chunk sets role.
	if chunks[0].Delta.Role != provider.RoleAssistant {
		t.Errorf("expected assistant role in first chunk, got %q", chunks[0].Delta.Role)
	}

	// Middle chunks have content.
	if chunks[1].Delta.Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", chunks[1].Delta.Content)
	}
	if chunks[2].Delta.Content != " world" {
		t.Errorf("expected ' world', got %q", chunks[2].Delta.Content)
	}

	// Final chunk has Done and Usage.
	if !chunks[3].Done {
		t.Error("expected Done=true on final chunk")
	}
	if chunks[3].Usage == nil {
		t.Fatal("expected usage on final chunk")
	}
	if chunks[3].Usage.TotalTokens != 7 {
		t.Errorf("expected 7 total tokens, got %d", chunks[3].Usage.TotalTokens)
	}

	// All chunks share the same ID.
	for i, c := range chunks {
		if c.ID != "chatcmpl-s1" {
			t.Errorf("chunk %d: expected ID chatcmpl-s1, got %s", i, c.ID)
		}
	}
}

func TestStreamChatCompletionHTTPError(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Invalid API key",
				"type":    "authentication_error",
				"code":    "invalid_api_key",
			},
		})
	})

	_, err := p.StreamChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gpt-4",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*provider.APIError)
	if !ok {
		t.Fatalf("expected *provider.APIError, got %T", err)
	}
	if apiErr.Status != 401 {
		t.Errorf("expected status 401, got %d", apiErr.Status)
	}
}

func TestListModels(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/models" {
			t.Errorf("expected /models, got %s", r.URL.Path)
		}

		resp := oaiModelsResponse{
			Data: []oaiModel{
				{ID: "gpt-4"},
				{ID: "gpt-4-turbo"},
				{ID: "gpt-3.5-turbo"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}
	for _, m := range models {
		if m.Provider != "openai" {
			t.Errorf("expected provider 'openai', got %q", m.Provider)
		}
	}
	if models[0].ID != "gpt-4" {
		t.Errorf("expected first model gpt-4, got %s", models[0].ID)
	}
}

func TestWithOrganization(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		org := r.Header.Get("OpenAI-Organization")
		if org != "org-test123" {
			t.Errorf("expected org header 'org-test123', got %q", org)
		}

		resp := oaiModelsResponse{Data: []oaiModel{{ID: "gpt-4"}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Re-initialize with org option.
	p.orgID = "org-test123"

	_, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithBaseURL(t *testing.T) {
	p := New("key", WithBaseURL("https://custom.api.com/v2/"))
	if p.baseURL != "https://custom.api.com/v2" {
		t.Errorf("expected trailing slash stripped, got %q", p.baseURL)
	}
}

func TestChatCompletionWithOptionalParams(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req oaiRequest
		json.NewDecoder(r.Body).Decode(&req)

		if req.Temperature == nil || *req.Temperature != 0.7 {
			t.Errorf("expected temperature 0.7, got %v", req.Temperature)
		}
		if req.TopP == nil || *req.TopP != 0.9 {
			t.Errorf("expected top_p 0.9, got %v", req.TopP)
		}
		if req.MaxTokens == nil || *req.MaxTokens != 100 {
			t.Errorf("expected max_tokens 100, got %v", req.MaxTokens)
		}
		if len(req.Stop) != 1 || req.Stop[0] != "\n" {
			t.Errorf("expected stop=[\\n], got %v", req.Stop)
		}

		resp := oaiResponse{
			ID:    "chatcmpl-opt",
			Model: "gpt-4",
			Choices: []oaiChoice{
				{Message: oaiMessage{Role: provider.RoleAssistant, Content: "ok"}},
			},
			Usage: &oaiUsage{TotalTokens: 1},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	temp := 0.7
	topP := 0.9
	maxTok := 100

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:       "gpt-4",
		Messages:    []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   &maxTok,
		Stop:        []string{"\n"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChatCompletionNoChoices(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := oaiResponse{
			ID:      "chatcmpl-empty",
			Model:   "gpt-4",
			Choices: []oaiChoice{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gpt-4",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
}
