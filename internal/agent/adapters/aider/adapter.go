package aider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for Aider CLI.
type Adapter struct{}

// New creates a new Aider adapter.
func New() *Adapter {
	return &Adapter{}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeAider
}

// Execute runs a task through Aider using --message --yes mode.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args, cleanup, err := buildArgs(task)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return &agent.Result{ExitCode: -1}, fmt.Errorf("aider arg build: %w", err)
	}

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session := &agent.Session{
		Command: "aider",
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, "")
	if err != nil {
		return result, fmt.Errorf("aider execution: %w", err)
	}

	normalizer := &AiderNormalizer{}
	if normErr := normalizer.Normalize(result); normErr != nil {
		_ = normErr
	}

	return result, nil
}

// buildArgs constructs the Aider CLI arguments.
// For long prompts (>1024 chars), uses --message-file with a temp file.
// Returns the args, a cleanup function, and any error.
func buildArgs(task agent.Task) ([]string, func(), error) {
	args := []string{
		"--yes", // auto-confirm all prompts
	}

	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	// Add any extra flags from config.
	args = append(args, task.Flags...)

	// Use --message-file for long prompts to avoid shell arg limits.
	if len(task.Prompt) > 1024 {
		tmpFile, err := os.CreateTemp("", "aider-prompt-*.txt")
		if err != nil {
			return nil, nil, fmt.Errorf("creating temp prompt file: %w", err)
		}
		if _, err := tmpFile.WriteString(task.Prompt); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return nil, nil, fmt.Errorf("writing prompt file: %w", err)
		}
		tmpFile.Close()

		args = append(args, "--message-file", tmpFile.Name())
		cleanup := func() { os.Remove(tmpFile.Name()) }
		return args, cleanup, nil
	}

	args = append(args, "--message", task.Prompt)
	return args, nil, nil
}

// TempDir returns the directory for aider temp files. Exposed for testing.
func TempDir() string {
	return filepath.Join(os.TempDir(), "electrictown-aider")
}

var _ agent.Backend = (*Adapter)(nil)
