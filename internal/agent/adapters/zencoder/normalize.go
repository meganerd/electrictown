package zencoder

import (
	"github.com/meganerd/electrictown/internal/agent"
)

// ZencoderNormalizer parses Zencoder's output for file changes.
type ZencoderNormalizer struct{}

// Normalize extracts file changes from Zencoder output.
func (n *ZencoderNormalizer) Normalize(result *agent.Result) error {
	if result.Stdout == "" {
		return nil
	}

	// Zencoder output may contain unified diffs.
	diffNorm := &agent.DiffNormalizer{}
	return diffNorm.Normalize(result)
}

func init() {
	agent.RegisterNormalizer(agent.TypeZencoder, &ZencoderNormalizer{})
}
