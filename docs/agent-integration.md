# Using electrictown from within AI coding agents

electrictown (`et`) is designed to be invoked by AI coding agents — Claude Code, Codex,
Cursor, Aider, etc. — as a parallel task executor and iterative builder. This document
describes the standard patterns for doing so.

---

## Core pattern: spawn → evaluate → loop

```
Agent reads task
  → calls et run --iterate --output-dir ./build/ "task"
  → et decomposes, runs 8 workers in parallel, synthesizes, iterates build
  → agent reads ./build/ and evaluates the result
  → if unsatisfactory, agent calls et run again with refined task or --max-iterations higher
```

---

## Claude Code integration

### Minimal invocation

Run a parallel coding task and write output to a working directory:

```bash
et run --output-dir ./build/ "Build a Go HTTP downloader with retry logic, checksum validation, and progress reporting"
```

Claude Code can then read the generated files, evaluate them against the original requirement,
and decide whether to refine or proceed.

### Evaluation → refinement loop

```bash
# Round 1: generate
et run --output-dir ./build/ --iterate "Build a REST API server in Go with CRUD endpoints for users"

# Claude Code evaluates build/. If incomplete:
et run --output-dir ./build/ \
  "The user endpoints are missing DELETE and PATCH. The User struct is missing Email. Fix these gaps." \
  --max-iterations 5 --iterate

# Continue until runnable
```

### Full iterative session (recommended)

```bash
et run \
  --output-dir ./src/ \
  --iterate \
  --max-iterations 5 \
  --timeout 60 \
  "Build a CLI tool that watches a directory for file changes and sends diffs to a webhook"
```

The `--iterate` flag makes `et` attempt `go build ./...` (or equivalent) after synthesis,
and re-dispatch fix subtasks to the specific workers whose files caused errors. This loops
up to `--max-iterations` times without agent intervention.

### Claude Code as an et role (v1.4+)

Claude Code can be used as any et role — mayor, worker, reviewer, or tester — via
the `agents:` section in your YAML config. This gives each role its own system prompt,
tool restrictions, permission mode, and budget cap.

#### Worker role with read/write access

```yaml
agents:
  cc-worker:
    type: claude-code
    model: sonnet
    permission_mode: bypassPermissions
    max_budget_usd: 2.00
    timeout: 1800

roles:
  polecat:
    agent: cc-worker
```

#### Mayor role with read-only tools

```yaml
agents:
  cc-mayor:
    type: claude-code
    model: opus
    permission_mode: plan
    system_prompt: >
      You are a project coordinator. Analyze tasks and decompose them into
      subtasks with [depends: N] markers for DAG execution.
    allowed_tools:
      - Read
      - Glob
      - Grep
    max_budget_usd: 0.50
    timeout: 300

roles:
  mayor:
    agent: cc-mayor
```

#### Reviewer role with structured JSON output

```yaml
agents:
  cc-reviewer:
    type: claude-code
    model: opus
    permission_mode: plan
    system_prompt: >
      Review the code output for correctness, style, and completeness.
      Score from 0-10 and provide actionable feedback.
    allowed_tools:
      - Read
      - Glob
      - Grep
      - "Bash(test:*)"
    json_schema: >
      {"type":"object","properties":{"score":{"type":"number"},
      "feedback":{"type":"string"},"pass":{"type":"boolean"}},
      "required":["score","feedback","pass"]}
    max_budget_usd: 1.00
    timeout: 600

roles:
  reviewer:
    agent: cc-reviewer
```

#### Multi-directory access

```yaml
agents:
  cc-full:
    type: claude-code
    model: sonnet
    add_dirs:
      - /data/shared-libs
      - /data/test-fixtures
    working_dir: /data/myproject
```

#### Available Claude Code config fields

| Field | Type | CLI Flag | Description |
|-------|------|----------|-------------|
| `system_prompt` | string | `--system-prompt` | System prompt for the role |
| `allowed_tools` | string[] | `--allowedTools` | Tools to allow (whitelist) |
| `disallowed_tools` | string[] | `--disallowedTools` | Tools to deny (blacklist) |
| `json_schema` | string | `--json-schema` | JSON Schema for structured output |
| `permission_mode` | string | `--permission-mode` | default, plan, acceptEdits, bypassPermissions, dontAsk, auto |
| `max_budget_usd` | float | `--max-budget-usd` | Max dollar spend per invocation |
| `add_dirs` | string[] | `--add-dir` | Additional directories for tool access |

These fields are validated at config load time and only allowed on `type: claude-code` agents.

---

## Codex / OpenAI Agents integration

Codex agents can shell out to `et` as a tool call:

```python
# In your agent's tool definition:
{
    "name": "electrictown_run",
    "description": "Run electrictown to build a parallel coding task across 8 workers. Returns path to output directory.",
    "parameters": {
        "task": "string — the coding task description",
        "output_dir": "string — directory to write generated files",
        "iterate": "bool — attempt to build and fix errors automatically"
    }
}

# Tool implementation:
import subprocess, os

def electrictown_run(task: str, output_dir: str, iterate: bool = True) -> dict:
    cmd = ["et", "run", "--output-dir", output_dir, "--timeout", "60"]
    if iterate:
        cmd += ["--iterate", "--max-iterations", "4"]
    cmd.append(task)
    result = subprocess.run(cmd, capture_output=True, text=True)
    return {
        "stdout": result.stdout,
        "stderr": result.stderr,
        "exit_code": result.returncode,
        "output_dir": output_dir
    }
```

After `electrictown_run`, the agent reads the output directory, evaluates files, and
calls the tool again with a refinement task if needed.

---

## Aider / Cursor integration

Use `et` for the **initial scaffold generation phase**, then hand off to Aider/Cursor
for interactive refinement:

```bash
# 1. Generate initial codebase with et (fast parallel generation)
et run --output-dir ./myapp/ --iterate "Build a Go gRPC service for user management with PostgreSQL"

# 2. Hand off to Aider for interactive refinement
cd myapp/
aider --model gpt-4o "Add integration tests for the user service"
```

---

## et session for persistent work

For longer-running tasks, use `et session` to keep a worker alive across multiple rounds:

```bash
# Spawn a session in a working directory
et session spawn --work-dir ./myproject/ "Build a distributed task queue in Go"

# Later, send a refinement
et session send <session-id> "The worker pool is missing graceful shutdown. Add context cancellation."

# Attach to watch progress
et session attach <session-id>
```

---

## Reading et output programmatically

`et` writes structured output to the log directory (default: `~/Documents/electrictown-logs/{date}_{id}/`):

| File | Contents |
|------|----------|
| `_synthesis.md` | Final synthesized response from the mayor |
| `worker-N.out` | Raw output from worker N (when no `--output-dir`) |
| `_build_iter1.log` | stdout+stderr from build iteration 1 (with `--iterate`) |

An agent can read `_synthesis.md` as its evaluation input:

```bash
LOGDIR=$(ls -td ~/Documents/electrictown-logs/*/ | head -1)
cat "${LOGDIR}/_synthesis.md"
```

---

## Recommended task description format

Workers perform better with specific, concrete task descriptions. Include:

1. **Language and framework** — "in Go using the standard library"
2. **File structure** — "package structure: `cmd/server/main.go`, `internal/handler/`, `internal/store/`"
3. **Interfaces** — define shared interfaces explicitly when workers will need to interoperate
4. **Acceptance criteria** — "must compile with `go build ./...` and pass `go vet ./...`"

Example:

```
Build a Go HTTP API server that:
- Listens on :8080
- Has endpoints: GET /health, GET /items, POST /items, DELETE /items/{id}
- Uses an in-memory store (map[string]Item, protected by sync.RWMutex)
- Item struct: {ID string, Name string, CreatedAt time.Time}
- Package structure: cmd/server/main.go, internal/handler/handler.go, internal/store/store.go
- Must compile with go build ./... and handle concurrent requests safely
```

---

## Spinning up a new et session from within Claude Code

When you want Claude Code itself to delegate parallel coding work to electrictown:

```bash
# In CLAUDE.md or as a tool instruction:
# "To generate a large code module, use: et run --output-dir {dir} --iterate '{task}'"
# "Then read {dir}/ and evaluate the output before proceeding."
```

Or as a slash command in Claude Code:

```
/run et run --output-dir ./generated/ --iterate --timeout 45 "Build the authentication module for this project"
```

Claude Code will execute the command, wait for completion, then read and evaluate the
generated files — continuing with whatever the user asked next.
