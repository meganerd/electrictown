// Package codex implements the agent.Backend adapter for OpenAI's Codex CLI.
// It uses `codex exec` for one-shot execution with --json for structured output.
// Note: Codex CLI has known instability in non-TTY environments.
package codex

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectResult holds Codex CLI detection information.
type DetectResult struct {
	Found   bool
	Path    string
	Version string
}

// Detect checks if Codex CLI is available.
func Detect() DetectResult {
	result := DetectResult{}

	path, err := exec.LookPath("codex")
	if err != nil {
		return result
	}
	result.Found = true
	result.Path = path

	out, err := exec.Command("codex", "--version").CombinedOutput()
	if err != nil {
		result.Version = "unknown"
		return result
	}
	result.Version = strings.TrimSpace(string(out))
	return result
}

// DefaultCommand returns the CLI binary name for Codex.
func DefaultCommand() string {
	return "codex"
}

// FormatDetectStatus returns a human-readable status string.
func FormatDetectStatus(d DetectResult) string {
	if !d.Found {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", d.Version, d.Path)
}
