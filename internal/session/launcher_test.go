package session

import (
	"context"
	"strings"
	"testing"
	"time"
)

// newTestSession creates a session with the given adapter command for launcher tests.
func newTestSession(cmd string, args []string, env map[string]string, timeout time.Duration) (*SessionLauncher, *Session) {
	adapter := &mockAdapter{
		name:      "test",
		builtCmd:  cmd,
		builtArgs: args,
	}
	launcher := NewSessionLauncher(adapter)

	if env == nil {
		env = map[string]string{}
	}

	sess := &Session{
		ID:   "test-session-001",
		Role: "polecat",
		Config: &SessionConfig{
			Provider: "test",
			Role:     "polecat",
			Command:  cmd,
			Args:     args,
			Env:      env,
			WorkDir:  "/tmp",
			Timeout:  timeout,
		},
		Status: StatusPending,
		Prompt: "test prompt",
	}

	launcher.mu.Lock()
	launcher.sessions[sess.ID] = sess
	launcher.mu.Unlock()

	return launcher, sess
}

func TestExecute_Success(t *testing.T) {
	launcher, sess := newTestSession("echo", []string{"hello world"}, nil, 5*time.Second)

	err := launcher.Execute(context.Background(), sess)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if sess.Status != StatusDone {
		t.Errorf("expected status %q, got %q", StatusDone, sess.Status)
	}

	output := sess.Output.String()
	if !strings.Contains(output, "hello world") {
		t.Errorf("expected output to contain 'hello world', got: %q", output)
	}
}

func TestExecute_Failure(t *testing.T) {
	launcher, sess := newTestSession("false", nil, nil, 5*time.Second)

	err := launcher.Execute(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error from failing command")
	}

	if sess.Status != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, sess.Status)
	}
}

func TestExecute_Timeout(t *testing.T) {
	launcher, sess := newTestSession("sleep", []string{"10"}, nil, 200*time.Millisecond)

	start := time.Now()
	err := launcher.Execute(context.Background(), sess)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timeout")
	}

	if sess.Status != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, sess.Status)
	}

	if !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context deadline exceeded error, got: %v", err)
	}

	// Should complete well before the 10 second sleep.
	if elapsed > 3*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestExecute_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	launcher, sess := newTestSession("sleep", []string{"10"}, nil, 0)

	done := make(chan error, 1)
	go func() {
		done <- launcher.Execute(ctx, sess)
	}()

	// Give the process time to start.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from cancelled context")
		}
		if sess.Status != StatusFailed {
			t.Errorf("expected status %q, got %q", StatusFailed, sess.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Execute to return after cancel")
	}
}

func TestExecute_EnvVars(t *testing.T) {
	customEnv := map[string]string{
		"ET_TEST_VAR_ALPHA": "alpha_value",
		"ET_TEST_VAR_BETA":  "beta_value",
	}
	launcher, sess := newTestSession("sh", []string{"-c", "printenv ET_TEST_VAR_ALPHA && printenv ET_TEST_VAR_BETA"}, customEnv, 5*time.Second)

	err := launcher.Execute(context.Background(), sess)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := sess.Output.String()
	if !strings.Contains(output, "alpha_value") {
		t.Errorf("expected output to contain 'alpha_value', got: %q", output)
	}
	if !strings.Contains(output, "beta_value") {
		t.Errorf("expected output to contain 'beta_value', got: %q", output)
	}
}

func TestExecuteAsync(t *testing.T) {
	launcher, sess := newTestSession("sh", []string{"-c", "sleep 0.1 && echo async_done"}, nil, 5*time.Second)

	// Should return immediately.
	start := time.Now()
	launcher.ExecuteAsync(context.Background(), sess)
	callDuration := time.Since(start)

	if callDuration > 500*time.Millisecond {
		t.Errorf("ExecuteAsync should return immediately, took %v", callDuration)
	}

	// Wait for the async operation to complete.
	deadline := time.After(3 * time.Second)
	for {
		sess.mu.Lock()
		status := sess.Status
		sess.mu.Unlock()

		if status == StatusDone || status == StatusFailed {
			break
		}

		select {
		case <-deadline:
			t.Fatalf("timed out waiting for async session to complete, status: %q", sess.Status)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	if sess.Status != StatusDone {
		t.Errorf("expected status %q, got %q", StatusDone, sess.Status)
	}

	output := sess.Output.String()
	if !strings.Contains(output, "async_done") {
		t.Errorf("expected output to contain 'async_done', got: %q", output)
	}
}

func TestStop(t *testing.T) {
	launcher, sess := newTestSession("sleep", []string{"10"}, nil, 0)

	done := make(chan error, 1)
	go func() {
		done <- launcher.Execute(context.Background(), sess)
	}()

	// Give the process time to start.
	time.Sleep(100 * time.Millisecond)

	err := launcher.Stop(sess.ID)
	if err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	select {
	case execErr := <-done:
		if execErr == nil {
			t.Fatal("expected error after Stop")
		}
		if sess.Status != StatusFailed {
			t.Errorf("expected status %q, got %q", StatusFailed, sess.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Execute to return after Stop")
	}
}

func TestStop_NotFound(t *testing.T) {
	adapter := &mockAdapter{name: "test"}
	launcher := NewSessionLauncher(adapter)

	err := launcher.Stop("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for stopping nonexistent session")
	}
	if !strings.Contains(err.Error(), "no running session") {
		t.Errorf("expected 'no running session' error, got: %v", err)
	}
}

func TestExecute_StatusTransitions(t *testing.T) {
	// Use a command with a small delay so we can observe status transitions.
	launcher, sess := newTestSession("sh", []string{"-c", "echo transitioning"}, nil, 5*time.Second)

	// Verify initial status.
	if sess.Status != StatusPending {
		t.Errorf("expected initial status %q, got %q", StatusPending, sess.Status)
	}

	err := launcher.Execute(context.Background(), sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After successful execution, status should be done.
	if sess.Status != StatusDone {
		t.Errorf("expected final status %q, got %q", StatusDone, sess.Status)
	}
}

func TestExecute_NonexistentBinary(t *testing.T) {
	launcher, sess := newTestSession("/nonexistent/binary/path", nil, nil, 5*time.Second)

	err := launcher.Execute(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error from nonexistent binary")
	}

	if sess.Status != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, sess.Status)
	}
}

func TestExecute_StderrCapture(t *testing.T) {
	launcher, sess := newTestSession("sh", []string{"-c", "echo stdout_msg && echo stderr_msg >&2"}, nil, 5*time.Second)

	err := launcher.Execute(context.Background(), sess)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := sess.Output.String()
	if !strings.Contains(output, "stdout_msg") {
		t.Errorf("expected stdout_msg in output, got: %q", output)
	}
	if !strings.Contains(output, "stderr_msg") {
		t.Errorf("expected stderr_msg in output, got: %q", output)
	}
}
