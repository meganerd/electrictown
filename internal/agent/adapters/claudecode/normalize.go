package claudecode

import (
	"encoding/json"

	"github.com/meganerd/electrictown/internal/agent"
)

// claudeResult represents the JSON output from Claude Code --output-format json.
type claudeResult struct {
	Type       string  `json:"type"`
	Subtype    string  `json:"subtype"`
	IsError    bool    `json:"is_error"`
	Result     string  `json:"result"`
	DurationMS int64   `json:"duration_ms"`
	CostUSD    float64 `json:"total_cost_usd"`
	StopReason string  `json:"stop_reason"`
	SessionID  string  `json:"session_id"`
	NumTurns   int     `json:"num_turns"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ClaudeCodeNormalizer parses Claude Code JSON output into structured results.
type ClaudeCodeNormalizer struct{}

// Normalize parses Claude Code's JSON output and enriches the Result.
// If the output is valid JSON, it extracts the result text, error status,
// and metadata. If JSON parsing fails, the raw stdout is preserved as-is.
func (n *ClaudeCodeNormalizer) Normalize(result *agent.Result) error {
	if result.Stdout == "" {
		return nil
	}

	var cr claudeResult
	if err := json.Unmarshal([]byte(result.Stdout), &cr); err != nil {
		// Not JSON — leave stdout as-is (might be plain text output).
		return nil
	}

	// Replace raw JSON stdout with the extracted result text.
	result.Stdout = cr.Result

	// If Claude Code reported an error, reflect it in the exit code.
	if cr.IsError && result.ExitCode == 0 {
		result.ExitCode = 1
	}

	// Parse the result text for diff-style file changes.
	diffNorm := &agent.DiffNormalizer{}
	return diffNorm.Normalize(result)
}

func init() {
	// Register the Claude Code normalizer in the global registry.
	agent.RegisterNormalizer(agent.TypeClaudeCode, &ClaudeCodeNormalizer{})
}
