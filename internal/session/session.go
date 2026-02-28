// Package session provides a provider-agnostic abstraction layer for launching
// and managing agent sessions. It decouples session lifecycle management from
// any specific agent CLI provider (Claude Code, Gemini CLI, etc.) through the
// ProviderAdapter interface.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/meganerd/electrictown/internal/provider"
)

// SessionStatus represents the lifecycle state of an agent session.
type SessionStatus string

const (
	StatusPending  SessionStatus = "pending"
	StatusStarting SessionStatus = "starting"
	StatusReady    SessionStatus = "ready"
	StatusRunning  SessionStatus = "running"
	StatusDone     SessionStatus = "done"
	StatusFailed   SessionStatus = "failed"
)

// SessionConfig describes everything needed to launch an agent session.
type SessionConfig struct {
	Provider         string            // adapter name
	Role             string            // agent role (mayor, polecat, etc.)
	Command          string            // CLI binary path
	Args             []string          // base arguments
	Env              map[string]string // environment variables
	WorkDir          string            // working directory
	OutputDir        string            // directory for writing output files (empty = stdout only)
	InstructionsFile string            // CLAUDE.md, AGENTS.md, etc.
	Model            string            // model to use (resolved from config)
	Timeout          time.Duration     // session timeout
}

// ReadinessStrategy describes how to detect that an agent is ready for input.
type ReadinessStrategy struct {
	Type         string        // "prompt", "delay", "health"
	PromptPrefix string        // for "prompt" type: prefix to look for in output
	Delay        time.Duration // for "delay" type: how long to wait
	HealthURL    string        // for "health" type: URL to poll
}

// Session represents a running agent session.
type Session struct {
	ID        string
	Role      string
	Config    *SessionConfig
	Status    SessionStatus
	StartedAt time.Time
	Prompt    string
	Output    strings.Builder // captured output

	mu sync.Mutex
}

// SetStatus updates the session status in a thread-safe manner.
func (s *Session) SetStatus(status SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
}

// ProviderAdapter abstracts how different agent CLIs are configured and launched.
// Each supported agent runtime (Claude Code, Gemini CLI, etc.) implements this
// interface to provide provider-specific behavior while keeping the session
// lifecycle management generic.
type ProviderAdapter interface {
	// Name returns the adapter identifier (e.g., "electrictown", "claude", "gemini").
	Name() string

	// ResolveConfig returns the session configuration for a given role.
	ResolveConfig(role string) (*SessionConfig, error)

	// ProvisionHooks writes any hooks/settings files needed in the work directory.
	ProvisionHooks(workDir string, role string) error

	// BuildCommand constructs the CLI command and args to launch the agent.
	BuildCommand(cfg *SessionConfig, prompt string) (cmd string, args []string)

	// ReadinessCheck returns how to detect the agent is ready for input.
	ReadinessCheck(cfg *SessionConfig) ReadinessStrategy
}

// SessionLauncher manages the lifecycle of agent sessions.
type SessionLauncher struct {
	adapter  ProviderAdapter
	exec     Executor // optional; defaults to SubprocessExecutor
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewSessionLauncher creates a new SessionLauncher with the given provider adapter.
// Uses SubprocessExecutor by default (lazily initialized on first Execute/Stop call).
func NewSessionLauncher(adapter ProviderAdapter) *SessionLauncher {
	return &SessionLauncher{
		adapter:  adapter,
		sessions: make(map[string]*Session),
	}
}

// NewSessionLauncherWithExecutor creates a SessionLauncher with an explicit Executor.
func NewSessionLauncherWithExecutor(adapter ProviderAdapter, exec Executor) *SessionLauncher {
	return &SessionLauncher{
		adapter:  adapter,
		exec:     exec,
		sessions: make(map[string]*Session),
	}
}

// Spawn creates a new agent session for the given role. It resolves the session
// configuration through the adapter, provisions any required hooks, and prepares
// the session for launch. The session is created in StatusPending and must be
// started separately (e.g., via tmux).
func (l *SessionLauncher) Spawn(role, workDir, prompt string) (*Session, error) {
	cfg, err := l.adapter.ResolveConfig(role)
	if err != nil {
		return nil, fmt.Errorf("resolve config for role %q: %w", role, err)
	}

	// Override workdir with the caller's value.
	cfg.WorkDir = workDir

	if err := l.adapter.ProvisionHooks(workDir, role); err != nil {
		return nil, fmt.Errorf("provision hooks for role %q: %w", role, err)
	}

	id, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}

	sess := &Session{
		ID:        id,
		Role:      role,
		Config:    cfg,
		Status:    StatusPending,
		StartedAt: time.Now(),
		Prompt:    prompt,
	}

	l.mu.Lock()
	l.sessions[id] = sess
	l.mu.Unlock()

	return sess, nil
}

// GetSession returns the session with the given ID, if it exists.
func (l *SessionLauncher) GetSession(id string) (*Session, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	sess, ok := l.sessions[id]
	return sess, ok
}

// ListSessions returns all tracked sessions.
func (l *SessionLauncher) ListSessions() []*Session {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]*Session, 0, len(l.sessions))
	for _, s := range l.sessions {
		result = append(result, s)
	}
	return result
}

// generateSessionID produces a random hex session identifier.
func generateSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ---------------------------------------------------------------------------
// ElectrictownAdapter
// ---------------------------------------------------------------------------

// ElectrictownAdapter implements ProviderAdapter using the electrictown provider
// router configuration. It routes all sessions through the electrictown router,
// which handles auth, rate limiting, cost tracking, and provider routing.
type ElectrictownAdapter struct {
	cfg        *provider.Config
	configPath string
}

// NewElectrictownAdapter creates an adapter backed by the given electrictown config.
// configPath is the filesystem path to the YAML config file, used when building
// CLI commands.
func NewElectrictownAdapter(cfg *provider.Config, configPath string) *ElectrictownAdapter {
	return &ElectrictownAdapter{
		cfg:        cfg,
		configPath: configPath,
	}
}

// Name returns "electrictown".
func (a *ElectrictownAdapter) Name() string {
	return "electrictown"
}

// ResolveConfig resolves the session configuration for a role by looking up the
// role's model assignment in the electrictown config. Falls back to defaults if
// the role is not explicitly configured.
func (a *ElectrictownAdapter) ResolveConfig(role string) (*SessionConfig, error) {
	_, modelName, err := a.cfg.ResolveRole(role)
	if err != nil {
		return nil, fmt.Errorf("electrictown: %w", err)
	}

	return &SessionConfig{
		Provider: "electrictown",
		Role:     role,
		Command:  "et",
		Args:     []string{},
		Env:      map[string]string{},
		Model:    modelName,
		Timeout:  30 * time.Minute,
	}, nil
}

// ProvisionHooks is a no-op for the electrictown adapter. The router handles
// all lifecycle management externally.
func (a *ElectrictownAdapter) ProvisionHooks(workDir string, role string) error {
	return nil
}

// BuildCommand constructs the et CLI command to launch an agent session.
// Returns the command and args: et run --config <path> --role <role> [--output-dir <dir>] <prompt>
func (a *ElectrictownAdapter) BuildCommand(cfg *SessionConfig, prompt string) (string, []string) {
	args := []string{
		"run",
		"--config", a.configPath,
		"--role", cfg.Role,
	}
	if cfg.OutputDir != "" {
		args = append(args, "--output-dir", cfg.OutputDir)
	}
	args = append(args, prompt)
	return "et", args
}

// ReadinessCheck returns a delay-based readiness strategy for electrictown
// sessions. The router handles readiness detection externally, so we use a
// simple delay to allow the agent CLI to initialize.
func (a *ElectrictownAdapter) ReadinessCheck(cfg *SessionConfig) ReadinessStrategy {
	return ReadinessStrategy{
		Type:  "delay",
		Delay: 3 * time.Second,
	}
}

// Compile-time interface compliance checks.
var _ ProviderAdapter = (*ElectrictownAdapter)(nil)
