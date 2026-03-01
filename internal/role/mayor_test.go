package role

import (
	"context"
	"errors"
	"testing"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

// --- Constructor tests ---

func TestNewMayor_Defaults(t *testing.T) {
	mock := &mockProvider{name: "test"}
	router := buildTestRouter(t, "mayor", mock)

	m := NewMayor(router)

	if m.router != router {
		t.Error("expected router to be set")
	}
	if m.role != "mayor" {
		t.Errorf("expected default role 'mayor', got %q", m.role)
	}
	if m.maxSubtasks != 10 {
		t.Errorf("expected default maxSubtasks 10, got %d", m.maxSubtasks)
	}
	if m.systemPrompt == "" {
		t.Error("expected non-empty default system prompt")
	}
	if m.tracker != nil {
		t.Error("expected nil tracker by default")
	}
}

func TestNewMayor_CustomOptions(t *testing.T) {
	mock := &mockProvider{name: "test"}
	router := buildTestRouter(t, "supervisor", mock)
	tracker := cost.NewTracker(nil)

	m := NewMayor(router,
		WithMayorRole("supervisor"),
		WithMayorSystemPrompt("custom prompt"),
		WithMayorCostTracker(tracker),
		WithMayorMaxSubtasks(10),
	)

	if m.role != "supervisor" {
		t.Errorf("expected role 'supervisor', got %q", m.role)
	}
	if m.systemPrompt != "custom prompt" {
		t.Errorf("expected custom system prompt, got %q", m.systemPrompt)
	}
	if m.tracker != tracker {
		t.Error("expected tracker to be set")
	}
	if m.maxSubtasks != 10 {
		t.Errorf("expected maxSubtasks 10, got %d", m.maxSubtasks)
	}
}

// --- Decompose tests ---

func TestDecompose_ReturnsParsedSubtasks(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		response: &provider.ChatResponse{
			ID:    "resp-1",
			Model: "mock-v1",
			Message: provider.Message{
				Role:    provider.RoleAssistant,
				Content: "1. Set up the database schema\n2. Create the API endpoints\n3. Write integration tests",
			},
			Usage: provider.Usage{PromptTokens: 50, CompletionTokens: 30, TotalTokens: 80},
			Done:  true,
		},
	}
	router := buildTestRouter(t, "mayor", mock)
	m := NewMayor(router)

	subtasks, err := m.Decompose(context.Background(), "Build a REST API")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(subtasks) != 3 {
		t.Fatalf("expected 3 subtasks, got %d: %v", len(subtasks), subtasks)
	}

	expected := []string{
		"Set up the database schema",
		"Create the API endpoints",
		"Write integration tests",
	}
	for i, want := range expected {
		if subtasks[i] != want {
			t.Errorf("subtask[%d]: expected %q, got %q", i, want, subtasks[i])
		}
	}
}

func TestDecompose_RespectsMaxSubtasks(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		response: &provider.ChatResponse{
			ID:    "resp-2",
			Model: "mock-v1",
			Message: provider.Message{
				Role: provider.RoleAssistant,
				Content: "1. Task one\n2. Task two\n3. Task three\n4. Task four\n5. Task five\n" +
					"6. Task six\n7. Task seven",
			},
			Usage: provider.Usage{PromptTokens: 50, CompletionTokens: 70, TotalTokens: 120},
			Done:  true,
		},
	}
	router := buildTestRouter(t, "mayor", mock)
	m := NewMayor(router, WithMayorMaxSubtasks(3))

	subtasks, err := m.Decompose(context.Background(), "Build everything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(subtasks) != 3 {
		t.Errorf("expected max 3 subtasks, got %d: %v", len(subtasks), subtasks)
	}
}

func TestDecompose_RecordsCostWhenTrackerProvided(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		response: &provider.ChatResponse{
			ID:    "resp-3",
			Model: "mock-v1",
			Message: provider.Message{
				Role:    provider.RoleAssistant,
				Content: "1. Single task",
			},
			Usage: provider.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			Done:  true,
		},
	}
	router := buildTestRouter(t, "mayor", mock)
	tracker := cost.NewTracker(nil)
	m := NewMayor(router, WithMayorCostTracker(tracker))

	_, err := m.Decompose(context.Background(), "Simple task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := tracker.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 cost record, got %d", len(records))
	}
	if records[0].Role != "mayor" {
		t.Errorf("expected role 'mayor', got %q", records[0].Role)
	}
	if records[0].TotalTokens != 150 {
		t.Errorf("expected 150 total tokens, got %d", records[0].TotalTokens)
	}
}

func TestDecompose_PropagatesRouterErrors(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		err:  errors.New("provider unavailable"),
	}
	router := buildTestRouter(t, "mayor", mock)
	m := NewMayor(router)

	_, err := m.Decompose(context.Background(), "Some task")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "provider unavailable" {
		t.Errorf("expected 'provider unavailable' error, got: %v", err)
	}
}

// --- Synthesize tests ---

func TestSynthesize_CombinesWorkerResults(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		response: &provider.ChatResponse{
			ID:    "resp-4",
			Model: "mock-v1",
			Message: provider.Message{
				Role:    provider.RoleAssistant,
				Content: "Combined: The schema is ready, the endpoints are live, and tests pass.",
			},
			Usage: provider.Usage{PromptTokens: 200, CompletionTokens: 100, TotalTokens: 300},
			Done:  true,
		},
	}
	router := buildTestRouter(t, "mayor", mock)
	m := NewMayor(router)

	results := []WorkerResult{
		{Role: "polecat", Subtask: "Create schema", Response: "Schema created with users and posts tables.", Tokens: 50},
		{Role: "polecat", Subtask: "Build endpoints", Response: "REST endpoints for CRUD operations are ready.", Tokens: 60},
	}

	synthesis, err := m.Synthesize(context.Background(), "Build a REST API", results)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if synthesis == "" {
		t.Error("expected non-empty synthesis response")
	}

	// Verify the request sent to the model includes worker results.
	if mock.lastReq == nil {
		t.Fatal("expected a request to be sent to the provider")
	}
	// Should have system message and user message with worker results.
	if len(mock.lastReq.Messages) < 2 {
		t.Errorf("expected at least 2 messages in request, got %d", len(mock.lastReq.Messages))
	}
}

// --- Plan tests ---

func TestPlan_ReturnsSummaryAndSubtasks(t *testing.T) {
	mock := &mockProvider{
		name: "test",
		response: &provider.ChatResponse{
			ID:    "resp-5",
			Model: "mock-v1",
			Message: provider.Message{
				Role: provider.RoleAssistant,
				Content: "## Summary\nWe will build a REST API in three phases.\n\n## Subtasks\n" +
					"1. Design the database schema\n2. Implement CRUD endpoints\n3. Add integration tests",
			},
			Usage: provider.Usage{PromptTokens: 80, CompletionTokens: 60, TotalTokens: 140},
			Done:  true,
		},
	}
	router := buildTestRouter(t, "mayor", mock)
	m := NewMayor(router)

	plan, err := m.Plan(context.Background(), "Build a REST API")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.Summary == "" {
		t.Error("expected non-empty plan summary")
	}
	if len(plan.Subtasks) == 0 {
		t.Error("expected at least one subtask in plan")
	}
}

// --- ParseSubtasks tests ---

func TestParseSubtasks_NumberedList(t *testing.T) {
	input := "1. First task\n2. Second task\n3. Third task"
	result := ParseSubtasks(input)

	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(result), result)
	}

	expected := []string{"First task", "Second task", "Third task"}
	for i, want := range expected {
		if result[i] != want {
			t.Errorf("item[%d]: expected %q, got %q", i, want, result[i])
		}
	}
}

func TestParseSubtasks_BulletLists(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "dash bullets",
			input: "- Alpha\n- Beta\n- Gamma",
			want:  []string{"Alpha", "Beta", "Gamma"},
		},
		{
			name:  "asterisk bullets",
			input: "* One thing\n* Another thing",
			want:  []string{"One thing", "Another thing"},
		},
		{
			name:  "unicode bullets",
			input: "\u2022 Bullet one\n\u2022 Bullet two",
			want:  []string{"Bullet one", "Bullet two"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSubtasks(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d items, got %d: %v", len(tc.want), len(got), got)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("item[%d]: expected %q, got %q", i, want, got[i])
				}
			}
		})
	}
}

func TestParseSubtasks_MixedFormats(t *testing.T) {
	input := "1. Numbered item\n- Dash item\n* Star item\n2) Paren item"
	result := ParseSubtasks(input)

	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d: %v", len(result), result)
	}

	expected := []string{"Numbered item", "Dash item", "Star item", "Paren item"}
	for i, want := range expected {
		if result[i] != want {
			t.Errorf("item[%d]: expected %q, got %q", i, want, result[i])
		}
	}
}

func TestParseSubtasks_EmptyInput(t *testing.T) {
	result := ParseSubtasks("")
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}

	result = ParseSubtasks("   \n  \n  ")
	if len(result) != 0 {
		t.Errorf("expected empty result for whitespace input, got %v", result)
	}
}
