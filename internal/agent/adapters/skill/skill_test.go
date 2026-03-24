package skill

import (
	"testing"

	"github.com/meganerd/electrictown/internal/agent"
)

func TestType(t *testing.T) {
	a := New("bun", "~/.claude/skills/test.ts", "arg")
	if a.Type() != agent.TypeSkill {
		t.Fatalf("expected TypeSkill, got %s", a.Type())
	}
}

func TestBuildArgsWithSkillPath(t *testing.T) {
	a := New("bun", "/home/user/.claude/skills/pai.ts", "arg")
	task := agent.Task{
		Prompt: "decompose this task",
		Flags:  []string{"--verbose"},
	}

	args, stdin := a.buildArgs(task)

	if stdin != "" {
		t.Fatalf("expected empty stdin for arg mode, got %q", stdin)
	}
	// Expected: [skill_path, --verbose, prompt]
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	if args[0] != "/home/user/.claude/skills/pai.ts" {
		t.Fatalf("expected skill path as first arg, got %q", args[0])
	}
	if args[1] != "--verbose" {
		t.Fatalf("expected flag, got %q", args[1])
	}
	if args[2] != "decompose this task" {
		t.Fatalf("expected prompt as last arg, got %q", args[2])
	}
}

func TestBuildArgsStdinMode(t *testing.T) {
	a := New("python", "/opt/analyze.py", "stdin")
	task := agent.Task{Prompt: "analyze this code"}

	args, stdin := a.buildArgs(task)

	if stdin != "analyze this code" {
		t.Fatalf("expected prompt on stdin, got %q", stdin)
	}
	// Only the skill path, no prompt arg.
	if len(args) != 1 {
		t.Fatalf("expected 1 arg (skill_path only), got %d: %v", len(args), args)
	}
}

func TestBuildArgsNoSkillPath(t *testing.T) {
	a := New("pai", "", "arg")
	task := agent.Task{Prompt: "do the thing"}

	args, stdin := a.buildArgs(task)

	if stdin != "" {
		t.Fatalf("expected empty stdin, got %q", stdin)
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg (prompt only), got %d: %v", len(args), args)
	}
	if args[0] != "do the thing" {
		t.Fatalf("expected prompt, got %q", args[0])
	}
}

func TestExpandHome(t *testing.T) {
	// Should not panic or error on non-home paths.
	if got := expandHome("/absolute/path"); got != "/absolute/path" {
		t.Fatalf("expected unchanged path, got %q", got)
	}
	if got := expandHome("relative/path"); got != "relative/path" {
		t.Fatalf("expected unchanged path, got %q", got)
	}
	// ~/something should expand (assuming HOME is set in test env).
	got := expandHome("~/test")
	if got == "~/test" {
		t.Fatal("expected ~ expansion")
	}
}

func TestDefaultInputMode(t *testing.T) {
	a := New("cmd", "", "")
	if a.InputMode != "arg" {
		t.Fatalf("expected default input_mode 'arg', got %q", a.InputMode)
	}
}

// Compile-time check.
var _ agent.Backend = (*Adapter)(nil)
