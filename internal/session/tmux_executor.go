package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/meganerd/electrictown/internal/tmux"
)

// TmuxExecutor implements Executor by launching agent sessions in tmux panes.
// Execute is non-blocking — it returns after the tmux session is created.
type TmuxExecutor struct {
	runner  tmux.Runner
	adapter ProviderAdapter
}

// NewTmuxExecutor creates a TmuxExecutor using the given tmux Runner and adapter.
func NewTmuxExecutor(runner tmux.Runner, adapter ProviderAdapter) *TmuxExecutor {
	return &TmuxExecutor{
		runner:  runner,
		adapter: adapter,
	}
}

// Execute creates a tmux session for the agent and sends the initial command.
// The session is named et-{role}-{short-hex} where short-hex is the first 4
// characters of the session ID. Returns immediately after session creation.
func (e *TmuxExecutor) Execute(ctx context.Context, sess *Session) error {
	sess.SetStatus(StatusStarting)

	// Build the tmux session name: et-{role}-{first 4 hex chars of ID}.
	shortHex := sess.ID
	if len(shortHex) > 4 {
		shortHex = shortHex[:4]
	}
	tmuxName := fmt.Sprintf("et-%s-%s", sess.Config.Role, shortHex)

	// Check for collision.
	if e.runner.HasSession(tmuxName) {
		sess.SetStatus(StatusFailed)
		return fmt.Errorf("tmux session %q already exists", tmuxName)
	}

	// Build the command from the adapter.
	cmdName, args := e.adapter.BuildCommand(sess.Config, sess.Prompt)
	fullCmd := cmdName
	if len(args) > 0 {
		fullCmd = cmdName + " " + strings.Join(args, " ")
	}

	// Create the tmux session with the command.
	if err := e.runner.NewSession(tmuxName, fullCmd, sess.Config.WorkDir); err != nil {
		sess.SetStatus(StatusFailed)
		return fmt.Errorf("create tmux session: %w", err)
	}

	// Store the tmux session name in the session output for later reference.
	sess.mu.Lock()
	sess.Output.WriteString(fmt.Sprintf("tmux-session: %s\n", tmuxName))
	sess.StartedAt = time.Now()
	sess.mu.Unlock()

	sess.SetStatus(StatusRunning)
	return nil
}

// Stop terminates the tmux session associated with the given session ID.
// It searches for sessions matching the et-*-{shortHex} pattern.
func (e *TmuxExecutor) Stop(sessionID string) error {
	shortHex := sessionID
	if len(shortHex) > 4 {
		shortHex = shortHex[:4]
	}

	// Find matching tmux session.
	sessions, err := e.runner.ListSessions()
	if err != nil {
		return fmt.Errorf("list tmux sessions: %w", err)
	}

	prefix := fmt.Sprintf("et-")
	suffix := fmt.Sprintf("-%s", shortHex)
	for _, name := range sessions {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			return e.runner.KillSession(name)
		}
	}

	return fmt.Errorf("no tmux session found for session ID %q", sessionID)
}

// WaitForReady polls capture-pane output for the configured prompt prefix.
// Uses 500ms poll interval and 60s timeout. Falls back to delay strategy
// if the readiness type is not "prompt".
func (e *TmuxExecutor) WaitForReady(ctx context.Context, sess *Session) error {
	strategy := e.adapter.ReadinessCheck(sess.Config)

	switch strategy.Type {
	case "prompt":
		return e.waitForPrompt(ctx, sess, strategy)
	case "delay":
		select {
		case <-time.After(strategy.Delay):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	default:
		// Unknown strategy, use a default 3s delay.
		select {
		case <-time.After(3 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// waitForPrompt polls capture-pane for the prompt prefix.
func (e *TmuxExecutor) waitForPrompt(ctx context.Context, sess *Session, strategy ReadinessStrategy) error {
	shortHex := sess.ID
	if len(shortHex) > 4 {
		shortHex = shortHex[:4]
	}
	tmuxName := fmt.Sprintf("et-%s-%s", sess.Config.Role, shortHex)

	timeout := 60 * time.Second
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("readiness timeout after %v waiting for prompt %q", timeout, strategy.PromptPrefix)
		case <-ticker.C:
			output, err := e.runner.CapturePane(tmuxName)
			if err != nil {
				continue // Session may not be fully initialized yet.
			}
			if strings.Contains(output, strategy.PromptPrefix) {
				sess.SetStatus(StatusReady)
				return nil
			}
		}
	}
}

// Compile-time interface compliance.
var _ Executor = (*TmuxExecutor)(nil)
