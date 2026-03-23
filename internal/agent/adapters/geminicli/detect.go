// Package geminicli implements the agent.Backend adapter for Google's
// Gemini CLI. It uses -p/--prompt mode with --output-format json for
// structured non-interactive execution.
package geminicli

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectResult holds Gemini CLI detection information.
type DetectResult struct {
	Found   bool
	Path    string
	Version string
}

// Detect checks if Gemini CLI is available.
func Detect() DetectResult {
	result := DetectResult{}

	path, err := exec.LookPath("gemini")
	if err != nil {
		return result
	}
	result.Found = true
	result.Path = path

	out, err := exec.Command("gemini", "--version").CombinedOutput()
	if err != nil {
		result.Version = "unknown"
		return result
	}
	result.Version = strings.TrimSpace(string(out))
	return result
}

// DefaultCommand returns the CLI binary name for Gemini CLI.
func DefaultCommand() string {
	return "gemini"
}

// FormatDetectStatus returns a human-readable status string.
func FormatDetectStatus(d DetectResult) string {
	if !d.Found {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", d.Version, d.Path)
}
