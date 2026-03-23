package opencode

import (
	"context"
	"fmt"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for OpenCode CLI.
// Supports two modes:
//   - Run mode: `opencode run "task"` for one-shot execution
//   - Serve mode: `opencode run --attach http://host:port "task"` for remote
type Adapter struct {
	// ServeURL is the optional URL of a running OpenCode server.
	// When set, tasks are submitted via --attach instead of local execution.
	ServeURL string
}

// New creates a new OpenCode adapter.
func New() *Adapter {
	return &Adapter{}
}

// NewWithServe creates an OpenCode adapter that connects to a running server.
func NewWithServe(url string) *Adapter {
	return &Adapter{ServeURL: url}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeOpenCode
}

// Execute runs a task through OpenCode using `opencode run`.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args := a.buildArgs(task)

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session := &agent.Session{
		Command: "opencode",
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, "")
	if err != nil {
		return result, fmt.Errorf("opencode execution: %w", err)
	}

	normalizer := &OpenCodeNormalizer{}
	if normErr := normalizer.Normalize(result); normErr != nil {
		_ = normErr
	}

	return result, nil
}

// buildArgs constructs the OpenCode CLI arguments.
func (a *Adapter) buildArgs(task agent.Task) []string {
	args := []string{"run"}

	// Quiet mode for script-friendly output.
	args = append(args, "-q")

	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	// Serve mode: attach to remote OpenCode server.
	if a.ServeURL != "" {
		args = append(args, "--attach", a.ServeURL)
	}

	// Add any extra flags from config.
	args = append(args, task.Flags...)

	// The prompt is the last argument.
	args = append(args, task.Prompt)

	return args
}

var _ agent.Backend = (*Adapter)(nil)
