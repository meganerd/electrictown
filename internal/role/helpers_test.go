package role

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/meganerd/electrictown/internal/provider"
)

// mockProvider implements provider.Provider for testing across all role tests.
type mockProvider struct {
	name     string
	response *provider.ChatResponse
	stream   provider.ChatStream
	err      error
	// lastReq captures the last request sent to this provider for assertions.
	lastReq *provider.ChatRequest
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) ChatCompletion(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *mockProvider) StreamChatCompletion(_ context.Context, req *provider.ChatRequest) (provider.ChatStream, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	if m.stream != nil {
		return m.stream, nil
	}
	return nil, errors.New("stream not configured on mock")
}

func (m *mockProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}

// mockStream implements provider.ChatStream for testing streaming responses.
type mockStream struct {
	chunks []*provider.ChatStreamChunk
	idx    int
}

func (s *mockStream) Next() (*provider.ChatStreamChunk, error) {
	if s.idx >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.idx]
	s.idx++
	return chunk, nil
}

func (s *mockStream) Close() error { return nil }

// buildTestRouter creates a Router with a mock provider wired to the given role.
func buildTestRouter(t *testing.T, roleName string, mock *mockProvider) *provider.Router {
	t.Helper()

	cfg := &provider.Config{
		Providers: map[string]provider.ProviderConfig{
			"test": {Type: "test", BaseURL: "http://localhost", APIKey: "key"},
		},
		Models: map[string]provider.ModelConfig{
			"test-model": {Provider: "test", Model: "mock-model"},
		},
		Roles: map[string]provider.RoleConfig{
			roleName: {Model: "test-model"},
		},
		Defaults: provider.DefaultsConfig{Model: "test-model"},
	}

	factories := map[string]provider.ProviderFactory{
		"test": func(_ provider.ProviderConfig) (provider.Provider, error) {
			return mock, nil
		},
	}

	router, err := provider.NewRouter(cfg, factories)
	if err != nil {
		t.Fatalf("failed to create test router: %v", err)
	}
	return router
}
