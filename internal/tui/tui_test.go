package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/meganerd/electrictown/internal/event"
)

func newTestModel() Model {
	bus := event.NewBus()
	sub := bus.Subscribe()
	return New(sub, "test task", "/etc/et.yaml", "pool: 3 workers", "1.1.0")
}

func newInputModel() Model {
	bus := event.NewBus()
	sub := bus.Subscribe()
	return New(sub, "", "/etc/et.yaml", "pool: 3 workers", "1.1.0")
}

func TestModelStartsInInputModeWhenNoTask(t *testing.T) {
	m := newInputModel()
	if m.mode != modeInput {
		t.Fatalf("expected input mode, got %d", m.mode)
	}
}

func TestModelStartsInExecutionModeWithTask(t *testing.T) {
	m := newTestModel()
	if m.mode != modeExecution {
		t.Fatalf("expected execution mode, got %d", m.mode)
	}
}

func TestInputViewRendering(t *testing.T) {
	m := newInputModel()
	m.width = 80
	m.height = 24
	view := m.viewInput()

	if !strings.Contains(view, "electrictown 1.1.0") {
		t.Fatalf("expected version in input view, got:\n%s", view)
	}
	if !strings.Contains(view, "/etc/et.yaml") {
		t.Fatalf("expected config path in input view, got:\n%s", view)
	}
	if !strings.Contains(view, "submit") {
		t.Fatalf("expected help bar in input view, got:\n%s", view)
	}
}

func TestModelHandleTaskStarted(t *testing.T) {
	m := newTestModel()

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
	m := newTestModel()

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
}

func TestModelHandleWorkerLifecycle(t *testing.T) {
	m := newTestModel()

	m.handleEvent(event.New(event.WorkerStarted, event.WorkerData{
		Index: 0, Total: 3, Model: "gpt-4o",
	}))
	if len(m.workers) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(m.workers))
	}
	if m.workers[0].status != "running" {
		t.Fatalf("expected running, got %s", m.workers[0].status)
	}

	m.handleEvent(event.New(event.WorkerProgress, event.WorkerData{
		Index: 0, Content: "writing code...",
	}))
	if m.workers[0].content != "writing code..." {
		t.Fatalf("expected content update, got %s", m.workers[0].content)
	}

	m.handleEvent(event.New(event.WorkerCompleted, event.WorkerData{
		Index: 0, Tokens: 1500, TPS: 45.2, Duration: 3 * time.Second, Score: 8,
	}))
	if m.workers[0].status != "done" {
		t.Fatalf("expected done, got %s", m.workers[0].status)
	}
}

func TestModelHandleCostUpdate(t *testing.T) {
	m := newTestModel()

	m.handleEvent(event.New(event.CostUpdate, event.CostUpdateData{
		TotalTokens:   42000,
		EstimatedCost: 0.15,
	}))

	if m.totalTokens != 42000 {
		t.Fatalf("expected 42000 tokens, got %d", m.totalTokens)
	}
}

func TestModelHandleRunCompleted(t *testing.T) {
	m := newTestModel()

	m.handleEvent(event.New(event.RunCompleted, event.RunCompletedData{
		Duration: 45 * time.Second,
	}))

	if !m.done {
		t.Fatal("expected done=true")
	}
}

func TestPhaseTimeline(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.phases = []phaseRecord{
		{name: "Phase 1: Decompose", duration: 2 * time.Second},
		{name: "Phase 2: Workers", duration: 5 * time.Second},
	}

	timeline := m.renderTimeline()
	if !strings.Contains(timeline, "Phase 1") {
		t.Fatalf("expected Phase 1 in timeline, got:\n%s", timeline)
	}
	if !strings.Contains(timeline, "→") {
		t.Fatalf("expected arrow separator in timeline, got:\n%s", timeline)
	}
}

func TestLogPane(t *testing.T) {
	m := newTestModel()
	m.width = 80

	m.appendLog(event.New(event.PhaseStarted, event.PhaseData{Name: "Phase 1: test"}))
	m.appendLog(event.New(event.WorkerCompleted, event.WorkerData{Index: 0, Tokens: 100, Duration: time.Second}))

	if len(m.logLines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(m.logLines))
	}

	pane := m.renderLogPane()
	if !strings.Contains(pane, "Log") {
		t.Fatalf("expected Log header, got:\n%s", pane)
	}
}

func TestLogPaneCap(t *testing.T) {
	m := newTestModel()
	for i := 0; i < 150; i++ {
		m.appendLog(event.New(event.PhaseStarted, event.PhaseData{Name: "Phase X"}))
	}
	if len(m.logLines) != maxLogLines {
		t.Fatalf("expected %d log lines, got %d", maxLogLines, len(m.logLines))
	}
}

func TestHelpBarContextSensitive(t *testing.T) {
	m := newInputModel()
	help := m.renderHelpBar()
	if !strings.Contains(help, "submit") {
		t.Fatalf("expected 'submit' in input help, got: %s", help)
	}

	m.mode = modeExecution
	help = m.renderHelpBar()
	if !strings.Contains(help, "quit") {
		t.Fatalf("expected 'quit' in execution help, got: %s", help)
	}

	m.done = true
	help = m.renderHelpBar()
	if !strings.Contains(help, "complete") {
		t.Fatalf("expected 'complete' in done help, got: %s", help)
	}
}

func TestExecutionView(t *testing.T) {
	m := newTestModel()
	m.width = 60
	m.height = 24
	m.currentPhase = "Phase 2: Workers executing..."

	m.subtasks = []subtaskState{
		{description: "write tests", status: "done"},
		{description: "implement feature", status: "running"},
	}
	m.workers = []workerState{
		{index: 0, model: "gpt-4o", status: "done", tokens: 500, duration: 2 * time.Second},
		{index: 1, model: "claude-3.7-sonnet", status: "running", content: "coding..."},
	}
	m.phases = []phaseRecord{
		{name: "Phase 1: Decompose", duration: 2 * time.Second},
	}
	m.appendLog(event.New(event.PhaseStarted, event.PhaseData{Name: "Phase 2"}))

	view := m.viewExecution()

	if !strings.Contains(view, "electrictown") {
		t.Fatalf("expected header, got:\n%s", view)
	}
	if !strings.Contains(view, "Phase 1") {
		t.Fatalf("expected timeline, got:\n%s", view)
	}
	if !strings.Contains(view, "Subtasks") {
		t.Fatalf("expected DAG, got:\n%s", view)
	}
	if !strings.Contains(view, "Workers") {
		t.Fatalf("expected workers, got:\n%s", view)
	}
	if !strings.Contains(view, "Log") {
		t.Fatalf("expected log pane, got:\n%s", view)
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

func TestStatusIcons(t *testing.T) {
	for _, status := range []string{"pending", "running", "done", "failed", "retrying", "unknown"} {
		icon := statusIcon(status)
		if icon == "" {
			t.Fatalf("empty icon for status %s", status)
		}
	}
}

func TestSpecialistAssigned(t *testing.T) {
	m := newTestModel()
	m.handleEvent(event.New(event.SubtaskDecomposed, event.SubtaskDecomposedData{
		Subtasks: []string{"frontend work", "backend work"},
	}))
	m.handleEvent(event.New(event.SpecialistAssigned, event.SpecialistAssignedData{
		Index: 0, Specialist: "frontend-dev", Model: "gpt-4o",
	}))
	if m.subtasks[0].specialist != "frontend-dev" {
		t.Fatalf("expected specialist assignment, got %s", m.subtasks[0].specialist)
	}
}
