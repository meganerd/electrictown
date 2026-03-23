// Package claudecode implements the agent.Backend adapter for Anthropic's
// Claude Code CLI. It uses -p/--print mode for non-interactive execution
// with --output-format json for structured output.
package claudecode

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectResult holds Claude Code CLI detection information.
type DetectResult struct {
	Found         bool
	Path          string
	Version       string
	Authenticated bool
}

// Detect checks if Claude Code CLI is available and reports its status.
func Detect() DetectResult {
	result := DetectResult{}

	path, err := exec.LookPath("claude")
	if err != nil {
		return result
	}
	result.Found = true
	result.Path = path

	// Get version and verify it's Claude Code (not another "claude" binary).
	out, err := exec.Command("claude", "--version").CombinedOutput()
	if err != nil {
		result.Version = "unknown"
		return result
	}

	version := strings.TrimSpace(string(out))
	result.Version = version

	// Claude Code version output contains "Claude Code".
	if strings.Contains(version, "Claude Code") || isSemanticVersion(version) {
		result.Authenticated = true // assume authenticated if binary runs
	}

	return result
}

// isSemanticVersion checks if a string looks like a semantic version (x.y.z).
func isSemanticVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		// Remove any non-numeric suffix (e.g., "81 (Claude Code)").
		p = strings.Fields(p)[0]
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// DefaultCommand returns the CLI binary name for Claude Code.
func DefaultCommand() string {
	return "claude"
}

// FormatDetectStatus returns a human-readable status string.
func FormatDetectStatus(d DetectResult) string {
	if !d.Found {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", d.Version, d.Path)
}
