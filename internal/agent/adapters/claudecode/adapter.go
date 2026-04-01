package claudecode

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

// defaultTimeout is the default max duration for a Claude Code execution.
const defaultTimeout = 30 * time.Minute

// Config holds Claude Code-specific configuration from the YAML agent config.
// Fields map to Claude Code CLI flags and are translated by buildArgs.
type Config struct {
	Model           string
	Flags           []string
	WorkingDir      string
	Env             map[string]string
	Timeout         time.Duration
	SystemPrompt    string
	AllowedTools    []string
	DisallowedTools []string
	JSONSchema      string
	PermissionMode  string
	MaxBudgetUSD    float64
	AddDirs         []string
}

// Adapter implements agent.Backend for Claude Code CLI.
type Adapter struct {
	config Config
}

// New creates a new Claude Code adapter with the given configuration.
// A zero Config produces the same behavior as the previous bare adapter.
func New(cfg Config) *Adapter {
	return &Adapter{config: cfg}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeClaudeCode
}

// Execute runs a task through Claude Code using -p/--print mode.
// Config fields are merged into the task — task fields take precedence.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	// Merge config defaults into task where task doesn't override.
	if task.WorkingDir == "" && a.config.WorkingDir != "" {
		task.WorkingDir = a.config.WorkingDir
	}
	if task.Model == "" && a.config.Model != "" {
		task.Model = a.config.Model
	}
	if len(a.config.Env) > 0 {
		if task.Env == nil {
			task.Env = make(map[string]string, len(a.config.Env))
		}
		for k, v := range a.config.Env {
			if _, exists := task.Env[k]; !exists {
				task.Env[k] = v
			}
		}
	}

	timeout := task.Timeout
	if timeout == 0 {
		timeout = a.config.Timeout
	}
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := a.buildArgs(task)

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
// Config fields are translated to their corresponding CLI flags.
func (a *Adapter) buildArgs(task agent.Task) []string {
	args := []string{
		"-p",                       // non-interactive print mode
		"--output-format", "json",  // structured JSON output
	}

	// Add model if specified (task overrides config — already merged in Execute).
	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	// Claude Code-specific flags from config.
	if a.config.SystemPrompt != "" {
		args = append(args, "--system-prompt", a.config.SystemPrompt)
	}
	if len(a.config.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(a.config.AllowedTools, " "))
	}
	if len(a.config.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(a.config.DisallowedTools, " "))
	}
	if a.config.JSONSchema != "" {
		args = append(args, "--json-schema", a.config.JSONSchema)
	}
	if a.config.PermissionMode != "" {
		args = append(args, "--permission-mode", a.config.PermissionMode)
	}
	if a.config.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(a.config.MaxBudgetUSD, 'f', -1, 64))
	}
	for _, dir := range a.config.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	// Extra flags from config, then from task (task flags last = highest precedence).
	args = append(args, a.config.Flags...)
	args = append(args, task.Flags...)

	// The prompt is the last argument.
	args = append(args, task.Prompt)

	return args
}

// Ensure Adapter implements agent.Backend at compile time.
var _ agent.Backend = (*Adapter)(nil)
