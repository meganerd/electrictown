# electrictown: Abstract Session Layer Design

**Generated:** 2026-02-25
**Companion:** `gastown-coupling-analysis.md` (coupling point inventory)
**Purpose:** Replace gastown's Claude Code session spawning with a generic agent launcher that works with any provider via the electrictown router

---

## 1. Problem Statement

Gastown spawns agent sessions in tmux by:
1. Resolving a `RuntimeConfig` (command, args, env, hooks, readiness heuristics)
2. Writing provider-specific settings files (`.claude/settings.json`, `.gemini/settings.json`, etc.)
3. Building a startup command with `export ENV=val && <agent-command> <prompt>`
4. Creating a tmux session and sending the command
5. Polling for readiness (prompt prefix or delay)
6. Sending work via hooks (SessionStart) or nudge (tmux send-keys)

Steps 1-3 are tightly coupled to specific providers. Steps 4-6 are already fairly abstract. The goal is to make the entire pipeline provider-agnostic by routing through electrictown.

---

## 2. Current Architecture (Gastown)

```
┌─────────────────────────────────────────────────┐
│  Witness / Scheduler / Mayor                     │
│  "spawn polecat for issue gt-abc"               │
└──────────────────┬──────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────┐
│  config.ResolveRoleAgentConfig(role, town, rig) │
│  - Checks town settings (default_agent)         │
│  - Checks rig settings (role_agents)            │
│  - Checks cost tier mappings                    │
│  - Falls back to AgentClaude preset             │
│  Returns: RuntimeConfig                         │
└──────────────────┬──────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────┐
│  runtime.EnsureSettingsForRole(...)             │
│  - Selects hook installer by rc.Hooks.Provider  │
│  - Writes .claude/settings.json (or equivalent) │
│  - Provisions slash commands                    │
└──────────────────┬──────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────┐
│  config.BuildStartupCommandFromConfig(...)      │
│  - Generates env vars (AgentEnv)                │
│  - Passthrough: ANTHROPIC_*, CLAUDE_CODE_*, AWS │
│  - Builds: export FOO=bar && claude --dsp <prompt>│
└──────────────────┬──────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────┐
│  tmux.NewSessionWithCommand(session, cmd, dir)  │
│  tmux.WaitForRuntimeReady(session, rc, timeout) │
│  tmux.NudgeSession(session, workInstructions)   │
└─────────────────────────────────────────────────┘
```

### Key Coupling Points in This Flow:
- `AgentEnv()` injects 25+ Claude-specific env vars
- `EnsureSettingsForRole()` writes Claude's hook format
- `BuildStartupCommand()` produces `claude --dangerously-skip-permissions`
- `WaitForRuntimeReady()` polls for Claude's `❯ ` prompt
- Quota system reads `CLAUDE_CONFIG_DIR` and scans for Claude rate-limit text

---

## 3. Target Architecture (electrictown)

### 3.1 Core Concept: The Router as Universal Backend

Instead of each agent session talking directly to its provider's API, all sessions go through electrictown's router, which:
- Accepts requests in a standard format (OpenAI-compatible or custom)
- Routes to the configured provider (Anthropic, OpenAI, Google, local, etc.)
- Handles auth, rate limiting, account rotation, cost tracking, retries
- Exposes a single endpoint that any compatible agent CLI can target

```
┌──────────────────────────────────────────────────────────────┐
│  Session Layer (new)                                          │
│  SessionLauncher.Spawn(role, workDir, prompt, opts)           │
└──────────────┬───────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────────┐
│  Provider Adapter (new)                                       │
│  adapter.ResolveConfig(role) -> SessionConfig                │
│  adapter.ProvisionHooks(workDir, role) -> error              │
│  adapter.BuildCommand(prompt) -> string                       │
│  adapter.ReadinessStrategy() -> PromptPrefix | Delay | Health│
└──────────────┬───────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────────┐
│  tmux (unchanged)                                             │
│  NewSessionWithCommand / WaitForRuntimeReady / NudgeSession  │
└──────────────────────────────────────────────────────────────┘
```

### 3.2 SessionConfig (replaces RuntimeConfig Claude defaults)

```go
// SessionConfig is the provider-agnostic replacement for RuntimeConfig.
// It describes everything needed to launch and monitor an agent session.
type SessionConfig struct {
    // Provider identifies the backend ("electrictown", "claude", "gemini", etc.)
    Provider string

    // Command is the CLI binary to invoke.
    Command string

    // Args are command-line arguments for autonomous mode.
    Args []string

    // Env are environment variables. For electrictown, this is minimal:
    // just ELECTRICTOWN_ENDPOINT, ELECTRICTOWN_MODEL, ELECTRICTOWN_API_KEY.
    Env map[string]string

    // Hooks describes the lifecycle hook system.
    Hooks *HooksConfig

    // Readiness describes how to detect the agent is ready for input.
    Readiness *ReadinessConfig

    // Session describes session ID and config dir env vars.
    Session *SessionIDConfig

    // Instructions is the instruction file name (CLAUDE.md, AGENTS.md, etc.)
    InstructionsFile string
}

type ReadinessConfig struct {
    // Strategy: "prompt" (poll for prefix), "delay" (fixed wait), "health" (HTTP check)
    Strategy string

    // PromptPrefix for "prompt" strategy (e.g., "> ", "❯ ")
    PromptPrefix string

    // DelayMs for "delay" strategy
    DelayMs int

    // HealthURL for "health" strategy (new: electrictown sidecar healthcheck)
    HealthURL string
}
```

### 3.3 Provider Adapter Interface

```go
// ProviderAdapter encapsulates all provider-specific behavior.
// Each supported agent runtime implements this interface.
type ProviderAdapter interface {
    // Name returns the provider identifier (e.g., "electrictown", "claude", "gemini")
    Name() string

    // ResolveConfig returns the SessionConfig for a given role and tier.
    ResolveConfig(role string, tier CostTier) *SessionConfig

    // ProvisionHooks writes lifecycle hooks/settings to the workspace.
    ProvisionHooks(settingsDir, workDir, role string) error

    // BuildCommand constructs the startup command string with env vars and prompt.
    BuildCommand(config *SessionConfig, env map[string]string, prompt string) string

    // EnvVars returns provider-specific environment variables to passthrough.
    // Replaces the hardcoded ANTHROPIC_*/CLAUDE_CODE_* list in env.go.
    EnvVars() []string

    // CostPerToken returns pricing info for cost tracking.
    // Returns nil if the provider handles cost tracking externally (e.g., router).
    CostPerToken(model string) *TokenPricing

    // RateLimitPatterns returns text patterns that indicate rate limiting.
    // Returns nil if the provider handles rate limiting externally.
    RateLimitPatterns() []string
}
```

### 3.4 The electrictown Adapter

When the provider is "electrictown", the adapter configures sessions to use any compatible agent CLI pointed at the electrictown router:

```go
type ElectrictownAdapter struct {
    Endpoint string // e.g., "http://localhost:9090"
    APIKey   string // Router auth token
}

func (a *ElectrictownAdapter) ResolveConfig(role string, tier CostTier) *SessionConfig {
    // The router determines the actual model based on tier.
    // The agent CLI just needs to know the endpoint.
    model := a.tierToModel(tier) // e.g., "auto", "fast", "balanced", "powerful"

    return &SessionConfig{
        Provider: "electrictown",
        Command:  "claude", // Any OpenAI-compatible CLI works
        Args:     []string{"--dangerously-skip-permissions"},
        Env: map[string]string{
            "ANTHROPIC_BASE_URL": a.Endpoint, // Point Claude at router
            "ANTHROPIC_API_KEY":  a.APIKey,
            "ANTHROPIC_MODEL":    model,
        },
        Hooks: &HooksConfig{
            Provider:     "claude",
            Dir:          ".claude",
            SettingsFile: "settings.json",
        },
        Readiness: &ReadinessConfig{
            Strategy:     "prompt",
            PromptPrefix: "❯ ",
        },
    }
}

func (a *ElectrictownAdapter) EnvVars() []string {
    // Only three vars needed -- router handles everything else
    return []string{
        "ANTHROPIC_BASE_URL",
        "ANTHROPIC_API_KEY",
        "ANTHROPIC_MODEL",
    }
}

func (a *ElectrictownAdapter) CostPerToken(model string) *TokenPricing {
    return nil // Router tracks costs centrally
}

func (a *ElectrictownAdapter) RateLimitPatterns() []string {
    return nil // Router handles rate limiting with retries/queuing
}
```

---

## 4. Migration Strategy

### Phase 1: Introduce Adapter Interface (non-breaking)

1. Define `ProviderAdapter` interface in `internal/config/adapter.go`
2. Implement `ClaudeAdapter` that wraps existing behavior (extracting from env.go, agents.go, types.go)
3. Implement `ElectrictownAdapter` as the new path
4. Add `adapter` field to `TownSettings` (default: "claude" for backwards compat)
5. Wire `ResolveRoleAgentConfig` to use adapter when available

**Files changed:** `internal/config/adapter.go` (new), `internal/config/types.go`, `internal/config/loader.go`

### Phase 2: Router-Based Auth & Quota (biggest win)

1. electrictown router handles all API auth (replaces `ANTHROPIC_API_KEY`, Bedrock/Vertex/Foundry env vars)
2. Router handles rate-limit detection and retry (replaces `internal/quota/` subsystem)
3. Router tracks costs per session (replaces `internal/cmd/costs.go` pricing table)
4. Account rotation moves from tmux-level `CLAUDE_CONFIG_DIR` swaps to router-level key rotation

**Files changed:** `internal/config/env.go` (simplified), `internal/quota/` (gutted or removed), `internal/cmd/costs.go` (reads from router API)

### Phase 3: Provider-Agnostic Cost Tiers

1. Replace `"claude-sonnet"` / `"claude-haiku"` tier mappings with abstract capability levels
2. Tier system maps to router model aliases: `"fast"`, `"balanced"`, `"powerful"`
3. Router resolves aliases to actual models based on configured provider

```go
// Before (gastown)
"claude-sonnet": {Command: "claude", Args: ["--model", "sonnet"]}

// After (electrictown)
"balanced": {Command: "claude", Args: [], Env: {"ANTHROPIC_MODEL": "balanced"}}
// Router translates "balanced" -> "claude-sonnet-4-5" or "gpt-4o" etc.
```

**Files changed:** `internal/config/cost_tier.go`, `internal/config/cost_tier_test.go`

### Phase 4: Generalized Hooks

1. Factor out hook template generation from `internal/claude/`, `internal/gemini/`, `internal/opencode/`, etc.
2. Create a single hook template engine that generates provider-specific formats from a common spec
3. Hook spec defines: lifecycle events, commands per event, matcher patterns
4. Each provider adapter implements hook generation from the spec

**Files changed:** `internal/claude/settings.go`, `internal/runtime/runtime.go`, new `internal/hooks/generator.go`

---

## 5. Session Lifecycle with electrictown

### Spawn Sequence

```
1. Resolve adapter (from town settings)
2. adapter.ResolveConfig(role, tier) -> SessionConfig
3. adapter.ProvisionHooks(settingsDir, workDir, role)
4. adapter.BuildCommand(config, env, prompt) -> cmdString
5. tmux.NewSessionWithCommand(sessionName, cmdString, workDir)
6. WaitForReady(sessionName, config.Readiness)
7. Send work (hooks auto-fire or nudge fallback)
```

### Readiness Detection (unchanged flow, new options)

```
if config.Readiness.Strategy == "prompt":
    poll tmux pane for config.Readiness.PromptPrefix
elif config.Readiness.Strategy == "delay":
    sleep config.Readiness.DelayMs
elif config.Readiness.Strategy == "health":
    poll config.Readiness.HealthURL until 200 OK
```

The "health" strategy is new for electrictown. A lightweight sidecar or the router itself can expose a per-session health endpoint that the agent CLI hits on startup.

### Rate Limit Handling

```
# Before (gastown):
1. Agent hits Claude API rate limit
2. Claude Code shows "You've hit your limit" in TUI
3. Quota scanner polls tmux panes, detects pattern
4. Rotator swaps CLAUDE_CONFIG_DIR, respawns pane
5. Agent restarts with different account

# After (electrictown):
1. Agent sends request to router
2. Router detects rate limit from upstream provider
3. Router retries with alternate key/account/provider
4. Agent never sees the rate limit
5. No tmux scanning needed
```

---

## 6. Compatibility Matrix

| Feature | Claude Direct | electrictown + Claude CLI | electrictown + Custom CLI |
|---------|--------------|--------------------------|--------------------------|
| Hooks (SessionStart etc.) | Native | Native (same hooks) | Sidecar or nudge |
| Session resume | --resume flag | --resume flag | Adapter-specific |
| Readiness detection | Prompt prefix | Prompt prefix | Health check |
| Rate limit handling | Tmux scan + rotate | Router handles | Router handles |
| Cost tracking | Hardcoded pricing | Router API | Router API |
| Account rotation | CLAUDE_CONFIG_DIR | Router key rotation | Router key rotation |
| Fork session | --fork-session | --fork-session | Not supported |

---

## 7. Key Design Decisions

### 7.1 Keep Claude Code as the default CLI

Even with electrictown routing, Claude Code CLI remains the best-supported agent. The router just intercepts its API calls. This means:
- All existing hooks work unchanged
- Session resume works unchanged
- Readiness detection works unchanged
- The only change is environment variables (ANTHROPIC_BASE_URL points to router)

### 7.2 Router handles auth, not the session layer

The session layer should not know about API keys, Bedrock profiles, or Vertex credentials. All auth lives in the router config. Sessions just get an endpoint URL and a session token.

### 7.3 Cost tiers become abstract capability levels

Instead of `"claude-sonnet"` and `"claude-haiku"`, use `"fast"`, `"balanced"`, `"powerful"`. The router maps these to actual models based on the configured provider. This makes the tier system provider-agnostic.

### 7.4 Quota system becomes router-level

The entire `internal/quota/` package (scan, rotate, executor, state) becomes unnecessary when the router handles rate limiting. The router can:
- Queue requests during rate limits
- Rotate between API keys automatically
- Fall back to alternate providers
- Report availability to gastown for scheduling decisions

### 7.5 Preserve backwards compatibility

The `ClaudeAdapter` wraps all existing behavior. Towns using `default_agent: "claude"` without electrictown continue to work exactly as before. electrictown is opt-in via `default_agent: "electrictown"` or per-role overrides.

---

## 8. Files to Create

| File | Purpose |
|------|---------|
| `internal/config/adapter.go` | `ProviderAdapter` interface + registration |
| `internal/config/adapter_claude.go` | Claude adapter (wraps existing behavior) |
| `internal/config/adapter_electrictown.go` | electrictown adapter (new) |
| `internal/config/adapter_test.go` | Adapter tests |
| `internal/config/capability_tier.go` | Abstract tier system (fast/balanced/powerful) |

## 9. Files to Modify

| File | Change |
|------|--------|
| `internal/config/types.go` | Add `Adapter` field to TownSettings, change DefaultRuntimeConfig |
| `internal/config/env.go` | Use adapter.EnvVars() instead of hardcoded list |
| `internal/config/cost_tier.go` | Use capability tiers instead of claude-sonnet/haiku |
| `internal/config/loader.go` | Wire adapter into ResolveRoleAgentConfig |
| `internal/runtime/runtime.go` | Use adapter.ProvisionHooks instead of switch |
| `internal/cmd/costs.go` | Query router for costs when adapter is electrictown |
| `internal/cmd/handoff.go` | Use adapter.EnvVars() for propagation list |
| `internal/constants/constants.go` | Rename ClaudeStartTimeout to AgentStartTimeout |
