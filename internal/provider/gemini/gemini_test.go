package gemini

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

// newTestServer creates an httptest.Server and a GeminiProvider pointed at it.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *GeminiProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("test-api-key", WithBaseURL(srv.URL))
	return srv, p
}

func TestName(t *testing.T) {
	p := New("key")
	if p.Name() != "gemini" {
		t.Fatalf("expected Name() = %q, got %q", "gemini", p.Name())
	}
}

func TestProviderInterface(t *testing.T) {
	// Compile-time check that GeminiProvider implements provider.Provider.
	var _ provider.Provider = (*GeminiProvider)(nil)
	var _ provider.ChatStream = (*sseStream)(nil)
}

func TestChatCompletion(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Validate method and path format.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		// Path should be /models/{model}:generateContent
		expectedPath := "/models/gemini-pro:generateContent"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %s, got %s", expectedPath, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if len(req.Contents) != 1 {
			t.Fatalf("expected 1 content entry, got %d", len(req.Contents))
		}
		if req.Contents[0].Role != "user" {
			t.Errorf("expected role 'user', got %q", req.Contents[0].Role)
		}
		if len(req.Contents[0].Parts) != 1 || req.Contents[0].Parts[0].Text != "Hello" {
			t.Errorf("unexpected parts: %+v", req.Contents[0].Parts)
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role:  "model",
						Parts: []geminiPart{{Text: "Hello there!"}},
					},
				},
			},
			UsageMetadata: &geminiUsageMetadata{
				PromptTokenCount:     5,
				CandidatesTokenCount: 3,
				TotalTokenCount:      8,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "gemini-pro",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "Hello there!" {
		t.Errorf("expected content 'Hello there!', got %q", resp.Message.Content)
	}
	if resp.Message.Role != provider.RoleAssistant {
		t.Errorf("expected role assistant, got %s", resp.Message.Role)
	}
	if resp.Model != "gemini-pro" {
		t.Errorf("expected model gemini-pro, got %s", resp.Model)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Errorf("expected 8 total tokens, got %d", resp.Usage.TotalTokens)
	}
	if resp.Usage.PromptTokens != 5 {
		t.Errorf("expected 5 prompt tokens, got %d", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 3 {
		t.Errorf("expected 3 completion tokens, got %d", resp.Usage.CompletionTokens)
	}
	if !resp.Done {
		t.Error("expected Done=true")
	}
}

func TestChatCompletionWithAuth(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// API key must be in query parameter, NOT in Authorization header.
		key := r.URL.Query().Get("key")
		if key != "test-api-key" {
			t.Errorf("expected query param key=test-api-key, got %q", key)
		}
		auth := r.Header.Get("Authorization")
		if auth != "" {
			t.Errorf("expected no Authorization header, got %q", auth)
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role:  "model",
						Parts: []geminiPart{{Text: "ok"}},
					},
				},
			},
			UsageMetadata: &geminiUsageMetadata{TotalTokenCount: 1},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gemini-pro",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChatCompletionWithSystemMessage(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		// System message should be extracted to system_instruction, not in contents.
		if req.SystemInstruction == nil {
			t.Fatal("expected system_instruction to be set")
		}
		if len(req.SystemInstruction.Parts) != 1 || req.SystemInstruction.Parts[0].Text != "You are a helpful assistant." {
			t.Errorf("unexpected system_instruction: %+v", req.SystemInstruction)
		}

		// Contents should only have the user message.
		if len(req.Contents) != 1 {
			t.Fatalf("expected 1 content entry (no system), got %d", len(req.Contents))
		}
		if req.Contents[0].Role != "user" {
			t.Errorf("expected role 'user', got %q", req.Contents[0].Role)
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role:  "model",
						Parts: []geminiPart{{Text: "I'm helpful!"}},
					},
				},
			},
			UsageMetadata: &geminiUsageMetadata{TotalTokenCount: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "gemini-pro",
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "You are a helpful assistant."},
			{Role: provider.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChatCompletionWithToolCalls(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify tools were translated to Gemini format.
		if len(req.Tools) != 1 {
			t.Fatalf("expected 1 tool declaration, got %d", len(req.Tools))
		}
		if len(req.Tools[0].FunctionDeclarations) != 1 {
			t.Fatalf("expected 1 function declaration, got %d", len(req.Tools[0].FunctionDeclarations))
		}
		if req.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
			t.Errorf("expected function name get_weather, got %s", req.Tools[0].FunctionDeclarations[0].Name)
		}

		// Respond with a function call.
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role: "model",
						Parts: []geminiPart{
							{
								FunctionCall: &geminiFunctionCall{
									Name: "get_weather",
									Args: map[string]any{"location": "NYC"},
								},
							},
						},
					},
				},
			},
			UsageMetadata: &geminiUsageMetadata{TotalTokenCount: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "gemini-pro",
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
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected function name get_weather, got %s", tc.Function.Name)
	}
	if tc.Type != "function" {
		t.Errorf("expected type 'function', got %s", tc.Type)
	}
	// Arguments should be valid JSON.
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("failed to parse tool call arguments: %v", err)
	}
	if args["location"] != "NYC" {
		t.Errorf("expected location=NYC, got %v", args["location"])
	}
}

func TestChatCompletionWithToolResponse(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify that a tool message was translated to functionResponse.
		foundFuncResponse := false
		for _, c := range req.Contents {
			for _, part := range c.Parts {
				if part.FunctionResponse != nil {
					foundFuncResponse = true
					if part.FunctionResponse.Name != "get_weather" {
						t.Errorf("expected function response name get_weather, got %s", part.FunctionResponse.Name)
					}
				}
			}
		}
		if !foundFuncResponse {
			t.Error("expected a functionResponse part in the request")
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role:  "model",
						Parts: []geminiPart{{Text: "The weather in NYC is sunny."}},
					},
				},
			},
			UsageMetadata: &geminiUsageMetadata{TotalTokenCount: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "gemini-pro",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "What's the weather in NYC?"},
			{
				Role:    provider.RoleAssistant,
				Content: "",
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_123",
						Type: "function",
						Function: provider.FunctionCall{
							Name:      "get_weather",
							Arguments: `{"location":"NYC"}`,
						},
					},
				},
			},
			{
				Role:       provider.RoleTool,
				Content:    `{"temp":"72F","condition":"sunny"}`,
				ToolCallID: "call_123",
				Name:       "get_weather",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChatCompletionAPIError(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Rate limit exceeded",
				"status":  "RESOURCE_EXHAUSTED",
				"code":    429,
			},
		})
	})

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gemini-pro",
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
		// Verify streaming endpoint path.
		expectedPath := "/models/gemini-pro:streamGenerateContent"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %s, got %s", expectedPath, r.URL.Path)
		}
		// Verify alt=sse query parameter.
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("expected query param alt=sse, got %q", r.URL.Query().Get("alt"))
		}
		// Verify API key in query parameter.
		if r.URL.Query().Get("key") != "test-api-key" {
			t.Errorf("expected query param key=test-api-key, got %q", r.URL.Query().Get("key"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("server does not support flushing")
		}

		chunks := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"!"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
	})

	stream, err := p.StreamChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gemini-pro",
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

	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	// First chunk has content.
	if chunks[0].Delta.Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", chunks[0].Delta.Content)
	}

	// Second chunk has content.
	if chunks[1].Delta.Content != " world" {
		t.Errorf("expected ' world', got %q", chunks[1].Delta.Content)
	}

	// Third chunk has content and Done + Usage.
	if chunks[2].Delta.Content != "!" {
		t.Errorf("expected '!', got %q", chunks[2].Delta.Content)
	}
	if !chunks[2].Done {
		t.Error("expected Done=true on final chunk")
	}
	if chunks[2].Usage == nil {
		t.Fatal("expected usage on final chunk")
	}
	if chunks[2].Usage.TotalTokens != 7 {
		t.Errorf("expected 7 total tokens, got %d", chunks[2].Usage.TotalTokens)
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
		// Verify API key in query parameter.
		if r.URL.Query().Get("key") != "test-api-key" {
			t.Errorf("expected query param key=test-api-key, got %q", r.URL.Query().Get("key"))
		}

		resp := geminiModelsResponse{
			Models: []geminiModel{
				{Name: "models/gemini-pro", DisplayName: "Gemini Pro"},
				{Name: "models/gemini-pro-vision", DisplayName: "Gemini Pro Vision"},
				{Name: "models/gemini-ultra", DisplayName: "Gemini Ultra"},
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
		if m.Provider != "gemini" {
			t.Errorf("expected provider 'gemini', got %q", m.Provider)
		}
	}
	// Model IDs should have the "models/" prefix stripped.
	if models[0].ID != "gemini-pro" {
		t.Errorf("expected first model ID 'gemini-pro', got %s", models[0].ID)
	}
	if models[0].Name != "Gemini Pro" {
		t.Errorf("expected first model name 'Gemini Pro', got %s", models[0].Name)
	}
}

func TestWithBaseURL(t *testing.T) {
	p := New("key", WithBaseURL("https://custom.api.com/v2/"))
	if p.baseURL != "https://custom.api.com/v2" {
		t.Errorf("expected trailing slash stripped, got %q", p.baseURL)
	}
}

func TestWithHTTPClient(t *testing.T) {
	customClient := &http.Client{}
	p := New("key", WithHTTPClient(customClient))
	if p.client != customClient {
		t.Error("expected custom HTTP client to be set")
	}
}

func TestChatCompletionNoCandidates(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "gemini-pro",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty candidates, got nil")
	}
}
