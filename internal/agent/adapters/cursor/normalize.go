package cursor

import (
	"github.com/meganerd/electrictown/internal/agent"
)

// CursorNormalizer parses Cursor's output for file changes.
type CursorNormalizer struct{}

// Normalize extracts file changes from Cursor output.
func (n *CursorNormalizer) Normalize(result *agent.Result) error {
	if result.Stdout == "" {
		return nil
	}

	// Cursor output may contain unified diffs.
	diffNorm := &agent.DiffNormalizer{}
	return diffNorm.Normalize(result)
}

func init() {
	agent.RegisterNormalizer(agent.TypeCursor, &CursorNormalizer{})
}
