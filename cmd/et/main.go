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
	"strings"
	"time"

	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/provider/anthropic"
	"github.com/meganerd/electrictown/internal/provider/gemini"
	"github.com/meganerd/electrictown/internal/provider/ollama"
	"github.com/meganerd/electrictown/internal/provider/openai"
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
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "models":
		if err := cmdModels(os.Args[2:]); err != nil {
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
  et models [--config path]
  et version

Commands:
  run      Execute supervisor→worker flow for a task
  models   List all available models from configured providers
  version  Print version information

Flags (run):
  --config   Path to config file (default: electrictown.yaml)
  --role     Supervisor role name (default: mayor; worker always uses polecat)

Flags (models):
  --config   Path to config file (default: electrictown.yaml)
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
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "electrictown.yaml", "path to config file")
	supervisorRole := fs.String("role", "mayor", "supervisor role name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	task := strings.Join(fs.Args(), " ")
	if task == "" {
		return fmt.Errorf("task description required\n\nUsage: et run [--config path] [--role name] \"task description\"")
	}

	workerRole := "polecat"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Load config and create router.
	cfg, err := provider.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	router, err := provider.NewRouter(cfg, buildFactories())
	if err != nil {
		return fmt.Errorf("creating router: %w", err)
	}

	fmt.Printf("electrictown\n")
	fmt.Printf("============\n")
	fmt.Printf("Config: %s\n", *configPath)
	fmt.Printf("Task:   %s\n\n", task)

	// Phase 1: Supervisor generates subtask via ChatCompletion.
	fmt.Printf("Phase 1: Supervisor (%s) analyzing task...\n", *supervisorRole)

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

	supervisorResp, err := router.ChatCompletionForRole(ctx, *supervisorRole, supervisorReq)
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
				Content: "You are a coding worker. Implement exactly what is asked. Output ONLY the code -- no explanations, no markdown fences unless specifically requested.",
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

	// Usage summary.
	fmt.Printf("\nDone: supervisor→worker round-trip complete\n")
	fmt.Printf("  Supervisor tokens: %d\n", supervisorResp.Usage.TotalTokens)
	return nil
}

// cmdModels implements the "et models" subcommand.
func cmdModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	configPath := fs.String("config", "electrictown.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := provider.LoadConfig(*configPath)
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
