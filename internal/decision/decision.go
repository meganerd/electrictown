// Package decision provides structured decision logging for observability.
// Every significant agent choice (decomposition, scoring, retry) is captured
// as a Decision record and written to a JSONL log for post-hoc analysis.
package decision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Decision captures a single agent decision with context.
type Decision struct {
	Timestamp string `json:"timestamp"`
	Phase     string `json:"phase"`       // e.g., "decompose", "review", "guardrail", "build-fix"
	Agent     string `json:"agent"`       // e.g., "mayor", "reviewer", "worker"
	Intent    string `json:"intent"`      // what the agent was trying to do
	Action    string `json:"action"`      // what action was chosen
	Outcome   string `json:"outcome"`     // "success", "failure", "retry"
	Detail    string `json:"detail"`      // brief explanation or metric
	TokenCost int    `json:"token_cost"`  // tokens consumed by this decision
}

// Logger writes Decision records to a JSONL file. It is safe for concurrent use.
type Logger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// NewLogger creates a decision logger writing to the given file path.
// The file is created if it doesn't exist, appended to if it does.
// Returns a no-op logger (nil) if the path is empty.
func NewLogger(path string) (*Logger, error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open decision log: %w", err)
	}
	return &Logger{
		file: f,
		enc:  json.NewEncoder(f),
	}, nil
}

// Log writes a decision record. If the logger is nil (no-op mode), this is a no-op.
func (l *Logger) Log(d Decision) {
	if l == nil {
		return
	}
	if d.Timestamp == "" {
		d.Timestamp = time.Now().Format(time.RFC3339)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enc.Encode(d) //nolint: silently drop encode errors for non-critical logging
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
