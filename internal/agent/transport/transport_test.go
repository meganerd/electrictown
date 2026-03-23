package transport

import (
	"os"
	"path/filepath"
	"testing"
)

// ═══════════════════════════════════════════════════════════════════
// Auth tests
// ═══════════════════════════════════════════════════════════════════

func TestAuthConfigResolve_Defaults(t *testing.T) {
	cfg := AuthConfig{}
	cfg.Resolve()
	if cfg.User == "" {
		t.Error("expected User to be resolved to current user")
	}
	if cfg.Port != 22 {
		t.Errorf("expected Port 22, got %d", cfg.Port)
	}
}

func TestAuthConfigResolve_Explicit(t *testing.T) {
	cfg := AuthConfig{User: "gustin", Port: 2222}
	cfg.Resolve()
	if cfg.User != "gustin" {
		t.Errorf("expected User gustin, got %s", cfg.User)
	}
	if cfg.Port != 2222 {
		t.Errorf("expected Port 2222, got %d", cfg.Port)
	}
}

func TestBuildAuthMethods_WithAgent(t *testing.T) {
	// This test only works if ssh-agent is running.
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		t.Skip("SSH_AUTH_SOCK not set, skipping ssh-agent test")
	}
	methods, err := BuildAuthMethods(AuthConfig{})
	if err != nil {
		t.Fatalf("BuildAuthMethods failed: %v", err)
	}
	if len(methods) == 0 {
		t.Error("expected at least one auth method when ssh-agent is available")
	}
}

func TestBuildAuthMethods_WithKeyFile(t *testing.T) {
	// Look for a real SSH key to test with.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	var keyPath string
	for _, name := range defaultKeyNames {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			keyPath = p
			break
		}
	}
	if keyPath == "" {
		t.Skip("no default SSH keys found, skipping key file test")
	}

	methods, err := BuildAuthMethods(AuthConfig{KeyPath: keyPath})
	if err != nil {
		t.Fatalf("BuildAuthMethods with key file failed: %v", err)
	}
	if len(methods) == 0 {
		t.Error("expected at least one auth method with explicit key")
	}
}

func TestBuildAuthMethods_InvalidKeyPath(t *testing.T) {
	_, err := BuildAuthMethods(AuthConfig{KeyPath: "/nonexistent/key"})
	if err == nil {
		t.Error("expected error for invalid key path")
	}
}

// ═══════════════════════════════════════════════════════════════════
// SSH manager tests
// ═══════════════════════════════════════════════════════════════════

func TestNewSSHManager(t *testing.T) {
	mgr := NewSSHManager()
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.pool == nil {
		t.Error("expected non-nil pool map")
	}
}

func TestSSHManagerClose_Empty(t *testing.T) {
	mgr := NewSSHManager()
	if err := mgr.Close(); err != nil {
		t.Errorf("Close on empty manager should not error, got: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════
// Remote command building tests
// ═══════════════════════════════════════════════════════════════════

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "'hello'"},
		{"hello world", "'hello world'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"path/to/file", "'path/to/file'"},
		{"$VAR", "'$VAR'"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBuildRemoteCommand_Simple(t *testing.T) {
	cmd := buildRemoteCommand("echo", []string{"hello"}, RemoteOpts{})
	if cmd != "echo 'hello'" {
		t.Errorf("expected 'echo 'hello'', got %q", cmd)
	}
}

func TestBuildRemoteCommand_WithWorkingDir(t *testing.T) {
	cmd := buildRemoteCommand("ls", nil, RemoteOpts{WorkingDir: "/tmp/project"})
	expected := "cd '/tmp/project' && ls"
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestBuildRemoteCommand_WithEnv(t *testing.T) {
	cmd := buildRemoteCommand("echo", []string{"test"}, RemoteOpts{
		Env: map[string]string{"FOO": "bar"},
	})
	// Env export should be present before the command.
	if cmd == "" {
		t.Error("expected non-empty command")
	}
	// Check that both the export and command are present.
	if !contains(cmd, "export FOO='bar'") {
		t.Errorf("expected env export in command, got %q", cmd)
	}
	if !contains(cmd, "echo 'test'") {
		t.Errorf("expected echo command, got %q", cmd)
	}
}

func TestBuildRemoteCommand_Full(t *testing.T) {
	cmd := buildRemoteCommand("claude", []string{"--prompt", "fix the bug"}, RemoteOpts{
		WorkingDir: "/home/gustin/project",
		Env:        map[string]string{"ANTHROPIC_API_KEY": "sk-test"},
	})
	if !contains(cmd, "cd '/home/gustin/project'") {
		t.Errorf("expected working dir in command, got %q", cmd)
	}
	if !contains(cmd, "export ANTHROPIC_API_KEY='sk-test'") {
		t.Errorf("expected env export in command, got %q", cmd)
	}
	if !contains(cmd, "claude '--prompt' 'fix the bug'") {
		t.Errorf("expected claude command with args, got %q", cmd)
	}
}

// ═══════════════════════════════════════════════════════════════════
// Integration tests (require localhost SSH)
// ═══════════════════════════════════════════════════════════════════

func TestSSHManager_LocalhostIntegration(t *testing.T) {
	// Skip unless explicitly enabled — requires SSH server on localhost.
	if os.Getenv("ET_TEST_SSH") == "" {
		t.Skip("set ET_TEST_SSH=1 to run SSH integration tests against localhost")
	}

	mgr := NewSSHManager()
	defer mgr.Close()

	cfg := AuthConfig{}
	cfg.Resolve()

	client, err := mgr.GetConnection("localhost", 22, cfg)
	if err != nil {
		t.Fatalf("GetConnection to localhost failed: %v", err)
	}

	// Test multiplexing: open two sessions on the same connection.
	session1, err := mgr.NewSession(client)
	if err != nil {
		t.Fatalf("NewSession 1 failed: %v", err)
	}
	session2, err := mgr.NewSession(client)
	if err != nil {
		t.Fatalf("NewSession 2 failed: %v", err)
	}

	out1, err := session1.CombinedOutput("echo session1")
	if err != nil {
		t.Fatalf("session1 echo failed: %v", err)
	}
	out2, err := session2.CombinedOutput("echo session2")
	if err != nil {
		t.Fatalf("session2 echo failed: %v", err)
	}

	if string(out1) != "session1\n" {
		t.Errorf("session1 output = %q, want 'session1\\n'", out1)
	}
	if string(out2) != "session2\n" {
		t.Errorf("session2 output = %q, want 'session2\\n'", out2)
	}

	// Test connection pooling: second GetConnection should return cached.
	client2, err := mgr.GetConnection("localhost", 22, cfg)
	if err != nil {
		t.Fatalf("second GetConnection failed: %v", err)
	}
	if client2 != client {
		t.Error("expected same client from pool (connection reuse)")
	}
}

// contains checks if substr is in s.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
