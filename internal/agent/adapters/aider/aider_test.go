package aider

import (
	"testing"

	"github.com/meganerd/electrictown/internal/agent"
)

func TestDefaultCommand(t *testing.T) {
	if cmd := DefaultCommand(); cmd != "aider" {
		t.Errorf("expected 'aider', got %q", cmd)
	}
}

func TestBuildArgs_Basic(t *testing.T) {
	task := agent.Task{Prompt: "fix the bug"}
	args, cleanup, err := buildArgs(task)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	assertContains(t, args, "--yes")
	assertContainsSeq(t, args, "--message", "fix the bug")
}

func TestBuildArgs_WithModel(t *testing.T) {
	task := agent.Task{Prompt: "test", Model: "gpt-4o"}
	args, cleanup, err := buildArgs(task)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	assertContainsSeq(t, args, "--model", "gpt-4o")
}

func TestBuildArgs_LongPromptUsesFile(t *testing.T) {
	longPrompt := make([]byte, 2000)
	for i := range longPrompt {
		longPrompt[i] = 'a'
	}
	task := agent.Task{Prompt: string(longPrompt)}
	args, cleanup, err := buildArgs(task)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected cleanup function for long prompt")
	}
	defer cleanup()
	assertContains(t, args, "--message-file")
}

func TestNormalize_SearchReplace(t *testing.T) {
	output := `Editing main.go...

main.go
<<<<<<< SEARCH
func main() {
    fmt.Println("hello")
}
=======
func main() {
    fmt.Println("hello world")
}
>>>>>>> REPLACE

Commit abc1234 fix: update greeting
`
	result := &agent.Result{Stdout: output, ExitCode: 0}
	n := &AiderNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(result.FileChanges) < 1 {
		t.Fatal("expected at least 1 file change")
	}
	if result.FileChanges[0].Path != "main.go" {
		t.Errorf("expected path main.go, got %q", result.FileChanges[0].Path)
	}
}

func TestNormalize_NoBlocks(t *testing.T) {
	result := &agent.Result{Stdout: "no changes needed", ExitCode: 0}
	n := &AiderNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(result.FileChanges) != 0 {
		t.Errorf("expected 0 file changes, got %d", len(result.FileChanges))
	}
}

func TestAdapterType(t *testing.T) {
	a := New()
	if a.Type() != agent.TypeAider {
		t.Errorf("expected type %q, got %q", agent.TypeAider, a.Type())
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
