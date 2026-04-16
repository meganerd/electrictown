// Package zencoder implements the agent.Backend adapter for Zencoder CLI
// (zencoder.ai). Zencoder is an AI coding agent that supports headless
// execution via `zen run "task"` for non-interactive one-shot mode.
package zencoder

import (
	"context"
	"fmt"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for Zencoder CLI.
type Adapter struct{}

// New creates a new Zencoder adapter.
func New() *Adapter {
	return &Adapter{}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeZencoder
}

// Execute runs a task through Zencoder using `zen run`.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args := buildArgs(task)

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session := &agent.Session{
		Command: "zen",
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, "")
	if err != nil {
		return result, fmt.Errorf("zencoder execution: %w", err)
	}

	normalizer := &ZencoderNormalizer{}
	if normErr := normalizer.Normalize(result); normErr != nil {
		_ = normErr
	}

	return result, nil
}

// buildArgs constructs the Zencoder CLI arguments for a task.
func buildArgs(task agent.Task) []string {
	args := []string{"run"}

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
