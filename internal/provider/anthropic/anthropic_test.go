package anthropic

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

// newTestServer creates an httptest server that records requests and responds
// with the given status and body.
func newTestServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	var captured *http.Request
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(capturedBody)))
		captured = r
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	// Return a pointer that will be populated after the request is made.
	// The caller must dereference after calling ChatCompletion.
	return srv, captured
}

func TestName(t *testing.T) {
	p := New("test-key")
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q, want %q", p.Name(), "anthropic")
	}
}

func TestChatCompletion_BasicRequest(t *testing.T) {
	var capturedReq anthropicRequest
	var capturedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := anthropicResponse{
			ID:   "msg_test123",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Hello! How can I help?"},
			},
			Model:      "claude-sonnet-4-20250514",
			StopReason: "end_turn",
			Usage:      anthropicUsage{InputTokens: 10, OutputTokens: 20},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("test-api-key", WithBaseURL(srv.URL))
	temp := 0.7
	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "Hello"},
		},
		Temperature: &temp,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify headers.
	if got := capturedHeaders.Get("x-api-key"); got != "test-api-key" {
		t.Errorf("x-api-key = %q, want %q", got, "test-api-key")
	}
	if got := capturedHeaders.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want %q", got, "2023-06-01")
	}
	if got := capturedHeaders.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	// Verify max_tokens default.
	if capturedReq.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want %d", capturedReq.MaxTokens, 4096)
	}

	// Verify response conversion.
	if resp.ID != "msg_test123" {
		t.Errorf("ID = %q, want %q", resp.ID, "msg_test123")
	}
	if resp.Message.Content != "Hello! How can I help?" {
		t.Errorf("Content = %q, want %q", resp.Message.Content, "Hello! How can I help?")
	}
	if resp.Message.Role != provider.RoleAssistant {
		t.Errorf("Role = %q, want %q", resp.Message.Role, provider.RoleAssistant)
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want %d", resp.Usage.PromptTokens, 10)
	}
	if resp.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want %d", resp.Usage.CompletionTokens, 20)
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want %d", resp.Usage.TotalTokens, 30)
	}
	if !resp.Done {
		t.Error("Done = false, want true")
	}
}

func TestChatCompletion_SystemMessageExtraction(t *testing.T) {
	var capturedReq anthropicRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := anthropicResponse{
			ID:      "msg_sys",
			Type:    "message",
			Role:    "assistant",
			Content: []anthropicContentBlock{{Type: "text", Text: "OK"}},
			Model:   "claude-sonnet-4-20250514",
			Usage:   anthropicUsage{InputTokens: 5, OutputTokens: 2},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "You are a helpful assistant."},
			{Role: provider.RoleSystem, Content: "Be concise."},
			{Role: provider.RoleUser, Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// System messages should be extracted to the top-level system field.
	expectedSystem := "You are a helpful assistant.\n\nBe concise."
	if capturedReq.System != expectedSystem {
		t.Errorf("system = %q, want %q", capturedReq.System, expectedSystem)
	}

	// Messages array should NOT contain system messages.
	if len(capturedReq.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(capturedReq.Messages))
	}
	if capturedReq.Messages[0].Role != "user" {
		t.Errorf("messages[0].role = %q, want %q", capturedReq.Messages[0].Role, "user")
	}
}

func TestChatCompletion_MaxTokensOverride(t *testing.T) {
	var capturedReq anthropicRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := anthropicResponse{
			ID:      "msg_tok",
			Content: []anthropicContentBlock{{Type: "text", Text: "OK"}},
			Model:   "claude-sonnet-4-20250514",
			Usage:   anthropicUsage{InputTokens: 1, OutputTokens: 1},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	maxTok := 8192
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:     "claude-sonnet-4-20250514",
		Messages:  []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
		MaxTokens: &maxTok,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.MaxTokens != 8192 {
		t.Errorf("max_tokens = %d, want %d", capturedReq.MaxTokens, 8192)
	}
}

func TestChatCompletion_ToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			ID:   "msg_tool",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Let me look that up."},
				{
					Type:  "tool_use",
					ID:    "toolu_123",
					Name:  "get_weather",
					Input: map[string]interface{}{"location": "San Francisco"},
				},
			},
			Model:      "claude-sonnet-4-20250514",
			StopReason: "tool_use",
			Usage:      anthropicUsage{InputTokens: 50, OutputTokens: 30},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Weather?"}},
		Tools: []provider.Tool{{
			Type: "function",
			Function: provider.ToolFunction{
				Name:        "get_weather",
				Description: "Get current weather",
				Parameters:  map[string]interface{}{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Message.Content != "Let me look that up." {
		t.Errorf("Content = %q, want %q", resp.Message.Content, "Let me look that up.")
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls count = %d, want 1", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "toolu_123" {
		t.Errorf("ToolCall.ID = %q, want %q", tc.ID, "toolu_123")
	}
	if tc.Type != "function" {
		t.Errorf("ToolCall.Type = %q, want %q", tc.Type, "function")
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("ToolCall.Function.Name = %q, want %q", tc.Function.Name, "get_weather")
	}

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if args["location"] != "San Francisco" {
		t.Errorf("location = %v, want %q", args["location"], "San Francisco")
	}
}

func TestChatCompletion_ToolResultMessage(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)

		resp := anthropicResponse{
			ID:      "msg_toolres",
			Content: []anthropicContentBlock{{Type: "text", Text: "It's 72F."}},
			Model:   "claude-sonnet-4-20250514",
			Usage:   anthropicUsage{InputTokens: 10, OutputTokens: 5},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "Weather?"},
			{Role: provider.RoleAssistant, Content: "", ToolCalls: []provider.ToolCall{{
				ID: "toolu_123", Type: "function",
				Function: provider.FunctionCall{Name: "get_weather", Arguments: `{"location":"SF"}`},
			}}},
			{Role: provider.RoleTool, ToolCallID: "toolu_123", Content: `{"temp":72}`},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tool result is sent as user message with tool_result content block.
	var raw map[string]interface{}
	json.Unmarshal(capturedBody, &raw)
	messages := raw["messages"].([]interface{})

	// Third message (index 2) should be the tool result converted to user role.
	toolMsg := messages[2].(map[string]interface{})
	if toolMsg["role"] != "user" {
		t.Errorf("tool result role = %v, want %q", toolMsg["role"], "user")
	}

	content := toolMsg["content"].([]interface{})
	block := content[0].(map[string]interface{})
	if block["type"] != "tool_result" {
		t.Errorf("block type = %v, want %q", block["type"], "tool_result")
	}
	if block["tool_use_id"] != "toolu_123" {
		t.Errorf("tool_use_id = %v, want %q", block["tool_use_id"], "toolu_123")
	}
}

func TestChatCompletion_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"max_tokens: must be at least 1"}}`)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
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
		t.Errorf("Status = %d, want %d", apiErr.Status, 400)
	}
	if apiErr.Type != "invalid_request_error" {
		t.Errorf("Type = %q, want %q", apiErr.Type, "invalid_request_error")
	}
	if !strings.Contains(apiErr.Message, "max_tokens") {
		t.Errorf("Message = %q, want it to contain 'max_tokens'", apiErr.Message)
	}
}

func TestChatCompletion_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()

	p := New("bad-key", WithBaseURL(srv.URL))
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})

	apiErr, ok := err.(*provider.APIError)
	if !ok {
		t.Fatalf("expected *provider.APIError, got %T", err)
	}
	if apiErr.Status != 401 {
		t.Errorf("Status = %d, want %d", apiErr.Status, 401)
	}

	// Verify error classification works.
	code := provider.ClassifyError(apiErr)
	if code != provider.ErrAuth {
		t.Errorf("ClassifyError = %q, want %q", code, provider.ErrAuth)
	}
}

func TestStreamChatCompletion(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	stream, err := p.StreamChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
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
			t.Fatalf("stream error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	// We expect: text_delta "Hello", text_delta " world", message_delta (usage), message_stop (done).
	if len(chunks) < 4 {
		t.Fatalf("expected at least 4 chunks, got %d", len(chunks))
	}

	// First text chunk.
	if chunks[0].Delta.Content != "Hello" {
		t.Errorf("chunk[0].Content = %q, want %q", chunks[0].Delta.Content, "Hello")
	}
	if chunks[0].ID != "msg_stream" {
		t.Errorf("chunk[0].ID = %q, want %q", chunks[0].ID, "msg_stream")
	}

	// Second text chunk.
	if chunks[1].Delta.Content != " world" {
		t.Errorf("chunk[1].Content = %q, want %q", chunks[1].Delta.Content, " world")
	}

	// Usage chunk (message_delta).
	if chunks[2].Usage == nil {
		t.Fatal("chunk[2].Usage is nil, expected usage data")
	}
	if chunks[2].Usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want %d", chunks[2].Usage.CompletionTokens, 5)
	}

	// Done chunk (message_stop).
	lastChunk := chunks[len(chunks)-1]
	if !lastChunk.Done {
		t.Error("last chunk Done = false, want true")
	}
}

func TestStreamChatCompletion_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"Rate limited"}}`)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	_, err := p.StreamChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
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
		t.Errorf("Status = %d, want %d", apiErr.Status, 429)
	}

	code := provider.ClassifyError(apiErr)
	if code != provider.ErrRateLimit {
		t.Errorf("ClassifyError = %q, want %q", code, provider.ErrRateLimit)
	}
}

func TestListModels(t *testing.T) {
	p := New("key")
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(models) == 0 {
		t.Fatal("expected non-empty model list")
	}

	// Verify all models have correct provider.
	for _, m := range models {
		if m.Provider != "anthropic" {
			t.Errorf("model %q has provider %q, want %q", m.ID, m.Provider, "anthropic")
		}
		if m.ID == "" {
			t.Error("model has empty ID")
		}
		if m.Name == "" {
			t.Error("model has empty Name")
		}
	}

	// Check that claude-sonnet-4-20250514 is present.
	found := false
	for _, m := range models {
		if m.ID == "claude-sonnet-4-20250514" {
			found = true
			break
		}
	}
	if !found {
		t.Error("claude-sonnet-4-20250514 not found in model list")
	}
}

func TestWithBaseURL(t *testing.T) {
	p := New("key", WithBaseURL("http://custom:1234"))
	if p.baseURL != "http://custom:1234" {
		t.Errorf("baseURL = %q, want %q", p.baseURL, "http://custom:1234")
	}
}

func TestWithHTTPClient(t *testing.T) {
	custom := &http.Client{}
	p := New("key", WithHTTPClient(custom))
	if p.client != custom {
		t.Error("custom HTTP client not set")
	}
}

func TestToolsTranslation(t *testing.T) {
	var capturedReq anthropicRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := anthropicResponse{
			ID:      "msg_tools",
			Content: []anthropicContentBlock{{Type: "text", Text: "OK"}},
			Model:   "claude-sonnet-4-20250514",
			Usage:   anthropicUsage{InputTokens: 1, OutputTokens: 1},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string"},
		},
	}
	_, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Search"}},
		Tools: []provider.Tool{{
			Type: "function",
			Function: provider.ToolFunction{
				Name:        "search",
				Description: "Search the web",
				Parameters:  params,
			},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tool translation: parameters -> input_schema.
	if len(capturedReq.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(capturedReq.Tools))
	}
	tool := capturedReq.Tools[0]
	if tool.Name != "search" {
		t.Errorf("tool.Name = %q, want %q", tool.Name, "search")
	}
	if tool.Description != "Search the web" {
		t.Errorf("tool.Description = %q, want %q", tool.Description, "Search the web")
	}
	if tool.InputSchema == nil {
		t.Error("tool.InputSchema is nil, expected parameters to be mapped")
	}
}

func TestMultipleTextBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			ID:   "msg_multi",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "First part. "},
				{Type: "text", Text: "Second part."},
			},
			Model: "claude-sonnet-4-20250514",
			Usage: anthropicUsage{InputTokens: 5, OutputTokens: 10},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	resp, err := p.ChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Multiple text blocks should be concatenated.
	expected := "First part. Second part."
	if resp.Message.Content != expected {
		t.Errorf("Content = %q, want %q", resp.Message.Content, expected)
	}
}

func TestStreamChatCompletion_ToolUse(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_stool","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_abc","name":"get_weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","input":"{\"location\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","input":"\"NYC\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":10,"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer srv.Close()

	p := New("key", WithBaseURL(srv.URL))
	stream, err := p.StreamChatCompletion(context.Background(), &provider.ChatRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Weather in NYC?"}},
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
			t.Fatalf("stream error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	// First chunk should be tool_use start with ID and name.
	if len(chunks) < 1 {
		t.Fatal("expected at least 1 chunk")
	}
	if len(chunks[0].Delta.ToolCalls) != 1 {
		t.Fatalf("chunk[0] ToolCalls count = %d, want 1", len(chunks[0].Delta.ToolCalls))
	}
	if chunks[0].Delta.ToolCalls[0].ID != "toolu_abc" {
		t.Errorf("tool call ID = %q, want %q", chunks[0].Delta.ToolCalls[0].ID, "toolu_abc")
	}
	if chunks[0].Delta.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool call name = %q, want %q", chunks[0].Delta.ToolCalls[0].Function.Name, "get_weather")
	}

	// Subsequent chunks should have argument fragments.
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}
	if len(chunks[1].Delta.ToolCalls) != 1 {
		t.Fatalf("chunk[1] ToolCalls count = %d, want 1", len(chunks[1].Delta.ToolCalls))
	}
	if chunks[1].Delta.ToolCalls[0].Function.Arguments != `{"location":` {
		t.Errorf("chunk[1] arguments = %q, want %q", chunks[1].Delta.ToolCalls[0].Function.Arguments, `{"location":`)
	}
}

// Verify the compile-time interface check.
func TestProviderInterface(t *testing.T) {
	var _ provider.Provider = (*AnthropicProvider)(nil)
}
