package geminicli

import (
	"context"
	"fmt"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for Google's Gemini CLI.
type Adapter struct{}

// New creates a new Gemini CLI adapter.
func New() *Adapter {
	return &Adapter{}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeGeminiCLI
}

// Execute runs a task through Gemini CLI using -p (prompt) mode.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args := buildArgs(task)

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session := &agent.Session{
		Command: "gemini",
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, "")
	if err != nil {
		return result, fmt.Errorf("gemini cli execution: %w", err)
	}

	normalizer := &GeminiCLINormalizer{}
	if normErr := normalizer.Normalize(result); normErr != nil {
		_ = normErr
	}

	return result, nil
}

// buildArgs constructs the Gemini CLI arguments.
func buildArgs(task agent.Task) []string {
	args := []string{
		"-p", // non-interactive prompt mode
	}

	// Request JSON output for structured parsing.
	args = append(args, "--output-format", "json")

	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	// Add any extra flags from config.
	args = append(args, task.Flags...)

	// The prompt is the last argument.
	args = append(args, task.Prompt)

	return args
}

var _ agent.Backend = (*Adapter)(nil)
