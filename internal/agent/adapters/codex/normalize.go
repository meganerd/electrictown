package codex

import (
	"encoding/json"

	"github.com/meganerd/electrictown/internal/agent"
)

// codexResult represents the JSON output from `codex exec --json`.
type codexResult struct {
	Result   string `json:"result"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error"`
}

// CodexNormalizer parses Codex CLI JSON output into structured results.
type CodexNormalizer struct{}

// Normalize parses Codex's JSON output and enriches the Result.
func (n *CodexNormalizer) Normalize(result *agent.Result) error {
	if result.Stdout == "" {
		return nil
	}

	var cr codexResult
	if err := json.Unmarshal([]byte(result.Stdout), &cr); err != nil {
		// Not JSON — leave stdout as-is (plain text mode).
		return nil
	}

	if cr.Result != "" {
		result.Stdout = cr.Result
	}
	if cr.Error != "" {
		result.Stderr = cr.Error
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
	}

	// Parse for diff-style file changes.
	diffNorm := &agent.DiffNormalizer{}
	return diffNorm.Normalize(result)
}

func init() {
	agent.RegisterNormalizer(agent.TypeCodex, &CodexNormalizer{})
}
