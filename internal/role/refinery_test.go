package role

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

// --- Refinery test helpers ---

func testerMockResponse() *provider.ChatResponse {
	return &provider.ChatResponse{
		ID:    "refine-001",
		Model: "mock-model",
		Message: provider.Message{
			Role:    provider.RoleAssistant,
			Content: "// Refined: improved naming and error handling\nfunc Hello() string { return \"hello\" }",
		},
		Usage: provider.Usage{
			PromptTokens:     120,
			CompletionTokens: 60,
			TotalTokens:      180,
		},
		Done: true,
	}
}

// --- Constructor tests ---

func TestNewTester_Defaults(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "tester", mp)

	r := NewTester(router)

	if r.role != "tester" {
		t.Errorf("expected default role 'refinery', got %q", r.role)
	}
	if r.router != router {
		t.Error("expected router to be set")
	}
	if r.tracker != nil {
		t.Error("expected tracker to be nil by default")
	}
	if r.systemPrompt == "" {
		t.Error("expected non-empty default system prompt")
	}
}

func TestNewTester_CustomOptions(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "custom-tester", mp)

	customPrompt := "You are a Go code polisher."
	tracker := cost.NewTracker(cost.DefaultPricing())

	r := NewTester(router,
		WithTesterRole("custom-tester"),
		WithRefinerySystemPrompt(customPrompt),
		WithRefineryCostTracker(tracker),
	)

	if r.role != "custom-tester" {
		t.Errorf("expected role 'custom-refinery', got %q", r.role)
	}
	if r.systemPrompt != customPrompt {
		t.Errorf("expected custom system prompt, got %q", r.systemPrompt)
	}
	if r.tracker != tracker {
		t.Error("expected tracker to be set")
	}
	if r.Role() != "custom-tester" {
		t.Errorf("Role() returned %q, expected 'custom-refinery'", r.Role())
	}
	if r.SystemPrompt() != customPrompt {
		t.Errorf("SystemPrompt() returned %q, expected %q", r.SystemPrompt(), customPrompt)
	}
}

// --- Refine tests ---

func TestRefine_ReturnsResponseFromRouter(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "tester", mp)

	r := NewTester(router)
	resp, err := r.Refine(context.Background(), "func hello() { return \"hello\" }")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != "refine-001" {
		t.Errorf("expected response ID 'refine-001', got %q", resp.ID)
	}

	// Verify system prompt was injected as first message.
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
}

func TestRefine_RecordsCostWhenTrackerProvided(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "tester", mp)

	tracker := cost.NewTracker(cost.DefaultPricing())
	r := NewTester(router, WithRefineryCostTracker(tracker))

	_, err := r.Refine(context.Background(), "some code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := tracker.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 cost record, got %d", len(records))
	}
	rec := records[0]
	if rec.Role != "tester" {
		t.Errorf("expected cost record role 'refinery', got %q", rec.Role)
	}
	if rec.Model != "mock-model" {
		t.Errorf("expected cost record model 'mock-model', got %q", rec.Model)
	}
	if rec.PromptTokens != 120 {
		t.Errorf("expected 120 prompt tokens, got %d", rec.PromptTokens)
	}
	if rec.CompletionTokens != 60 {
		t.Errorf("expected 60 completion tokens, got %d", rec.CompletionTokens)
	}
}

func TestRefine_WithoutTrackerDoesNotPanic(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "tester", mp)

	r := NewTester(router) // no tracker

	resp, err := r.Refine(context.Background(), "some code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

// --- RefineWithFeedback tests ---

func TestRefineWithFeedback_IncludesInputAndFeedback(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "tester", mp)

	r := NewTester(router)
	resp, err := r.RefineWithFeedback(context.Background(), "func foo() {}", "improve error handling")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify both input and feedback are present in the user message.
	if mp.lastReq == nil {
		t.Fatal("provider did not receive a request")
	}
	if len(mp.lastReq.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(mp.lastReq.Messages))
	}
	userContent := mp.lastReq.Messages[1].Content
	if !strings.Contains(userContent, "func foo() {}") {
		t.Errorf("user message should contain the input code, got %q", userContent)
	}
	if !strings.Contains(userContent, "improve error handling") {
		t.Errorf("user message should contain the feedback, got %q", userContent)
	}
}

// --- Summarize tests ---

func TestSummarize_PassesContentToModel(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "tester", mp)

	r := NewTester(router)
	resp, err := r.Summarize(context.Background(), "a very long piece of content that needs summarizing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify the content was passed in the user message.
	if mp.lastReq == nil {
		t.Fatal("provider did not receive a request")
	}
	userContent := mp.lastReq.Messages[1].Content
	if !strings.Contains(userContent, "a very long piece of content that needs summarizing") {
		t.Errorf("user message should contain the content to summarize, got %q", userContent)
	}
}

// --- Error propagation tests ---

func TestRefine_PropagatesRouterErrors(t *testing.T) {
	expectedErr := fmt.Errorf("provider unavailable")
	mp := &mockProvider{name: "test", err: expectedErr}
	router := buildTestRouter(t, "tester", mp)

	r := NewTester(router)
	_, err := r.Refine(context.Background(), "some code")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("expected error to contain 'provider unavailable', got %q", err.Error())
	}
}

// --- System prompt tests ---

func TestDefaultRefinerySystemPrompt_Content(t *testing.T) {
	mp := &mockProvider{name: "test", response: testerMockResponse()}
	router := buildTestRouter(t, "tester", mp)

	r := NewTester(router)
	prompt := r.SystemPrompt()

	if prompt == "" {
		t.Fatal("system prompt should not be empty")
	}
	// Verify it mentions refinery/refine/improve/quality.
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "refine") && !strings.Contains(lower, "improve") && !strings.Contains(lower, "quality") {
		t.Errorf("default system prompt should mention refine, improve, or quality, got %q", prompt)
	}
}
