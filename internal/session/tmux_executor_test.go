package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// mockRunner implements tmux.Runner for testing TmuxExecutor.
type mockRunner struct {
	sessions   map[string]string // name -> captured pane content
	newErr     error
	sendErr    error
	captureErr error
	listErr    error
	killErr    error
}

func newMockRunner() *mockRunner {
	return &mockRunner{
		sessions: make(map[string]string),
	}
}

func (m *mockRunner) NewSession(name, command, workDir string) error {
	if m.newErr != nil {
		return m.newErr
	}
	m.sessions[name] = ""
	return nil
}

func (m *mockRunner) SendKeys(name, text string) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	if _, ok := m.sessions[name]; !ok {
		return fmt.Errorf("session %q not found", name)
	}
	return nil
}

func (m *mockRunner) CapturePane(name string) (string, error) {
	if m.captureErr != nil {
		return "", m.captureErr
	}
	content, ok := m.sessions[name]
	if !ok {
		return "", fmt.Errorf("session %q not found", name)
	}
	return content, nil
}

func (m *mockRunner) ListSessions() ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var names []string
	for name := range m.sessions {
		names = append(names, name)
	}
	return names, nil
}

func (m *mockRunner) KillSession(name string) error {
	if m.killErr != nil {
		return m.killErr
	}
	if _, ok := m.sessions[name]; !ok {
		return fmt.Errorf("session %q not found", name)
	}
	delete(m.sessions, name)
	return nil
}

func (m *mockRunner) HasSession(name string) bool {
	_, ok := m.sessions[name]
	return ok
}

// --- TmuxExecutor tests ---

func TestTmuxExecutor_Execute_CreatesSession(t *testing.T) {
	runner := newMockRunner()
	adapter := &mockAdapter{name: "test"}
	executor := NewTmuxExecutor(runner, adapter)

	sess := &Session{
		ID:   "abcdef1234567890",
		Role: "polecat",
		Config: &SessionConfig{
			Provider: "test",
			Role:     "polecat",
			Command:  "et",
			Args:     []string{"run"},
			Env:      map[string]string{},
			WorkDir:  "/tmp",
		},
		Status: StatusPending,
		Prompt: "fix the bug",
	}

	err := executor.Execute(context.Background(), sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sess.Status != StatusRunning {
		t.Errorf("expected status %q, got %q", StatusRunning, sess.Status)
	}

	// Verify tmux session was created with correct name pattern.
	expectedName := "et-polecat-abcd"
	if !runner.HasSession(expectedName) {
		t.Errorf("expected tmux session %q to exist", expectedName)
	}

	// Verify output contains tmux session name.
	output := sess.Output.String()
	if !strings.Contains(output, expectedName) {
		t.Errorf("expected output to contain %q, got: %q", expectedName, output)
	}
}

func TestTmuxExecutor_Execute_CollisionError(t *testing.T) {
	runner := newMockRunner()
	// Pre-create a session that would collide.
	runner.sessions["et-polecat-abcd"] = ""

	adapter := &mockAdapter{name: "test"}
	executor := NewTmuxExecutor(runner, adapter)

	sess := &Session{
		ID:   "abcdef1234567890",
		Role: "polecat",
		Config: &SessionConfig{
			Provider: "test",
			Role:     "polecat",
			Command:  "et",
			Env:      map[string]string{},
		},
		Status: StatusPending,
		Prompt: "task",
	}

	err := executor.Execute(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error from name collision")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
	if sess.Status != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, sess.Status)
	}
}

func TestTmuxExecutor_Execute_CreateError(t *testing.T) {
	runner := newMockRunner()
	runner.newErr = fmt.Errorf("tmux not available")

	adapter := &mockAdapter{name: "test"}
	executor := NewTmuxExecutor(runner, adapter)

	sess := &Session{
		ID:   "abcdef1234567890",
		Role: "polecat",
		Config: &SessionConfig{
			Provider: "test",
			Role:     "polecat",
			Command:  "et",
			Env:      map[string]string{},
		},
		Status: StatusPending,
		Prompt: "task",
	}

	err := executor.Execute(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error from failed session creation")
	}
	if sess.Status != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, sess.Status)
	}
}

func TestTmuxExecutor_Stop(t *testing.T) {
	runner := newMockRunner()
	runner.sessions["et-polecat-abcd"] = ""

	adapter := &mockAdapter{name: "test"}
	executor := NewTmuxExecutor(runner, adapter)

	err := executor.Stop("abcdef1234567890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.HasSession("et-polecat-abcd") {
		t.Error("expected tmux session to be killed")
	}
}

func TestTmuxExecutor_Stop_NotFound(t *testing.T) {
	runner := newMockRunner()
	adapter := &mockAdapter{name: "test"}
	executor := NewTmuxExecutor(runner, adapter)

	err := executor.Stop("nonexistent12345")
	if err == nil {
		t.Fatal("expected error for stopping nonexistent session")
	}
	if !strings.Contains(err.Error(), "no tmux session found") {
		t.Errorf("expected 'no tmux session found' error, got: %v", err)
	}
}

func TestTmuxExecutor_SessionNaming(t *testing.T) {
	tests := []struct {
		id       string
		role     string
		expected string
	}{
		{"abcdef1234567890", "polecat", "et-polecat-abcd"},
		{"1234567890abcdef", "mayor", "et-mayor-1234"},
		{"ff00", "crew", "et-crew-ff00"},
		{"ab", "witness", "et-witness-ab"},
	}

	for _, tt := range tests {
		runner := newMockRunner()
		adapter := &mockAdapter{name: "test"}
		executor := NewTmuxExecutor(runner, adapter)

		sess := &Session{
			ID:   tt.id,
			Role: tt.role,
			Config: &SessionConfig{
				Provider: "test",
				Role:     tt.role,
				Command:  "et",
				Env:      map[string]string{},
			},
			Status: StatusPending,
			Prompt: "task",
		}

		err := executor.Execute(context.Background(), sess)
		if err != nil {
			t.Fatalf("id=%q role=%q: unexpected error: %v", tt.id, tt.role, err)
		}

		if !runner.HasSession(tt.expected) {
			t.Errorf("id=%q role=%q: expected session %q, got sessions: %v",
				tt.id, tt.role, tt.expected, runner.sessions)
		}
	}
}

func TestTmuxExecutor_WaitForReady_Delay(t *testing.T) {
	runner := newMockRunner()
	adapter := &mockAdapter{
		name: "test",
		readiness: ReadinessStrategy{
			Type:  "delay",
			Delay: 50 * time.Millisecond,
		},
	}
	executor := NewTmuxExecutor(runner, adapter)

	sess := &Session{
		ID:   "abcdef1234567890",
		Role: "polecat",
		Config: &SessionConfig{
			Provider: "test",
			Role:     "polecat",
		},
		Status: StatusRunning,
	}

	start := time.Now()
	err := executor.WaitForReady(context.Background(), sess)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if elapsed < 40*time.Millisecond {
		t.Errorf("expected at least 40ms delay, got %v", elapsed)
	}
}

func TestTmuxExecutor_WaitForReady_ContextCancel(t *testing.T) {
	runner := newMockRunner()
	adapter := &mockAdapter{
		name: "test",
		readiness: ReadinessStrategy{
			Type:  "delay",
			Delay: 10 * time.Second,
		},
	}
	executor := NewTmuxExecutor(runner, adapter)

	sess := &Session{
		ID:   "abcdef1234567890",
		Role: "polecat",
		Config: &SessionConfig{
			Provider: "test",
			Role:     "polecat",
		},
		Status: StatusRunning,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := executor.WaitForReady(ctx, sess)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}

func TestTmuxExecutor_ImplementsExecutor(t *testing.T) {
	var _ Executor = (*TmuxExecutor)(nil)
}

func TestNewSessionLauncherWithExecutor(t *testing.T) {
	runner := newMockRunner()
	adapter := &mockAdapter{name: "test"}
	executor := NewTmuxExecutor(runner, adapter)

	launcher := NewSessionLauncherWithExecutor(adapter, executor)
	if launcher == nil {
		t.Fatal("expected non-nil launcher")
	}
	if launcher.exec == nil {
		t.Fatal("expected executor to be set")
	}
}
