// et is the electrictown CLI. It routes tasks through a supervisor→worker flow
// using configurable LLM providers and role-based model assignment.
//
// Usage:
//
//	et run [--config path] [--role name] "task description"
//	et models [--config path]
//	et version
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/meganerd/electrictown/internal/build"
	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/pool"
	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/provider/anthropic"
	"github.com/meganerd/electrictown/internal/provider/gemini"
	"github.com/meganerd/electrictown/internal/provider/ollama"
	"github.com/meganerd/electrictown/internal/provider/openai"
	"github.com/meganerd/electrictown/internal/role"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	subcmd := os.Args[1]
	switch subcmd {
	case "run":
		if err := cmdRun(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", friendlyError(err))
			os.Exit(1)
		}
	case "models":
		if err := cmdModels(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", friendlyError(err))
			os.Exit(1)
		}
	case "nodes":
		if err := cmdNodes(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", friendlyError(err))
			os.Exit(1)
		}
	case "session":
		if err := cmdSession(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("et %s\n", version)
	case "--help", "-h", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", subcmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `electrictown - LLM supervisor/worker task router

Usage:
  et run [--config path] [--role name] "task description"
  et session <spawn|list|attach|kill|send> [args]
  et models [--config path]
  et nodes  [--config path]
  et version

Commands:
  run      Execute supervisor→worker flow for a task
  session  Manage interactive agent sessions in tmux
  models   List all available models from configured providers
  nodes    Ping Ollama nodes, list models, show availability
  version  Print version information

Flags (run):
  --config          Path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)
  --role            Supervisor role name (default: mayor; worker always uses polecat)
  --no-synthesize   Skip synthesis, print raw per-worker output (pool mode only)
  --no-reviewer     Skip Phase 2.5 reviewer scoring of worker outputs
  --no-tester       Skip Phase 4 tester polish of synthesized output
  --iterate         Enable Phase 5 iterative build/fix loop (requires --output-dir)
  --max-iterations  Max build/fix iterations for --iterate (default: 3)
  --max-subtasks    Max subtasks for decomposition (0 = Mayor default of 10)
  --timeout         Total timeout in minutes for the entire run (default: 30)
  --output-dir      Directory to write output files (default: stdout only)

Flags (models, nodes):
  --config   Path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)

Run 'et session --help' for session management details.
`)
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
			var opts []ollama.OllamaOption
			if pc.AuthType != "" {
				opts = append(opts, ollama.WithAuthType(pc.AuthType))
			}
			return ollama.New(baseURL, pc.APIKey, opts...), nil
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

// cmdRun implements the "et run" subcommand.
// When the worker role has a pool configured, it uses a three-phase pipeline:
// decompose → parallel execute → synthesize. Otherwise, it falls back to the
// original single-worker streaming flow.
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)")
	supervisorRole := fs.String("role", "mayor", "supervisor role name")
	noSynthesize := fs.Bool("no-synthesize", false, "skip synthesis, print raw per-worker output")
	noReviewer := fs.Bool("no-reviewer", false, "skip Phase 2.5 reviewer scoring of worker outputs")
	noTester := fs.Bool("no-tester", false, "skip Phase 4 tester polish of synthesized output")
	iterate := fs.Bool("iterate", false, "enable Phase 5 iterative build/fix loop (requires --output-dir)")
	maxIterations := fs.Int("max-iterations", 3, "max build/fix iterations for --iterate (default: 3)")
	maxSubtasks := fs.Int("max-subtasks", 0, "max subtasks (0 = use Mayor default of 10)")
	timeoutMins := fs.Int("timeout", 30, "total timeout in minutes for the entire run")
	outputDir := fs.String("output-dir", "", "directory to write output files (default: stdout only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	task := strings.Join(fs.Args(), " ")
	if task == "" {
		return fmt.Errorf("task description required\n\nUsage: et run [--config path] [--role name] \"task description\"")
	}

	workerRole := "polecat"

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutMins)*time.Minute)
	defer cancel()

	// Resolve config path (explicit or auto-discover).
	resolvedConfig, err := findConfig(*configPath)
	if err != nil {
		return err
	}

	// Load config and create router.
	cfg, err := provider.LoadConfig(resolvedConfig)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	router, err := provider.NewRouter(cfg, buildFactories())
	if err != nil {
		return fmt.Errorf("creating router: %w", err)
	}

	// Build the per-run log directory: {log_dir}/{YYYY-MM-DD}_{shortID}.
	baseLogDir, err := cfg.ResolveLogDir()
	if err != nil {
		return fmt.Errorf("resolving log_dir: %w", err)
	}
	runID, err := generateShortID()
	if err != nil {
		return fmt.Errorf("generating run ID: %w", err)
	}
	runLogDir := filepath.Join(baseLogDir, time.Now().Format("2006-01-02")+"_"+runID)

	fmt.Printf("electrictown %s\n", version)
	fmt.Printf("============\n")
	fmt.Printf("Config: %s\n", resolvedConfig)
	fmt.Printf("Task:   %s\n", task)
	fmt.Printf("Logs:   %s\n\n", runLogDir)

	// Check if the worker role has a pool configured.
	poolAliases := cfg.PoolForRole(workerRole)
	if len(poolAliases) > 0 {
		return cmdRunParallel(ctx, router, cfg, task, *supervisorRole, poolAliases, *noSynthesize, *noReviewer, *noTester, *iterate, *maxIterations, *maxSubtasks, *outputDir, runLogDir)
	}

	// Legacy single-worker flow (no pool configured).
	return cmdRunSingle(ctx, router, task, *supervisorRole, workerRole, *outputDir, runLogDir)
}

// cmdRunParallel implements the multi-phase pipeline:
//
//	1. Decompose  2. Parallel workers  2.5. Reviewer (optional)  3. Synthesize
//	4. Tester (optional)  5. Build/fix loop (optional, requires --iterate)
func cmdRunParallel(ctx context.Context, router *provider.Router, cfg *provider.Config, task, supervisorRole string, poolAliases []string, noSynthesize, noReviewer, noTester, iterate bool, maxIterations, maxSubtasks int, outputDir, runLogDir string) error {
	// Shared cost tracker for all roles in this run.
	tracker := cost.NewTracker(cost.DefaultPricing())

	// Build Mayor with options.
	var mayorOpts []role.MayorOption
	mayorOpts = append(mayorOpts, role.WithMayorRole(supervisorRole))
	mayorOpts = append(mayorOpts, role.WithMayorCostTracker(tracker))
	if maxSubtasks > 0 {
		mayorOpts = append(mayorOpts, role.WithMayorMaxSubtasks(maxSubtasks))
	}
	mayor := role.NewMayor(router, mayorOpts...)

	// Phase 1: Decompose (with spinner showing live token count).
	fmt.Printf("Phase 1: Supervisor (%s) decomposing task...\n", supervisorRole)
	stopSpin1 := startSpinner(spinLabelWithToks("  decomposing", tracker))
	subtasks, err := mayor.Decompose(ctx, task)
	stopSpin1()
	if err != nil {
		return fmt.Errorf("supervisor decompose failed: %w", err)
	}
	fmt.Printf("  Subtasks: %d\n", len(subtasks))
	for i, st := range subtasks {
		fmt.Printf("  [%d] %s\n", i+1, truncate(st, 100))
	}
	fmt.Println()

	// Phase 2: Parallel worker execution with live progress.
	n := len(subtasks)
	fmt.Printf("Phase 2: Workers executing in parallel (%d subtasks, %d pool members)...\n", n, len(poolAliases))

	balancer := provider.NewBalancer(provider.StrategyRoundRobin)
	wp := pool.New(router, balancer, poolAliases)
	workerSystemPrompt := workerPrompt(outputDir)

	lp := newLiveProgress(n)
	wp.SetProgressHook(func(idx int, r role.WorkerResult) {
		status := "✓"
		if strings.HasPrefix(r.Response, "error:") {
			status = "✗"
		}
		toks := fmt.Sprintf("%d tok", r.Tokens)
		tps := ""
		if r.Elapsed > 0 && r.Tokens > 0 {
			tps = fmt.Sprintf(", %.0f tok/s", float64(r.Tokens)/r.Elapsed.Seconds())
		}
		lp.update(idx, fmt.Sprintf("  [%d/%d] %-18s %s (%s%s, %.1fs)",
			idx+1, n, truncate(r.Role, 18), status, toks, tps, r.Elapsed.Seconds()))
	})

	results := wp.ExecuteAll(ctx, subtasks, workerSystemPrompt)
	fmt.Println()

	// Phase 2.5: Reviewer (optional — skipped if --no-reviewer or role not configured).
	if !noReviewer {
		if _, ok := cfg.Roles["reviewer"]; ok {
			fmt.Printf("Phase 2.5: Reviewer scoring worker outputs...\n")
			reviewer := role.NewReviewer(router, role.WithWitnessCostTracker(tracker))
			for i := range results {
				if strings.HasPrefix(results[i].Response, "error:") {
					continue
				}
				score, note, err := reviewer.Score(ctx, results[i].Subtask, results[i].Response)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  reviewer[%d]: %v\n", i+1, err)
					continue
				}
				results[i].ReviewScore = score
				results[i].ReviewNote = note
				results[i].Flagged = score > 0 && score < 6
				flag := "✓"
				if results[i].Flagged {
					flag = "⚑"
				}
				fmt.Printf("  [%d/%d] score=%d/10 %s %s\n", i+1, len(results), score, flag, truncate(note, 80))
			}
			fmt.Println()
		} else {
			fmt.Fprintf(os.Stderr, "  note: reviewer role not configured — skipping Phase 2.5\n")
		}
	}

	// Phase 3: Synthesize (unless --no-synthesize).
	// Collect file→worker map during output writing (used by Phase 5).
	fileWorkerMap := make(map[string]int)
	if noSynthesize {
		for i, r := range results {
			fmt.Printf("--- Worker %d (%s: subtask %d) ---\n", i+1, r.Role, i+1)
			fmt.Println(r.Response)
			files := parseMultiFileOutput(r.Response)
			written := writeWorkerFiles(files, i, outputDir, runLogDir)
			for f := range written {
				fileWorkerMap[f] = i
			}
		}
		return nil
	}

	fmt.Printf("Phase 3: Supervisor synthesizing results...\n")
	stopSpin3 := startSpinner(spinLabelWithToks("  synthesizing", tracker))
	synthesis, err := mayor.Synthesize(ctx, task, results)
	stopSpin3()
	if err != nil {
		return fmt.Errorf("supervisor synthesize failed: %w", err)
	}

	// Phase 4: Tester polish (optional — skipped if --no-tester or role not configured).
	if !noTester {
		if _, ok := cfg.Roles["tester"]; ok {
			fmt.Printf("Phase 4: Tester polishing synthesized output...\n")
			stopSpin4 := startSpinner(spinLabelWithToks("  refining", tracker))
			tester := role.NewTester(router, role.WithRefineryCostTracker(tracker))
			refined, err := tester.Refine(ctx, synthesis)
			stopSpin4()
			if err != nil {
				fmt.Fprintf(os.Stderr, "  tester failed: %v — using raw synthesis\n", err)
			} else {
				synthesis = refined.Message.Content
				fmt.Printf("  Tester refined output (%d tokens)\n", refined.Usage.TotalTokens)
			}
			fmt.Println()
		} else {
			fmt.Fprintf(os.Stderr, "  note: tester role not configured — skipping Phase 4\n")
		}
	}

	fmt.Printf("\n--- Final Output ---\n")
	fmt.Println(synthesis)
	fmt.Printf("--------------------\n")

	// Write code files to output-dir; logs and synthesis to run log dir.
	for i, r := range results {
		files := parseMultiFileOutput(r.Response)
		written := writeWorkerFiles(files, i, outputDir, runLogDir)
		for f := range written {
			fileWorkerMap[f] = i
		}
	}
	if err := writeOutputFile(runLogDir, "_synthesis.md", synthesis); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not write _synthesis.md: %v\n", err)
	} else {
		fmt.Printf("  → logged %s\n", filepath.Join(runLogDir, "_synthesis.md"))
	}

	// Phase 5: Iterative build/fix loop (optional).
	if iterate && outputDir != "" {
		runner := build.DetectRunner(outputDir)
		if runner == nil {
			fmt.Fprintf(os.Stderr, "  note: no build system detected in %s — skipping Phase 5\n", outputDir)
		} else {
			fmt.Printf("Phase 5: Iterative build/fix loop (%s, max %d iterations)...\n", runner.Name(), maxIterations)
			buildOK := false
			for iter := 1; iter <= maxIterations; iter++ {
				fmt.Printf("  [iter %d/%d] building...\n", iter, maxIterations)
				stdout, stderr, buildErr := runner.Run(ctx, outputDir)
				_ = stdout

				// Log full build output.
				logContent := "=== stdout ===\n" + stdout + "\n=== stderr ===\n" + stderr
				if err := writeOutputFile(runLogDir, fmt.Sprintf("_build_iter%d.log", iter), logContent); err != nil {
					fmt.Fprintf(os.Stderr, "  warning: could not write build log: %v\n", err)
				}

				if buildErr == nil {
					fmt.Printf("  ✓ Build succeeded on iteration %d\n", iter)
					buildOK = true
					break
				}

				fmt.Printf("  ✗ Build failed:\n")
				fmt.Println(build.ErrorSummary(stderr, 20))

				if iter == maxIterations {
					break
				}

				// Parse errors, attribute to workers, dispatch targeted fixes.
				buildErrors := build.NormalizeErrorPaths(build.ParseBuildErrors(stderr), outputDir)
				workerErrors := build.MapFilesToWorkers(buildErrors, fileWorkerMap)

				if len(workerErrors) == 0 {
					fmt.Fprintf(os.Stderr, "  could not attribute errors to workers — skipping fix dispatch\n")
					break
				}

				fmt.Printf("  Dispatching fix subtasks to %d worker(s)...\n", len(workerErrors))
				fixSubtasks := buildFixSubtasks(workerErrors, outputDir)

				fixResults := wp.ExecuteAll(ctx, fixSubtasks, workerSystemPrompt)
				for workerIdx, fixResult := range fixResults {
					fixFiles := parseMultiFileOutput(fixResult.Response)
					written := writeWorkerFiles(fixFiles, workerIdx, outputDir, runLogDir)
					for f := range written {
						fileWorkerMap[f] = workerIdx
					}
				}
			}

			if !buildOK {
				fmt.Printf("  ✗ Max iterations reached — build still failing\n")
			}
			fmt.Println()
		}
	}

	// Token summary by role.
	sum := tracker.Summary()
	if sum.TotalTokens > 0 {
		fmt.Printf("\n--- Token Usage ---\n")
		for _, roleName := range []string{"mayor", "reviewer", "tester"} {
			if rs, ok := sum.ByRole[roleName]; ok {
				fmt.Printf("  %-12s %s tok\n", roleName+":", formatToks(rs.Tokens))
			}
		}
		fmt.Printf("  %-12s %s tok\n", "total:", formatToks(sum.TotalTokens))
		fmt.Printf("-------------------\n")
	}

	return nil
}

// cmdRunSingle implements the legacy single-worker streaming flow.
func cmdRunSingle(ctx context.Context, router *provider.Router, task, supervisorRole, workerRole, outputDir, runLogDir string) error {
	// Phase 1: Supervisor generates subtask via ChatCompletion.
	fmt.Printf("Phase 1: Supervisor (%s) analyzing task...\n", supervisorRole)

	supervisorReq := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role:    provider.RoleSystem,
				Content: "You are a coding supervisor. Given a task, produce a clear, concise implementation plan with exactly ONE subtask for a worker to implement. Output ONLY the subtask description -- no preamble, no numbering, just the task description the worker needs.",
			},
			{
				Role:    provider.RoleUser,
				Content: task,
			},
		},
	}

	supervisorResp, err := router.ChatCompletionForRole(ctx, supervisorRole, supervisorReq)
	if err != nil {
		return fmt.Errorf("supervisor request failed: %w", err)
	}

	subtask := strings.TrimSpace(supervisorResp.Message.Content)
	fmt.Printf("  model=%s (%d tokens)\n", supervisorResp.Model, supervisorResp.Usage.TotalTokens)
	fmt.Printf("  Subtask: %s\n\n", truncate(subtask, 120))

	// Phase 2: Worker executes subtask via StreamChatCompletion.
	fmt.Printf("Phase 2: Worker (%s) executing subtask (streaming)...\n", workerRole)

	workerReq := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role:    provider.RoleSystem,
				Content: workerPrompt(outputDir),
			},
			{
				Role:    provider.RoleUser,
				Content: subtask,
			},
		},
	}

	stream, err := router.StreamChatCompletionForRole(ctx, workerRole, workerReq)
	if err != nil {
		return fmt.Errorf("worker stream request failed: %w", err)
	}
	defer stream.Close()

	var totalContent strings.Builder
	firstChunk := true
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("worker stream error: %w", err)
		}

		if firstChunk && chunk.Model != "" {
			fmt.Printf("  model=%s\n\n", chunk.Model)
			fmt.Printf("--- Worker Output ---\n")
			firstChunk = false
		}

		if chunk.Delta.Content != "" {
			fmt.Print(chunk.Delta.Content)
			totalContent.WriteString(chunk.Delta.Content)
		}

		if chunk.Done && chunk.Usage != nil {
			fmt.Printf("\n---------------------\n")
			fmt.Printf("  Worker tokens: %d\n", chunk.Usage.TotalTokens)
		}
	}

	if firstChunk {
		fmt.Printf("  (streaming)\n\n--- Worker Output ---\n")
		fmt.Print(totalContent.String())
		fmt.Printf("\n---------------------\n")
	}

	// Write output: named files → output-dir; unnamed → log dir.
	files := parseMultiFileOutput(totalContent.String())
	writeWorkerFiles(files, 0, outputDir, runLogDir)

	// Usage summary.
	fmt.Printf("\nDone: supervisor→worker round-trip complete\n")
	fmt.Printf("  Supervisor tokens: %d\n", supervisorResp.Usage.TotalTokens)
	return nil
}

// cmdModels implements the "et models" subcommand.
func cmdModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedConfig, err := findConfig(*configPath)
	if err != nil {
		return err
	}

	cfg, err := provider.LoadConfig(resolvedConfig)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	router, err := provider.NewRouter(cfg, buildFactories())
	if err != nil {
		return fmt.Errorf("creating router: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := router.ListAllModels(ctx)
	if err != nil {
		return fmt.Errorf("listing models: %w", err)
	}

	if len(models) == 0 {
		fmt.Println("No models available.")
		return nil
	}

	// Print formatted table.
	fmt.Printf("%-15s %s\n", "PROVIDER", "MODEL ID")
	fmt.Printf("%-15s %s\n", "--------", "--------")
	for _, m := range models {
		fmt.Printf("%-15s %s\n", m.Provider, m.ID)
	}

	return nil
}

// friendlyError rewrites known raw error messages into actionable plain-text hints.
func friendlyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return msg + "\n  hint: the target host is not reachable — check that the Ollama service is running and the base_url in your config is correct"
	case strings.Contains(msg, "no such host"):
		return msg + "\n  hint: hostname could not be resolved — verify the base_url in your config points to a reachable host"
	case strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "deadline exceeded"):
		return msg + "\n  hint: the request timed out — the model may be loading or the host may be overloaded"
	case strings.Contains(msg, "x-api-key") || strings.Contains(msg, "authentication") || strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "unauthorized"):
		return msg + "\n  hint: check that your API key environment variable is exported in your shell"
	default:
		return msg
	}
}

// findConfig resolves the config file path. If explicit is non-empty it is
// returned as-is. Otherwise electrictown.yaml is searched in the current
// directory first, then $HOME.
func findConfig(explicit string) (string, error) {
	const name = "electrictown.yaml"
	if explicit != "" {
		return explicit, nil
	}
	if _, err := os.Stat(name); err == nil {
		return name, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no config specified and cannot determine home directory: %w", err)
	}
	p := filepath.Join(home, name)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("no config file found; tried ./%s and %s — use --config to specify a path", name, p)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// FileOutput holds a single parsed file from a worker response.
type FileOutput struct {
	Name    string // relative path; empty means unnamed (goes to log dir)
	Content string
}

// workerPrompt returns the system prompt for workers.
// When outputDir is set, instructs multi-file output with ===FILE: === delimiters.
func workerPrompt(outputDir string) string {
	base := "You are a coding worker. Implement exactly what is asked."
	if outputDir != "" {
		return base + `

Output all required source files using this exact format — one block per file:

===FILE: relative/path/to/file.go===
<complete file content here>
===ENDFILE===

Rules:
- Output ONLY file content — no explanations, no commentary.
- Each file must be complete and standalone (proper package declaration, all imports).
- Use relative paths from the project root.
- You may output as many files as the subtask requires.`
	}
	return base + " Output ONLY the code — no explanations, no markdown fences unless specifically requested."
}

// parseMultiFileOutput parses worker response into a slice of FileOutput.
// Handles three formats (in priority order):
//  1. Multi-file: ===FILE: path=== ... ===ENDFILE===
//  2. Single-file legacy: FILENAME: path\n<content>
//  3. Unnamed fallback: entire response as unnamed content
func parseMultiFileOutput(response string) []FileOutput {
	// Try multi-file format first.
	if strings.Contains(response, "===FILE:") {
		return parseMultiFileBlocks(response)
	}

	// Try legacy single-file FILENAME: header.
	const prefix = "FILENAME: "
	idx := strings.Index(response, "\n")
	if idx >= 0 {
		firstLine := strings.TrimSpace(response[:idx])
		if strings.HasPrefix(firstLine, prefix) {
			name := strings.TrimPrefix(firstLine, prefix)
			name = strings.TrimPrefix(strings.TrimSpace(name), "/")
			return []FileOutput{{Name: name, Content: response[idx+1:]}}
		}
	}

	// Unnamed fallback.
	return []FileOutput{{Name: "", Content: response}}
}

// fileBlockPattern matches ===FILE: path=== ... ===ENDFILE=== blocks.
var fileBlockPattern = regexp.MustCompile(`(?s)===FILE:\s*([^\n=]+?)===\s*\n(.*?)(?:===ENDFILE===|(?:===FILE:))`)

func parseMultiFileBlocks(response string) []FileOutput {
	// Append a sentinel so the last block is caught by the non-lookahead pattern.
	text := response + "\n===FILE: __sentinel__==="
	matches := fileBlockPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return []FileOutput{{Name: "", Content: response}}
	}
	files := make([]FileOutput, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if name == "__sentinel__" {
			continue
		}
		name = strings.TrimPrefix(name, "/")
		content := strings.TrimLeft(m[2], "\n")
		// Strip trailing ===ENDFILE=== if present.
		content = strings.TrimSuffix(strings.TrimRight(content, "\n\r\t "), "===ENDFILE===")
		content = strings.TrimRight(content, "\n\r\t ")
		if name != "" {
			files = append(files, FileOutput{Name: name, Content: content})
		}
	}
	if len(files) == 0 {
		return []FileOutput{{Name: "", Content: response}}
	}
	return files
}

// writeOutputFile writes content to path/filename, creating parent directories.
func writeOutputFile(dir, filename, content string) error {
	fullPath := filepath.Join(dir, filename)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("create directories for %s: %w", fullPath, err)
	}
	return os.WriteFile(fullPath, []byte(content), 0644)
}

// startSpinner launches an animated spinner on stderr. labelFn is called on
// each tick to get the current label (allowing live cost/token updates).
// Returns a stop function that stops the spinner and clears the line.
func startSpinner(labelFn func() string) func() {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			case <-time.After(80 * time.Millisecond):
				fmt.Fprintf(os.Stderr, "\r%s %s ", frames[i%len(frames)], labelFn())
				i++
			}
		}
	}()
	return func() {
		close(stop)
		wg.Wait()
	}
}

// spinLabel returns a static label function for startSpinner.
func spinLabel(s string) func() string { return func() string { return s } }

// spinLabelWithToks returns a label function that appends a live token count.
func spinLabelWithToks(base string, tracker *cost.Tracker) func() string {
	return func() string {
		total := tracker.Summary().TotalTokens
		if total == 0 {
			return base
		}
		return fmt.Sprintf("%s [%s tok]", base, formatToks(total))
	}
}

// formatToks formats a token count as "1.2k" or "123".
func formatToks(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// liveProgress renders per-worker status lines in-place using ANSI cursor moves.
type liveProgress struct {
	mu      sync.Mutex
	lines   []string
	started bool
}

func newLiveProgress(n int) *liveProgress {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("  [%d/%d] waiting...", i+1, n)
	}
	return &liveProgress{lines: lines}
}

func (lp *liveProgress) update(idx int, line string) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	if idx >= 0 && idx < len(lp.lines) {
		lp.lines[idx] = line
	}
	n := len(lp.lines)
	if !lp.started {
		for _, l := range lp.lines {
			fmt.Println(l)
		}
		lp.started = true
		return
	}
	// Cursor up n lines, then reprint each.
	fmt.Printf("\033[%dA", n)
	for _, l := range lp.lines {
		fmt.Printf("\r\033[K%s\n", l)
	}
}

// writeWorkerFiles writes parsed file outputs from a single worker.
// Named files go to outputDir (when set); unnamed fallback goes to logDir as workerN.out.
// Returns a map of written named file paths (relative) to confirm what was written.
func writeWorkerFiles(files []FileOutput, workerIdx int, outputDir, logDir string) map[string]struct{} {
	written := make(map[string]struct{})
	for _, f := range files {
		if f.Name != "" && outputDir != "" {
			if err := writeOutputFile(outputDir, f.Name, f.Content); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not write %s: %v\n", f.Name, err)
			} else {
				fmt.Printf("  → wrote %s\n", filepath.Join(outputDir, f.Name))
				written[f.Name] = struct{}{}
			}
		}
	}
	// If no named files were written (or outputDir unset), log the raw response.
	if len(written) == 0 {
		logFile := fmt.Sprintf("worker-%d.out", workerIdx+1)
		raw := files[0].Content
		if err := writeOutputFile(logDir, logFile, raw); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not write log %s: %v\n", logFile, err)
		} else {
			fmt.Printf("  → logged %s\n", filepath.Join(logDir, logFile))
		}
	}
	return written
}

// buildFixSubtasks builds targeted fix subtask prompts for workers with build errors.
// Each prompt includes the failing file's current content and the attributed errors.
func buildFixSubtasks(workerErrors map[int][]build.BuildError, outputDir string) []string {
	subtasks := make([]string, 0, len(workerErrors))
	for _, errs := range workerErrors {
		var sb strings.Builder
		sb.WriteString("Your previous output had build errors. Fix ONLY the files listed below.\n\n")
		for _, e := range errs {
			sb.WriteString(fmt.Sprintf("File: %s\n", e.File))
			content, readErr := os.ReadFile(filepath.Join(outputDir, e.File))
			if readErr == nil {
				sb.WriteString("Current content:\n```\n")
				sb.Write(content)
				sb.WriteString("\n```\n")
			}
			sb.WriteString(fmt.Sprintf("Build error (line %d): %s\n\n", e.Line, e.Message))
		}
		sb.WriteString("Output the corrected file(s) using ===FILE: path=== ... ===ENDFILE=== format.")
		subtasks = append(subtasks, sb.String())
	}
	return subtasks
}
