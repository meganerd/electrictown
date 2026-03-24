package event

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuffer is a thread-safe wrapper around bytes.Buffer for testing.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (n int, err error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func (sb *safeBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Len()
}

func TestCLISubscriberTaskStarted(t *testing.T) {
	buf := &safeBuffer{}
	cli := NewCLISubscriber(buf, buf)

	bus := NewBus()
	sub := bus.Subscribe()

	go cli.Run(sub)

	bus.Emit(TaskStarted, TaskStartedData{
		Task:       "build a thing",
		ConfigPath: "/etc/et.yaml",
		LogDir:     "/tmp/logs",
		Version:    "1.0.0",
	})
	bus.Close()

	output := waitForSafeOutput(buf, 100)

	if !strings.Contains(output, "electrictown 1.0.0") {
		t.Fatalf("expected version header, got:\n%s", output)
	}
	if !strings.Contains(output, "Config: /etc/et.yaml") {
		t.Fatalf("expected config path, got:\n%s", output)
	}
	if !strings.Contains(output, "Task:   build a thing") {
		t.Fatalf("expected task, got:\n%s", output)
	}
}

func TestCLISubscriberPhaseStarted(t *testing.T) {
	buf := &safeBuffer{}
	cli := NewCLISubscriber(buf, buf)

	bus := NewBus()
	sub := bus.Subscribe()

	go cli.Run(sub)

	bus.Emit(PhaseStarted, PhaseData{
		Name: "Phase 1: Supervisor (mayor) decomposing task...",
	})
	bus.Close()

	output := waitForSafeOutput(buf, 100)

	if !strings.Contains(output, "Phase 1") {
		t.Fatalf("expected phase header, got:\n%s", output)
	}
}

func TestCLISubscriberSubtaskDecomposed(t *testing.T) {
	buf := &safeBuffer{}
	cli := NewCLISubscriber(buf, buf)

	bus := NewBus()
	sub := bus.Subscribe()

	go cli.Run(sub)

	bus.Emit(SubtaskDecomposed, SubtaskDecomposedData{
		Subtasks: []string{"write tests", "implement feature", "update docs"},
		HasDeps:  true,
	})
	bus.Close()

	output := waitForSafeOutput(buf, 100)

	if !strings.Contains(output, "Subtasks: 3") {
		t.Fatalf("expected subtask count, got:\n%s", output)
	}
	if !strings.Contains(output, "[1] write tests") {
		t.Fatalf("expected subtask listing, got:\n%s", output)
	}
	if !strings.Contains(output, "Dependencies detected") {
		t.Fatalf("expected dependency note, got:\n%s", output)
	}
}

func TestCLISubscriberSpecialistAssigned(t *testing.T) {
	buf := &safeBuffer{}
	cli := NewCLISubscriber(buf, buf)

	bus := NewBus()
	sub := bus.Subscribe()

	go cli.Run(sub)

	bus.Emit(SpecialistAssigned, SpecialistAssignedData{
		Index:      0,
		Specialist: "frontend-dev",
		Model:      "gpt-4o",
	})
	bus.Emit(SpecialistAssigned, SpecialistAssignedData{
		Index:      1,
		Specialist: "",
		Model:      "",
	})
	bus.Close()

	output := waitForSafeOutput(buf, 100)

	if !strings.Contains(output, "[1] → frontend-dev (gpt-4o)") {
		t.Fatalf("expected specialist assignment, got:\n%s", output)
	}
	if !strings.Contains(output, "[2] → general-default") {
		t.Fatalf("expected general fallback, got:\n%s", output)
	}
}

// waitForSafeOutput polls the thread-safe buffer for content.
func waitForSafeOutput(buf *safeBuffer, maxTicks int) string {
	for i := 0; i < maxTicks; i++ {
		if buf.Len() > 0 {
			// Give a bit more time for remaining writes.
			time.Sleep(5 * time.Millisecond)
			return buf.String()
		}
		time.Sleep(time.Millisecond)
	}
	return buf.String()
}
