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
	"strings"
	"time"

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
  et version

Commands:
  run      Execute supervisor→worker flow for a task
  session  Manage interactive agent sessions in tmux
  models   List all available models from configured providers
  version  Print version information

Flags (run):
  --config         Path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)
  --role           Supervisor role name (default: mayor; worker always uses polecat)
  --no-synthesize  Skip synthesis, print raw per-worker output (pool mode only)
  --max-subtasks   Max subtasks for decomposition (0 = Mayor default of 5)
  --timeout        Total timeout in minutes for the entire run (default: 30)
  --output-dir     Directory to write output files (default: stdout only)

Flags (models):
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
	maxSubtasks := fs.Int("max-subtasks", 0, "max subtasks (0 = use Mayor default of 5)")
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
		return cmdRunParallel(ctx, router, cfg, task, *supervisorRole, poolAliases, *noSynthesize, *maxSubtasks, *outputDir, runLogDir)
	}

	// Legacy single-worker flow (no pool configured).
	return cmdRunSingle(ctx, router, task, *supervisorRole, workerRole, *outputDir, runLogDir)
}

// cmdRunParallel implements the three-phase pipeline: decompose → parallel execute → synthesize.
func cmdRunParallel(ctx context.Context, router *provider.Router, cfg *provider.Config, task, supervisorRole string, poolAliases []string, noSynthesize bool, maxSubtasks int, outputDir, runLogDir string) error {
	// Build Mayor with options.
	var mayorOpts []role.MayorOption
	mayorOpts = append(mayorOpts, role.WithMayorRole(supervisorRole))
	if maxSubtasks > 0 {
		mayorOpts = append(mayorOpts, role.WithMayorMaxSubtasks(maxSubtasks))
	}
	mayor := role.NewMayor(router, mayorOpts...)

	// Phase 1: Decompose.
	fmt.Printf("Phase 1: Supervisor (%s) decomposing task...\n", supervisorRole)
	subtasks, err := mayor.Decompose(ctx, task)
	if err != nil {
		return fmt.Errorf("supervisor decompose failed: %w", err)
	}
	fmt.Printf("  Subtasks: %d\n\n", len(subtasks))
	for i, st := range subtasks {
		fmt.Printf("  [%d] %s\n", i+1, truncate(st, 100))
	}
	fmt.Println()

	// Phase 2: Parallel worker execution.
	fmt.Printf("Phase 2: Workers executing in parallel (%d subtasks, %d pool members)...\n", len(subtasks), len(poolAliases))

	balancer := provider.NewBalancer(provider.StrategyRoundRobin)
	wp := pool.New(router, balancer, poolAliases)
	workerSystemPrompt := workerPrompt(outputDir)

	start := time.Now()
	results := wp.ExecuteAll(ctx, subtasks, workerSystemPrompt)
	elapsed := time.Since(start)

	for i, r := range results {
		status := "✓"
		if strings.HasPrefix(r.Response, "error:") {
			status = "✗"
		}
		fmt.Printf("  [%d/%d] %-16s %s (%d tokens, %.1fs)\n", i+1, len(results), r.Role, status, r.Tokens, elapsed.Seconds())
	}
	fmt.Println()

	// Phase 3: Synthesize (unless --no-synthesize).
	if noSynthesize {
		for i, r := range results {
			fmt.Printf("--- Worker %d (%s: subtask %d) ---\n", i+1, r.Role, i+1)
			fmt.Println(r.Response)
			filename, content := parseWorkerOutput(r.Response)
			if filename != "" && outputDir != "" {
				// Named file → output dir.
				if err := writeOutputFile(outputDir, filename, content); err != nil {
					fmt.Fprintf(os.Stderr, "  warning: could not write %s: %v\n", filename, err)
				} else {
					fmt.Printf("  → wrote %s\n", filepath.Join(outputDir, filename))
				}
			} else {
				// Unnamed output → log dir.
				logFile := fmt.Sprintf("worker-%d.out", i+1)
				if err := writeOutputFile(runLogDir, logFile, r.Response); err != nil {
					fmt.Fprintf(os.Stderr, "  warning: could not write log %s: %v\n", logFile, err)
				} else {
					fmt.Printf("  → logged %s\n", filepath.Join(runLogDir, logFile))
				}
			}
		}
		return nil
	}

	fmt.Printf("Phase 3: Supervisor synthesizing results...\n")
	synthesis, err := mayor.Synthesize(ctx, task, results)
	if err != nil {
		return fmt.Errorf("supervisor synthesize failed: %w", err)
	}

	fmt.Printf("\n--- Final Output ---\n")
	fmt.Println(synthesis)
	fmt.Printf("--------------------\n")

	// Write code files to output-dir; logs and synthesis to run log dir.
	for i, r := range results {
		filename, content := parseWorkerOutput(r.Response)
		if filename != "" && outputDir != "" {
			// Named file → output dir.
			if err := writeOutputFile(outputDir, filename, content); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not write %s: %v\n", filename, err)
			} else {
				fmt.Printf("  → wrote %s\n", filepath.Join(outputDir, filename))
			}
		} else {
			// Unnamed output → log dir.
			logFile := fmt.Sprintf("worker-%d.out", i+1)
			if err := writeOutputFile(runLogDir, logFile, r.Response); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not write log %s: %v\n", logFile, err)
			} else {
				fmt.Printf("  → logged %s\n", filepath.Join(runLogDir, logFile))
			}
		}
	}
	if err := writeOutputFile(runLogDir, "_synthesis.md", synthesis); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not write _synthesis.md: %v\n", err)
	} else {
		fmt.Printf("  → logged %s\n", filepath.Join(runLogDir, "_synthesis.md"))
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

	// Write output: named file → output-dir; unnamed → log dir.
	content := totalContent.String()
	filename, fileContent := parseWorkerOutput(content)
	if filename != "" && outputDir != "" {
		if err := writeOutputFile(outputDir, filename, fileContent); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not write %s: %v\n", filename, err)
		} else {
			fmt.Printf("  → wrote %s\n", filepath.Join(outputDir, filename))
		}
	} else {
		if err := writeOutputFile(runLogDir, "output.txt", content); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not write log output.txt: %v\n", err)
		} else {
			fmt.Printf("  → logged %s\n", filepath.Join(runLogDir, "output.txt"))
		}
	}

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

// workerPrompt returns the system prompt for workers, adding a FILENAME: instruction
// when outputDir is set so the LLM indicates which file its output belongs to.
func workerPrompt(outputDir string) string {
	base := "You are a coding worker. Implement exactly what is asked."
	if outputDir != "" {
		return base + " Start your response with EXACTLY this line:\nFILENAME: relative/path/to/file\nThen output ONLY the file content — no explanations, no markdown fences unless the file format requires them."
	}
	return base + " Output ONLY the code -- no explanations, no markdown fences unless specifically requested."
}

// parseWorkerOutput extracts a filename and content from a worker response that
// begins with "FILENAME: path". Returns empty filename if the format is absent.
func parseWorkerOutput(response string) (filename, content string) {
	const prefix = "FILENAME: "
	idx := strings.Index(response, "\n")
	if idx < 0 {
		return "", response
	}
	firstLine := strings.TrimSpace(response[:idx])
	if !strings.HasPrefix(firstLine, prefix) {
		return "", response
	}
	filename = strings.TrimSpace(strings.TrimPrefix(firstLine, prefix))
	// Strip leading slash to ensure relative path.
	filename = strings.TrimPrefix(filename, "/")
	content = response[idx+1:]
	return filename, content
}

// writeOutputFile writes content to path/filename, creating parent directories.
func writeOutputFile(dir, filename, content string) error {
	fullPath := filepath.Join(dir, filename)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("create directories for %s: %w", fullPath, err)
	}
	return os.WriteFile(fullPath, []byte(content), 0644)
}
