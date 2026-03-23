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
// Argument building tests
// ═══════════════════════════════════════════════════════════════════

func TestBuildArgs_Basic(t *testing.T) {
	task := agent.Task{Prompt: "fix the bug"}
	args := buildArgs(task)

	// Must contain -p and --output-format json.
	assertContains(t, args, "-p")
	assertContainsSeq(t, args, "--output-format", "json")
	// Prompt must be last.
	if args[len(args)-1] != "fix the bug" {
		t.Errorf("prompt should be last arg, got %q", args[len(args)-1])
	}
}

func TestBuildArgs_WithModel(t *testing.T) {
	task := agent.Task{
		Prompt: "test",
		Model:  "claude-sonnet-4-20250514",
	}
	args := buildArgs(task)
	assertContainsSeq(t, args, "--model", "claude-sonnet-4-20250514")
}

func TestBuildArgs_WithFlags(t *testing.T) {
	task := agent.Task{
		Prompt: "test",
		Flags:  []string{"--max-turns", "5", "--allowedTools", "Bash Edit"},
	}
	args := buildArgs(task)
	assertContains(t, args, "--max-turns")
	assertContains(t, args, "5")
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
	a := New()
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

func assertContainsSeq(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Errorf("args %v does not contain sequence %q %q", args, key, value)
}
