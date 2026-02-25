package provider

import (
	"context"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock provider
// ---------------------------------------------------------------------------

// mockProvider implements Provider for testing. Each method can be overridden
// with a closure; if the closure is nil the method returns a sensible zero value
// or an error.
type mockProvider struct {
	name           string
	chatFn         func(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	streamFn       func(ctx context.Context, req *ChatRequest) (ChatStream, error)
	listModelsFn   func(ctx context.Context) ([]Model, error)
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, req)
	}
	return &ChatResponse{ID: "mock", Model: req.Model, Done: true}, nil
}

func (m *mockProvider) StreamChatCompletion(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	return &mockStream{model: req.Model}, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]Model, error) {
	if m.listModelsFn != nil {
		return m.listModelsFn(ctx)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Mock stream
// ---------------------------------------------------------------------------

type mockStream struct {
	model string
	done  bool
}

func (s *mockStream) Next() (*ChatStreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return &ChatStreamChunk{
		ID:    "chunk-1",
		Model: s.model,
		Delta: MessageDelta{Content: "hello"},
		Done:  true,
	}, nil
}

func (s *mockStream) Close() error { return nil }

// ---------------------------------------------------------------------------
// Test config and helpers
// ---------------------------------------------------------------------------

// routerTestConfig returns a Config that wires:
//   - provider "primary" (type=mock-primary)   -> model alias "model-a" -> actual model "real-model-a"
//   - provider "fallback" (type=mock-fallback)  -> model alias "model-b" -> actual model "real-model-b"
//   - role "leader"  -> primary=model-a, fallbacks=[model-b]
//   - role "worker"  -> primary=model-a, no fallbacks
func routerTestConfig() *Config {
	return &Config{
		Providers: map[string]ProviderConfig{
			"primary": {
				Type:    "mock-primary",
				BaseURL: "http://primary",
				APIKey:  "pk",
			},
			"fallback": {
				Type:    "mock-fallback",
				BaseURL: "http://fallback",
				APIKey:  "fk",
			},
		},
		Models: map[string]ModelConfig{
			"model-a": {Provider: "primary", Model: "real-model-a"},
			"model-b": {Provider: "fallback", Model: "real-model-b"},
		},
		Roles: map[string]RoleConfig{
			"leader": {Model: "model-a", Fallbacks: []string{"model-b"}},
			"worker": {Model: "model-a"},
		},
		Defaults: DefaultsConfig{
			Model: "model-a",
		},
	}
}

// newTestRouter creates a Router with the provided mock providers wired to the
// factory types expected by routerTestConfig.
func newTestRouter(t *testing.T, primary, fallback *mockProvider) *Router {
	t.Helper()
	cfg := routerTestConfig()
	factories := map[string]ProviderFactory{
		"mock-primary": func(_ ProviderConfig) (Provider, error) {
			return primary, nil
		},
		"mock-fallback": func(_ ProviderConfig) (Provider, error) {
			return fallback, nil
		},
	}
	r, err := NewRouter(cfg, factories)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRouterChatCompletion(t *testing.T) {
	primary := &mockProvider{name: "primary"}
	fallback := &mockProvider{name: "fallback"}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{
		Model:    "model-a",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}
	resp, err := r.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("ChatCompletion error: %v", err)
	}
	if resp.Model != "real-model-a" {
		t.Errorf("expected model real-model-a, got %s", resp.Model)
	}
}

func TestRouterChatCompletionForRole(t *testing.T) {
	var calledModel string
	primary := &mockProvider{
		name: "primary",
		chatFn: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			calledModel = req.Model
			return &ChatResponse{ID: "ok", Model: req.Model, Done: true}, nil
		},
	}
	fallback := &mockProvider{name: "fallback"}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "test"}}}
	resp, err := r.ChatCompletionForRole(context.Background(), "leader", req)
	if err != nil {
		t.Fatalf("ChatCompletionForRole error: %v", err)
	}
	if calledModel != "real-model-a" {
		t.Errorf("expected primary to be called with real-model-a, got %s", calledModel)
	}
	if resp.ID != "ok" {
		t.Errorf("expected response ID ok, got %s", resp.ID)
	}
}

func TestRouterFallbackOnRateLimit(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		chatFn: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return nil, &APIError{Status: 429, Code: "rate_limit", Message: "too many requests"}
		},
	}
	var fallbackModel string
	fallback := &mockProvider{
		name: "fallback",
		chatFn: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			fallbackModel = req.Model
			return &ChatResponse{ID: "fb-ok", Model: req.Model, Done: true}, nil
		},
	}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "please"}}}
	resp, err := r.ChatCompletionForRole(context.Background(), "leader", req)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if resp.ID != "fb-ok" {
		t.Errorf("expected fallback response, got ID %s", resp.ID)
	}
	if fallbackModel != "real-model-b" {
		t.Errorf("expected fallback model real-model-b, got %s", fallbackModel)
	}
}

func TestRouterFallbackOnServerError(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		chatFn: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return nil, &APIError{Status: 500, Code: "server_error", Message: "internal server error"}
		},
	}
	fallback := &mockProvider{
		name: "fallback",
		chatFn: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{ID: "fb-500", Model: req.Model, Done: true}, nil
		},
	}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "help"}}}
	resp, err := r.ChatCompletionForRole(context.Background(), "leader", req)
	if err != nil {
		t.Fatalf("expected fallback on 500, got error: %v", err)
	}
	if resp.ID != "fb-500" {
		t.Errorf("expected fb-500, got %s", resp.ID)
	}
}

func TestRouterNoFallbackOnAuthError(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		chatFn: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return nil, &APIError{Status: 401, Code: "auth", Message: "unauthorized"}
		},
	}
	fallbackCalled := false
	fallback := &mockProvider{
		name: "fallback",
		chatFn: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			fallbackCalled = true
			return &ChatResponse{ID: "should-not-reach", Done: true}, nil
		},
	}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "secret"}}}
	_, err := r.ChatCompletionForRole(context.Background(), "leader", req)
	if err == nil {
		t.Fatal("expected auth error to propagate, got nil")
	}
	if fallbackCalled {
		t.Error("fallback should NOT be attempted for auth errors")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 401 {
		t.Errorf("expected status 401, got %d", apiErr.Status)
	}
}

func TestRouterFallbackExhausted(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		chatFn: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return nil, &APIError{Status: 429, Code: "rate_limit", Message: "rate limited"}
		},
	}
	fallback := &mockProvider{
		name: "fallback",
		chatFn: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return nil, &APIError{Status: 500, Code: "server_error", Message: "fallback also down"}
		},
	}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "help"}}}
	_, err := r.ChatCompletionForRole(context.Background(), "leader", req)
	if err == nil {
		t.Fatal("expected exhausted fallback error, got nil")
	}
	expected := "all fallbacks exhausted"
	if !containsSubstring(err.Error(), expected) {
		t.Errorf("expected error containing %q, got: %v", expected, err)
	}
}

func TestRouterStreamChatCompletionForRole(t *testing.T) {
	var streamedModel string
	primary := &mockProvider{
		name: "primary",
		streamFn: func(_ context.Context, req *ChatRequest) (ChatStream, error) {
			streamedModel = req.Model
			return &mockStream{model: req.Model}, nil
		},
	}
	fallback := &mockProvider{name: "fallback"}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "stream me"}}}
	stream, err := r.StreamChatCompletionForRole(context.Background(), "leader", req)
	if err != nil {
		t.Fatalf("StreamChatCompletionForRole error: %v", err)
	}
	defer stream.Close()

	if streamedModel != "real-model-a" {
		t.Errorf("expected stream model real-model-a, got %s", streamedModel)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("stream.Next error: %v", err)
	}
	if chunk.Delta.Content != "hello" {
		t.Errorf("expected chunk content hello, got %s", chunk.Delta.Content)
	}

	// Second call should return EOF.
	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestRouterStreamFallbackOnError(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		streamFn: func(_ context.Context, _ *ChatRequest) (ChatStream, error) {
			return nil, &APIError{Status: 429, Code: "rate_limit", Message: "rate limited"}
		},
	}
	var fallbackStreamModel string
	fallback := &mockProvider{
		name: "fallback",
		streamFn: func(_ context.Context, req *ChatRequest) (ChatStream, error) {
			fallbackStreamModel = req.Model
			return &mockStream{model: req.Model}, nil
		},
	}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "stream fallback"}}}
	stream, err := r.StreamChatCompletionForRole(context.Background(), "leader", req)
	if err != nil {
		t.Fatalf("expected stream fallback to succeed, got error: %v", err)
	}
	defer stream.Close()

	if fallbackStreamModel != "real-model-b" {
		t.Errorf("expected fallback stream model real-model-b, got %s", fallbackStreamModel)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("fallback stream.Next error: %v", err)
	}
	if chunk.Model != "real-model-b" {
		t.Errorf("expected chunk model real-model-b, got %s", chunk.Model)
	}
}

func TestRouterStreamNoFallbackOnAuthError(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		streamFn: func(_ context.Context, _ *ChatRequest) (ChatStream, error) {
			return nil, &APIError{Status: 403, Code: "forbidden", Message: "forbidden"}
		},
	}
	fallbackCalled := false
	fallback := &mockProvider{
		name: "fallback",
		streamFn: func(_ context.Context, _ *ChatRequest) (ChatStream, error) {
			fallbackCalled = true
			return &mockStream{}, nil
		},
	}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "forbidden stream"}}}
	_, err := r.StreamChatCompletionForRole(context.Background(), "leader", req)
	if err == nil {
		t.Fatal("expected auth error to propagate for stream, got nil")
	}
	if fallbackCalled {
		t.Error("stream fallback should NOT be attempted for auth errors (403)")
	}
}

func TestRouterStreamFallbackExhausted(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		streamFn: func(_ context.Context, _ *ChatRequest) (ChatStream, error) {
			return nil, &APIError{Status: 500, Code: "server_error", Message: "primary down"}
		},
	}
	fallback := &mockProvider{
		name: "fallback",
		streamFn: func(_ context.Context, _ *ChatRequest) (ChatStream, error) {
			return nil, &APIError{Status: 500, Code: "server_error", Message: "fallback also down"}
		},
	}
	r := newTestRouter(t, primary, fallback)

	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "all down"}}}
	_, err := r.StreamChatCompletionForRole(context.Background(), "leader", req)
	if err == nil {
		t.Fatal("expected exhausted stream fallback error, got nil")
	}
	expected := "all stream fallbacks exhausted"
	if !containsSubstring(err.Error(), expected) {
		t.Errorf("expected error containing %q, got: %v", expected, err)
	}
}

func TestRouterNoFallbacksConfigured(t *testing.T) {
	primary := &mockProvider{
		name: "primary",
		chatFn: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			return nil, &APIError{Status: 429, Code: "rate_limit", Message: "rate limited"}
		},
	}
	fallback := &mockProvider{name: "fallback"}
	r := newTestRouter(t, primary, fallback)

	// "worker" role has no fallbacks configured.
	req := &ChatRequest{Messages: []Message{{Role: RoleUser, Content: "no fallbacks"}}}
	_, err := r.ChatCompletionForRole(context.Background(), "worker", req)
	if err == nil {
		t.Fatal("expected error when no fallbacks configured, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 429 {
		t.Errorf("expected original 429 error returned, got status %d", apiErr.Status)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && searchSubstring(s, sub)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
