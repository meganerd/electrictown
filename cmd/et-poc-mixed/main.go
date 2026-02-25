// et-poc-mixed demonstrates mixed cloud+local routing.
//
// Scenario:
//
//  1. Cloud supervisor (mayor) decomposes a task into subtasks
//  2. Local workers (polecat) execute subtasks via Ollama
//  3. Fallback: if local Ollama is down/fails, workers fall back to cloud
//  4. Cloud synthesizer combines results
//
// Usage:
//
//	go run cmd/et-poc-mixed/main.go --config electrictown.yaml "Build a REST API for user management"
//
// With local-only config (no cloud keys needed):
//
//	go run cmd/et-poc-mixed/main.go --config electrictown-local.yaml "Build a REST API"
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/provider/anthropic"
	"github.com/meganerd/electrictown/internal/provider/gemini"
	"github.com/meganerd/electrictown/internal/provider/ollama"
	"github.com/meganerd/electrictown/internal/provider/openai"
)

// ANSI color codes.
const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m" // supervisor
	colorGreen  = "\033[32m" // workers
	colorYellow = "\033[33m" // synthesizer
	colorRed    = "\033[31m" // errors / fallbacks
	colorDim    = "\033[2m"  // timing info
	colorBold   = "\033[1m"
)

const defaultTask = "Build a REST API for user management with endpoints for create, read, update, and delete operations"

// phaseResult holds metadata for a completed phase.
type phaseResult struct {
	Role            string
	RequestedModel  string // model configured for the role
	ActualModel     string // model that actually responded
	Provider        string
	Tokens          int
	PromptTokens    int
	CompletionTokens int
	Duration        time.Duration
	FallbackUsed    bool
	Output          string
}

// workerResult holds output from a single parallel worker.
type workerResult struct {
	Index   int
	Subtask string
	Phase   phaseResult
	Err     error
}

func main() {
	fs := flag.NewFlagSet("et-poc-mixed", flag.ExitOnError)
	configPath := fs.String("config", "electrictown.yaml", "path to config file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	task := strings.Join(fs.Args(), " ")
	if task == "" {
		task = defaultTask
	}

	if err := run(*configPath, task); err != nil {
		fmt.Fprintf(os.Stderr, "%serror: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}
}

func run(configPath, task string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	totalStart := time.Now()

	// ── Banner ──────────────────────────────────────────────────────────
	fmt.Printf("%s%s", colorBold, colorCyan)
	fmt.Printf("electrictown - mixed cloud+local routing PoC\n")
	fmt.Printf("============================================%s\n", colorReset)
	fmt.Printf("%sConfig:%s %s\n", colorDim, colorReset, configPath)
	fmt.Printf("%sTask:%s   %s\n\n", colorDim, colorReset, task)

	// ── Load config and create router ───────────────────────────────────
	cfg, err := provider.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	router, err := provider.NewRouter(cfg, buildFactories())
	if err != nil {
		return fmt.Errorf("creating router: %w", err)
	}

	// ── Cost tracker ────────────────────────────────────────────────────
	tracker := cost.NewTracker(cost.DefaultPricing())

	// Resolve configured models for each role so we can detect fallbacks.
	mayorModel := resolveRoleModel(cfg, "mayor")
	polecatModel := resolveRoleModel(cfg, "polecat")
	refineryModel := resolveRoleModel(cfg, "refinery")

	fmt.Printf("%sRole mapping:%s\n", colorDim, colorReset)
	fmt.Printf("  mayor    (supervisor)  -> %s\n", mayorModel)
	fmt.Printf("  polecat  (workers)     -> %s\n", polecatModel)
	fmt.Printf("  refinery (synthesizer) -> %s\n\n", refineryModel)

	// ════════════════════════════════════════════════════════════════════
	// Phase 1: Supervisor decomposes the task
	// ════════════════════════════════════════════════════════════════════
	fmt.Printf("%s%s[Phase 1] Supervisor (mayor) decomposing task...%s\n", colorBold, colorCyan, colorReset)
	phaseStart := time.Now()

	supervisorReq := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role: provider.RoleSystem,
				Content: "You are a coding supervisor. Given a task, decompose it into 2-3 small, " +
					"independent subtasks that workers can implement in parallel. " +
					"Output ONLY a numbered list (e.g., \"1. ...\"), one subtask per line. " +
					"No preamble, no summary. Each subtask must be self-contained.",
			},
			{
				Role:    provider.RoleUser,
				Content: task,
			},
		},
	}

	supervisorResp, err := router.ChatCompletionForRole(ctx, "mayor", supervisorReq)
	if err != nil {
		return fmt.Errorf("supervisor (mayor) failed: %w", err)
	}
	supervisorDuration := time.Since(phaseStart)

	supervisorPhase := phaseResult{
		Role:             "mayor",
		RequestedModel:   mayorModel,
		ActualModel:      supervisorResp.Model,
		Provider:         identifyProvider(cfg, supervisorResp.Model),
		Tokens:           supervisorResp.Usage.TotalTokens,
		PromptTokens:     supervisorResp.Usage.PromptTokens,
		CompletionTokens: supervisorResp.Usage.CompletionTokens,
		Duration:         supervisorDuration,
		FallbackUsed:     supervisorResp.Model != mayorModel,
		Output:           strings.TrimSpace(supervisorResp.Message.Content),
	}

	tracker.Record(
		supervisorPhase.Provider,
		supervisorPhase.ActualModel,
		"mayor",
		cost.Usage{
			PromptTokens:     supervisorResp.Usage.PromptTokens,
			CompletionTokens: supervisorResp.Usage.CompletionTokens,
			TotalTokens:      supervisorResp.Usage.TotalTokens,
		},
	)

	printPhaseResult("Phase 1", colorCyan, &supervisorPhase)

	subtasks := parseSubtasks(supervisorPhase.Output)
	if len(subtasks) == 0 {
		return fmt.Errorf("supervisor produced no parseable subtasks from:\n%s", supervisorPhase.Output)
	}

	fmt.Printf("\n  %sSubtasks:%s\n", colorDim, colorReset)
	for i, st := range subtasks {
		fmt.Printf("    %d. %s\n", i+1, truncate(st, 100))
	}
	fmt.Println()

	// ════════════════════════════════════════════════════════════════════
	// Phase 2: Workers execute subtasks in parallel
	// ════════════════════════════════════════════════════════════════════
	n := len(subtasks)
	fmt.Printf("%s%s[Phase 2] Dispatching %d workers (polecat) in parallel...%s\n", colorBold, colorGreen, n, colorReset)
	phaseStart = time.Now()

	results := make([]workerResult, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i, st := range subtasks {
		go func(idx int, subtask string) {
			defer wg.Done()
			results[idx] = executeWorker(ctx, router, cfg, tracker, polecatModel, idx, subtask)
		}(i, st)
	}

	wg.Wait()
	workersDuration := time.Since(phaseStart)

	// Print worker results.
	var workerOutputs []string
	succeeded := 0
	failed := 0

	for _, r := range results {
		label := fmt.Sprintf("Worker %d", r.Index+1)
		if r.Err != nil {
			failed++
			fmt.Printf("\n  %s%s [FAILED]%s %s\n", colorRed, label, colorReset, truncate(r.Subtask, 80))
			fmt.Printf("    error: %v\n", r.Err)
			workerOutputs = append(workerOutputs, fmt.Sprintf("Subtask %d FAILED: %v", r.Index+1, r.Err))
		} else {
			succeeded++
			printPhaseResult(label, colorGreen, &r.Phase)
			workerOutputs = append(workerOutputs, fmt.Sprintf("Subtask %d result:\n%s", r.Index+1, r.Phase.Output))
		}
	}

	fmt.Printf("\n  %sPhase 2 total:%s %s (%d/%d succeeded)\n\n",
		colorDim, colorReset, workersDuration.Truncate(time.Millisecond), succeeded, n)

	if succeeded == 0 {
		return fmt.Errorf("all %d workers failed -- cannot synthesize", n)
	}

	// ════════════════════════════════════════════════════════════════════
	// Phase 3: Synthesizer combines worker outputs
	// ════════════════════════════════════════════════════════════════════
	fmt.Printf("%s%s[Phase 3] Synthesizer (refinery) combining results...%s\n", colorBold, colorYellow, colorReset)
	phaseStart = time.Now()

	combinedWorkerOutput := strings.Join(workerOutputs, "\n\n---\n\n")

	synthReq := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role: provider.RoleSystem,
				Content: "You are a code synthesizer. You receive the outputs of multiple workers " +
					"who each implemented a subtask. Combine their outputs into a single, coherent, " +
					"well-structured result. Fix any inconsistencies. Output the final combined code/result.",
			},
			{
				Role: provider.RoleUser,
				Content: fmt.Sprintf("Original task: %s\n\nWorker outputs:\n\n%s",
					task, combinedWorkerOutput),
			},
		},
	}

	synthResp, err := router.ChatCompletionForRole(ctx, "refinery", synthReq)
	if err != nil {
		return fmt.Errorf("synthesizer (refinery) failed: %w", err)
	}
	synthDuration := time.Since(phaseStart)

	synthPhase := phaseResult{
		Role:             "refinery",
		RequestedModel:   refineryModel,
		ActualModel:      synthResp.Model,
		Provider:         identifyProvider(cfg, synthResp.Model),
		Tokens:           synthResp.Usage.TotalTokens,
		PromptTokens:     synthResp.Usage.PromptTokens,
		CompletionTokens: synthResp.Usage.CompletionTokens,
		Duration:         synthDuration,
		FallbackUsed:     synthResp.Model != refineryModel,
		Output:           strings.TrimSpace(synthResp.Message.Content),
	}

	tracker.Record(
		synthPhase.Provider,
		synthPhase.ActualModel,
		"refinery",
		cost.Usage{
			PromptTokens:     synthResp.Usage.PromptTokens,
			CompletionTokens: synthResp.Usage.CompletionTokens,
			TotalTokens:      synthResp.Usage.TotalTokens,
		},
	)

	printPhaseResult("Phase 3", colorYellow, &synthPhase)

	// Print synthesized output (truncated for PoC).
	fmt.Printf("\n%s--- Synthesized Output (first 500 chars) ---%s\n", colorYellow, colorReset)
	fmt.Println(truncate(synthPhase.Output, 500))
	fmt.Printf("%s--- End ---%s\n\n", colorYellow, colorReset)

	// ════════════════════════════════════════════════════════════════════
	// Summary
	// ════════════════════════════════════════════════════════════════════
	totalDuration := time.Since(totalStart)
	summary := tracker.Summary()

	fmt.Printf("%s%s", colorBold, colorCyan)
	fmt.Printf("Cost & Routing Summary\n")
	fmt.Printf("======================%s\n", colorReset)
	fmt.Printf("Total time:   %s\n", totalDuration.Truncate(time.Millisecond))
	fmt.Printf("Total tokens: %d (prompt: %d, completion: %d)\n",
		summary.TotalTokens, summary.TotalPromptTokens, summary.TotalCompletionTokens)
	fmt.Printf("Est. cost:    $%.6f\n\n", summary.TotalCost)

	// Per-role breakdown.
	fmt.Printf("%sBy Role:%s\n", colorDim, colorReset)
	for role, rs := range summary.ByRole {
		fmt.Printf("  %-12s  %d reqs, %5d tokens, $%.6f\n", role, rs.Requests, rs.Tokens, rs.Cost)
	}

	// Per-provider breakdown.
	fmt.Printf("\n%sBy Provider:%s\n", colorDim, colorReset)
	for prov, ps := range summary.ByProvider {
		fmt.Printf("  %-12s  %d reqs, %5d tokens, $%.6f\n", prov, ps.Requests, ps.Tokens, ps.Cost)
	}

	// Per-model breakdown.
	fmt.Printf("\n%sBy Model:%s\n", colorDim, colorReset)
	for model, ms := range summary.ByModel {
		fmt.Printf("  %-30s  %d reqs, %5d tokens, $%.6f\n", model, ms.Requests, ms.Tokens, ms.Cost)
	}

	// Fallback detection.
	fmt.Printf("\n%sFallback Activity:%s\n", colorDim, colorReset)
	phases := []phaseResult{supervisorPhase}
	for _, r := range results {
		if r.Err == nil {
			phases = append(phases, r.Phase)
		}
	}
	phases = append(phases, synthPhase)

	anyFallback := false
	for _, p := range phases {
		if p.FallbackUsed {
			anyFallback = true
			fmt.Printf("  %s[FALLBACK]%s %s: requested %q, got %q (%s)\n",
				colorRed, colorReset, p.Role, p.RequestedModel, p.ActualModel, p.Provider)
		}
	}
	if !anyFallback {
		fmt.Printf("  (none -- all roles served by primary model)\n")
	}

	fmt.Printf("\n%sDone.%s\n", colorBold, colorReset)
	return nil
}

// buildFactories returns the provider factory map wiring all four adapters.
func buildFactories() map[string]provider.ProviderFactory {
	return map[string]provider.ProviderFactory{
		"openai": func(pc provider.ProviderConfig) (provider.Provider, error) {
			var opts []openai.Option
			if pc.BaseURL != "" {
				opts = append(opts, openai.WithBaseURL(pc.BaseURL))
			}
			return openai.New(pc.APIKey, opts...), nil
		},
		"anthropic": func(pc provider.ProviderConfig) (provider.Provider, error) {
			var opts []anthropic.Option
			if pc.BaseURL != "" {
				opts = append(opts, anthropic.WithBaseURL(pc.BaseURL))
			}
			return anthropic.New(pc.APIKey, opts...), nil
		},
		"ollama": func(pc provider.ProviderConfig) (provider.Provider, error) {
			baseURL := pc.BaseURL
			if baseURL == "" {
				baseURL = "http://localhost:11434"
			}
			return ollama.New(baseURL, pc.APIKey), nil
		},
		"gemini": func(pc provider.ProviderConfig) (provider.Provider, error) {
			var opts []gemini.Option
			if pc.BaseURL != "" {
				opts = append(opts, gemini.WithBaseURL(pc.BaseURL))
			}
			return gemini.New(pc.APIKey, opts...), nil
		},
	}
}

// executeWorker sends a single subtask to a polecat-role worker via streaming
// and collects the full response, tracking cost.
func executeWorker(
	ctx context.Context,
	router *provider.Router,
	cfg *provider.Config,
	tracker *cost.Tracker,
	requestedModel string,
	index int,
	subtask string,
) workerResult {
	start := time.Now()

	req := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role: provider.RoleSystem,
				Content: "You are a coding worker. Implement exactly what is asked. " +
					"Output ONLY the code -- no explanations, no markdown fences unless specifically requested.",
			},
			{
				Role:    provider.RoleUser,
				Content: subtask,
			},
		},
	}

	stream, err := router.StreamChatCompletionForRole(ctx, "polecat", req)
	if err != nil {
		return workerResult{
			Index:   index,
			Subtask: subtask,
			Err:     fmt.Errorf("stream request failed: %w", err),
		}
	}
	defer stream.Close()

	var buf strings.Builder
	var actualModel string
	var finalUsage *provider.Usage

	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return workerResult{
				Index:   index,
				Subtask: subtask,
				Err:     fmt.Errorf("stream read error: %w", err),
			}
		}
		if chunk.Model != "" && actualModel == "" {
			actualModel = chunk.Model
		}
		if chunk.Delta.Content != "" {
			buf.WriteString(chunk.Delta.Content)
		}
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}
	}

	duration := time.Since(start)

	tokens := 0
	promptTokens := 0
	completionTokens := 0
	if finalUsage != nil {
		tokens = finalUsage.TotalTokens
		promptTokens = finalUsage.PromptTokens
		completionTokens = finalUsage.CompletionTokens
	}

	prov := identifyProvider(cfg, actualModel)

	tracker.Record(prov, actualModel, "polecat", cost.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      tokens,
	})

	return workerResult{
		Index:   index,
		Subtask: subtask,
		Phase: phaseResult{
			Role:             "polecat",
			RequestedModel:   requestedModel,
			ActualModel:      actualModel,
			Provider:         prov,
			Tokens:           tokens,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			Duration:         duration,
			FallbackUsed:     actualModel != requestedModel,
			Output:           strings.TrimSpace(buf.String()),
		},
	}
}

// printPhaseResult prints a formatted phase completion line.
func printPhaseResult(label, color string, p *phaseResult) {
	fallbackTag := ""
	if p.FallbackUsed {
		fallbackTag = fmt.Sprintf(" %s[FALLBACK from %s]%s", colorRed, p.RequestedModel, colorReset)
	}
	fmt.Printf("  %s%s%s: model=%s provider=%s tokens=%d %s(%s)%s%s\n",
		color, label, colorReset,
		p.ActualModel, p.Provider, p.Tokens,
		colorDim, p.Duration.Truncate(time.Millisecond), colorReset,
		fallbackTag)
}

// resolveRoleModel returns the primary model name configured for a role.
func resolveRoleModel(cfg *provider.Config, role string) string {
	_, model, err := cfg.ResolveRole(role)
	if err != nil {
		return "(unknown)"
	}
	return model
}

// identifyProvider tries to identify which provider name served a given model.
func identifyProvider(cfg *provider.Config, model string) string {
	for alias, mc := range cfg.Models {
		if mc.Model == model {
			_ = alias
			return mc.Provider
		}
	}
	return "unknown"
}

// parseSubtasks splits the supervisor's response into individual subtask strings.
var numberPrefix = regexp.MustCompile(`^\s*\d+[\.\)\:]\s*`)
var bulletPrefix = regexp.MustCompile(`^\s*[\-\*]\s+`)

func parseSubtasks(raw string) []string {
	lines := strings.Split(raw, "\n")
	var subtasks []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		cleaned := numberPrefix.ReplaceAllString(trimmed, "")
		cleaned = bulletPrefix.ReplaceAllString(cleaned, "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			continue
		}

		lower := strings.ToLower(cleaned)
		if strings.HasPrefix(lower, "here ") || strings.HasPrefix(lower, "note:") || strings.HasPrefix(lower, "summary") {
			continue
		}

		subtasks = append(subtasks, cleaned)
	}

	return subtasks
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
