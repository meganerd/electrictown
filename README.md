# electrictown

Provider-agnostic multi-agent coding orchestrator in Go.

![Go 1.25+](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)
![Tests](https://img.shields.io/badge/tests-passing-brightgreen)
![License](https://img.shields.io/badge/license-MIT-blue)

## Overview

Electrictown is a multi-agent coding orchestrator that decouples agent role assignment from any specific LLM provider. It defines a unified `Provider` interface and routes requests through a configuration-driven router, so any model from any provider can fill any agent role -- supervisor, worker, reviewer, or polisher.

The problem it solves is tight coupling between agent orchestration logic and LLM vendor APIs. When your supervisor is hardwired to Claude and your workers are hardwired to GPT, switching providers means rewriting orchestration code. Electrictown eliminates that coupling: providers are adapters behind a common interface, models are aliases in a YAML config, and roles map to aliases with automatic fallback chains.

The result is a system where you can run a Claude supervisor with Ollama local workers and a Gemini reviewer, swap any of them by editing one line of YAML, and get automatic failover to fallback models on rate limits or server errors -- all while tracking per-request costs by role, provider, and model.

## Acknowledgments

Electrictown builds on ideas from two projects that deserve recognition:

- **[steveyegge/gastown](https://github.com/AstroGamesCo/gastown)** -- The original multi-agent coding orchestrator that pioneered the role system (mayor, polecat, witness, refinery) that electrictown adopts. Electrictown started as a study of gastown's architecture and is now a clean-room implementation in Go with provider-agnostic routing as its core design goal.

- **[BerriAI/litellm](https://github.com/BerriAI/litellm)** -- Inspired the provider routing concept: a unified interface across LLM providers with model aliasing and fallback chains. Rather than using litellm as a Python dependency, electrictown implements its own Go-native routing layer using only `net/http`.

Both projects are independent works. Electrictown is its own implementation with its own design decisions, but it would not exist without these two as starting points.

## Features

- **4 provider adapters** -- Ollama (local + cloud), OpenAI, Anthropic, Google Gemini -- all native `net/http`, zero SDKs
- **Unified provider router** with model alias resolution and direct `provider/model` addressing
- **Role-based model assignment** -- mayor, polecat, witness, refinery (or any custom role name)
- **Automatic fallback chains** on rate limit (429), timeout, server error (5xx), and context window overflow
- **Cost tracking** with per-request recording and breakdowns by role, provider, and model
- **Session layer** with provider-agnostic agent launching via the `ProviderAdapter` interface
- **Tmux/Byobu session management** -- spawn, list, attach, kill, and send to persistent agent sessions in tmux panes with auto-detection of byobu
- **Cross-platform builds** -- Linux amd64, arm64, riscv64, ppc64, ppc64le
- **Parallel worker pool** -- fan out subtasks across multiple hosts/models concurrently with bounded concurrency
- **Single external dependency** -- `gopkg.in/yaml.v3`

## Parallel Worker Pool

When a role has a `pool` configured, `et run` uses a three-phase pipeline:

1. **Decompose** -- the Mayor supervisor breaks the task into discrete subtasks
2. **Parallel Execute** -- the WorkerPool fans subtasks out across pool members concurrently
3. **Synthesize** -- the Mayor combines worker results into a unified response

Pool members are model aliases, each mapping to a different provider/host. This enables heterogeneous fleet utilization -- e.g., localhost, cloud-host, windows-host-with-ollama, and multiple rk3588 boards all running Ollama.

```yaml
roles:
  polecat:
    model: qwen-local            # primary (used when pool is empty)
    pool:                         # parallel worker pool
      - qwen-local               # localhost
      - qwen-ai01                # ai01
      - qwen-phoenix             # phoenix
      - qwen-rk3588-1            # rk3588 #1
    fallbacks: [qwen-cloud]      # fallback if ALL pool members fail
```

Concurrency is bounded to `min(subtasks, pool_size)` goroutines. Per-worker errors don't abort other workers. Results are returned in subtask order regardless of completion order.

## Architecture

```
                    electrictown.yaml
                          |
                     LoadConfig()
                          |
                       Config
                          |
                    +-----+-----+
                    |   Router   |
                    +-----+-----+
                          |
          +-------+-------+-------+-------+
          |       |       |       |       |
       Ollama  OpenAI Anthropic Gemini  (...)
       Adapter Adapter Adapter  Adapter
          |       |       |       |
          v       v       v       v
       LLM API  LLM API LLM API LLM API
```

### Role System

| Role | Type | Responsibility | Key Methods |
|------|------|---------------|-------------|
| **Mayor** | Supervisor | Decomposes tasks into subtasks, synthesizes worker results | `Decompose()`, `Synthesize()`, `Plan()` |
| **Polecat** | Worker | Executes coding tasks, produces output | `Execute()`, `ExecuteStream()`, `ExecuteWithContext()` |
| **Witness** | Reviewer | Reviews code for correctness, security, quality | `Review()`, `ReviewWithContext()`, `Validate()` |
| **Refinery** | Polisher | Improves code quality, style, documentation | `Refine()`, `RefineWithFeedback()`, `Summarize()` |

All roles are provider-agnostic. Each uses the router's `ChatCompletionForRole()` method, which resolves the configured model and handles fallbacks automatically.

## Quick Start

```bash
# Clone
git clone https://github.com/meganerd/electrictown.git
cd electrictown

# Build
make build

# Configure (edit electrictown.yaml with your providers and API keys)
cp electrictown.yaml electrictown-local.yaml
vi electrictown-local.yaml

# Run
./build/et run --config electrictown-local.yaml "implement a binary search in Go"
```

## Configuration

Electrictown uses a single YAML file with four sections: `providers`, `models`, `roles`, and `defaults`.

```yaml
# Provider connections -- type + endpoint + auth
providers:
  anthropic:
    type: anthropic                        # adapter type: openai | anthropic | ollama | gemini
    base_url: https://api.anthropic.com
    api_key: $ANTHROPIC_API_KEY            # env var references resolved at load time

  openai:
    type: openai
    base_url: https://api.openai.com/v1
    api_key: $OPENAI_API_KEY

  ollama-local:
    type: ollama
    base_url: http://localhost:11434       # no API key needed for local Ollama

  ollama-cloud:
    type: ollama
    base_url: https://api.ollama.com
    api_key: $OLLAMA_API_KEY

# Model aliases -- decouple role config from actual model IDs
models:
  claude-sonnet:
    provider: anthropic
    model: claude-sonnet-4-20250514

  gpt4o:
    provider: openai
    model: gpt-4o

  qwen-coder-local:
    provider: ollama-local
    model: qwen3-coder:32b

  qwen-coder-cloud:
    provider: ollama-cloud
    model: qwen3-coder:480b-cloud

# Role assignments -- map roles to model aliases with fallback chains
roles:
  mayor:
    model: claude-sonnet
    fallbacks: [gpt4o]                     # try gpt4o if Claude hits rate limit

  polecat:
    model: qwen-coder-local
    pool:                                    # parallel worker pool (optional)
      - qwen-coder-local                     # localhost
      - qwen-coder-cloud                     # cloud fallback
    fallbacks: [qwen-coder-cloud]          # fall back to cloud if local is down

  witness:
    model: gpt4o

  refinery:
    model: claude-sonnet

# Defaults -- used when a role has no explicit config
defaults:
  model: qwen-coder-local
  fallbacks: [qwen-coder-cloud]
  max_tokens: 4096
  temperature: 0.0
```

API keys use `$ENV_VAR` syntax and are resolved from the environment at config load time. The config is validated on load -- unknown provider references, duplicate fallbacks, and empty fields are caught immediately.

## Authentication

Ollama providers support three `auth_type` values: `bearer` (default), `basic`, and `none`.

**Local Ollama (no auth needed):**

```yaml
ollama-local:
  type: ollama
  base_url: http://localhost:11434
```

No `api_key` or `auth_type` required. When `api_key` is absent, no authorization header is sent.

**Ollama Cloud (bearer token):**

```yaml
ollama-cloud:
  type: ollama
  base_url: https://ollama.com/api
  api_key: $OLLAMA_API_KEY
```

When `api_key` is set and `auth_type` is omitted, it defaults to `bearer`. The request sends `Authorization: Bearer <key>`.

**Network Ollama behind reverse proxy (bearer):**

```yaml
ollama-network:
  type: ollama
  base_url: https://ollama.internal.example.com
  api_key: $OLLAMA_BEARER_TOKEN
```

Same behavior as cloud -- the reverse proxy forwards the Bearer header to Ollama or validates it itself.

**Network Ollama behind reverse proxy (basic auth):**

```yaml
ollama-proxied:
  type: ollama
  base_url: https://ollama.internal.example.com
  api_key: $OLLAMA_BASIC_CREDS  # user:password
  auth_type: basic
```

Sends `Authorization: Basic <base64(user:password)>`. Use this with Caddy, nginx, or any proxy that gates access with HTTP basic auth. See [docs/caddy-reverse-proxy.md](docs/caddy-reverse-proxy.md) for a detailed proxy setup guide.

**Note:** `auth_type` only applies to Ollama providers. OpenAI, Anthropic, and Gemini providers always use their native authentication mechanisms and ignore `auth_type`.

## Build

```bash
make build                        # Build for host OS/arch -> build/
make build GOARCH=arm64           # Build for specific architecture
make build-all                    # All platforms -> dist/ with tarballs + sha256sums
make verify-cross                 # Build + verify all platform binaries
make test                         # Run all tests
make test-cover                   # Tests with coverage report
make lint                         # go vet
make clean                        # Remove build/ and dist/
```

Supported platforms: `linux/amd64`, `linux/arm64`, `linux/riscv64`, `linux/ppc64`, `linux/ppc64le`. All builds use `CGO_ENABLED=0` for fully static binaries.

## CLI Usage

```
et run [--config path] [--role name] "task description"
et session <spawn|list|attach|kill|send> [args]
et models [--config path]
et version
```

**`et run`** executes a supervisor-to-worker flow. When the worker role (`polecat`) has a `pool` configured, it uses a three-phase pipeline: decompose → parallel execute → synthesize. Otherwise, single-worker streaming is used.

```bash
# Single-worker mode (no pool configured)
et run "implement a rate limiter with token bucket algorithm"

# Parallel pool mode (pool configured in polecat role)
et run --config fleet.yaml "implement a REST API for user management"

# Skip synthesis -- print raw per-worker output
et run --no-synthesize "implement a REST API"

# Limit subtasks
et run --max-subtasks 3 "build a web server"

# Specify config and supervisor role
et run --config prod.yaml --role mayor "refactor the auth middleware"
```

**`et session`** manages interactive agent sessions in tmux/byobu panes. Sessions are persistent, observable, and manageable via CLI.

```bash
# Spawn a new agent session in tmux
et session spawn --role polecat --dir /tmp/project "implement binary search"

# List active et-* sessions
et session list

# Attach to a running session
et session attach et-polecat-a3f2

# Send input to a session
et session send et-polecat-a3f2 "add error handling"

# Kill a session
et session kill et-polecat-a3f2
```

Sessions are named `et-{role}-{short-hex}` and discovered statelessly from tmux. Byobu is auto-detected and used for session creation when available.

**`et models`** lists all available models from all configured providers.

```bash
et models --config electrictown.yaml
```

**`et version`** prints the version (set from git tags at build time).

## Role System

### Mayor (Supervisor)

The Mayor decomposes high-level tasks into discrete subtasks and synthesizes worker results. It parses numbered lists, bullet points, and structured `## Summary` / `## Subtasks` sections from model output.

```go
mayor := role.NewMayor(router,
    role.WithMayorCostTracker(tracker),
    role.WithMayorMaxSubtasks(5),
)
subtasks, err := mayor.Decompose(ctx, "build a REST API for user management")
result, err := mayor.Synthesize(ctx, task, workerResults)
```

### Polecat (Worker)

The Polecat executes coding tasks. It supports single-shot execution, streaming, and multi-turn conversations with history.

```go
polecat := role.NewPolecat(router,
    role.WithCostTracker(tracker),
    role.WithSystemPrompt("You are a Go expert."),
)
resp, err := polecat.Execute(ctx, "implement binary search")
stream, err := polecat.ExecuteStream(ctx, "implement merge sort")
```

### Witness (Reviewer)

The Witness reviews code for correctness, security, and quality. It can review standalone code, review code against the original task, or validate output against acceptance criteria.

```go
witness := role.NewWitness(router,
    role.WithWitnessCostTracker(tracker),
)
review, err := witness.Review(ctx, code)
review, err := witness.ReviewWithContext(ctx, task, code)
validation, err := witness.Validate(ctx, criteria, output)
```

### Refinery (Polisher)

The Refinery improves code quality -- fixing bugs, improving naming, adding error handling, and ensuring consistent style. It can refine with or without specific feedback, and can produce summaries.

```go
refinery := role.NewRefinery(router,
    role.WithRefineryCostTracker(tracker),
)
refined, err := refinery.Refine(ctx, rawCode)
refined, err := refinery.RefineWithFeedback(ctx, rawCode, "add context to errors")
summary, err := refinery.Summarize(ctx, verboseContent)
```

## Cost Tracking

The cost tracker records per-request token usage with configurable per-model pricing and produces breakdowns by provider, model, and role.

```go
tracker := cost.NewTracker(cost.DefaultPricing())

// After requests are made through roles with the tracker attached:
summary := tracker.Summary()
fmt.Printf("Total cost: $%.4f (%d requests, %d tokens)\n",
    summary.TotalCost, summary.TotalRequests, summary.TotalTokens)

// Filter by role
mayorCost := tracker.SummaryForRole("mayor")
```

Local Ollama models default to $0.00 cost. Cloud model pricing is configured per 1M tokens (prompt and completion separately).

## Provider Interface

All provider adapters implement this interface:

```go
type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    StreamChatCompletion(ctx context.Context, req *ChatRequest) (ChatStream, error)
    ListModels(ctx context.Context) ([]Model, error)
}
```

Adding a new provider means implementing these four methods and registering a factory function. No SDK dependencies -- all adapters use `net/http` directly.

## License

MIT
