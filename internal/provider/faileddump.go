package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FailedRequestDump captures context from a failed LLM request for offline debugging.
type FailedRequestDump struct {
	Timestamp   string    `json:"timestamp"`
	Model       string    `json:"model"`
	ErrorType   string    `json:"error_type"`
	ErrorMsg    string    `json:"error_message"`
	NumMessages int       `json:"num_messages"`
	Messages    []Message `json:"messages"`
	TokenEst    int       `json:"estimated_tokens"`
}

// DumpFailedRequest writes a failed request's context to ~/.electrictown/failed_requests/
// as a JSON file. Messages are included for debugging but system prompt content is
// truncated to avoid leaking sensitive configuration. Returns the file path written,
// or an error if the dump itself fails.
func DumpFailedRequest(model string, messages []Message, reqErr error) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed dump: cannot determine home dir: %w", err)
	}

	dir := filepath.Join(home, ".electrictown", "failed_requests")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed dump: mkdir: %w", err)
	}

	// Sanitize messages: truncate system prompts to avoid huge dumps,
	// and ensure no raw API keys are present (messages shouldn't contain
	// keys, but truncation limits exposure surface).
	sanitized := make([]Message, len(messages))
	for i, m := range messages {
		sanitized[i] = m
		if m.Role == RoleSystem && len(m.Content) > 500 {
			sanitized[i].Content = m.Content[:500] + "... [truncated]"
		}
	}

	// Estimate tokens: ~4 chars per token.
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}

	errType := "unknown"
	if reqErr != nil {
		errType = string(ClassifyError(reqErr))
	}

	dump := FailedRequestDump{
		Timestamp:   time.Now().Format(time.RFC3339),
		Model:       model,
		ErrorType:   errType,
		ErrorMsg:    reqErr.Error(),
		NumMessages: len(messages),
		Messages:    sanitized,
		TokenEst:    totalChars / 4,
	}

	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed dump: marshal: %w", err)
	}

	ts := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s.json", errType, ts)
	path := filepath.Join(dir, filename)

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed dump: write: %w", err)
	}

	return path, nil
}
