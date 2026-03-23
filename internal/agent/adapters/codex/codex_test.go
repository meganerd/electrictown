package codex

import (
	"testing"

	"github.com/meganerd/electrictown/internal/agent"
)

func TestDefaultCommand(t *testing.T) {
	if cmd := DefaultCommand(); cmd != "codex" {
		t.Errorf("expected 'codex', got %q", cmd)
	}
}

func TestBuildArgs_Basic(t *testing.T) {
	task := agent.Task{Prompt: "fix the bug"}
	args := buildArgs(task)
	if args[0] != "exec" {
		t.Errorf("first arg should be 'exec', got %q", args[0])
	}
	if args[len(args)-1] != "fix the bug" {
		t.Errorf("prompt should be last arg")
	}
	assertContains(t, args, "--json")
}

func TestBuildArgs_WithModel(t *testing.T) {
	task := agent.Task{Prompt: "test", Model: "o4-mini"}
	args := buildArgs(task)
	assertContainsSeq(t, args, "--model", "o4-mini")
}

func TestBuildArgs_WithFlags(t *testing.T) {
	task := agent.Task{Prompt: "test", Flags: []string{"--full-auto"}}
	args := buildArgs(task)
	assertContains(t, args, "--full-auto")
}

func TestNormalize_JSONOutput(t *testing.T) {
	jsonOut := `{"result":"Fixed the bug in main.go","exit_code":0}`
	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &CodexNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.Stdout != "Fixed the bug in main.go" {
		t.Errorf("unexpected stdout: %q", result.Stdout)
	}
}

func TestNormalize_ErrorOutput(t *testing.T) {
	jsonOut := `{"result":"","exit_code":1,"error":"OPENAI_API_KEY not set"}`
	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &CodexNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
	if result.Stderr != "OPENAI_API_KEY not set" {
		t.Errorf("unexpected stderr: %q", result.Stderr)
	}
}

func TestNormalize_PlainText(t *testing.T) {
	result := &agent.Result{Stdout: "plain text output", ExitCode: 0}
	n := &CodexNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.Stdout != "plain text output" {
		t.Error("plain text should be preserved")
	}
}

func TestAdapterType(t *testing.T) {
	a := New()
	if a.Type() != agent.TypeCodex {
		t.Errorf("expected type %q, got %q", agent.TypeCodex, a.Type())
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
