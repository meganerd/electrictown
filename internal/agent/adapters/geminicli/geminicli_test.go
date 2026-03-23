package geminicli

import (
	"testing"

	"github.com/meganerd/electrictown/internal/agent"
)

func TestDefaultCommand(t *testing.T) {
	if cmd := DefaultCommand(); cmd != "gemini" {
		t.Errorf("expected 'gemini', got %q", cmd)
	}
}

func TestBuildArgs_Basic(t *testing.T) {
	task := agent.Task{Prompt: "fix the bug"}
	args := buildArgs(task)
	assertContains(t, args, "-p")
	assertContainsSeq(t, args, "--output-format", "json")
	if args[len(args)-1] != "fix the bug" {
		t.Error("prompt should be last arg")
	}
}

func TestBuildArgs_WithModel(t *testing.T) {
	task := agent.Task{Prompt: "test", Model: "gemini-2.5-pro"}
	args := buildArgs(task)
	assertContainsSeq(t, args, "--model", "gemini-2.5-pro")
}

func TestBuildArgs_WithFlags(t *testing.T) {
	task := agent.Task{Prompt: "test", Flags: []string{"--sandbox"}}
	args := buildArgs(task)
	assertContains(t, args, "--sandbox")
}

func TestNormalize_JSONResponse(t *testing.T) {
	jsonOut := `{"response":"Fixed the bug by adding error handling to the HTTP handler."}`
	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &GeminiCLINormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.Stdout != "Fixed the bug by adding error handling to the HTTP handler." {
		t.Errorf("unexpected stdout: %q", result.Stdout)
	}
}

func TestNormalize_JSONResult(t *testing.T) {
	jsonOut := `{"result":"Done."}`
	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &GeminiCLINormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.Stdout != "Done." {
		t.Errorf("unexpected stdout: %q", result.Stdout)
	}
}

func TestNormalize_ErrorOutput(t *testing.T) {
	jsonOut := `{"error":"Authentication required"}`
	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &GeminiCLINormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
	if result.Stderr != "Authentication required" {
		t.Errorf("unexpected stderr: %q", result.Stderr)
	}
}

func TestNormalize_PlainText(t *testing.T) {
	result := &agent.Result{Stdout: "plain text output", ExitCode: 0}
	n := &GeminiCLINormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.Stdout != "plain text output" {
		t.Error("plain text should be preserved")
	}
}

func TestNormalize_WithDiffs(t *testing.T) {
	jsonOut := `{"response":"Fixed:\n\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new"}`
	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &GeminiCLINormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
}

func TestAdapterType(t *testing.T) {
	a := New()
	if a.Type() != agent.TypeGeminiCLI {
		t.Errorf("expected type %q, got %q", agent.TypeGeminiCLI, a.Type())
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
