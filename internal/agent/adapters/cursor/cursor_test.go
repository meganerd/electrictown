package cursor

import (
	"testing"

	"github.com/meganerd/electrictown/internal/agent"
)

func TestDefaultCommand(t *testing.T) {
	if cmd := DefaultCommand(); cmd != "cursor" {
		t.Errorf("expected 'cursor', got %q", cmd)
	}
}

func TestBuildArgs_Basic(t *testing.T) {
	task := agent.Task{Prompt: "fix the bug"}
	args := buildArgs(task)
	if args[0] != "agent" {
		t.Errorf("first arg should be 'agent', got %q", args[0])
	}
	assertContains(t, args, "--print")
	assertContainsSeq(t, args, "--prompt", "fix the bug")
}

func TestBuildArgs_WithModel(t *testing.T) {
	task := agent.Task{Prompt: "test", Model: "claude-3.5-sonnet"}
	args := buildArgs(task)
	assertContainsSeq(t, args, "--model", "claude-3.5-sonnet")
}

func TestBuildArgs_WithFlags(t *testing.T) {
	task := agent.Task{Prompt: "test", Flags: []string{"--no-git"}}
	args := buildArgs(task)
	assertContains(t, args, "--no-git")
}

func TestNormalize_WithDiff(t *testing.T) {
	output := "Fixed:\n\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new"
	result := &agent.Result{Stdout: output, ExitCode: 0}
	n := &CursorNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
}

func TestNormalize_PlainText(t *testing.T) {
	result := &agent.Result{Stdout: "no changes", ExitCode: 0}
	n := &CursorNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(result.FileChanges) != 0 {
		t.Errorf("expected 0 file changes")
	}
}

func TestAdapterType(t *testing.T) {
	a := New()
	if a.Type() != agent.TypeCursor {
		t.Errorf("expected type %q, got %q", agent.TypeCursor, a.Type())
	}
}

func TestAdapterImplementsBackend(t *testing.T) {
	var _ agent.Backend = (*Adapter)(nil)
}

func assertContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, want)
}

func assertContainsSeq(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Errorf("args %v does not contain sequence %q %q", args, key, value)
}
