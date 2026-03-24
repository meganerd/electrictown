package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/meganerd/electrictown/internal/event"
)

func TestModelHandleTaskStarted(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)

	m.handleEvent(event.New(event.TaskStarted, event.TaskStartedData{
		Task:       "build the thing",
		ConfigPath: "/etc/et.yaml",
		LogDir:     "/tmp/logs",
		Version:    "1.0.0",
	}))

	if m.version != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %s", m.version)
	}
	if m.task != "build the thing" {
		t.Fatalf("expected task, got %s", m.task)
	}
}

func TestModelHandleSubtaskDecomposed(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)

	m.handleEvent(event.New(event.SubtaskDecomposed, event.SubtaskDecomposedData{
		Subtasks: []string{"a", "b", "c"},
		HasDeps:  true,
	}))

	if len(m.subtasks) != 3 {
		t.Fatalf("expected 3 subtasks, got %d", len(m.subtasks))
	}
	if !m.hasDeps {
		t.Fatal("expected hasDeps=true")
	}
	for _, st := range m.subtasks {
		if st.status != "pending" {
			t.Fatalf("expected pending status, got %s", st.status)
		}
	}
}

func TestModelHandleWorkerLifecycle(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)

	// Start.
	m.handleEvent(event.New(event.WorkerStarted, event.WorkerData{
		Index: 0, Total: 3, Model: "gpt-4o",
	}))
	if len(m.workers) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(m.workers))
	}
	if m.workers[0].status != "running" {
		t.Fatalf("expected running, got %s", m.workers[0].status)
	}

	// Progress.
	m.handleEvent(event.New(event.WorkerProgress, event.WorkerData{
		Index: 0, Content: "writing code...",
	}))
	if m.workers[0].content != "writing code..." {
		t.Fatalf("expected content update, got %s", m.workers[0].content)
	}

	// Complete.
	m.handleEvent(event.New(event.WorkerCompleted, event.WorkerData{
		Index: 0, Tokens: 1500, TPS: 45.2, Duration: 3 * time.Second, Score: 8,
	}))
	if m.workers[0].status != "done" {
		t.Fatalf("expected done, got %s", m.workers[0].status)
	}
	if m.workers[0].tokens != 1500 {
		t.Fatalf("expected 1500 tokens, got %d", m.workers[0].tokens)
	}
}

func TestModelHandleWorkerFailed(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)

	m.handleEvent(event.New(event.WorkerFailed, event.WorkerData{
		Index: 1, Total: 2, Error: "timeout",
	}))

	if m.workers[1].status != "failed" {
		t.Fatalf("expected failed, got %s", m.workers[1].status)
	}
}

func TestModelHandleCostUpdate(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)

	m.handleEvent(event.New(event.CostUpdate, event.CostUpdateData{
		TotalTokens: 42000,
		EstimatedCost: 0.15,
	}))

	if m.totalTokens != 42000 {
		t.Fatalf("expected 42000 tokens, got %d", m.totalTokens)
	}
}

func TestModelHandleRunCompleted(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)

	m.handleEvent(event.New(event.RunCompleted, event.RunCompletedData{
		Duration: 45 * time.Second,
	}))

	if !m.done {
		t.Fatal("expected done=true")
	}
}

func TestModelView(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)
	m.width = 60
	m.height = 24
	m.version = "1.0.0"
	m.task = "test task"
	m.currentPhase = "Phase 2: Workers executing..."

	m.subtasks = []subtaskState{
		{description: "write tests", status: "done"},
		{description: "implement feature", status: "running"},
		{description: "update docs", status: "pending"},
	}

	m.workers = []workerState{
		{index: 0, model: "gpt-4o", status: "done", tokens: 500, duration: 2 * time.Second},
		{index: 1, model: "claude-3.7-sonnet", status: "running", content: "coding..."},
		{index: 2, model: "gemini-2.5-pro", status: "idle"},
	}

	view := m.View()

	if !strings.Contains(view, "electrictown 1.0.0") {
		t.Fatalf("expected version in view, got:\n%s", view)
	}
	if !strings.Contains(view, "test task") {
		t.Fatalf("expected task in view, got:\n%s", view)
	}
	if !strings.Contains(view, "Phase 2") {
		t.Fatalf("expected phase in view, got:\n%s", view)
	}
	if !strings.Contains(view, "Subtasks") {
		t.Fatalf("expected subtasks section in view, got:\n%s", view)
	}
	if !strings.Contains(view, "Workers") {
		t.Fatalf("expected workers section in view, got:\n%s", view)
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.5K"},
		{42000, "42.0K"},
		{1500000, "1.5M"},
	}

	for _, tt := range tests {
		got := formatTokens(tt.input)
		if got != tt.expected {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestStatusIcon(t *testing.T) {
	// Just verify they don't panic.
	for _, status := range []string{"pending", "running", "done", "failed", "retrying", "unknown"} {
		icon := statusIcon(status)
		if icon == "" {
			t.Fatalf("empty icon for status %s", status)
		}
	}
}

func TestSpecialistAssigned(t *testing.T) {
	bus := event.NewBus()
	sub := bus.Subscribe()
	m := New(sub)

	// Set up subtasks first.
	m.handleEvent(event.New(event.SubtaskDecomposed, event.SubtaskDecomposedData{
		Subtasks: []string{"frontend work", "backend work"},
	}))

	m.handleEvent(event.New(event.SpecialistAssigned, event.SpecialistAssignedData{
		Index:      0,
		Specialist: "frontend-dev",
		Model:      "gpt-4o",
	}))

	if m.subtasks[0].specialist != "frontend-dev" {
		t.Fatalf("expected specialist assignment, got %s", m.subtasks[0].specialist)
	}
}
