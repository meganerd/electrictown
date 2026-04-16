package agent

import (
	"regexp"
	"strings"
)

// Normalizer parses agent-specific output into structured FileChange entries.
// Each agent adapter provides its own Normalizer implementation.
type Normalizer interface {
	// Normalize extracts file changes from raw agent output and enriches
	// the Result. The result's Stdout/Stderr/ExitCode are already populated;
	// the normalizer adds FileChanges and may clean up Stdout (e.g., strip
	// diff blocks to leave only prose).
	Normalize(result *Result) error
}

// DefaultNormalizer is a pass-through that returns the result unchanged.
// Used when no agent-specific normalizer is registered.
type DefaultNormalizer struct{}

func (d *DefaultNormalizer) Normalize(_ *Result) error {
	return nil
}

// DiffNormalizer parses unified diff blocks from agent output into FileChange
// structs. It detects "--- a/path" / "+++ b/path" headers followed by diff hunks.
type DiffNormalizer struct{}

// unifiedDiffHeader matches --- a/path and +++ b/path lines.
var unifiedDiffHeader = regexp.MustCompile(`(?m)^---\s+a/(.+)\n\+\+\+\s+b/(.+)`)

func (d *DiffNormalizer) Normalize(result *Result) error {
	diffs := extractDiffs(result.Stdout)
	result.FileChanges = append(result.FileChanges, diffs...)
	return nil
}

// extractDiffs finds unified diff blocks in text and returns FileChange entries.
func extractDiffs(text string) []FileChange {
	matches := unifiedDiffHeader.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var changes []FileChange
	for i, match := range matches {
		if len(match) < 6 {
			continue
		}
		// Extract the +++ path (destination file).
		path := text[match[4]:match[5]]

		// Extract the full diff block (from --- to next --- or end of text).
		start := match[0]
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		diffBlock := strings.TrimSpace(text[start:end])

		action := ActionModify
		if strings.Contains(diffBlock, "new file mode") {
			action = ActionCreate
		} else if strings.Contains(diffBlock, "deleted file mode") {
			action = ActionDelete
		}

		changes = append(changes, FileChange{
			Path:   path,
			Action: action,
			Diff:   diffBlock,
		})
	}
	return changes
}

// NormalizerRegistry maps agent types to their normalizer implementations.
var NormalizerRegistry = map[AgentType]Normalizer{
	// Default entries — adapters override these when they register.
	TypeClaudeCode: &DefaultNormalizer{},
	TypeCodex:      &DefaultNormalizer{},
	TypeAider:      &DiffNormalizer{},
	TypeOpenCode:   &DefaultNormalizer{},
	TypeOpenClaw:   &DefaultNormalizer{},
	TypeGeminiCLI:  &DefaultNormalizer{},
	TypeZencoder:   &DefaultNormalizer{},
}

// GetNormalizer returns the normalizer for an agent type, falling back to
// DefaultNormalizer if none is registered.
func GetNormalizer(t AgentType) Normalizer {
	if n, ok := NormalizerRegistry[t]; ok {
		return n
	}
	return &DefaultNormalizer{}
}

// RegisterNormalizer allows agent adapters to register their normalizer.
func RegisterNormalizer(t AgentType, n Normalizer) {
	NormalizerRegistry[t] = n
}
