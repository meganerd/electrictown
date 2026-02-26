package tmux

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// mockCmd records the command and args it was called with, and returns
// a configurable output via a simple shell echo.
type cmdRecorder struct {
	calls []cmdCall
	// output is what the mock command should print to stdout.
	output string
	// fail controls whether the command exits non-zero.
	fail bool
}

type cmdCall struct {
	name string
	args []string
}

func (r *cmdRecorder) makeCmd(name string, args ...string) *exec.Cmd {
	r.calls = append(r.calls, cmdCall{name: name, args: args})
	if r.fail {
		return exec.Command("sh", "-c", fmt.Sprintf("echo %q >&2; exit 1", r.output))
	}
	if r.output != "" {
		return exec.Command("echo", r.output)
	}
	return exec.Command("true")
}

// --- NewSession ---

func TestTmuxRunner_NewSession(t *testing.T) {
	rec := &cmdRecorder{}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	err := runner.NewSession("test-session", "bash", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(rec.calls))
	}

	call := rec.calls[0]
	if call.name != "tmux" {
		t.Errorf("expected command 'tmux', got %q", call.name)
	}

	argsStr := strings.Join(call.args, " ")
	if !strings.Contains(argsStr, "new-session") {
		t.Errorf("expected 'new-session' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "-s test-session") {
		t.Errorf("expected '-s test-session' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "-c /tmp") {
		t.Errorf("expected '-c /tmp' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "bash") {
		t.Errorf("expected 'bash' in args, got: %v", call.args)
	}
}

func TestTmuxRunner_NewSession_NoWorkDir(t *testing.T) {
	rec := &cmdRecorder{}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	err := runner.NewSession("test-session", "bash", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argsStr := strings.Join(rec.calls[0].args, " ")
	if strings.Contains(argsStr, "-c") {
		t.Errorf("expected no '-c' flag when workDir is empty, got: %v", rec.calls[0].args)
	}
}

func TestTmuxRunner_NewSession_Error(t *testing.T) {
	rec := &cmdRecorder{fail: true, output: "duplicate session"}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	err := runner.NewSession("test-session", "bash", "/tmp")
	if err == nil {
		t.Fatal("expected error from failed new-session")
	}
	if !strings.Contains(err.Error(), "tmux new-session") {
		t.Errorf("expected tmux error message, got: %v", err)
	}
}

// --- SendKeys ---

func TestTmuxRunner_SendKeys(t *testing.T) {
	rec := &cmdRecorder{}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	err := runner.SendKeys("test-session", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := rec.calls[0]
	if call.name != "tmux" {
		t.Errorf("expected command 'tmux', got %q", call.name)
	}

	argsStr := strings.Join(call.args, " ")
	if !strings.Contains(argsStr, "send-keys") {
		t.Errorf("expected 'send-keys' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "-t test-session") {
		t.Errorf("expected '-t test-session' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "hello world") {
		t.Errorf("expected 'hello world' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "Enter") {
		t.Errorf("expected 'Enter' in args, got: %v", call.args)
	}
}

// --- CapturePane ---

func TestTmuxRunner_CapturePane(t *testing.T) {
	rec := &cmdRecorder{output: "pane content here"}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	output, err := runner.CapturePane("test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(output, "pane content here") {
		t.Errorf("expected 'pane content here' in output, got: %q", output)
	}

	call := rec.calls[0]
	argsStr := strings.Join(call.args, " ")
	if !strings.Contains(argsStr, "capture-pane") {
		t.Errorf("expected 'capture-pane' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "-p") {
		t.Errorf("expected '-p' in args, got: %v", call.args)
	}
}

// --- ListSessions ---

func TestTmuxRunner_ListSessions(t *testing.T) {
	rec := &cmdRecorder{output: "session1\nsession2\nsession3"}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	sessions, err := runner.ListSessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d: %v", len(sessions), sessions)
	}
	expected := []string{"session1", "session2", "session3"}
	for i, name := range expected {
		if sessions[i] != name {
			t.Errorf("session[%d]: expected %q, got %q", i, name, sessions[i])
		}
	}
}

func TestTmuxRunner_ListSessions_NoServer(t *testing.T) {
	rec := &cmdRecorder{fail: true, output: "no server running"}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	sessions, err := runner.ListSessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions when no server, got %d", len(sessions))
	}
}

// --- KillSession ---

func TestTmuxRunner_KillSession(t *testing.T) {
	rec := &cmdRecorder{}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	err := runner.KillSession("test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := rec.calls[0]
	argsStr := strings.Join(call.args, " ")
	if !strings.Contains(argsStr, "kill-session") {
		t.Errorf("expected 'kill-session' in args, got: %v", call.args)
	}
	if !strings.Contains(argsStr, "-t test-session") {
		t.Errorf("expected '-t test-session' in args, got: %v", call.args)
	}
}

func TestTmuxRunner_KillSession_Error(t *testing.T) {
	rec := &cmdRecorder{fail: true, output: "session not found"}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	err := runner.KillSession("nonexistent")
	if err == nil {
		t.Fatal("expected error from kill-session")
	}
}

// --- HasSession ---

func TestTmuxRunner_HasSession(t *testing.T) {
	rec := &cmdRecorder{}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	exists := runner.HasSession("test-session")
	if !exists {
		t.Error("expected HasSession to return true for successful has-session")
	}

	call := rec.calls[0]
	argsStr := strings.Join(call.args, " ")
	if !strings.Contains(argsStr, "has-session") {
		t.Errorf("expected 'has-session' in args, got: %v", call.args)
	}
}

func TestTmuxRunner_HasSession_NotFound(t *testing.T) {
	rec := &cmdRecorder{fail: true, output: "session not found"}
	runner := NewTmuxRunnerWithCmd(rec.makeCmd)

	exists := runner.HasSession("nonexistent")
	if exists {
		t.Error("expected HasSession to return false for failed has-session")
	}
}

// --- Interface compliance ---

func TestTmuxRunner_ImplementsRunner(t *testing.T) {
	var _ Runner = (*TmuxRunner)(nil)
}
