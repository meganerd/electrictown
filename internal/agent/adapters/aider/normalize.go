package aider

import (
	"regexp"
	"strings"

	"github.com/meganerd/electrictown/internal/agent"
)

// AiderNormalizer parses Aider's terminal output for search/replace blocks
// and git commit messages.
type AiderNormalizer struct{}

// searchReplacePattern matches Aider's SEARCH/REPLACE block format:
// <<<<<<< SEARCH
// old code
// =======
// new code
// >>>>>>> REPLACE
var searchReplacePattern = regexp.MustCompile(
	`(?m)^(.+)\n<<<<<<< SEARCH\n([\s\S]*?)\n=======\n([\s\S]*?)\n>>>>>>> REPLACE`,
)

// commitPattern matches Aider's commit message output.
var commitPattern = regexp.MustCompile(`(?m)^Commit [a-f0-9]+ (.+)$`)

// Normalize extracts file changes from Aider's search/replace blocks.
func (n *AiderNormalizer) Normalize(result *agent.Result) error {
	if result.Stdout == "" {
		return nil
	}

	// Extract search/replace blocks.
	matches := searchReplacePattern.FindAllStringSubmatch(result.Stdout, -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		filePath := strings.TrimSpace(match[1])
		result.FileChanges = append(result.FileChanges, agent.FileChange{
			Path:   filePath,
			Action: agent.ActionModify,
			Diff:   match[0], // full search/replace block as diff
		})
	}

	// Also try standard unified diff parsing.
	diffNorm := &agent.DiffNormalizer{}
	return diffNorm.Normalize(result)
}

func init() {
	agent.RegisterNormalizer(agent.TypeAider, &AiderNormalizer{})
}
