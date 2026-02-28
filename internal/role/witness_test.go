package role

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

// --- Witness test helpers ---

func defaultWitnessMockResponse() *provider.ChatResponse {
	return &provider.ChatResponse{
		ID:    "resp-w01",
		Model: "mock-model",
		Message: provider.Message{
			Role:    provider.RoleAssistant,
			Content: "LGTM. No critical issues found. Minor: consider adding error handling on line 12.",
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

func TestNewReviewer_Defaults(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)

	w := NewReviewer(router)

	if w.role != "reviewer" {
		t.Errorf("expected default role 'witness', got %q", w.role)
	}
	if w.router != router {
		t.Error("expected router to be set")
	}
	if w.tracker != nil {
		t.Error("expected tracker to be nil by default")
	}
	if w.systemPrompt == "" {
		t.Error("expected non-empty default system prompt")
	}
}

func TestNewReviewer_CustomOptions(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)
	tracker := cost.NewTracker(nil)

	w := NewReviewer(router,
		WithReviewerRole("reviewer"),
		WithWitnessSystemPrompt("custom reviewer prompt"),
		WithWitnessCostTracker(tracker),
	)

	if w.role != "reviewer" {
		t.Errorf("expected role 'reviewer', got %q", w.role)
	}
	if w.systemPrompt != "custom reviewer prompt" {
		t.Errorf("expected custom system prompt, got %q", w.systemPrompt)
	}
	if w.tracker != tracker {
		t.Error("expected tracker to be set")
	}
}

// --- Review tests ---

func TestReview_ReturnsResponseFromRouter(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)

	w := NewReviewer(router)
	resp, err := w.Review(context.Background(), "func Hello() string { return \"hello\" }")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != "resp-w01" {
		t.Errorf("expected response ID 'resp-w01', got %q", resp.ID)
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
	if !strings.Contains(mp.lastReq.Messages[1].Content, "func Hello()") {
		t.Errorf("user message should contain the code to review, got %q", mp.lastReq.Messages[1].Content)
	}
}

func TestReview_RecordsCostWhenTrackerProvided(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)

	tracker := cost.NewTracker(cost.DefaultPricing())
	w := NewReviewer(router, WithWitnessCostTracker(tracker))

	_, err := w.Review(context.Background(), "some code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := tracker.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 cost record, got %d", len(records))
	}
	rec := records[0]
	if rec.Role != "reviewer" {
		t.Errorf("expected cost record role 'witness', got %q", rec.Role)
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

func TestReview_WithoutTrackerDoesNotPanic(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)

	w := NewReviewer(router) // no tracker

	resp, err := w.Review(context.Background(), "some code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

// --- ReviewWithContext tests ---

func TestReviewWithContext_IncludesTaskAndCode(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)

	w := NewReviewer(router)
	resp, err := w.ReviewWithContext(context.Background(), "implement a hello function", "func Hello() string { return \"hello\" }")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify the request includes both task and code.
	if mp.lastReq == nil {
		t.Fatal("provider did not receive a request")
	}
	if len(mp.lastReq.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(mp.lastReq.Messages))
	}
	userContent := mp.lastReq.Messages[1].Content
	if !strings.Contains(userContent, "implement a hello function") {
		t.Errorf("user message should contain the task, got %q", userContent)
	}
	if !strings.Contains(userContent, "func Hello()") {
		t.Errorf("user message should contain the code, got %q", userContent)
	}
}

// --- Validate tests ---

func TestValidate_PassesCriteriaAndOutput(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)

	w := NewReviewer(router)
	resp, err := w.Validate(context.Background(), "must return a string", "func Hello() string { return \"hello\" }")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify the request includes both criteria and output.
	if mp.lastReq == nil {
		t.Fatal("provider did not receive a request")
	}
	if len(mp.lastReq.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(mp.lastReq.Messages))
	}
	userContent := mp.lastReq.Messages[1].Content
	if !strings.Contains(userContent, "must return a string") {
		t.Errorf("user message should contain the criteria, got %q", userContent)
	}
	if !strings.Contains(userContent, "func Hello()") {
		t.Errorf("user message should contain the output, got %q", userContent)
	}
}

// --- Error propagation tests ---

func TestReview_PropagatesRouterErrors(t *testing.T) {
	expectedErr := fmt.Errorf("provider unavailable")
	mp := &mockProvider{name: "test", err: expectedErr}
	router := buildTestRouter(t, "reviewer", mp)

	w := NewReviewer(router)
	_, err := w.Review(context.Background(), "some code")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("expected error to contain 'provider unavailable', got %q", err.Error())
	}
}

// --- System prompt tests ---

func TestDefaultWitnessSystemPrompt_Content(t *testing.T) {
	mp := &mockProvider{name: "test", response: defaultWitnessMockResponse()}
	router := buildTestRouter(t, "reviewer", mp)

	w := NewReviewer(router)
	prompt := w.SystemPrompt()

	if prompt == "" {
		t.Fatal("system prompt should not be empty")
	}
	// Verify it mentions review/code/security.
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "review") && !strings.Contains(lower, "code") {
		t.Errorf("default system prompt should mention review or code, got %q", prompt)
	}
	if !strings.Contains(lower, "security") {
		t.Errorf("default system prompt should mention security, got %q", prompt)
	}
}
