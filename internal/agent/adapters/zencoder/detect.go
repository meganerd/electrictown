package zencoder

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectResult holds Zencoder CLI detection information.
type DetectResult struct {
	Found   bool
	Path    string
	Version string
}

// Detect checks if Zencoder CLI is available.
func Detect() DetectResult {
	result := DetectResult{}

	path, err := exec.LookPath("zen")
	if err != nil {
		return result
	}
	result.Found = true
	result.Path = path

	out, err := exec.Command("zen", "--version").CombinedOutput()
	if err != nil {
		result.Version = "unknown"
		return result
	}
	result.Version = strings.TrimSpace(string(out))
	return result
}

// DefaultCommand returns the CLI binary name for Zencoder.
func DefaultCommand() string {
	return "zen"
}

// FormatDetectStatus returns a human-readable status string.
func FormatDetectStatus(d DetectResult) string {
	if !d.Found {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", d.Version, d.Path)
}
