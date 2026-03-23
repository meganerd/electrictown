// Package cursor implements the agent.Backend adapter for Cursor
// (cursor.com). Cursor is an AI-powered code editor with a CLI interface
// that supports background/headless agent mode.
package cursor

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectResult holds Cursor CLI detection information.
type DetectResult struct {
	Found   bool
	Path    string
	Version string
}

// Detect checks if Cursor CLI is available.
func Detect() DetectResult {
	result := DetectResult{}

	path, err := exec.LookPath("cursor")
	if err != nil {
		return result
	}
	result.Found = true
	result.Path = path

	out, err := exec.Command("cursor", "--version").CombinedOutput()
	if err != nil {
		result.Version = "unknown"
		return result
	}
	result.Version = strings.TrimSpace(string(out))
	return result
}

// DefaultCommand returns the CLI binary name for Cursor.
func DefaultCommand() string {
	return "cursor"
}

// FormatDetectStatus returns a human-readable status string.
func FormatDetectStatus(d DetectResult) string {
	if !d.Found {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", d.Version, d.Path)
}
