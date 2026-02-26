package pool

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/meganerd/electrictown/internal/provider"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	name   string
	chatFn func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error)
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) ChatCompletion(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, req)
	}
	return &provider.ChatResponse{
		ID:      "mock",
		Model:   req.Model,
		Message: provider.Message{Role: provider.RoleAssistant, Content: "response for: " + req.Messages[len(req.Messages)-1].Content},
		Usage:   provider.Usage{TotalTokens: 100},
		Done:    true,
	}, nil
}

func (m *mockProvider) StreamChatCompletion(ctx context.Context, req *provider.ChatRequest) (provider.ChatStream, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return nil, nil
}

// testConfig builds a Config with N mock providers, one model alias per provider.
func testConfig(aliases []string) *provider.Config {
	providers := make(map[string]provider.ProviderConfig)
	models := make(map[string]provider.ModelConfig)
	for i, alias := range aliases {
		provName := fmt.Sprintf("prov-%d", i)
		providers[provName] = provider.ProviderConfig{
			Type:    fmt.Sprintf("mock-%d", i),
			BaseURL: fmt.Sprintf("http://host-%d", i),
		}
		models[alias] = provider.ModelConfig{
			Provider: provName,
			Model:    fmt.Sprintf("real-model-%d", i),
		}
	}
	return &provider.Config{
		Providers: providers,
		Models:    models,
		Roles:     map[string]provider.RoleConfig{},
		Defaults:  provider.DefaultsConfig{Model: aliases[0]},
	}
}

// newTestRouter creates a Router with mock providers that echo back request model names.
func newTestRouter(t *testing.T, aliases []string, chatFn func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error)) *provider.Router {
	t.Helper()
	cfg := testConfig(aliases)
	factories := make(map[string]provider.ProviderFactory)
	for i := range aliases {
		mockType := fmt.Sprintf("mock-%d", i)
		mp := &mockProvider{name: mockType, chatFn: chatFn}
		factories[mockType] = func(pc provider.ProviderConfig) (provider.Provider, error) {
			return mp, nil
		}
	}
	r, err := provider.NewRouter(cfg, factories)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func TestExecuteAll_Basic(t *testing.T) {
	aliases := []string{"model-a", "model-b", "model-c"}
	router := newTestRouter(t, aliases, nil)
	balancer := provider.NewBalancer(provider.StrategyRoundRobin)

	wp := New(router, balancer, aliases)
	subtasks := []string{"task 1", "task 2", "task 3"}

	results := wp.ExecuteAll(context.Background(), subtasks, "you are a worker")

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify results are in subtask order.
	for i, r := range results {
		expected := fmt.Sprintf("task %d", i+1)
		if r.Subtask != expected {
			t.Errorf("result[%d].Subtask = %q, want %q", i, r.Subtask, expected)
		}
		if r.Response == "" {
			t.Errorf("result[%d].Response is empty", i)
		}
		if r.Role == "" {
			t.Errorf("result[%d].Role is empty", i)
		}
	}
}

func TestExecuteAll_OrderPreserved(t *testing.T) {
	aliases := []string{"model-a", "model-b"}
	router := newTestRouter(t, aliases, func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		userMsg := req.Messages[len(req.Messages)-1].Content
		return &provider.ChatResponse{
			ID:      "ok",
			Model:   req.Model,
			Message: provider.Message{Role: provider.RoleAssistant, Content: "done:" + userMsg},
			Usage:   provider.Usage{TotalTokens: 50},
			Done:    true,
		}, nil
	})
	balancer := provider.NewBalancer(provider.StrategyRoundRobin)

	wp := New(router, balancer, aliases)
	subtasks := []string{"alpha", "bravo", "charlie", "delta", "echo"}

	results := wp.ExecuteAll(context.Background(), subtasks, "sys")

	for i, r := range results {
		if r.Subtask != subtasks[i] {
			t.Errorf("result[%d].Subtask = %q, want %q", i, r.Subtask, subtasks[i])
		}
		if !strings.Contains(r.Response, subtasks[i]) {
			t.Errorf("result[%d].Response = %q, expected to contain %q", i, r.Response, subtasks[i])
		}
	}
}

func TestExecuteAll_PartialFailure(t *testing.T) {
	aliases := []string{"model-a"}
	// Fail any request whose user message contains "fail-me".
	router := newTestRouter(t, aliases, func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		userMsg := req.Messages[len(req.Messages)-1].Content
		if strings.Contains(userMsg, "fail-me") {
			return nil, fmt.Errorf("model unavailable")
		}
		return &provider.ChatResponse{
			ID:      "ok",
			Model:   req.Model,
			Message: provider.Message{Role: provider.RoleAssistant, Content: "success"},
			Usage:   provider.Usage{TotalTokens: 100},
			Done:    true,
		}, nil
	})
	balancer := provider.NewBalancer(provider.StrategyRoundRobin)

	wp := New(router, balancer, aliases)
	subtasks := []string{"task-1", "fail-me", "task-3"}

	results := wp.ExecuteAll(context.Background(), subtasks, "sys")

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// "fail-me" (index 1) should have failed.
	if !strings.Contains(results[1].Response, "error:") {
		t.Errorf("expected error in result[1], got: %s", results[1].Response)
	}
	// Others should have succeeded.
	if results[0].Response != "success" {
		t.Errorf("expected success in result[0], got: %s", results[0].Response)
	}
	if results[2].Response != "success" {
		t.Errorf("expected success in result[2], got: %s", results[2].Response)
	}
}

func TestExecuteAll_BoundedConcurrency(t *testing.T) {
	aliases := []string{"model-a", "model-b"} // pool of 2
	var maxConcurrent int32
	var current int32

	router := newTestRouter(t, aliases, func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		c := atomic.AddInt32(&current, 1)
		defer atomic.AddInt32(&current, -1)

		// Track the maximum concurrent value observed.
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if c <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, c) {
				break
			}
		}

		return &provider.ChatResponse{
			ID:      "ok",
			Model:   req.Model,
			Message: provider.Message{Role: provider.RoleAssistant, Content: "done"},
			Usage:   provider.Usage{TotalTokens: 10},
			Done:    true,
		}, nil
	})
	balancer := provider.NewBalancer(provider.StrategyRoundRobin)

	wp := New(router, balancer, aliases)
	// 5 subtasks with pool of 2 — concurrency should be capped at 2.
	subtasks := []string{"a", "b", "c", "d", "e"}

	results := wp.ExecuteAll(context.Background(), subtasks, "sys")

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	mc := atomic.LoadInt32(&maxConcurrent)
	if mc > 2 {
		t.Errorf("max concurrency was %d, expected <= 2 (pool size)", mc)
	}
}

func TestExecuteAll_EmptySubtasks(t *testing.T) {
	aliases := []string{"model-a"}
	router := newTestRouter(t, aliases, nil)
	balancer := provider.NewBalancer(provider.StrategyRoundRobin)

	wp := New(router, balancer, aliases)
	results := wp.ExecuteAll(context.Background(), nil, "sys")

	if len(results) != 0 {
		t.Errorf("expected 0 results for empty subtasks, got %d", len(results))
	}
}

func TestExecuteAll_SingleSubtask(t *testing.T) {
	aliases := []string{"model-a", "model-b", "model-c"}
	router := newTestRouter(t, aliases, nil)
	balancer := provider.NewBalancer(provider.StrategyRoundRobin)

	wp := New(router, balancer, aliases)
	results := wp.ExecuteAll(context.Background(), []string{"only-one"}, "sys")

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Subtask != "only-one" {
		t.Errorf("expected subtask 'only-one', got %q", results[0].Subtask)
	}
}
