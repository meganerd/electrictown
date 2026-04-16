package zencoder

import (
	"testing"

	"github.com/meganerd/electrictown/internal/agent"
)

func TestDefaultCommand(t *testing.T) {
	if cmd := DefaultCommand(); cmd != "zen" {
		t.Errorf("expected 'zen', got %q", cmd)
	}
}

func TestBuildArgs_Basic(t *testing.T) {
	task := agent.Task{Prompt: "fix the bug"}
	args := buildArgs(task)
	if args[0] != "run" {
		t.Errorf("first arg should be 'run', got %q", args[0])
	}
	if args[len(args)-1] != "fix the bug" {
		t.Error("prompt should be last arg")
	}
}

func TestBuildArgs_WithModel(t *testing.T) {
	task := agent.Task{Prompt: "test", Model: "gpt-4o"}
	args := buildArgs(task)
	assertContainsSeq(t, args, "--model", "gpt-4o")
}

func TestBuildArgs_WithFlags(t *testing.T) {
	task := agent.Task{Prompt: "test", Flags: []string{"--json"}}
	args := buildArgs(task)
	assertContains(t, args, "--json")
}

func TestNormalize_WithDiff(t *testing.T) {
	output := "Fixed:\n\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n package main\n\n+import \"fmt\"\n func main() {}"
	result := &agent.Result{Stdout: output, ExitCode: 0}
	n := &ZencoderNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
}

func TestNormalize_PlainText(t *testing.T) {
	result := &agent.Result{Stdout: "no changes", ExitCode: 0}
	n := &ZencoderNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(result.FileChanges) != 0 {
		t.Errorf("expected 0 file changes")
	}
}

func TestNormalize_Empty(t *testing.T) {
	result := &agent.Result{Stdout: "", ExitCode: 0}
	n := &ZencoderNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
}

func TestAdapterType(t *testing.T) {
	a := New()
	if a.Type() != agent.TypeZencoder {
		t.Errorf("expected type %q, got %q", agent.TypeZencoder, a.Type())
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
