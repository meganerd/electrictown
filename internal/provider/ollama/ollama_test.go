package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/meganerd/electrictown/internal/provider"
)

func TestName(t *testing.T) {
	p := New("http://localhost:11434", "")
	if p.Name() != "ollama" {
		t.Errorf("expected name 'ollama', got %q", p.Name())
	}
}

func TestChatCompletion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected /api/chat, got %s", r.URL.Path)
		}

		var body ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if body.Model != "llama3" {
			t.Errorf("expected model 'llama3', got %q", body.Model)
		}
		if body.Stream {
			t.Error("expected stream=false for non-streaming request")
		}
		if len(body.Messages) != 1 || body.Messages[0].Content != "Hello" {
			t.Errorf("unexpected messages: %+v", body.Messages)
		}

		resp := ollamaChatResponse{
			Model: "llama3",
			Message: ollamaMessage{
				Role:    "assistant",
				Content: "Hi there!",
			},
			Done:             true,
			PromptEvalCount:  10,
			EvalCount:        5,
			TotalDuration:    1000000000,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New(srv.URL, "")
	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "llama3",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != "llama3" {
		t.Errorf("expected model 'llama3', got %q", resp.Model)
	}
	if resp.Message.Content != "Hi there!" {
		t.Errorf("expected content 'Hi there!', got %q", resp.Message.Content)
	}
	if resp.Message.Role != provider.RoleAssistant {
		t.Errorf("expected role 'assistant', got %q", resp.Message.Role)
	}
	if !resp.Done {
		t.Error("expected done=true")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("expected 10 prompt tokens, got %d", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 5 {
		t.Errorf("expected 5 completion tokens, got %d", resp.Usage.CompletionTokens)
	}
}

func TestChatCompletionWithAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key-123" {
			t.Errorf("expected 'Bearer test-key-123', got %q", auth)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp := ollamaChatResponse{
			Model:   "llama3",
			Message: ollamaMessage{Role: "assistant", Content: "Authenticated!"},
			Done:    true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New(srv.URL, "test-key-123")
	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "llama3",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "Authenticated!" {
		t.Errorf("expected 'Authenticated!', got %q", resp.Message.Content)
	}
}

func TestChatCompletionAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "model not found",
		})
	}))
	defer srv.Close()

	p := New(srv.URL, "")
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "nonexistent",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*provider.APIError)
	if !ok {
		t.Fatalf("expected *provider.APIError, got %T", err)
	}
	if apiErr.Status != 400 {
		t.Errorf("expected status 400, got %d", apiErr.Status)
	}
}

func TestStreamChatCompletion(t *testing.T) {
	chunks := []ollamaChatResponse{
		{Model: "llama3", Message: ollamaMessage{Role: "assistant", Content: "Hello"}, Done: false},
		{Model: "llama3", Message: ollamaMessage{Role: "assistant", Content: " world"}, Done: false},
		{Model: "llama3", Message: ollamaMessage{Role: "assistant", Content: ""}, Done: true, PromptEvalCount: 8, EvalCount: 3},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&body)
		if !body.Stream {
			t.Error("expected stream=true for streaming request")
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := New(srv.URL, "")
	stream, err := p.StreamChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "llama3",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()

	// Chunk 1
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("unexpected error on chunk 1: %v", err)
	}
	if chunk.Delta.Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", chunk.Delta.Content)
	}
	if chunk.Done {
		t.Error("expected done=false on chunk 1")
	}

	// Chunk 2
	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("unexpected error on chunk 2: %v", err)
	}
	if chunk.Delta.Content != " world" {
		t.Errorf("expected ' world', got %q", chunk.Delta.Content)
	}

	// Chunk 3 (final)
	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("unexpected error on chunk 3: %v", err)
	}
	if !chunk.Done {
		t.Error("expected done=true on final chunk")
	}
	if chunk.Usage == nil {
		t.Fatal("expected usage on final chunk")
	}
	if chunk.Usage.PromptTokens != 8 {
		t.Errorf("expected 8 prompt tokens, got %d", chunk.Usage.PromptTokens)
	}

	// After final chunk, Next should return io.EOF
	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF after stream done, got %v", err)
	}
}

func TestStreamChatCompletionCancelContext(t *testing.T) {
	// Cancel context before making the request so the HTTP call fails immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaChatResponse{
			Model:   "llama3",
			Message: ollamaMessage{Role: "assistant", Content: "nope"},
			Done:    true,
		})
	}))
	defer srv.Close()

	p := New(srv.URL, "")
	_, err := p.StreamChatCompletion(ctx, &provider.ChatRequest{
		Model:    "llama3",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/tags" {
			t.Errorf("expected /api/tags, got %s", r.URL.Path)
		}

		resp := ollamaTagsResponse{
			Models: []ollamaModelInfo{
				{Name: "llama3:latest", Model: "llama3:latest", Size: 4000000000},
				{Name: "mistral:7b", Model: "mistral:7b", Size: 3000000000},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New(srv.URL, "")
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "llama3:latest" {
		t.Errorf("expected model ID 'llama3:latest', got %q", models[0].ID)
	}
	if models[0].Provider != "ollama" {
		t.Errorf("expected provider 'ollama', got %q", models[0].Provider)
	}
	if models[1].Name != "mistral:7b" {
		t.Errorf("expected model name 'mistral:7b', got %q", models[1].Name)
	}
}

func TestListModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()

	p := New(srv.URL, "")
	_, err := p.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestChatCompletionWithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{
			Model: "llama3",
			Message: ollamaMessage{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ollamaToolCall{
					{
						Function: ollamaFunctionCall{
							Name:      "get_weather",
							Arguments: map[string]interface{}{"city": "Seattle"},
						},
					},
				},
			},
			Done: true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New(srv.URL, "")
	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "llama3",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "What's the weather?"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %q", tc.Function.Name)
	}
	if tc.Type != "function" {
		t.Errorf("expected type 'function', got %q", tc.Type)
	}
	// Arguments should be a JSON string
	if !strings.Contains(tc.Function.Arguments, "Seattle") {
		t.Errorf("expected arguments to contain 'Seattle', got %q", tc.Function.Arguments)
	}
}

func TestChatCompletionWithTemperature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&body)

		if body.Options == nil {
			t.Fatal("expected options to be set")
		}
		if temp, ok := body.Options["temperature"]; !ok || temp != 0.7 {
			t.Errorf("expected temperature 0.7, got %v", temp)
		}
		if topP, ok := body.Options["top_p"]; !ok || topP != 0.9 {
			t.Errorf("expected top_p 0.9, got %v", topP)
		}

		resp := ollamaChatResponse{
			Model:   "llama3",
			Message: ollamaMessage{Role: "assistant", Content: "response"},
			Done:    true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	temp := 0.7
	topP := 0.9
	p := New(srv.URL, "")
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:       "llama3",
		Messages:    []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
		Temperature: &temp,
		TopP:        &topP,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderInterface(t *testing.T) {
	// Compile-time check that OllamaProvider implements provider.Provider.
	var _ provider.Provider = (*OllamaProvider)(nil)
}
