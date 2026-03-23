// Package agent defines the core interface and types for CLI coding agent
// backends. Unlike the provider package (LLM chat completions via HTTP),
// agent backends model process lifecycle: spawn a CLI tool, feed it a task,
// and collect results (text output + file changes).
package agent

import (
	"context"
	"time"
)

// AgentType identifies a supported coding agent CLI.
type AgentType string

const (
	TypeClaudeCode AgentType = "claude-code"
	TypeCodex      AgentType = "codex"
	TypeAider      AgentType = "aider"
	TypeOpenCode   AgentType = "opencode"
	TypeOpenClaw   AgentType = "openclaw"
	TypeGeminiCLI  AgentType = "gemini-cli"
)

// ValidAgentTypes returns all recognized agent type strings.
func ValidAgentTypes() []AgentType {
	return []AgentType{
		TypeClaudeCode, TypeCodex, TypeAider,
		TypeOpenCode, TypeOpenClaw, TypeGeminiCLI,
	}
}

// IsValidAgentType reports whether t is a recognized agent type.
func IsValidAgentType(t AgentType) bool {
	for _, valid := range ValidAgentTypes() {
		if t == valid {
			return true
		}
	}
	return false
}

// Backend is the core interface that all coding agent adapters implement.
// It models process-based execution rather than HTTP request/response.
type Backend interface {
	// Type returns the agent type identifier (e.g., "claude-code", "aider").
	Type() AgentType

	// Execute runs a task through the coding agent and returns the result.
	// The context controls timeout and cancellation.
	Execute(ctx context.Context, task Task) (*Result, error)
}

// Task describes a unit of work to send to a coding agent.
type Task struct {
	// Prompt is the task description / instruction for the agent.
	Prompt string

	// WorkingDir is the directory the agent should operate in.
	// If empty, the agent uses its default (typically cwd).
	WorkingDir string

	// Env contains additional environment variables for the agent process.
	// These are merged with the current process environment.
	Env map[string]string

	// Timeout is the maximum duration for the agent to complete the task.
	// Zero means no timeout (rely on context deadline instead).
	Timeout time.Duration

	// Model is the model the agent should use (mapped to --model flag).
	// If empty, the agent uses its default model.
	Model string

	// Flags contains additional CLI flags to pass to the agent.
	Flags []string
}

// Result captures the output of an agent execution.
type Result struct {
	// Stdout is the agent's standard output (prose, explanations, logs).
	Stdout string

	// Stderr is the agent's standard error output.
	Stderr string

	// FileChanges lists files the agent modified, created, or deleted.
	FileChanges []FileChange

	// ExitCode is the agent process exit code (0 = success).
	ExitCode int

	// Duration is how long the agent took to complete.
	Duration time.Duration
}

// Success reports whether the agent exited cleanly (exit code 0).
func (r *Result) Success() bool {
	return r.ExitCode == 0
}

// FileChange represents a single file modification by the agent.
type FileChange struct {
	// Path is the file path relative to the working directory.
	Path string

	// Action describes what happened: "create", "modify", "delete".
	Action FileAction

	// Diff is the unified diff of the change (if available).
	Diff string

	// Content is the full file content after the change (if available).
	// For deletions, this is empty.
	Content string
}

// FileAction describes the type of file change.
type FileAction string

const (
	ActionCreate FileAction = "create"
	ActionModify FileAction = "modify"
	ActionDelete FileAction = "delete"
)
