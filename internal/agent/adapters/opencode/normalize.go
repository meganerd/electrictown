package opencode

import (
	"github.com/meganerd/electrictown/internal/agent"
)

// OpenCodeNormalizer parses OpenCode's output for file changes.
// OpenCode in quiet mode produces clean output suitable for diff parsing.
type OpenCodeNormalizer struct{}

// Normalize extracts file changes from OpenCode output.
func (n *OpenCodeNormalizer) Normalize(result *agent.Result) error {
	if result.Stdout == "" {
		return nil
	}

	// OpenCode output may contain unified diffs.
	diffNorm := &agent.DiffNormalizer{}
	return diffNorm.Normalize(result)
}

func init() {
	agent.RegisterNormalizer(agent.TypeOpenCode, &OpenCodeNormalizer{})
}
