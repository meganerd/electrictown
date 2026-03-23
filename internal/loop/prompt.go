package loop

import (
	"fmt"
	"os"
	"strings"
)

// DefaultPromptTemplate is the template used when no custom template is provided.
const DefaultPromptTemplate = `You are working on the electrictown project. Complete the following ticket.

## Ticket: {{.ID}} — {{.Title}}

{{.Description}}

## Acceptance Criteria

{{.Acceptance}}

## Instructions

1. Implement ONLY this one ticket. Do not work on other tickets.
2. Follow existing code patterns and conventions in the codebase.
3. Run "go build ./..." to verify the build passes.
4. Run "go test ./..." to verify all tests pass.
5. If you create new code, add appropriate unit tests.
6. Do NOT commit — the loop runner handles commits.
7. When done, exit cleanly.
`

// BuildPrompt constructs the prompt for a single ticket iteration.
// If templatePath is non-empty, the custom template is loaded from that file.
func BuildPrompt(ticket Ticket, templatePath string) (string, error) {
	tmpl := DefaultPromptTemplate
	if templatePath != "" {
		data, err := os.ReadFile(templatePath)
		if err != nil {
			return "", fmt.Errorf("reading template %s: %w", templatePath, err)
		}
		tmpl = string(data)
	}

	// Simple template substitution (avoids text/template for simplicity).
	result := tmpl
	result = strings.ReplaceAll(result, "{{.ID}}", ticket.ID)
	result = strings.ReplaceAll(result, "{{.Title}}", ticket.Title)
	result = strings.ReplaceAll(result, "{{.Description}}", ticket.Description)
	result = strings.ReplaceAll(result, "{{.Acceptance}}", ticket.Acceptance)
	result = strings.ReplaceAll(result, "{{.Priority}}", fmt.Sprintf("%d", ticket.Priority))
	result = strings.ReplaceAll(result, "{{.Type}}", ticket.Type)

	return result, nil
}
