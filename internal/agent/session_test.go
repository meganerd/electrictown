package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunOneShotEcho(t *testing.T) {
	s := &Session{
		Command: "echo",
		Args:    []string{"hello world"},
	}
	result, err := s.RunOneShot(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if got := strings.TrimSpace(result.Stdout); got != "hello world" {
		t.Errorf("expected stdout %q, got %q", "hello world", got)
	}
	if result.Duration <= 0 {
		t.Error("duration should be positive")
	}
}

func TestRunOneShotStdin(t *testing.T) {
	s := &Session{
		Command: "cat",
	}
	result, err := s.RunOneShot(context.Background(), "input data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.Stdout; got != "input data" {
		t.Errorf("expected stdout %q, got %q", "input data", got)
	}
}

func TestRunOneShotNonZeroExit(t *testing.T) {
	s := &Session{
		Command: "false",
	}
	result, err := s.RunOneShot(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error (non-zero exit is not an error): %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code from 'false'")
	}
}

func TestRunOneShotTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	s := &Session{
		Command: "sleep",
		Args:    []string{"10"},
	}
	_, err := s.RunOneShot(ctx, "")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestRunOneShotEnv(t *testing.T) {
	s := &Session{
		Command: "sh",
		Args:    []string{"-c", "echo $TEST_VAR_ET"},
		Env:     map[string]string{"TEST_VAR_ET": "agent_test_value"},
	}
	result, err := s.RunOneShot(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "agent_test_value" {
		t.Errorf("expected stdout %q, got %q", "agent_test_value", got)
	}
}

func TestRunOneShotWorkingDir(t *testing.T) {
	s := &Session{
		Command: "pwd",
		Dir:     "/tmp",
	}
	result, err := s.RunOneShot(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "/tmp" {
		t.Errorf("expected working dir /tmp, got %q", got)
	}
}

func TestRunOneShotBinaryNotFound(t *testing.T) {
	s := &Session{
		Command: "nonexistent-binary-xyz-12345",
	}
	result, err := s.RunOneShot(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if result.ExitCode != -1 {
		t.Errorf("expected exit code -1 for missing binary, got %d", result.ExitCode)
	}
}

func TestBuildEnv(t *testing.T) {
	// nil map returns nil (inherit parent env).
	if got := buildEnv(nil); got != nil {
		t.Errorf("expected nil for empty env, got %v", got)
	}
	// Non-nil map returns parent env + overrides.
	env := buildEnv(map[string]string{"FOO": "bar"})
	if env == nil {
		t.Fatal("expected non-nil env")
	}
	found := false
	for _, e := range env {
		if e == "FOO=bar" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected FOO=bar in env")
	}
}
