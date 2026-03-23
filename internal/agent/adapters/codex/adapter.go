package codex

import (
	"context"
	"fmt"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for OpenAI's Codex CLI.
type Adapter struct{}

// New creates a new Codex adapter.
func New() *Adapter {
	return &Adapter{}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeCodex
}

// Execute runs a task through Codex CLI using `codex exec`.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args := buildArgs(task)

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session := &agent.Session{
		Command: "codex",
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, "")
	if err != nil {
		return result, fmt.Errorf("codex execution: %w", err)
	}

	normalizer := &CodexNormalizer{}
	if normErr := normalizer.Normalize(result); normErr != nil {
		_ = normErr
	}

	return result, nil
}

// buildArgs constructs the Codex CLI arguments.
// Uses `codex exec` for one-shot execution.
func buildArgs(task agent.Task) []string {
	args := []string{"exec"}

	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	// Add --json for structured output.
	args = append(args, "--json")

	// Add any extra flags from config.
	args = append(args, task.Flags...)

	// The prompt is the last argument.
	args = append(args, task.Prompt)

	return args
}

var _ agent.Backend = (*Adapter)(nil)
