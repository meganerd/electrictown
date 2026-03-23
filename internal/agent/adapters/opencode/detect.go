// Package opencode implements the agent.Backend adapter for OpenCode
// (opencode.ai). OpenCode is Go-based with strong headless support via
// `opencode run` for one-shot and `opencode serve` for persistent server mode.
package opencode

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectResult holds OpenCode CLI detection information.
type DetectResult struct {
	Found   bool
	Path    string
	Version string
}

// Detect checks if OpenCode CLI is available.
func Detect() DetectResult {
	result := DetectResult{}

	path, err := exec.LookPath("opencode")
	if err != nil {
		return result
	}
	result.Found = true
	result.Path = path

	out, err := exec.Command("opencode", "--version").CombinedOutput()
	if err != nil {
		result.Version = "unknown"
		return result
	}
	result.Version = strings.TrimSpace(string(out))
	return result
}

// DefaultCommand returns the CLI binary name for OpenCode.
func DefaultCommand() string {
	return "opencode"
}

// FormatDetectStatus returns a human-readable status string.
func FormatDetectStatus(d DetectResult) string {
	if !d.Found {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", d.Version, d.Path)
}
