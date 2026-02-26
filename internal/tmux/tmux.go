// Package tmux provides a provider-agnostic interface for managing tmux sessions.
// It wraps tmux shell commands behind a Runner interface, enabling testability
// via injectable command execution.
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// Runner abstracts tmux operations for session management.
type Runner interface {
	// NewSession creates a new detached tmux session with the given name.
	// The command is executed inside the session. workDir sets the starting directory.
	NewSession(name, command, workDir string) error

	// SendKeys sends text input to the named tmux session.
	SendKeys(name, text string) error

	// CapturePane captures the visible content of the named session's pane.
	CapturePane(name string) (string, error)

	// ListSessions returns the names of all active tmux sessions.
	ListSessions() ([]string, error)

	// KillSession terminates the named tmux session.
	KillSession(name string) error

	// HasSession checks whether a tmux session with the given name exists.
	HasSession(name string) bool
}

// CmdFunc is the signature for creating an *exec.Cmd. It matches exec.Command.
type CmdFunc func(name string, args ...string) *exec.Cmd

// TmuxRunner implements Runner by calling the tmux binary via os/exec.
// The runCmd field is injectable for testing.
type TmuxRunner struct {
	runCmd CmdFunc
}

// NewTmuxRunner creates a TmuxRunner that calls the real tmux binary.
func NewTmuxRunner() *TmuxRunner {
	return &TmuxRunner{
		runCmd: exec.Command,
	}
}

// NewTmuxRunnerWithCmd creates a TmuxRunner with a custom command function for testing.
func NewTmuxRunnerWithCmd(fn CmdFunc) *TmuxRunner {
	return &TmuxRunner{
		runCmd: fn,
	}
}

// NewSession creates a new detached tmux session.
func (r *TmuxRunner) NewSession(name, command, workDir string) error {
	args := []string{"new-session", "-d", "-s", name}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	if command != "" {
		args = append(args, command)
	}

	cmd := r.runCmd("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session %q: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SendKeys sends text to the named tmux session followed by Enter.
func (r *TmuxRunner) SendKeys(name, text string) error {
	cmd := r.runCmd("tmux", "send-keys", "-t", name, text, "Enter")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys %q: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CapturePane captures the visible content of the session's current pane.
func (r *TmuxRunner) CapturePane(name string) (string, error) {
	cmd := r.runCmd("tmux", "capture-pane", "-t", name, "-p")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane %q: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ListSessions returns the names of all active tmux sessions.
func (r *TmuxRunner) ListSessions() ([]string, error) {
	cmd := r.runCmd("tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// tmux returns error when no sessions exist — treat as empty list.
		if strings.Contains(string(out), "no server running") ||
			strings.Contains(string(out), "no sessions") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}

// KillSession terminates the named tmux session.
func (r *TmuxRunner) KillSession(name string) error {
	cmd := r.runCmd("tmux", "kill-session", "-t", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session %q: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// HasSession checks whether a tmux session with the given name exists.
func (r *TmuxRunner) HasSession(name string) bool {
	cmd := r.runCmd("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

// Compile-time interface compliance.
var _ Runner = (*TmuxRunner)(nil)
