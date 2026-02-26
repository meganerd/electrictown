package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ByobuRunner wraps a TmuxRunner, using byobu for session creation and
// delegating pane operations to the underlying tmux. Byobu is detected
// via the BYOBU_BACKEND environment variable or PATH lookup.
type ByobuRunner struct {
	inner *TmuxRunner
}

// NewByobuRunner creates a ByobuRunner that uses byobu for session creation
// and delegates pane/session operations to the given TmuxRunner.
func NewByobuRunner(inner *TmuxRunner) *ByobuRunner {
	return &ByobuRunner{inner: inner}
}

// NewSession creates a new detached session using byobu.
func (b *ByobuRunner) NewSession(name, command, workDir string) error {
	args := []string{"new-session", "-d", "-s", name}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	if command != "" {
		args = append(args, command)
	}

	cmd := b.inner.runCmd("byobu", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("byobu new-session %q: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SendKeys delegates to the underlying TmuxRunner.
func (b *ByobuRunner) SendKeys(name, text string) error {
	return b.inner.SendKeys(name, text)
}

// CapturePane delegates to the underlying TmuxRunner.
func (b *ByobuRunner) CapturePane(name string) (string, error) {
	return b.inner.CapturePane(name)
}

// ListSessions delegates to the underlying TmuxRunner.
func (b *ByobuRunner) ListSessions() ([]string, error) {
	return b.inner.ListSessions()
}

// KillSession delegates to the underlying TmuxRunner.
func (b *ByobuRunner) KillSession(name string) error {
	return b.inner.KillSession(name)
}

// HasSession delegates to the underlying TmuxRunner.
func (b *ByobuRunner) HasSession(name string) bool {
	return b.inner.HasSession(name)
}

// DetectByobu checks whether byobu is available via the BYOBU_BACKEND
// environment variable or PATH lookup.
func DetectByobu() bool {
	if os.Getenv("BYOBU_BACKEND") != "" {
		return true
	}
	_, err := exec.LookPath("byobu")
	return err == nil
}

// NewAutoRunner returns a ByobuRunner if byobu is detected, otherwise
// a plain TmuxRunner.
func NewAutoRunner() Runner {
	tr := NewTmuxRunner()
	if DetectByobu() {
		return NewByobuRunner(tr)
	}
	return tr
}

// Compile-time interface compliance.
var _ Runner = (*ByobuRunner)(nil)
