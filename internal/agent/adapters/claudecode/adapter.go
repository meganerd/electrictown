package claudecode

import (
	"context"
	"fmt"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

// defaultTimeout is the default max duration for a Claude Code execution.
const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for Claude Code CLI.
type Adapter struct{}

// New creates a new Claude Code adapter.
func New() *Adapter {
	return &Adapter{}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeClaudeCode
}

// Execute runs a task through Claude Code using -p/--print mode.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args := buildArgs(task)

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session := &agent.Session{
		Command: "claude",
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, "")
	if err != nil {
		return result, fmt.Errorf("claude code execution: %w", err)
	}

	// Normalize output — parse JSON result if available.
	normalizer := &ClaudeCodeNormalizer{}
	if normErr := normalizer.Normalize(result); normErr != nil {
		// Normalization failure is non-fatal — raw output is still useful.
		_ = normErr
	}

	return result, nil
}

// buildArgs constructs the Claude Code CLI arguments for a task.
func buildArgs(task agent.Task) []string {
	args := []string{
		"-p",                    // non-interactive print mode
		"--output-format", "json", // structured JSON output
	}

	// Add model if specified.
	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	// Add any extra flags from config.
	args = append(args, task.Flags...)

	// The prompt is the last argument.
	args = append(args, task.Prompt)

	return args
}

// Ensure Adapter implements agent.Backend at compile time.
var _ agent.Backend = (*Adapter)(nil)
