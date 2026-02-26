# Electrictown - Agent Workflow Guide

## Project Overview

Electrictown is a provider-agnostic multi-agent coding orchestrator. It forks
gastown's architecture (Go) and replaces the Anthropic/Claude Code coupling with
a flexible model routing layer inspired by LiteLLM, reimplemented in Go.

## Using Beads (bd)

This project uses `bd` (beads) for issue tracking with dependency chains.

### Key Commands

```bash
bd list                    # All issues
bd ready                   # Unblocked work ready to claim
bd blocked                 # Issues waiting on dependencies
bd show ET-<id>            # Issue details
bd dep tree ET-<id>        # Dependency visualization
bd update ET-<id> --status in_progress  # Claim work
bd close ET-<id>           # Mark done
```

### Workflow

1. Run `bd ready` to find available work
2. Claim a task: `bd update ET-<id> --status in_progress --assignee <you>`
3. Do the work on a feature branch
4. Close when done: `bd close ET-<id> --reason "description"`
5. Check `bd ready` for next available task

### Dependency Rules

- Never work on a blocked task — check `bd blocked` first
- Epics are parent containers; work on child tasks
- When discovering new work, create issues with `bd create` and link dependencies

## Architecture

### Core Components

- **Provider Router** (Go LiteLLM equivalent): Unified OpenAI-compatible interface
  routing to any LLM provider (Ollama local/cloud, OpenAI, Anthropic, Google, etc.)
- **Agent Orchestrator** (gastown fork): Mayor, Polecats, Crew, Witness, Refinery
  roles managing multi-agent coding workflows
- **Config System**: YAML-driven model→provider mapping per agent role
- **Tmux Session Manager** (`internal/tmux/`): Provider-agnostic tmux/byobu session
  management via `Runner` interface. `TmuxRunner` calls tmux directly, `ByobuRunner`
  auto-detects byobu and delegates pane operations to tmux.
- **Executor Layer** (`internal/session/executor.go`): `Executor` interface abstracts
  session launch strategy. `SubprocessExecutor` for `et run`, `TmuxExecutor` for
  `et session spawn`. New executors can be added without modifying `SessionLauncher`.

- **WorkerPool** (`internal/pool/`): Parallel worker dispatcher that fans subtasks out
  across multiple model aliases using the `Balancer` for round-robin assignment and the
  `Router` for request routing. Bounded concurrency via semaphore channel. Results ordered
  by subtask index. Per-worker errors don't abort other workers.

### Design Principles

1. **Go-native**: All glue code in Go. No Python/Node dependencies.
2. **Self-contained**: Minimal external dependencies. 3rd party libs used in PoC
   tracked for replacement (see dependency-replacement epic).
3. **Provider-agnostic**: Any model from any provider for any gastown role.
4. **Frontier supervisor**: Configurable supervisor model (e.g., Claude, GPT-4)
   orchestrates cheaper worker models (e.g., local Ollama).
