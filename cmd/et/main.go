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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/meganerd/electrictown/internal/build"
	"github.com/meganerd/electrictown/internal/cache"
	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/decision"
	"github.com/meganerd/electrictown/internal/fileutil"
	"github.com/meganerd/electrictown/internal/jina"
	"github.com/meganerd/electrictown/internal/pool"
	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/provider/anthropic"
	"github.com/meganerd/electrictown/internal/provider/gemini"
	"github.com/meganerd/electrictown/internal/provider/ollama"
	"github.com/meganerd/electrictown/internal/provider/openai"
	"github.com/meganerd/electrictown/internal/rag"
	"github.com/meganerd/electrictown/internal/role"
	"github.com/meganerd/electrictown/internal/validate"
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
	case "rag":
		if err := cmdRag(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
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
  et rag     <ingest|query|stats> [flags] [args]
  et models  [--config path]
  et nodes   [--config path]
  et version

Commands:
  run      Execute supervisor→worker flow for a task
  session  Manage interactive agent sessions in tmux
  rag      Manage RAG knowledge base (ingest, query, stats)
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
  --rag-url         Qdrant server URL for RAG context injection (empty = disabled)
  --rag-collection  Qdrant collection name (default: et-knowledge)
  --rag-embed-url   Ollama URL for RAG embeddings (default: http://ai01:11434)
  --jina-key            Jina AI Reader API key for mayor-driven URL fetch (falls back to JINA_API_KEY env var)
  --no-coordinate       Skip Phase 1.5 coordination brief generation
  --guardrail-retries   Max retries for workers scoring below guardrail threshold (default: 1)
  --guardrail-threshold Minimum reviewer score (1-10) before triggering retry (default: 6)
  --no-specialists      Disable specialist routing (ignore specialists config)

Flags (models, nodes):
  --config   Path to config file (default: ./electrictown.yaml, then $HOME/electrictown.yaml)

Run 'et session --help' for session management details.
Run 'et rag ingest --help', 'et rag query --help', or 'et rag stats --help' for RAG details.
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
	timeoutMins := fs.Int("timeout", 45, "total timeout in minutes for the entire run")
	outputDir := fs.String("output-dir", "", "directory to write output files (default: stdout only)")
	ragURL := fs.String("rag-url", "", "Qdrant server URL for RAG context injection (empty = disabled)")
	ragCollection := fs.String("rag-collection", "et-knowledge", "Qdrant collection name for RAG")
	ragEmbedURL := fs.String("rag-embed-url", "http://ai01:11434", "Ollama URL for RAG embeddings")
	jinaKey := fs.String("jina-key", "", "Jina AI Reader API key for mayor-driven URL fetch (falls back to JINA_API_KEY env var)")
	noCoordinate := fs.Bool("no-coordinate", false, "skip Phase 1.5 coordination brief generation")
	guardrailRetries := fs.Int("guardrail-retries", 1, "max retries for workers scoring below guardrail threshold")
	guardrailThreshold := fs.Int("guardrail-threshold", 6, "minimum reviewer score (1-10) before triggering guardrail retry")
	noSpecialists := fs.Bool("no-specialists", false, "disable specialist routing (ignore specialists config)")
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
	if err := os.MkdirAll(runLogDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: cannot create log directory %s: %s — continuing without logs\n", runLogDir, classifyFSError(err))
	}

	fmt.Printf("electrictown %s\n", version)
	fmt.Printf("============\n")
	fmt.Printf("Config: %s\n", resolvedConfig)
	fmt.Printf("Task:   %s\n", task)
	fmt.Printf("Logs:   %s\n", runLogDir)
	fmt.Printf("Start:  %s\n\n", time.Now().Format("15:04:05"))

	// Check if the worker role has a pool configured.
	poolAliases := cfg.PoolForRole(workerRole)
	if len(poolAliases) > 0 {
		return cmdRunParallel(ctx, router, cfg, task, *supervisorRole, poolAliases, *noSynthesize, *noReviewer, *noTester, *iterate, *maxIterations, *maxSubtasks, *outputDir, runLogDir, *ragURL, *ragCollection, *ragEmbedURL, *jinaKey, *noCoordinate, *guardrailRetries, *guardrailThreshold, *noSpecialists)
	}

	// Legacy single-worker flow (no pool configured).
	return cmdRunSingle(ctx, router, task, *supervisorRole, workerRole, *outputDir, runLogDir)
}

// cmdRunParallel implements the multi-phase pipeline:
//
//	0. RAG (optional)  0.5. Jina fetch (optional)  1. Decompose  2. Parallel workers
//	2.5. Reviewer (optional)  3. Synthesize  4. Tester (optional)
//	5. Build/fix loop (optional, requires --iterate)
func cmdRunParallel(ctx context.Context, router *provider.Router, cfg *provider.Config, task, supervisorRole string, poolAliases []string, noSynthesize, noReviewer, noTester, iterate bool, maxIterations, maxSubtasks int, outputDir, runLogDir, ragURL, ragCollection, ragEmbedURL, jinaKey string, noCoordinate bool, guardrailRetries, guardrailThreshold int, noSpecialists bool) error {
	// Shared cost tracker for all roles in this run.
	tracker := cost.NewTracker(cost.DefaultPricing())

	// Phase timing tracker.
	pt := newPhaseTracker()

	// Decision logger for observability.
	decLog, decErr := decision.NewLogger(filepath.Join(runLogDir, "_decisions.jsonl"))
	if decErr != nil {
		fmt.Fprintf(os.Stderr, "  warning: decision logger: %v — continuing without\n", decErr)
	}
	defer decLog.Close()

	// Build Mayor with options.
	var mayorOpts []role.MayorOption
	mayorOpts = append(mayorOpts, role.WithMayorRole(supervisorRole))
	mayorOpts = append(mayorOpts, role.WithMayorCostTracker(tracker))
	if maxSubtasks > 0 {
		mayorOpts = append(mayorOpts, role.WithMayorMaxSubtasks(maxSubtasks))
	}
	// Inject specialist config into mayor for routing-aware decomposition.
	hasSpecialists := !noSpecialists && len(cfg.Specialists) > 0
	if hasSpecialists {
		mayorOpts = append(mayorOpts, role.WithMayorSpecialists(cfg.Specialists))
	}
	mayor := role.NewMayor(router, mayorOpts...)

	// Phase 0: RAG context retrieval (optional — only when --rag-url is set).
	ragContext := ""
	workerRAGContext := ""
	if ragURL != "" {
		fmt.Printf("Phase 0: RAG context retrieval from %s (collection: %s)...\n", ragURL, ragCollection)
		ragClient := rag.NewClient(ragURL, ragCollection)
		ragEmbedder := rag.NewEmbedder(ragEmbedURL, rag.DefaultEmbedModel)
		retriever := rag.NewRetriever(ragClient, ragEmbedder)
		results, err := retriever.Retrieve(ctx, task, 3)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: RAG retrieval failed: %v — continuing without context\n", err)
		} else {
			ragContext = retriever.FormatContext(results)
			workerRAGContext = ragContext
			fmt.Printf("  Retrieved %d chunks\n", len(results))
		}
		fmt.Println()
	}

	// Augment the task with RAG context for the mayor decompose call.
	decomposeTask := task
	if ragContext != "" {
		decomposeTask = ragContext + "\n---\n\n" + task
	}

	// Phase 0.5: Mayor staleness assessment + Jina Reader URL fetch (optional).
	// Runs when --jina-key is set (or JINA_API_KEY env var). Skipped when no key.
	resolvedJinaKey := jinaKey
	if resolvedJinaKey == "" {
		resolvedJinaKey = os.Getenv("JINA_API_KEY")
	}
	if resolvedJinaKey != "" {
		fmt.Printf("Phase 0.5: Mayor assessing knowledge staleness...\n")
		pt.start("Phase 0.5 assess")
		stopSpin05 := startSpinner(spinLabelWithToks("  assessing", tracker))
		assess, assessErr := mayor.Assess(ctx, task)
		stopSpin05()
		if assessErr != nil {
			fmt.Fprintf(os.Stderr, "  warning: mayor assess failed: %v — continuing without Jina fetch\n", assessErr)
		} else {
			fmt.Printf("  Staleness risk: %s\n", assess.StalenessRisk)
			if len(assess.FetchURLs) > 0 {
				fmt.Printf("  Fetching %d URL(s) via Jina Reader...\n", len(assess.FetchURLs))
				jinaClient := jina.New(resolvedJinaKey)
				var jinaBuilder strings.Builder
				for _, u := range assess.FetchURLs {
					content, fetchErr := jinaClient.FetchURL(ctx, u)
					if fetchErr != nil {
						fmt.Fprintf(os.Stderr, "  warning: Jina fetch %s: %v\n", u, fetchErr)
						continue
					}
					if len(content) > 8192 {
						content = content[:8192]
					}
					jinaBuilder.WriteString(fmt.Sprintf("=== Fetched: %s ===\n%s\n\n", u, content))
					fmt.Printf("  ✓ fetched %s (%d chars)\n", u, len(content))
				}
				if jinaBuilder.Len() > 0 {
					fetched := jinaBuilder.String()
					decomposeTask = fetched + "\n---\n\n" + decomposeTask
					workerRAGContext = fetched + "\n\n" + workerRAGContext
				}
			}
		}
		pt.stop()
		fmt.Println()
	}

	// Phase 1: Decompose (with spinner showing live token count).
	fmt.Printf("Phase 1: Supervisor (%s) decomposing task...\n", supervisorRole)
	pt.start("Phase 1 decompose")
	stopSpin1 := startSpinner(spinLabelWithToks("  decomposing", tracker))
	subtasks, err := mayor.Decompose(ctx, decomposeTask)
	stopSpin1()
	if err != nil {
		return fmt.Errorf("supervisor decompose failed: %w", err)
	}
	// Parse dependency markers from subtasks.
	deps := pool.ParseDependencies(subtasks)
	hasDeps := pool.HasDependencies(deps)

	decLog.Log(decision.Decision{
		Phase:   "decompose",
		Agent:   supervisorRole,
		Intent:  "split task into parallel subtasks",
		Action:  fmt.Sprintf("produced %d subtasks", len(subtasks)),
		Outcome: "success",
		Detail:  truncate(task, 120),
	})

	fmt.Printf("  Subtasks: %d\n", len(subtasks))
	for i, st := range subtasks {
		fmt.Printf("  [%d] %s\n", i+1, truncate(st, 100))
	}
	if hasDeps {
		fmt.Printf("  Dependencies detected — will execute in waves\n")
	}
	pt.stop()
	fmt.Println()

	// Phase 1.25: Specialist resolution (when specialists are configured).
	var resolvedModels []string
	var resolvedFallbacks [][]string
	if hasSpecialists {
		fmt.Printf("Phase 1.25: Resolving specialist assignments...\n")
		specialistNames := cfg.SpecialistNames()
		resolvedModels = make([]string, len(subtasks))
		resolvedFallbacks = make([][]string, len(subtasks))
		specialistBalancers := make(map[string]*provider.Balancer)

		for i, st := range subtasks {
			assigned := pool.ParseSpecialistAssignment(st)
			if assigned == "" {
				// No marker — use default pool via balancer (empty override).
				resolvedModels[i] = ""
				fmt.Printf("  [%d] → general-default\n", i+1)
				continue
			}

			// Check if specialist exists in config.
			spec, ok := cfg.Specialists[assigned]
			if !ok {
				// Try fuzzy match.
				if match, found := pool.FuzzyMatchSpecialist(assigned, specialistNames); found {
					fmt.Fprintf(os.Stderr, "  ⚠ [%d] specialist %q not found, using fuzzy match %q\n", i+1, assigned, match)
					assigned = match
					spec = cfg.Specialists[match]
				} else {
					fmt.Fprintf(os.Stderr, "  ⚠ [%d] specialist %q not found, falling back to general-default\n", i+1, assigned)
					resolvedModels[i] = ""

					decLog.Log(decision.Decision{
						Phase:   "specialist-resolve",
						Agent:   "orchestrator",
						Intent:  fmt.Sprintf("resolve specialist %q for subtask %d", assigned, i+1),
						Action:  "fell back to general-default",
						Outcome: "warning",
						Detail:  fmt.Sprintf("specialist %q not in config", assigned),
					})
					continue
				}
			}

			// Resolve model alias for this specialist.
			if len(spec.Pool) > 0 {
				if _, exists := specialistBalancers[assigned]; !exists {
					specialistBalancers[assigned] = provider.NewBalancer(provider.StrategyRoundRobin)
				}
				resolvedModels[i] = specialistBalancers[assigned].Select(assigned, spec.Pool)
			} else {
				resolvedModels[i] = spec.Model
			}

			// Wire specialist fallback chain.
			if len(spec.Fallbacks) > 0 {
				resolvedFallbacks[i] = spec.Fallbacks
			}

			fmt.Printf("  [%d] → %s (%s)\n", i+1, assigned, resolvedModels[i])

			decLog.Log(decision.Decision{
				Phase:   "specialist-resolve",
				Agent:   "orchestrator",
				Intent:  fmt.Sprintf("assign subtask %d to specialist", i+1),
				Action:  fmt.Sprintf("routed to %s via %s", assigned, resolvedModels[i]),
				Outcome: "success",
				Detail:  truncate(st, 120),
			})
		}
		fmt.Println()
	}

	// Phase 1.5: Coordination brief (optional — skipped if --no-coordinate).
	workerSystemPrompt := workerPrompt(outputDir)
	if workerRAGContext != "" {
		workerSystemPrompt = workerRAGContext + "\n---\n\n" + workerSystemPrompt
	}
	if !noCoordinate && len(subtasks) > 1 {
		fmt.Printf("Phase 1.5: Mayor producing coordination brief...\n")
		pt.start("Phase 1.5 coordinate")
		stopSpin15 := startSpinner(spinLabelWithToks("  coordinating", tracker))
		brief, coordErr := mayor.Coordinate(ctx, task, subtasks)
		stopSpin15()
		if coordErr != nil {
			fmt.Fprintf(os.Stderr, "  warning: coordination brief failed: %v — continuing without\n", coordErr)
		} else if brief != "" {
			workerSystemPrompt = "## Project Coordination\n" + brief + "\n---\n\n" + workerSystemPrompt
			fmt.Printf("  ✓ coordination brief injected (%d chars)\n", len(brief))
		}
		pt.stop()
		fmt.Println()
	}

	// Initialize response cache for deduplication in build/fix iterations.
	responseCache := cache.New()
	_ = responseCache // used in Phase 5

	// Phase 2: Worker execution (parallel or DAG-ordered).
	n := len(subtasks)
	balancer := provider.NewBalancer(provider.StrategyRoundRobin)
	wp := pool.New(router, balancer, poolAliases)

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

	var results []role.WorkerResult
	pt.start("Phase 2 workers")
	if hasDeps {
		fmt.Printf("Phase 2: Workers executing with dependency ordering (%d subtasks, %d pool members)...\n", n, len(poolAliases))
		var dagErr error
		if resolvedModels != nil {
			results, dagErr = wp.ExecuteDAGWithModels(ctx, subtasks, deps, resolvedModels, resolvedFallbacks, workerSystemPrompt)
		} else {
			results, dagErr = wp.ExecuteDAG(ctx, subtasks, deps, workerSystemPrompt)
		}
		if dagErr != nil {
			return fmt.Errorf("DAG execution failed: %w", dagErr)
		}
	} else {
		fmt.Printf("Phase 2: Workers executing in parallel (%d subtasks, %d pool members)...\n", n, len(poolAliases))
		if resolvedModels != nil {
			results = wp.ExecuteAllWithModels(ctx, subtasks, resolvedModels, resolvedFallbacks, workerSystemPrompt)
		} else {
			results = wp.ExecuteAll(ctx, subtasks, workerSystemPrompt)
		}
	}
	pt.stop()
	fmt.Println()

	// Phase 2.25: Structured output validation (when --output-dir is set).
	if outputDir != "" {
		validationRetried := 0
		for i := range results {
			if strings.HasPrefix(results[i].Response, "error:") {
				continue
			}
			ok, valErrs := validate.ValidateFileBlocks(results[i].Response)
			if ok {
				continue
			}
			validationRetried++
			fmt.Printf("  ⚠ worker[%d] output validation failed: %s\n", i+1, strings.Join(valErrs, "; "))
			// Retry once with validation feedback.
			retryPrompt := fmt.Sprintf(
				"Your previous output had format errors:\n%s\n\nOriginal subtask: %s\n\nPlease output corrected files using ===FILE: path=== ... ===ENDFILE=== format.",
				strings.Join(valErrs, "\n"), results[i].Subtask,
			)
			retryReq := &provider.ChatRequest{
				Model: results[i].Role,
				Messages: []provider.Message{
					{Role: provider.RoleSystem, Content: workerSystemPrompt},
					{Role: provider.RoleUser, Content: retryPrompt},
				},
			}
			resp, retryErr := router.ChatCompletion(ctx, retryReq)
			if retryErr != nil {
				fmt.Fprintf(os.Stderr, "  validation retry[%d] failed: %v\n", i+1, retryErr)
				continue
			}
			results[i].Response = resp.Message.Content
			results[i].Tokens += resp.Usage.TotalTokens
			fmt.Printf("  ✓ worker[%d] re-submitted after validation fix\n", i+1)
		}
		if validationRetried > 0 {
			fmt.Println()
		}
	}

	// Phase 2.5: Reviewer + guardrail retries (optional).
	if !noReviewer {
		if _, ok := cfg.Roles["reviewer"]; ok {
			fmt.Printf("Phase 2.5: Reviewer scoring worker outputs...\n")
			pt.start("Phase 2.5 reviewer")
			reviewer := role.NewReviewer(router, role.WithWitnessCostTracker(tracker))
			for i := range results {
				if strings.HasPrefix(results[i].Response, "error:") {
					continue
				}
				score, note, scoreErr := reviewer.Score(ctx, results[i].Subtask, results[i].Response)
				if scoreErr != nil {
					fmt.Fprintf(os.Stderr, "  reviewer[%d]: %v\n", i+1, scoreErr)
					continue
				}
				results[i].ReviewScore = score
				results[i].ReviewNote = note
				results[i].Flagged = score > 0 && score < guardrailThreshold

				decLog.Log(decision.Decision{
					Phase:     "review",
					Agent:     "reviewer",
					Intent:    fmt.Sprintf("score worker %d output", i+1),
					Action:    fmt.Sprintf("scored %d/10", score),
					Outcome:   map[bool]string{true: "flagged", false: "passed"}[results[i].Flagged],
					Detail:    truncate(note, 120),
					TokenCost: results[i].Tokens,
				})

				// Guardrail retry loop: re-dispatch workers scoring below threshold.
				guardDoom := pool.NewDoomLoop()
				guardDoom.Check(results[i].Response) // seed with original response
				retryCount := 0
				for results[i].Flagged && retryCount < guardrailRetries {
					retryCount++
					fmt.Printf("  [%d/%d] score=%d/10 ⚑ retrying (%d/%d): %s\n",
						i+1, len(results), score, retryCount, guardrailRetries, truncate(note, 60))
					retryPrompt := fmt.Sprintf(
						"Your previous output scored %d/10. Reviewer feedback: %s\n\nOriginal subtask: %s\n\nPlease revise your output to address the reviewer's feedback.",
						score, note, results[i].Subtask,
					)
					retryReq := &provider.ChatRequest{
						Model: results[i].Role,
						Messages: []provider.Message{
							{Role: provider.RoleSystem, Content: workerSystemPrompt},
							{Role: provider.RoleUser, Content: retryPrompt},
						},
					}
					resp, retryErr := router.ChatCompletion(ctx, retryReq)
					if retryErr != nil {
						fmt.Fprintf(os.Stderr, "  guardrail retry[%d]: %v\n", i+1, retryErr)
						break
					}
					results[i].Response = resp.Message.Content
					results[i].Tokens += resp.Usage.TotalTokens

					// Doom-loop detection: abort if worker produces identical output.
					if guardDoom.Check(results[i].Response) {
						fmt.Fprintf(os.Stderr, "  ⚠ worker[%d] doom loop: identical output after retry — aborting\n", i+1)
						decLog.Log(decision.Decision{
							Phase:   "guardrail",
							Agent:   results[i].Role,
							Intent:  "improve output via retry",
							Action:  "doom loop detected — aborted",
							Outcome: "failure",
							Detail:  "identical response after feedback retry",
						})
						break
					}

					// Re-score the revised output.
					score, note, scoreErr = reviewer.Score(ctx, results[i].Subtask, results[i].Response)
					if scoreErr != nil {
						fmt.Fprintf(os.Stderr, "  guardrail re-score[%d]: %v\n", i+1, scoreErr)
						break
					}
					results[i].ReviewScore = score
					results[i].ReviewNote = note
					results[i].Flagged = score > 0 && score < guardrailThreshold

					decLog.Log(decision.Decision{
						Phase:   "guardrail",
						Agent:   results[i].Role,
						Intent:  "improve output via retry",
						Action:  fmt.Sprintf("re-scored %d/10 after retry %d", score, retryCount),
						Outcome: map[bool]string{true: "still-flagged", false: "improved"}[results[i].Flagged],
						Detail:  truncate(note, 120),
					})
				}

				flag := "✓"
				if results[i].Flagged {
					flag = "⚑"
				}
				fmt.Printf("  [%d/%d] score=%d/10 %s %s\n", i+1, len(results), results[i].ReviewScore, flag, truncate(results[i].ReviewNote, 80))
			}
			pt.stop()
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
	pt.start("Phase 3 synthesize")
	stopSpin3 := startSpinner(spinLabelWithToks("  synthesizing", tracker))
	synthesis, err := mayor.Synthesize(ctx, task, results)
	stopSpin3()
	if err != nil {
		return fmt.Errorf("supervisor synthesize failed (during %s): %w", pt.currentPhase(), err)
	}
	pt.stop()

	// Phase 4: Tester polish (optional — skipped if --no-tester or role not configured).
	if !noTester {
		if _, ok := cfg.Roles["tester"]; ok {
			fmt.Printf("Phase 4: Tester polishing synthesized output...\n")
			pt.start("Phase 4 tester")
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
			pt.stop()
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
			buildDoom := pool.NewDoomLoop()
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

				// Doom-loop detection: abort if identical errors repeat.
				if buildDoom.Check(stderr) {
					fmt.Fprintf(os.Stderr, "  ⚠ build doom loop: identical errors after fix — aborting\n")
					decLog.Log(decision.Decision{
						Phase:   "build-fix",
						Agent:   "builder",
						Intent:  "fix build errors",
						Action:  "doom loop detected — aborted",
						Outcome: "failure",
						Detail:  "identical build errors after worker fix attempt",
					})
					break
				}

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

	// Phase timing summary.
	fmt.Printf("\n--- Phase Timing ---\n")
	fmt.Print(pt.summary())
	fmt.Printf("--------------------\n")

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
		return msg + "\n  hint: the request timed out — increase --timeout or use --no-reviewer/--no-tester to skip slow phases"
	case strings.Contains(msg, "x-api-key") || strings.Contains(msg, "authentication") || strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "unauthorized"):
		return msg + "\n  hint: check that your API key environment variable is exported in your shell"
	case strings.Contains(msg, "permission denied"):
		return msg + "\n  hint: check file/directory ownership and permissions"
	case strings.Contains(msg, "read-only file system"):
		return msg + "\n  hint: the target path is on a read-only mount — choose a writable directory"
	case strings.Contains(msg, "no space left on device"):
		return msg + "\n  hint: free disk space or choose a different output/log directory"
	case strings.Contains(msg, "disk quota exceeded"):
		return msg + "\n  hint: disk quota exceeded — free space or increase quota"
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

// writeOutputFile writes content to path/filename atomically (temp + rename).
func writeOutputFile(dir, filename, content string) error {
	fullPath := filepath.Join(dir, filename)
	return fileutil.AtomicWrite(fullPath, []byte(content), 0644)
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

// spinLabelWithToks returns a label function that appends a live token count and elapsed time.
func spinLabelWithToks(base string, tracker *cost.Tracker) func() string {
	start := time.Now()
	return func() string {
		elapsed := time.Since(start).Seconds()
		total := tracker.Summary().TotalTokens
		if total == 0 {
			return fmt.Sprintf("%s [%.0fs]", base, elapsed)
		}
		return fmt.Sprintf("%s [%s tok, %.0fs]", base, formatToks(total), elapsed)
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

// phaseTracker tracks elapsed time per phase and cumulative run time.
type phaseTracker struct {
	runStart   time.Time
	phaseStart time.Time
	phaseName  string
	phases     []phaseRecord
	mu         sync.Mutex
}

type phaseRecord struct {
	name    string
	elapsed time.Duration
}

func newPhaseTracker() *phaseTracker {
	now := time.Now()
	return &phaseTracker{runStart: now}
}

func (pt *phaseTracker) start(name string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.phaseName = name
	pt.phaseStart = time.Now()
}

func (pt *phaseTracker) stop() time.Duration {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	elapsed := time.Since(pt.phaseStart)
	if pt.phaseName != "" {
		pt.phases = append(pt.phases, phaseRecord{name: pt.phaseName, elapsed: elapsed})
	}
	cumulative := time.Since(pt.runStart)
	fmt.Printf("  done (%.1fs, cumulative %.1fs)\n", elapsed.Seconds(), cumulative.Seconds())
	return elapsed
}

func (pt *phaseTracker) currentPhase() string {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.phaseName
}

func (pt *phaseTracker) summary() string {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-20s %s\n", "PHASE", "ELAPSED"))
	sb.WriteString(fmt.Sprintf("%-20s %s\n", "-----", "-------"))
	for _, p := range pt.phases {
		sb.WriteString(fmt.Sprintf("%-20s %.1fs\n", p.name, p.elapsed.Seconds()))
	}
	total := time.Since(pt.runStart)
	sb.WriteString(fmt.Sprintf("%-20s %.1fs\n", "TOTAL", total.Seconds()))
	return sb.String()
}

// classifyFSError returns a user-friendly description of filesystem errors.
func classifyFSError(err error) string {
	if err == nil {
		return ""
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EACCES:
			return "permission denied — check directory ownership and permissions"
		case syscall.EROFS:
			return "read-only file system — the mount point does not allow writes"
		case syscall.ENOSPC:
			return "no space left on device — free disk space or choose a different path"
		case syscall.EDQUOT:
			return "disk quota exceeded — free space or increase quota"
		}
	}
	return err.Error()
}
