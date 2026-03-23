# et loop — Ralph Wiggum Unattended Task Runner

The `et loop` subcommand implements the [Ralph Wiggum pattern](https://ghuntley.com/ralph/) for autonomous, unattended task execution. It reads tickets from bd (beads), executes each one via `et run`, verifies with `go build`/`go test`, commits on success, and moves to the next ticket.

## Quick Start

```bash
# Run the loop for all ready tickets
et loop --config ~/electrictown_loop.yaml

# Scope to a specific epic
et loop --config ~/electrictown_loop.yaml --epic ET-225

# Dry run (show what would execute without doing it)
et loop --config ~/electrictown_loop.yaml --dry-run

# Before bed: run up to 50 iterations, 10s cooldown
et loop --config ~/electrictown_loop.yaml --max-iterations 50 --cooldown 10
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | (required) | Path to loop-specific et config YAML |
| `--epic` | (none) | Scope to tickets in this epic |
| `--max-iterations` | 20 | Safety cap on total iterations |
| `--max-retries` | 3 | Per-ticket retry limit before skipping |
| `--cooldown` | 5 | Seconds to wait between iterations |
| `--dry-run` | false | Show what would run without executing |
| `--notify` | true | Send voice notifications on events |
| `--template` | (built-in) | Path to custom prompt template |

## Loop Config

Use a separate config for loop runs to control costs. Local models for generation, mid-tier for planning:

```yaml
# electrictown_loop.yaml — cost-optimized for unattended runs
providers:
  ollama-ai01:
    type: ollama
    base_url: http://ai01:11434
  ollama-local:
    type: ollama
    base_url: http://localhost:11434

models:
  qwen3.5-27b:
    provider: ollama-ai01
    model: qwen3.5:27b
  qwen-coder-local:
    provider: ollama-local
    model: qwen3-coder:latest

roles:
  mayor:
    model: qwen3.5-27b          # strong reasoning for task decomposition
  polecat:
    model: qwen-coder-local     # fast local generation
    pool:
      - qwen-coder-local
      - qwen3.5-27b
  reviewer:
    model: qwen3.5-27b          # code review needs reasoning
  tester:
    model: qwen-coder-local     # test generation is mechanical

defaults:
  model: qwen-coder-local
  max_tokens: 4096
  temperature: 0.0
```

## How It Works

Each iteration:

1. **Read** — calls `bd ready` to get the next unblocked ticket
2. **Filter** — skips epics, skips tickets that exhausted retries
3. **Prompt** — builds a prompt from ticket title, description, and acceptance criteria
4. **Execute** — runs `et run` with the loop config
5. **Verify** — runs `go build ./...` and `go test ./...`
6. **Commit** — if both pass, commits with ticket ID in the message
7. **Close** — calls `bd close` on the ticket
8. **Cooldown** — waits before next iteration
9. **Repeat** — until no tickets remain or max iterations reached

The key Ralph Wiggum insight: each `et run` invocation is a fresh process with clean context. Progress persists in the filesystem (code changes) and git history, not in the LLM's context window.

## Notifications

The loop sends voice notifications via the PAI notification endpoint (`localhost:8888/notify`) for:
- Loop start
- Ticket failure after max retries
- Loop completion (with summary stats)

## Custom Prompt Templates

Use `--template` to provide a custom prompt. The template supports these placeholders:

- `{{.ID}}` — ticket ID (e.g., "ET-225")
- `{{.Title}}` — ticket title
- `{{.Description}}` — full description
- `{{.Acceptance}}` — acceptance criteria
- `{{.Priority}}` — priority number
- `{{.Type}}` — ticket type (task, feature, etc.)
