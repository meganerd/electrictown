package role

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

// --- Polecat test helpers ---

func defaultMockResponse() *provider.ChatResponse {
	return &provider.ChatResponse{
		ID:    "resp-001",
		Model: "mock-model",
		Message: provider.Message{
			Role:    provider.RoleAssistant,
			Content: "func Hello() string { return \"hello\" }",
		},
		Usage: provider.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
		Done: true,
	}
}

// --- Constructor tests ---

func TestNewPolecat_Defaults(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "polecat", mp)

	p := NewPolecat(router)

	if p.role != "polecat" {
		t.Errorf("expected default role 'polecat', got %q", p.role)
	}
	if p.router != router {
		t.Error("expected router to be set")
	}
	if p.tracker != nil {
		t.Error("expected tracker to be nil by default")
	}
	if p.systemPrompt == "" {
		t.Error("expected non-empty default system prompt")
	}
}

func TestNewPolecat_WithCustomRole(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "custom-worker", mp)

	p := NewPolecat(router, WithRole("custom-worker"))

	if p.role != "custom-worker" {
		t.Errorf("expected role 'custom-worker', got %q", p.role)
	}
}

func TestNewPolecat_WithCustomSystemPrompt(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "polecat", mp)

	customPrompt := "You are a Go specialist. Write idiomatic Go."
	p := NewPolecat(router, WithSystemPrompt(customPrompt))

	if p.systemPrompt != customPrompt {
		t.Errorf("expected custom system prompt, got %q", p.systemPrompt)
	}
	if p.SystemPrompt() != customPrompt {
		t.Errorf("SystemPrompt() returned %q, expected %q", p.SystemPrompt(), customPrompt)
	}
}

// --- Execute tests ---

func TestExecute_ReturnsResponseFromRouter(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "polecat", mp)

	p := NewPolecat(router)
	resp, err := p.Execute(context.Background(), "write a hello function")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != "resp-001" {
		t.Errorf("expected response ID 'resp-001', got %q", resp.ID)
	}
	if resp.Message.Content != "func Hello() string { return \"hello\" }" {
		t.Errorf("unexpected response content: %q", resp.Message.Content)
	}

	// Verify the system prompt was injected as first message.
	if mp.lastReq == nil {
		t.Fatal("provider did not receive a request")
	}
	if len(mp.lastReq.Messages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(mp.lastReq.Messages))
	}
	if mp.lastReq.Messages[0].Role != provider.RoleSystem {
		t.Errorf("first message role should be system, got %q", mp.lastReq.Messages[0].Role)
	}
	if mp.lastReq.Messages[1].Role != provider.RoleUser {
		t.Errorf("second message role should be user, got %q", mp.lastReq.Messages[1].Role)
	}
	if mp.lastReq.Messages[1].Content != "write a hello function" {
		t.Errorf("user message content mismatch: %q", mp.lastReq.Messages[1].Content)
	}
}

func TestExecute_RecordsCostWhenTrackerProvided(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "polecat", mp)

	tracker := cost.NewTracker(cost.DefaultPricing())
	p := NewPolecat(router, WithCostTracker(tracker))

	_, err := p.Execute(context.Background(), "write something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := tracker.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 cost record, got %d", len(records))
	}
	rec := records[0]
	if rec.Role != "polecat" {
		t.Errorf("expected cost record role 'polecat', got %q", rec.Role)
	}
	if rec.Model != "mock-model" {
		t.Errorf("expected cost record model 'mock-model', got %q", rec.Model)
	}
	if rec.PromptTokens != 100 {
		t.Errorf("expected 100 prompt tokens, got %d", rec.PromptTokens)
	}
	if rec.CompletionTokens != 50 {
		t.Errorf("expected 50 completion tokens, got %d", rec.CompletionTokens)
	}
}

func TestExecute_WithoutTrackerDoesNotPanic(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "polecat", mp)

	p := NewPolecat(router) // no tracker

	resp, err := p.Execute(context.Background(), "do something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

// --- ExecuteStream tests ---

func TestExecuteStream_ReturnsStreamFromRouter(t *testing.T) {
	ms := &mockStream{
		chunks: []*provider.ChatStreamChunk{
			{ID: "chunk-1", Delta: provider.MessageDelta{Content: "func "}, Done: false},
			{ID: "chunk-2", Delta: provider.MessageDelta{Content: "Hello()"}, Done: false},
			{ID: "chunk-3", Delta: provider.MessageDelta{Content: ""}, Done: true, Usage: &provider.Usage{TotalTokens: 50}},
		},
	}
	mp := &mockProvider{name: "test", stream: ms}
	router := buildTestRouter(t, "polecat", mp)

	p := NewPolecat(router)
	stream, err := p.ExecuteStream(context.Background(), "write a hello function")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()

	// Read all chunks.
	var content strings.Builder
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		content.WriteString(chunk.Delta.Content)
	}

	if content.String() != "func Hello()" {
		t.Errorf("expected streamed content 'func Hello()', got %q", content.String())
	}

	// Verify system prompt was injected.
	if mp.lastReq == nil {
		t.Fatal("provider did not receive a request")
	}
	if mp.lastReq.Messages[0].Role != provider.RoleSystem {
		t.Errorf("first message role should be system, got %q", mp.lastReq.Messages[0].Role)
	}
}

// --- ExecuteWithContext tests ---

func TestExecuteWithContext_PassesConversationHistory(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "polecat", mp)

	p := NewPolecat(router)

	history := []provider.Message{
		{Role: provider.RoleUser, Content: "write a hello function"},
		{Role: provider.RoleAssistant, Content: "func Hello() {}"},
		{Role: provider.RoleUser, Content: "add a return value"},
	}

	resp, err := p.ExecuteWithContext(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify the system prompt was prepended and history preserved.
	if mp.lastReq == nil {
		t.Fatal("provider did not receive a request")
	}
	// system + 3 history messages = 4 total
	if len(mp.lastReq.Messages) != 4 {
		t.Fatalf("expected 4 messages (system + 3 history), got %d", len(mp.lastReq.Messages))
	}
	if mp.lastReq.Messages[0].Role != provider.RoleSystem {
		t.Errorf("first message should be system, got %q", mp.lastReq.Messages[0].Role)
	}
	if mp.lastReq.Messages[1].Content != "write a hello function" {
		t.Errorf("second message content mismatch: %q", mp.lastReq.Messages[1].Content)
	}
	if mp.lastReq.Messages[3].Content != "add a return value" {
		t.Errorf("fourth message content mismatch: %q", mp.lastReq.Messages[3].Content)
	}
}

// --- Error propagation tests ---

func TestExecute_PropagatesRouterErrors(t *testing.T) {
	expectedErr := fmt.Errorf("provider unavailable")
	mp := &mockProvider{name: "test", err: expectedErr}
	router := buildTestRouter(t, "polecat", mp)

	p := NewPolecat(router)
	_, err := p.Execute(context.Background(), "do something")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("expected error to contain 'provider unavailable', got %q", err.Error())
	}
}

// --- System prompt tests ---

func TestDefaultSystemPrompt_Content(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultMockResponse()}
	router := buildTestRouter(t, "polecat", mp)

	p := NewPolecat(router)
	prompt := p.SystemPrompt()

	if prompt == "" {
		t.Fatal("system prompt should not be empty")
	}
	// Verify it mentions coding/worker/implement.
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "code") && !strings.Contains(lower, "coding") && !strings.Contains(lower, "implement") {
		t.Errorf("default system prompt should mention coding or implementation, got %q", prompt)
	}
}
