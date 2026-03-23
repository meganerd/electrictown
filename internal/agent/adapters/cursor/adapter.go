package cursor

import (
	"context"
	"fmt"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for Cursor CLI.
// Cursor supports a background agent mode via its CLI for headless execution.
type Adapter struct{}

// New creates a new Cursor adapter.
func New() *Adapter {
	return &Adapter{}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeCursor
}

// Execute runs a task through Cursor using its CLI agent mode.
// Cursor's CLI supports `cursor agent` for headless execution (as of 2026).
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args := buildArgs(task)

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session := &agent.Session{
		Command: "cursor",
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, "")
	if err != nil {
		return result, fmt.Errorf("cursor execution: %w", err)
	}

	normalizer := &CursorNormalizer{}
	if normErr := normalizer.Normalize(result); normErr != nil {
		_ = normErr
	}

	return result, nil
}

// buildArgs constructs the Cursor CLI arguments for headless agent execution.
func buildArgs(task agent.Task) []string {
	args := []string{
		"agent",   // headless agent subcommand
		"--print", // non-interactive output mode
	}

	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	// Add any extra flags from config.
	args = append(args, task.Flags...)

	// The prompt/instruction.
	args = append(args, "--prompt", task.Prompt)

	return args
}

var _ agent.Backend = (*Adapter)(nil)
