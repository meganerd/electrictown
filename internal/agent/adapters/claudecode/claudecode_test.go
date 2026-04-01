package claudecode

import (
	"testing"

	"github.com/meganerd/electrictown/internal/agent"
)

// ═══════════════════════════════════════════════════════════════════
// Detection tests
// ═══════════════════════════════════════════════════════════════════

func TestDetect(t *testing.T) {
	result := Detect()
	// We can't guarantee claude is installed in CI, so just verify
	// the struct is populated correctly.
	if result.Found {
		if result.Path == "" {
			t.Error("found but path is empty")
		}
		if result.Version == "" {
			t.Error("found but version is empty")
		}
		t.Logf("Claude Code detected: %s", FormatDetectStatus(result))
	} else {
		t.Log("Claude Code not found (expected in environments without it)")
	}
}

func TestDefaultCommand(t *testing.T) {
	if cmd := DefaultCommand(); cmd != "claude" {
		t.Errorf("expected 'claude', got %q", cmd)
	}
}

func TestIsSemanticVersion(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"2.1.81", true},
		{"2.1.81 (Claude Code)", true},
		{"1.0", true},
		{"1.0.0.0", true},
		{"hello", false},
		{"", false},
		{"1.2.3.4.5", false},
	}
	for _, tt := range tests {
		if got := isSemanticVersion(tt.input); got != tt.valid {
			t.Errorf("isSemanticVersion(%q) = %v, want %v", tt.input, got, tt.valid)
		}
	}
}

func TestFormatDetectStatus(t *testing.T) {
	notFound := DetectResult{Found: false}
	if s := FormatDetectStatus(notFound); s != "not installed" {
		t.Errorf("expected 'not installed', got %q", s)
	}

	found := DetectResult{Found: true, Version: "2.1.81", Path: "/usr/bin/claude"}
	s := FormatDetectStatus(found)
	if s != "v2.1.81 at /usr/bin/claude" {
		t.Errorf("unexpected format: %q", s)
	}
}

// ═══════════════════════════════════════════════════════════════════
// Argument building tests — basic (zero config)
// ═══════════════════════════════════════════════════════════════════

func TestBuildArgs_Basic(t *testing.T) {
	a := New(Config{})
	task := agent.Task{Prompt: "fix the bug"}
	args := a.buildArgs(task)

	// Must contain -p and --output-format json.
	assertContains(t, args, "-p")
	assertContainsSeq(t, args, "--output-format", "json")
	// Prompt must be last.
	if args[len(args)-1] != "fix the bug" {
		t.Errorf("prompt should be last arg, got %q", args[len(args)-1])
	}
}

func TestBuildArgs_WithModel(t *testing.T) {
	a := New(Config{})
	task := agent.Task{
		Prompt: "test",
		Model:  "claude-sonnet-4-20250514",
	}
	args := a.buildArgs(task)
	assertContainsSeq(t, args, "--model", "claude-sonnet-4-20250514")
}

func TestBuildArgs_WithFlags(t *testing.T) {
	a := New(Config{})
	task := agent.Task{
		Prompt: "test",
		Flags:  []string{"--max-turns", "5", "--allowedTools", "Bash Edit"},
	}
	args := a.buildArgs(task)
	assertContains(t, args, "--max-turns")
	assertContains(t, args, "5")
}

// ═══════════════════════════════════════════════════════════════════
// Argument building tests — Claude Code config fields
// ═══════════════════════════════════════════════════════════════════

func TestBuildArgs_SystemPrompt(t *testing.T) {
	a := New(Config{SystemPrompt: "You are a code reviewer."})
	args := a.buildArgs(agent.Task{Prompt: "review this"})
	assertContainsSeq(t, args, "--system-prompt", "You are a code reviewer.")
}

func TestBuildArgs_AllowedTools(t *testing.T) {
	a := New(Config{AllowedTools: []string{"Read", "Glob", "Grep"}})
	args := a.buildArgs(agent.Task{Prompt: "search"})
	assertContainsSeq(t, args, "--allowedTools", "Read Glob Grep")
}

func TestBuildArgs_DisallowedTools(t *testing.T) {
	a := New(Config{DisallowedTools: []string{"Bash", "Edit"}})
	args := a.buildArgs(agent.Task{Prompt: "read only"})
	assertContainsSeq(t, args, "--disallowedTools", "Bash Edit")
}

func TestBuildArgs_JSONSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"score":{"type":"number"}}}`
	a := New(Config{JSONSchema: schema})
	args := a.buildArgs(agent.Task{Prompt: "score this"})
	assertContainsSeq(t, args, "--json-schema", schema)
}

func TestBuildArgs_PermissionMode(t *testing.T) {
	a := New(Config{PermissionMode: "plan"})
	args := a.buildArgs(agent.Task{Prompt: "plan it"})
	assertContainsSeq(t, args, "--permission-mode", "plan")
}

func TestBuildArgs_MaxBudgetUSD(t *testing.T) {
	a := New(Config{MaxBudgetUSD: 2.5})
	args := a.buildArgs(agent.Task{Prompt: "expensive task"})
	assertContainsSeq(t, args, "--max-budget-usd", "2.5")
}

func TestBuildArgs_AddDirs(t *testing.T) {
	a := New(Config{AddDirs: []string{"/data/src", "/data/tests"}})
	args := a.buildArgs(agent.Task{Prompt: "multi-dir"})
	// Should produce --add-dir /data/src --add-dir /data/tests
	assertContainsSeq(t, args, "--add-dir", "/data/src")
	assertContainsSeq(t, args, "--add-dir", "/data/tests")
}

func TestBuildArgs_AllFieldsCombined(t *testing.T) {
	a := New(Config{
		Model:           "opus",
		SystemPrompt:    "Be concise.",
		AllowedTools:    []string{"Read"},
		DisallowedTools: []string{"Bash"},
		JSONSchema:      `{"type":"object"}`,
		PermissionMode:  "plan",
		MaxBudgetUSD:    1.0,
		AddDirs:         []string{"/extra"},
		Flags:           []string{"--verbose"},
	})
	// Simulate Execute's merge: config model goes into task.Model when empty.
	args := a.buildArgs(agent.Task{Prompt: "do everything", Model: "opus"})

	assertContainsSeq(t, args, "--model", "opus")
	assertContainsSeq(t, args, "--system-prompt", "Be concise.")
	assertContainsSeq(t, args, "--allowedTools", "Read")
	assertContainsSeq(t, args, "--disallowedTools", "Bash")
	assertContainsSeq(t, args, "--json-schema", `{"type":"object"}`)
	assertContainsSeq(t, args, "--permission-mode", "plan")
	assertContainsSeq(t, args, "--max-budget-usd", "1")
	assertContainsSeq(t, args, "--add-dir", "/extra")
	assertContains(t, args, "--verbose")
	// Prompt must still be last.
	if args[len(args)-1] != "do everything" {
		t.Errorf("prompt should be last arg, got %q", args[len(args)-1])
	}
}

func TestBuildArgs_ZeroConfig_BackwardCompatible(t *testing.T) {
	// Zero config should produce identical args to the old adapter.
	a := New(Config{})
	task := agent.Task{Prompt: "hello", Model: "sonnet", Flags: []string{"--max-turns", "3"}}
	args := a.buildArgs(task)

	expected := []string{"-p", "--output-format", "json", "--model", "sonnet", "--max-turns", "3", "hello"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, exp := range expected {
		if args[i] != exp {
			t.Errorf("arg[%d]: expected %q, got %q", i, exp, args[i])
		}
	}
}

func TestBuildArgs_ConfigModelMergedFromTask(t *testing.T) {
	// Task model should override config model (merged in Execute, reflected here).
	a := New(Config{Model: "haiku"})
	// Simulate what Execute does: task.Model takes precedence.
	task := agent.Task{Prompt: "test", Model: "opus"}
	args := a.buildArgs(task)
	assertContainsSeq(t, args, "--model", "opus")
	assertNotContains(t, args, "haiku")
}

// ═══════════════════════════════════════════════════════════════════
// Normalizer tests
// ═══════════════════════════════════════════════════════════════════

func TestNormalize_JSONOutput(t *testing.T) {
	jsonOut := `{"type":"result","subtype":"success","is_error":false,"duration_ms":5000,"result":"Here is the fix:\n\nI modified main.go to add error handling.","total_cost_usd":0.05,"num_turns":3,"stop_reason":"end_turn","session_id":"abc123","usage":{"input_tokens":100,"output_tokens":200}}`

	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &ClaudeCodeNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	if result.Stdout != "Here is the fix:\n\nI modified main.go to add error handling." {
		t.Errorf("unexpected stdout: %q", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestNormalize_ErrorOutput(t *testing.T) {
	jsonOut := `{"type":"result","subtype":"success","is_error":true,"result":"Credit balance is too low","duration_ms":1000,"total_cost_usd":0,"session_id":"abc","usage":{"input_tokens":0,"output_tokens":0}}`

	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &ClaudeCodeNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	if result.Stdout != "Credit balance is too low" {
		t.Errorf("unexpected stdout: %q", result.Stdout)
	}
	// is_error should set exit code to 1.
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1 for error, got %d", result.ExitCode)
	}
}

func TestNormalize_PlainText(t *testing.T) {
	// Non-JSON output should be preserved as-is.
	result := &agent.Result{Stdout: "some plain text output", ExitCode: 0}
	n := &ClaudeCodeNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if result.Stdout != "some plain text output" {
		t.Errorf("plain text should be preserved, got %q", result.Stdout)
	}
}

func TestNormalize_EmptyOutput(t *testing.T) {
	result := &agent.Result{Stdout: "", ExitCode: 0}
	n := &ClaudeCodeNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
}

func TestNormalize_WithDiffs(t *testing.T) {
	jsonOut := `{"type":"result","subtype":"success","is_error":false,"result":"Fixed the bug:\n\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n package main\n\n+import \"fmt\"\n func main() {}","duration_ms":3000,"total_cost_usd":0.02,"session_id":"abc","usage":{"input_tokens":50,"output_tokens":100}}`

	result := &agent.Result{Stdout: jsonOut, ExitCode: 0}
	n := &ClaudeCodeNormalizer{}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	if result.FileChanges[0].Path != "main.go" {
		t.Errorf("expected path main.go, got %q", result.FileChanges[0].Path)
	}
}

// ═══════════════════════════════════════════════════════════════════
// Backend interface compliance
// ═══════════════════════════════════════════════════════════════════

func TestAdapterType(t *testing.T) {
	a := New(Config{})
	if a.Type() != agent.TypeClaudeCode {
		t.Errorf("expected type %q, got %q", agent.TypeClaudeCode, a.Type())
	}
}

func TestAdapterImplementsBackend(t *testing.T) {
	var _ agent.Backend = (*Adapter)(nil)
}

// ═══════════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════════

func assertContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, want)
}

func assertNotContains(t *testing.T, args []string, notWant string) {
	t.Helper()
	for _, a := range args {
		if a == notWant {
			t.Errorf("args %v should not contain %q", args, notWant)
			return
		}
	}
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
