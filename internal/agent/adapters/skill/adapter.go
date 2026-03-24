// Package skill implements agent.Backend for arbitrary CLI skills.
// A skill is any executable that accepts a task prompt (via trailing argument
// or stdin) and produces text output on stdout. This covers Claude Code Skills
// (bun/ts), shell scripts, Python scripts, or any command-line tool.
//
// YAML config examples:
//
//	agents:
//	  pai-mayor:
//	    type: skill
//	    command: bun                                      # runtime
//	    skill_path: ~/.claude/skills/PAI/Tools/pai.ts     # script path (first arg to command)
//	    input_mode: arg                                   # "arg" (default) or "stdin"
//
//	  my-analyzer:
//	    type: skill
//	    command: /usr/local/bin/analyze                    # direct binary, no skill_path
//	    input_mode: stdin                                 # pipe task via stdin
//
//	  pai-alias:
//	    type: skill
//	    command: pai                                       # uses PATH lookup
package skill

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meganerd/electrictown/internal/agent"
)

const defaultTimeout = 30 * time.Minute

// Adapter implements agent.Backend for CLI skill execution.
type Adapter struct {
	// Command is the runtime binary (e.g., "bun", "node", "python", "pai").
	Command string

	// SkillPath is the path to the skill script. When set, it is passed as
	// the first argument to Command (e.g., "bun <skill_path> <task>").
	// When empty, Command is invoked directly with the task.
	SkillPath string

	// InputMode controls how the task prompt is delivered:
	//   "arg"   — appended as the final CLI argument (default)
	//   "stdin" — piped to the process's standard input
	InputMode string
}

// New creates a skill adapter with the given settings.
func New(command, skillPath, inputMode string) *Adapter {
	if inputMode == "" {
		inputMode = "arg"
	}
	if skillPath != "" {
		skillPath = expandHome(skillPath)
	}
	return &Adapter{
		Command:   command,
		SkillPath: skillPath,
		InputMode: inputMode,
	}
}

// Type returns the agent type identifier.
func (a *Adapter) Type() agent.AgentType {
	return agent.TypeSkill
}

// Execute runs the skill with the given task.
func (a *Adapter) Execute(ctx context.Context, task agent.Task) (*agent.Result, error) {
	args, stdin := a.buildArgs(task)

	timeout := task.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := a.Command
	if command == "" {
		return nil, fmt.Errorf("skill adapter: no command configured")
	}

	session := &agent.Session{
		Command: command,
		Args:    args,
		Dir:     task.WorkingDir,
		Env:     task.Env,
	}

	result, err := session.RunOneShot(ctx, stdin)
	if err != nil {
		return result, fmt.Errorf("skill execution: %w", err)
	}

	return result, nil
}

// buildArgs constructs the CLI arguments and optional stdin for the skill.
func (a *Adapter) buildArgs(task agent.Task) (args []string, stdin string) {
	// If SkillPath is set, it's the first arg (the script for the runtime).
	if a.SkillPath != "" {
		args = append(args, a.SkillPath)
	}

	// Add any extra flags from config.
	args = append(args, task.Flags...)

	// Deliver the prompt.
	switch a.InputMode {
	case "stdin":
		stdin = task.Prompt
	default: // "arg"
		args = append(args, task.Prompt)
	}

	return args, stdin
}

// expandHome replaces a leading ~/ with the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

var _ agent.Backend = (*Adapter)(nil)
