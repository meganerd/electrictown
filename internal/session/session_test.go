package session

import (
	"strings"
	"testing"
	"time"

	"github.com/meganerd/electrictown/internal/provider"
)

// mockAdapter implements ProviderAdapter for testing.
type mockAdapter struct {
	name           string
	resolveErr     error
	provisionErr   error
	config         *SessionConfig
	readiness      ReadinessStrategy
	builtCmd       string
	builtArgs      []string
}

func (m *mockAdapter) Name() string { return m.name }

func (m *mockAdapter) ResolveConfig(role string) (*SessionConfig, error) {
	if m.resolveErr != nil {
		return nil, m.resolveErr
	}
	if m.config != nil {
		cfg := *m.config
		cfg.Role = role
		return &cfg, nil
	}
	return &SessionConfig{
		Provider: m.name,
		Role:     role,
		Command:  "mock-agent",
		Args:     []string{"--auto"},
		Env:      map[string]string{"MOCK_KEY": "test"},
		WorkDir:  "/tmp/mock",
		Model:    "mock-model",
		Timeout:  5 * time.Minute,
	}, nil
}

func (m *mockAdapter) ProvisionHooks(workDir string, role string) error {
	return m.provisionErr
}

func (m *mockAdapter) BuildCommand(cfg *SessionConfig, prompt string) (string, []string) {
	if m.builtCmd != "" {
		return m.builtCmd, m.builtArgs
	}
	return cfg.Command, append(cfg.Args, prompt)
}

func (m *mockAdapter) ReadinessCheck(cfg *SessionConfig) ReadinessStrategy {
	if m.readiness.Type != "" {
		return m.readiness
	}
	return ReadinessStrategy{
		Type:  "delay",
		Delay: 2 * time.Second,
	}
}

// --- Tests ---

func TestNewSessionLauncher(t *testing.T) {
	adapter := &mockAdapter{name: "test"}
	launcher := NewSessionLauncher(adapter)

	if launcher == nil {
		t.Fatal("expected non-nil launcher")
	}
	if launcher.adapter.Name() != "test" {
		t.Errorf("expected adapter name 'test', got %q", launcher.adapter.Name())
	}
	if launcher.sessions == nil {
		t.Fatal("expected sessions map to be initialized")
	}
}

func TestSpawnSession(t *testing.T) {
	adapter := &mockAdapter{name: "mock"}
	launcher := NewSessionLauncher(adapter)

	sess, err := launcher.Spawn("polecat", "/tmp/work", "fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if sess.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if sess.Role != "polecat" {
		t.Errorf("expected role 'polecat', got %q", sess.Role)
	}
	if sess.Config == nil {
		t.Fatal("expected non-nil config")
	}
	if sess.Config.Provider != "mock" {
		t.Errorf("expected provider 'mock', got %q", sess.Config.Provider)
	}
	if sess.Config.WorkDir != "/tmp/work" {
		t.Errorf("expected workdir '/tmp/work', got %q", sess.Config.WorkDir)
	}
	if sess.Status != StatusPending {
		t.Errorf("expected status %q, got %q", StatusPending, sess.Status)
	}
	if sess.StartedAt.IsZero() {
		t.Error("expected non-zero StartedAt")
	}
	if sess.Prompt != "fix the bug" {
		t.Errorf("expected prompt 'fix the bug', got %q", sess.Prompt)
	}
}

func TestSpawnSession_ResolveError(t *testing.T) {
	adapter := &mockAdapter{
		name:       "mock",
		resolveErr: errTest("resolve failed"),
	}
	launcher := NewSessionLauncher(adapter)

	_, err := launcher.Spawn("unknown", "/tmp", "do stuff")
	if err == nil {
		t.Fatal("expected error from resolve failure")
	}
	if !strings.Contains(err.Error(), "resolve failed") {
		t.Errorf("expected resolve error, got: %v", err)
	}
}

func TestSpawnSession_ProvisionError(t *testing.T) {
	adapter := &mockAdapter{
		name:         "mock",
		provisionErr: errTest("provision hooks failed"),
	}
	launcher := NewSessionLauncher(adapter)

	_, err := launcher.Spawn("polecat", "/tmp", "do stuff")
	if err == nil {
		t.Fatal("expected error from provision failure")
	}
	if !strings.Contains(err.Error(), "provision hooks failed") {
		t.Errorf("expected provision error, got: %v", err)
	}
}

func TestGetSession(t *testing.T) {
	adapter := &mockAdapter{name: "mock"}
	launcher := NewSessionLauncher(adapter)

	sess, _ := launcher.Spawn("polecat", "/tmp", "work")

	got, ok := launcher.GetSession(sess.ID)
	if !ok {
		t.Fatal("expected to find session")
	}
	if got.ID != sess.ID {
		t.Errorf("expected session ID %q, got %q", sess.ID, got.ID)
	}

	_, ok = launcher.GetSession("nonexistent-id")
	if ok {
		t.Error("expected not to find nonexistent session")
	}
}

func TestListSessions(t *testing.T) {
	adapter := &mockAdapter{name: "mock"}
	launcher := NewSessionLauncher(adapter)

	sessions := launcher.ListSessions()
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}

	launcher.Spawn("polecat", "/tmp/a", "task a")
	launcher.Spawn("mayor", "/tmp/b", "task b")
	launcher.Spawn("crew", "/tmp/c", "task c")

	sessions = launcher.ListSessions()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}

	roles := make(map[string]bool)
	for _, s := range sessions {
		roles[s.Role] = true
	}
	for _, expected := range []string{"polecat", "mayor", "crew"} {
		if !roles[expected] {
			t.Errorf("expected role %q in sessions", expected)
		}
	}
}

func TestSessionStatus(t *testing.T) {
	// Verify all status constants are distinct.
	statuses := []SessionStatus{
		StatusPending,
		StatusStarting,
		StatusReady,
		StatusRunning,
		StatusDone,
		StatusFailed,
	}
	seen := make(map[SessionStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status value: %q", s)
		}
		seen[s] = true
	}

	// Verify valid status transitions via SetStatus.
	adapter := &mockAdapter{name: "mock"}
	launcher := NewSessionLauncher(adapter)
	sess, _ := launcher.Spawn("polecat", "/tmp", "work")

	if sess.Status != StatusPending {
		t.Errorf("expected initial status %q, got %q", StatusPending, sess.Status)
	}

	sess.SetStatus(StatusStarting)
	if sess.Status != StatusStarting {
		t.Errorf("expected status %q, got %q", StatusStarting, sess.Status)
	}

	sess.SetStatus(StatusReady)
	if sess.Status != StatusReady {
		t.Errorf("expected status %q, got %q", StatusReady, sess.Status)
	}

	sess.SetStatus(StatusRunning)
	if sess.Status != StatusRunning {
		t.Errorf("expected status %q, got %q", StatusRunning, sess.Status)
	}

	sess.SetStatus(StatusDone)
	if sess.Status != StatusDone {
		t.Errorf("expected status %q, got %q", StatusDone, sess.Status)
	}
}

// --- ElectrictownAdapter tests ---

func newTestConfig() *provider.Config {
	return &provider.Config{
		Providers: map[string]provider.ProviderConfig{
			"anthropic": {
				Type:    "anthropic",
				BaseURL: "https://api.anthropic.com",
				APIKey:  "test-key",
			},
			"ollama-local": {
				Type:    "ollama",
				BaseURL: "http://localhost:11434",
			},
		},
		Models: map[string]provider.ModelConfig{
			"claude-sonnet": {Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"qwen-local":    {Provider: "ollama-local", Model: "qwen3-coder:32b"},
		},
		Roles: map[string]provider.RoleConfig{
			"mayor":   {Model: "claude-sonnet", Fallbacks: []string{"qwen-local"}},
			"polecat": {Model: "qwen-local"},
			"crew":    {Model: "qwen-local"},
		},
		Defaults: provider.DefaultsConfig{
			Model:       "qwen-local",
			MaxTokens:   4096,
			Temperature: 0.0,
		},
	}
}

func TestElectrictownAdapter_Name(t *testing.T) {
	cfg := newTestConfig()
	adapter := NewElectrictownAdapter(cfg, "/etc/electrictown/config.yaml")
	if adapter.Name() != "electrictown" {
		t.Errorf("expected name 'electrictown', got %q", adapter.Name())
	}
}

func TestElectrictownAdapter_ResolveConfig(t *testing.T) {
	cfg := newTestConfig()
	adapter := NewElectrictownAdapter(cfg, "/etc/electrictown/config.yaml")

	// Test resolving a known role.
	sessCfg, err := adapter.ResolveConfig("mayor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sessCfg.Provider != "electrictown" {
		t.Errorf("expected provider 'electrictown', got %q", sessCfg.Provider)
	}
	if sessCfg.Role != "mayor" {
		t.Errorf("expected role 'mayor', got %q", sessCfg.Role)
	}
	if sessCfg.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model 'claude-sonnet-4-20250514', got %q", sessCfg.Model)
	}
	if sessCfg.Command != "et" {
		t.Errorf("expected command 'et', got %q", sessCfg.Command)
	}

	// Test resolving a role that falls back to defaults.
	sessCfg, err = adapter.ResolveConfig("unknown-role")
	if err != nil {
		t.Fatalf("unexpected error for default role: %v", err)
	}
	if sessCfg.Model != "qwen3-coder:32b" {
		t.Errorf("expected default model 'qwen3-coder:32b', got %q", sessCfg.Model)
	}
}

func TestElectrictownAdapter_ResolveConfig_NoDefault(t *testing.T) {
	cfg := newTestConfig()
	cfg.Defaults.Model = ""
	adapter := NewElectrictownAdapter(cfg, "/etc/electrictown/config.yaml")

	_, err := adapter.ResolveConfig("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown role with no default")
	}
}

func TestElectrictownAdapter_BuildCommand(t *testing.T) {
	cfg := newTestConfig()
	adapter := NewElectrictownAdapter(cfg, "/etc/electrictown/config.yaml")

	sessCfg := &SessionConfig{
		Provider: "electrictown",
		Role:     "polecat",
		Command:  "et",
		Model:    "qwen3-coder:32b",
	}

	cmd, args := adapter.BuildCommand(sessCfg, "fix the authentication bug")
	if cmd != "et" {
		t.Errorf("expected command 'et', got %q", cmd)
	}

	// Verify the args contain the expected pieces.
	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "run") {
		t.Errorf("expected 'run' in args, got: %v", args)
	}
	if !strings.Contains(argsStr, "--config") {
		t.Errorf("expected '--config' in args, got: %v", args)
	}
	if !strings.Contains(argsStr, "--role") {
		t.Errorf("expected '--role' in args, got: %v", args)
	}
	if !strings.Contains(argsStr, "polecat") {
		t.Errorf("expected 'polecat' in args, got: %v", args)
	}
	if !strings.Contains(argsStr, "fix the authentication bug") {
		t.Errorf("expected prompt in args, got: %v", args)
	}
}

func TestElectrictownAdapter_ReadinessCheck(t *testing.T) {
	cfg := newTestConfig()
	adapter := NewElectrictownAdapter(cfg, "/etc/electrictown/config.yaml")

	sessCfg := &SessionConfig{
		Provider: "electrictown",
		Role:     "polecat",
	}

	readiness := adapter.ReadinessCheck(sessCfg)
	if readiness.Type != "delay" {
		t.Errorf("expected readiness type 'delay', got %q", readiness.Type)
	}
	if readiness.Delay <= 0 {
		t.Error("expected positive delay duration")
	}
}

func TestElectrictownAdapter_ProvisionHooks(t *testing.T) {
	cfg := newTestConfig()
	adapter := NewElectrictownAdapter(cfg, "/etc/electrictown/config.yaml")

	// ProvisionHooks is a no-op for electrictown, should not error.
	err := adapter.ProvisionHooks("/tmp/test", "polecat")
	if err != nil {
		t.Errorf("expected no error from ProvisionHooks, got: %v", err)
	}
}

func TestSpawnSession_UniqueIDs(t *testing.T) {
	adapter := &mockAdapter{name: "mock"}
	launcher := NewSessionLauncher(adapter)

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		sess, err := launcher.Spawn("crew", "/tmp", "work")
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		if ids[sess.ID] {
			t.Fatalf("duplicate session ID on spawn %d: %s", i, sess.ID)
		}
		ids[sess.ID] = true
	}
}

func TestSessionOutput(t *testing.T) {
	adapter := &mockAdapter{name: "mock"}
	launcher := NewSessionLauncher(adapter)

	sess, _ := launcher.Spawn("polecat", "/tmp", "work")
	sess.Output.WriteString("line 1\n")
	sess.Output.WriteString("line 2\n")

	if !strings.Contains(sess.Output.String(), "line 1") {
		t.Error("expected output to contain 'line 1'")
	}
	if !strings.Contains(sess.Output.String(), "line 2") {
		t.Error("expected output to contain 'line 2'")
	}
}

// errTest is a simple error type for testing.
type errTest string

func (e errTest) Error() string { return string(e) }
