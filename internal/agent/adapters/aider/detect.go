// Package aider implements the agent.Backend adapter for the aider-chat
// CLI. It uses --message and --yes flags for non-interactive execution
// with git integration for tracking changes.
package aider

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectResult holds Aider CLI detection information.
type DetectResult struct {
	Found   bool
	Path    string
	Version string
}

// Detect checks if Aider CLI is available.
func Detect() DetectResult {
	result := DetectResult{}

	path, err := exec.LookPath("aider")
	if err != nil {
		return result
	}
	result.Found = true
	result.Path = path

	out, err := exec.Command("aider", "--version").CombinedOutput()
	if err != nil {
		result.Version = "unknown"
		return result
	}
	result.Version = strings.TrimSpace(string(out))
	return result
}

// DefaultCommand returns the CLI binary name for Aider.
func DefaultCommand() string {
	return "aider"
}

// FormatDetectStatus returns a human-readable status string.
func FormatDetectStatus(d DetectResult) string {
	if !d.Found {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", d.Version, d.Path)
}
