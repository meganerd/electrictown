package tmux

import (
	"os"
	"testing"
)

func TestDetectByobu_EnvSet(t *testing.T) {
	// Save and restore the env var.
	orig := os.Getenv("BYOBU_BACKEND")
	defer os.Setenv("BYOBU_BACKEND", orig)

	os.Setenv("BYOBU_BACKEND", "tmux")

	if !DetectByobu() {
		t.Error("expected DetectByobu() to return true when BYOBU_BACKEND is set")
	}
}

func TestDetectByobu_EnvUnset(t *testing.T) {
	// Save and restore the env var.
	orig := os.Getenv("BYOBU_BACKEND")
	defer os.Setenv("BYOBU_BACKEND", orig)

	os.Unsetenv("BYOBU_BACKEND")

	// This test's result depends on whether byobu is in PATH.
	// We just verify it doesn't panic.
	_ = DetectByobu()
}

func TestNewByobuRunner_DelegatesSendKeys(t *testing.T) {
	rec := &cmdRecorder{}
	inner := NewTmuxRunnerWithCmd(rec.makeCmd)
	runner := NewByobuRunner(inner)

	err := runner.SendKeys("test-session", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(rec.calls))
	}

	// SendKeys should delegate to tmux, not byobu.
	if rec.calls[0].name != "tmux" {
		t.Errorf("expected SendKeys to use 'tmux', got %q", rec.calls[0].name)
	}
}

func TestNewByobuRunner_NewSessionUsesByobu(t *testing.T) {
	rec := &cmdRecorder{}
	inner := NewTmuxRunnerWithCmd(rec.makeCmd)
	runner := NewByobuRunner(inner)

	err := runner.NewSession("test-session", "bash", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(rec.calls))
	}

	// NewSession should use byobu, not tmux.
	if rec.calls[0].name != "byobu" {
		t.Errorf("expected NewSession to use 'byobu', got %q", rec.calls[0].name)
	}
}

func TestNewByobuRunner_DelegatesCapturePane(t *testing.T) {
	rec := &cmdRecorder{output: "captured"}
	inner := NewTmuxRunnerWithCmd(rec.makeCmd)
	runner := NewByobuRunner(inner)

	output, err := runner.CapturePane("test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output == "" {
		t.Error("expected non-empty capture output")
	}
	if rec.calls[0].name != "tmux" {
		t.Errorf("expected CapturePane to use 'tmux', got %q", rec.calls[0].name)
	}
}

func TestNewByobuRunner_DelegatesListSessions(t *testing.T) {
	rec := &cmdRecorder{output: "session1"}
	inner := NewTmuxRunnerWithCmd(rec.makeCmd)
	runner := NewByobuRunner(inner)

	sessions, err := runner.ListSessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
	if rec.calls[0].name != "tmux" {
		t.Errorf("expected ListSessions to use 'tmux', got %q", rec.calls[0].name)
	}
}

func TestNewByobuRunner_DelegatesKillSession(t *testing.T) {
	rec := &cmdRecorder{}
	inner := NewTmuxRunnerWithCmd(rec.makeCmd)
	runner := NewByobuRunner(inner)

	err := runner.KillSession("test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.calls[0].name != "tmux" {
		t.Errorf("expected KillSession to use 'tmux', got %q", rec.calls[0].name)
	}
}

func TestNewByobuRunner_DelegatesHasSession(t *testing.T) {
	rec := &cmdRecorder{}
	inner := NewTmuxRunnerWithCmd(rec.makeCmd)
	runner := NewByobuRunner(inner)

	exists := runner.HasSession("test-session")
	if !exists {
		t.Error("expected HasSession to return true")
	}
	if rec.calls[0].name != "tmux" {
		t.Errorf("expected HasSession to use 'tmux', got %q", rec.calls[0].name)
	}
}

func TestNewAutoRunner_ReturnsRunner(t *testing.T) {
	// Just verify it returns something without panicking.
	runner := NewAutoRunner()
	if runner == nil {
		t.Fatal("expected non-nil runner from NewAutoRunner")
	}
}

func TestByobuRunner_ImplementsRunner(t *testing.T) {
	var _ Runner = (*ByobuRunner)(nil)
}
