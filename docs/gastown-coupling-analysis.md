# Gastown Coupling Analysis: Claude Code & Anthropic Dependencies

**Generated:** 2026-02-25
**Source:** `/tmp/gastown` (full codebase scan of all 65 `internal/` subdirectories)
**Purpose:** Foundation document for electrictown provider-agnostic routing layer

---

## Executive Summary

Gastown already has a **surprisingly mature multi-agent architecture**. It supports 11 agent presets (claude, gemini, codex, cursor, auggie, amp, opencode, copilot, pi, omp) via an `AgentRegistry` with per-preset configs for hooks, session management, and readiness detection.

**However**, Claude Code remains deeply embedded as the **assumed default** throughout the codebase. The coupling falls into five categories with ~45 distinct coupling points. The good news: gastown's existing abstraction layers (`AgentPresetInfo`, `RuntimeConfig`, `HookInstaller` registry) mean most changes are Medium difficulty -- replacing defaults and fallbacks rather than redesigning architecture.

---

## Category A: Hook Integration

### A1. Claude Code settings.json templates (embedded)
- **Files:**
  - `/tmp/gastown/internal/claude/config/settings-autonomous.json` (lines 1-81)
  - `/tmp/gastown/internal/claude/config/settings-interactive.json` (lines 1-81)
- **What:** Embedded JSON templates with Claude Code-specific hook structure: `PreToolUse`, `SessionStart`, `PreCompact`, `UserPromptSubmit`, `Stop`. The autonomous variant adds `gt mail check --inject` to SessionStart. These are Claude Code's native hook format.
- **Difficulty:** Hard
- **Notes:** These are the heart of gastown-to-agent communication. The hooks call `gt prime --hook`, `gt mail check --inject`, `gt costs record`, and `gt tap guard pr-workflow`. electrictown must either: (a) generate equivalent hooks per provider, or (b) use a sidecar process that provides these lifecycle events provider-agnostically.

### A2. Claude settings.go -- EnsureSettings family
- **File:** `/tmp/gastown/internal/claude/settings.go` (lines 1-93)
- **What:** Package `claude` that writes `.claude/settings.json` from embedded templates. Functions: `EnsureSettings`, `EnsureSettingsAt`, `EnsureSettingsForRole`, `EnsureSettingsForRoleAt`. All specific to Claude Code's `.claude/settings.json` convention.
- **Difficulty:** Medium
- **Notes:** Already abstracted behind `config.RegisterHookInstaller("claude", ...)` in runtime init. The package itself is Claude-specific but only called through the installer registry.

### A3. Hook installer registry (already abstracted)
- **File:** `/tmp/gastown/internal/runtime/runtime.go` (lines 20-47)
- **What:** `init()` registers hook installers for claude, gemini, opencode, copilot, omp, pi. Each installer knows how to provision its agent's hook/settings files.
- **Difficulty:** Easy
- **Notes:** This is gastown's existing abstraction. electrictown just needs to add its own provider here (or replace the claude entry).

### A4. HooksProvider field in AgentPresetInfo
- **File:** `/tmp/gastown/internal/config/agents.go` (lines 107-115)
- **What:** Each preset declares `HooksProvider`, `HooksDir`, `HooksSettingsFile`, `HooksInformational`. Claude uses `"claude"`, `.claude`, `settings.json`. This drives which hook installer runs.
- **Difficulty:** Easy
- **Notes:** Clean design. electrictown provider would register its own HooksProvider value.

### A5. Doctor check for stale Claude settings
- **File:** `/tmp/gastown/internal/doctor/claude_settings_check.go` (lines 1-170+)
- **What:** `ClaudeSettingsCheck` struct validates `.claude/settings.json` files match expected templates. Detects missing hooks, wrong locations, stale settings.
- **Difficulty:** Medium
- **Notes:** Tightly coupled to Claude's settings format. Needs to become provider-aware or get a parallel check per provider.

---

## Category B: Session Management

### B1. resolveClaudePath -- binary discovery
- **File:** `/tmp/gastown/internal/config/types.go` (lines 663-683)
- **What:** Finds the `claude` binary: tries `$PATH`, then `~/.claude/local/claude`, falls back to bare `"claude"`. Called by `RuntimeConfigFromPreset` for the Claude preset only.
- **Difficulty:** Easy
- **Notes:** Only invoked when preset is `AgentClaude`. Other agents use their preset's `Command` directly.

### B2. DefaultRuntimeConfig -- defaults to Claude
- **File:** `/tmp/gastown/internal/config/types.go` (lines 474-477)
- **What:** `DefaultRuntimeConfig()` returns `normalizeRuntimeConfig(&RuntimeConfig{Provider: "claude"})`. This is the zero-value fallback everywhere.
- **Difficulty:** Medium
- **Notes:** Pervasive impact. Every `nil` RuntimeConfig becomes a Claude config. electrictown must either change this default or ensure all callers provide explicit configs.

### B3. normalizeRuntimeConfig -- Claude as default provider
- **File:** `/tmp/gastown/internal/config/types.go` (lines 540-580+)
- **What:** When `Provider` is empty, defaults to `"claude"`. Cascades to `defaultRuntimeCommand("claude")`, `defaultRuntimeArgs("claude")`, `defaultPromptMode("claude")`.
- **Difficulty:** Medium
- **Notes:** Central normalization function. Changing the default provider here changes all implicit behavior.

### B4. DefaultAgentPreset -- returns AgentClaude
- **File:** `/tmp/gastown/internal/config/agents.go` (lines 494-497)
- **What:** `DefaultAgentPreset() AgentPreset` returns `AgentClaude`. Used as fallback when no agent is specified.
- **Difficulty:** Easy
- **Notes:** Single line change, but affects behavior throughout.

### B5. NewTownSettings -- default_agent = "claude"
- **File:** `/tmp/gastown/internal/config/types.go` (lines 96-105)
- **What:** `NewTownSettings()` sets `DefaultAgent: "claude"`.
- **Difficulty:** Easy
- **Notes:** This is the user-facing default. Town settings file explicitly stores agent preference.

### B6. GetProcessNames -- fallback to Claude's process names
- **File:** `/tmp/gastown/internal/config/agents.go` (lines 583-590)
- **What:** When agent is unknown or has no ProcessNames, defaults to `[]string{"node", "claude"}`.
- **Difficulty:** Easy
- **Notes:** Safety fallback for backwards compatibility. Also in `ResolveProcessNames` (line 638).

### B7. ReadyPromptPrefix -- Claude's prompt character
- **File:** `/tmp/gastown/internal/config/agents.go` (line 184), `/tmp/gastown/internal/tmux/tmux.go` (line 1869)
- **What:** Claude preset uses `ReadyPromptPrefix: "❯ "`. `DefaultReadyPromptPrefix` const is also `"❯ "`. Used to detect when Claude is idle/ready for input by polling tmux pane content.
- **Difficulty:** Medium
- **Notes:** Fundamental to readiness detection. Other agents use delay-based fallback. electrictown's router would need its own readiness signal.

### B8. ClaudeStartTimeout constant
- **File:** `/tmp/gastown/internal/constants/constants.go` (lines 12-14)
- **What:** `ClaudeStartTimeout = 60 * time.Second`. Named after Claude but used as generic startup timeout for all agents.
- **Difficulty:** Easy
- **Notes:** Just a naming issue. Used in ~8 callsites across polecat, refinery, session, and sling_helpers.

### B9. WaitForRuntimeReady -- tmux prompt polling
- **File:** `/tmp/gastown/internal/tmux/tmux.go` (lines 1792-1870)
- **What:** Polls tmux pane for `ReadyPromptPrefix` to detect agent readiness. Falls back to delay-based detection when prefix is empty. This is how gastown knows a session is ready for work.
- **Difficulty:** Medium
- **Notes:** Already has the abstraction (prefix vs delay). electrictown agents would just use their own prefix or delay.

### B10. CLAUDE_SESSION_ID fallback in SessionIDFromEnv
- **File:** `/tmp/gastown/internal/runtime/runtime.go` (lines 88-107)
- **What:** Falls back to `os.Getenv("CLAUDE_SESSION_ID")` when `GT_SESSION_ID_ENV` and agent-preset lookup both fail.
- **Difficulty:** Easy
- **Notes:** Backwards compatibility fallback. Can be removed once all sessions use `GT_SESSION_ID_ENV`.

### B11. CLAUDECODE env var clearing
- **File:** `/tmp/gastown/internal/config/env.go` (lines 179-184)
- **What:** Clears `CLAUDECODE=""` to prevent Claude Code v2.x nested session detection error.
- **Difficulty:** Easy
- **Notes:** Claude-specific workaround. Harmless for other providers.

---

## Category C: Config/Settings

### C1. Cost tier model mappings
- **File:** `/tmp/gastown/internal/config/cost_tier.go` (lines 60-114)
- **What:** Economy and Budget tiers hardcode `"claude-sonnet"` and `"claude-haiku"` as role-agent mappings. `claudeSonnetPreset()` and `claudeHaikuPreset()` create `RuntimeConfig` with `Command: "claude"` and `--model sonnet/haiku`.
- **Difficulty:** Hard
- **Notes:** The cost tier system assumes Claude model variants. electrictown would need a provider-agnostic tier system that maps to different models per provider (e.g., sonnet->gpt-4o-mini, haiku->gpt-3.5).

### C2. Anthropic API environment variables
- **File:** `/tmp/gastown/internal/config/env.go` (lines 253-309)
- **What:** Hardcoded passthrough of 25+ Claude Code-specific env vars: `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_FOUNDRY`, AWS/GCP credentials for Bedrock/Vertex, `CLAUDE_CODE_CLIENT_CERT/KEY`, etc.
- **Difficulty:** Hard
- **Notes:** This is the deepest API coupling. Each provider backend (Bedrock, Vertex, Foundry, direct) has its own env vars. electrictown's router would replace all of these with a single set of router env vars.

### C3. CLAUDE_CONFIG_DIR in AgentEnv
- **File:** `/tmp/gastown/internal/config/env.go` (lines 146-149)
- **What:** Sets `CLAUDE_CONFIG_DIR` from `RuntimeConfigDir`. Used for quota rotation (switching between accounts).
- **Difficulty:** Medium
- **Notes:** Core to the quota/account rotation system. Already abstracted via `RuntimeSessionConfig.ConfigDirEnv` which defaults to `"CLAUDE_CONFIG_DIR"` for Claude.

### C4. CLAUDE_CODE_ENABLE_TELEMETRY propagation
- **File:** `/tmp/gastown/internal/config/env.go` (lines 186-199)
- **What:** When `GT_OTEL_METRICS_URL` is set, configures Claude Code's internal OTEL telemetry to export to the same VictoriaMetrics endpoint.
- **Difficulty:** Medium
- **Notes:** Provider-specific telemetry integration. Each agent would need its own telemetry bridge.

### C5. NODE_OPTIONS clearing
- **File:** `/tmp/gastown/internal/config/env.go` (lines 169-177)
- **What:** Clears `NODE_OPTIONS=""` to prevent VSCode debugger flags from breaking Claude's Node.js runtime.
- **Difficulty:** Easy
- **Notes:** Specific to Node.js-based agents (Claude, OpenCode, Pi). Harmless for others.

### C6. Model pricing table
- **File:** `/tmp/gastown/internal/cmd/costs.go` (lines 224-238)
- **What:** Hardcoded per-token pricing for `claude-opus-4-5-20251101`, `claude-sonnet-4-20250514`, `claude-3-5-haiku-20241022`. Fallback to Sonnet pricing.
- **Difficulty:** Medium
- **Notes:** electrictown would need a provider-agnostic pricing table or API-based cost lookup.

### C7. Handoff env var propagation
- **File:** `/tmp/gastown/internal/cmd/handoff.go` (lines 647-676)
- **What:** `claudeEnvVars` list of 20+ env vars to propagate during handoff: `ANTHROPIC_API_KEY`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_ENABLE_TELEMETRY`, OTEL vars, etc.
- **Difficulty:** Medium
- **Notes:** Handoff needs to propagate whatever env vars the current provider requires. Should be driven by the agent's preset config.

### C8. Account management (CLAUDE_CONFIG_DIR-based)
- **File:** `/tmp/gastown/internal/cmd/account.go` (lines 60+, 200+, 226+, 469+)
- **What:** Account system uses `CLAUDE_CONFIG_DIR` to switch between Claude Code accounts. Each account is a directory like `~/.claude-accounts/work`. Instructions tell users to run `CLAUDE_CONFIG_DIR=<path> claude`.
- **Difficulty:** Hard
- **Notes:** Deeply coupled to Claude's config dir model. electrictown's router would handle auth routing internally.

### C9. Quota rotation system
- **File:** `/tmp/gastown/internal/quota/` (state.go, executor.go, scan.go, rotate.go)
- **What:** Scans tmux panes for rate-limit patterns (e.g., "You've hit your limit", "resets 7pm"), reads `CLAUDE_CONFIG_DIR` from sessions, rotates to alternate accounts. ~500 lines.
- **Difficulty:** Hard
- **Notes:** Entire subsystem assumes Claude Code rate limit UX. electrictown's router would handle rate limiting and account rotation at the API level, making this subsystem unnecessary.

### C10. Rate limit detection patterns
- **File:** `/tmp/gastown/internal/constants/constants.go` (lines 304-309)
- **What:** `DefaultRateLimitPatterns` with Claude-specific strings: "You've hit your limit", "resets {time}", "Stop and wait for limit to reset", "Add funds to continue with extra usage".
- **Difficulty:** Medium
- **Notes:** Entirely Claude Code UI text. Other agents would have different rate limit messages. electrictown's router handles rate limiting before it reaches the agent.

---

## Category D: Agent Communication

### D1. Startup fallback system
- **File:** `/tmp/gastown/internal/runtime/runtime.go` (lines 109-231)
- **What:** When an agent doesn't support hooks, gastown sends fallback commands via tmux nudge: `gt prime`, `gt mail check --inject`. The `StartupFallbackInfo` struct describes the agent's capabilities matrix.
- **Difficulty:** Medium
- **Notes:** Already provider-agnostic design. The matrix checks `hasHooks` and `hasPrompt` per agent, then determines beacon content and nudge timing.

### D2. Beacon system for startup context
- **File:** `/tmp/gastown/internal/polecat/session_manager.go` (lines 256-267)
- **What:** Formats a "beacon" (startup prompt) that includes work instructions, predecessor info, and optionally a "Run gt prime" instruction. Delivered as CLI argument or tmux nudge.
- **Difficulty:** Easy
- **Notes:** Already agent-agnostic. Uses RuntimeConfig to decide delivery method.

### D3. gt prime / gt mail check -- the integration protocol
- **Files:** Referenced in hooks, fallback commands, beacon content
- **What:** `gt prime` injects context (CLAUDE.md content, role identity, workspace state) into the agent. `gt mail check --inject` delivers pending mail. These are the core gastown-to-agent communication verbs.
- **Difficulty:** Easy (these are agent-external commands)
- **Notes:** These commands are CLI tools that work with any agent. They inject text via hooks (Claude) or nudge (others). No change needed for electrictown.

### D4. NudgeSession -- tmux send-keys
- **File:** `/tmp/gastown/internal/tmux/tmux.go` (lines 827-851, 940-1100)
- **What:** Sends text to agent sessions via tmux `send-keys`. Handles transient errors, retries, copy-mode interrupts. Used for: work delivery, startup fallback, mail injection.
- **Difficulty:** Easy
- **Notes:** Already provider-agnostic. Works with any interactive CLI agent.

### D5. BuildCommandWithPrompt -- opencode special case
- **File:** `/tmp/gastown/internal/config/types.go` (lines 498-520)
- **What:** Hardcodes `if resolved.Command == "opencode"` to use `--prompt` flag instead of positional arg.
- **Difficulty:** Easy
- **Notes:** Should be moved to a preset field (e.g., `PromptFlag`) rather than hardcoded command check.

---

## Category E: Hardcoded Assumptions

### E1. "claude" as built-in preset name
- **File:** `/tmp/gastown/internal/config/agents.go` (lines 17-19, 164-188)
- **What:** `AgentClaude AgentPreset = "claude"` is the canonical preset. Its `AgentPresetInfo` includes: Command `"claude"`, Args `["--dangerously-skip-permissions"]`, ProcessNames `["node", "claude"]`, SessionIDEnv `"CLAUDE_SESSION_ID"`, ResumeFlag `"--resume"`, ContinueFlag `"--continue"`, SupportsHooks/SupportsForkSession: true, ConfigDir `".claude"`, HooksProvider `"claude"`, ReadyPromptPrefix `"❯ "`, InstructionsFile `"CLAUDE.md"`.
- **Difficulty:** N/A (reference data, not a change target)
- **Notes:** This is correct and well-structured. The issue is that other code defaults to this preset.

### E2. CLAUDE.md as instructions file for Claude
- **File:** `/tmp/gastown/internal/config/agents.go` (line 186), `/tmp/gastown/internal/polecat/manager.go` (lines 715-716)
- **What:** Claude uses `CLAUDE.md`, all other agents use `AGENTS.md`. Only `~/gt/CLAUDE.md` exists on disk.
- **Difficulty:** Easy
- **Notes:** Already per-preset via `InstructionsFile` field. No change needed.

### E3. `.claude/commands/` slash command provisioning
- **File:** `/tmp/gastown/internal/templates/templates.go` (lines 210-228), `/tmp/gastown/internal/rig/overlay.go` (line 78)
- **What:** Provisions `.claude/commands/` directory with standard slash commands. Also listed in git overlay patterns.
- **Difficulty:** Medium
- **Notes:** Already has `commands.ProvisionFor(workDir, provider)` in runtime.go which is per-provider. The overlay pattern list in rig needs updating for multi-provider.

### E4. Anthropic GitHub links in comments
- **Files:** Multiple files across the codebase
- **What:** Comments reference `github.com/anthropics/gastown`, `github.com/anthropics/claude-code`, `docs.anthropic.com`. ~15 occurrences.
- **Difficulty:** Easy
- **Notes:** Cosmetic. Update during fork.

### E5. `beads` package references Anthropic
- **File:** `/tmp/gastown/internal/beads/beads.go` (line 25)
- **What:** Error message: `"bd not installed: run 'pip install beads-cli' or see https://github.com/anthropics/beads"`
- **Difficulty:** Easy
- **Notes:** URL reference. Update to new home if beads is forked.

### E6. Model names in docs and examples
- **Files:**
  - `/tmp/gastown/docs/INSTALLING.md` (lines 156, 166)
  - `/tmp/gastown/docs/overview.md` (line 209)
  - `/tmp/gastown/docs/reference.md` (line 494)
  - `/tmp/gastown/docs/examples/town-settings.example.json` (line 56)
- **What:** Examples use `claude-haiku`, `claude-sonnet`, `claude-opus` as agent names.
- **Difficulty:** Easy
- **Notes:** Documentation updates.

### E7. generate-newsletter.py -- hardcoded Anthropic client
- **File:** `/tmp/gastown/scripts/generate-newsletter.py` (lines 23, 383-393, 505, 601, 634)
- **What:** Uses `client.messages.create()` (Anthropic SDK), defaults to `claude-opus-4-1-20250805`, has Claude-specific pricing display.
- **Difficulty:** Medium
- **Notes:** Standalone script. Can be updated independently.

### E8. gt-model-eval promptfoo config
- **File:** `/tmp/gastown/gt-model-eval/promptfooconfig.yaml` (lines 18-39)
- **What:** Configures `anthropic:messages:claude-opus-4-6`, `claude-sonnet-4-5-20250929`, `claude-haiku-4-5-20251001` as eval targets.
- **Difficulty:** Easy
- **Notes:** Eval config. Add additional providers as needed.

### E9. DefaultDebounceMs comment
- **File:** `/tmp/gastown/internal/constants/constants.go` (lines 19-21)
- **What:** Comment says "500ms is required for Claude Code to reliably process paste before Enter."
- **Difficulty:** Easy
- **Notes:** Comment only. The debounce value may differ per agent.

---

## Coupling Density by Package

| Package | Coupling Points | Severity |
|---------|----------------|----------|
| `internal/config/` | 12 | High -- types.go, agents.go, env.go, cost_tier.go, loader.go |
| `internal/claude/` | 3 | High -- dedicated Claude package |
| `internal/runtime/` | 4 | Medium -- already abstracted |
| `internal/tmux/` | 3 | Medium -- readiness detection |
| `internal/quota/` | 4 | High -- entire subsystem |
| `internal/polecat/` | 3 | Medium -- session spawning |
| `internal/constants/` | 3 | Low -- naming/values |
| `internal/cmd/` | 5 | Medium -- handoff, costs, account |
| `internal/doctor/` | 1 | Medium -- settings check |
| `internal/templates/` | 1 | Low -- slash commands |
| Other (docs, scripts) | 6 | Low -- documentation/tooling |

---

## Summary: What electrictown Replaces

### Must Replace (blocks all other work)
1. **Default provider** -- Change from `"claude"` to configurable/router-based
2. **Env var passthrough** -- Replace 25+ `ANTHROPIC_*`/`CLAUDE_CODE_*` vars with router config
3. **Cost tier model mappings** -- Provider-agnostic model tiers
4. **Quota/rate-limit system** -- Router handles this at API level

### Should Replace (improves architecture)
5. **Account management** -- Router-level auth, not per-session CLAUDE_CONFIG_DIR
6. **Hook templates** -- Generate per-provider hooks from a single template
7. **Pricing table** -- Router reports costs, not hardcoded per-model lookup
8. **Doctor checks** -- Provider-aware validation

### Can Keep (already abstracted)
9. **AgentRegistry** -- Clean preset system, just add electrictown preset
10. **Hook installer registry** -- Register electrictown's installer
11. **Startup fallback system** -- Already handles hook vs no-hook agents
12. **Beacon/nudge system** -- Provider-agnostic tmux communication
13. **Tmux session management** -- Works with any CLI agent
14. **gt prime / gt mail** -- External commands, agent-agnostic
