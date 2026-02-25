// et-poc demonstrates electrictown's core concept: a frontier model (supervisor)
// decomposes a coding task and routes subtasks to worker models via the provider
// router. All model assignments come from electrictown.yaml — no hardcoded models.
//
// Usage:
//
//	go run cmd/et-poc/main.go [config-path] [task-description]
//
// Examples:
//
//	go run cmd/et-poc/main.go electrictown.yaml "Write a Go function to reverse a string"
//	go run cmd/et-poc/main.go  # uses defaults
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/provider/anthropic"
	"github.com/meganerd/electrictown/internal/provider/ollama"
	"github.com/meganerd/electrictown/internal/provider/openai"
)

const defaultTask = "Write a Go function called ReverseString that takes a string and returns it reversed, handling UTF-8 correctly. Include a brief doc comment."

func main() {
	configPath := "electrictown.yaml"
	task := defaultTask

	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}
	if len(os.Args) > 2 {
		task = strings.Join(os.Args[2:], " ")
	}

	if err := run(configPath, task); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath, task string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Load config.
	fmt.Printf("⚡ electrictown PoC\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("📋 Config: %s\n", configPath)
	fmt.Printf("📋 Task:   %s\n\n", task)

	cfg, err := provider.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build provider factories.
	factories := map[string]provider.ProviderFactory{
		"openai": func(pc provider.ProviderConfig) (provider.Provider, error) {
			opts := []openai.Option{}
			if pc.BaseURL != "" {
				opts = append(opts, openai.WithBaseURL(pc.BaseURL))
			}
			return openai.New(pc.APIKey, opts...), nil
		},
		"anthropic": func(pc provider.ProviderConfig) (provider.Provider, error) {
			opts := []anthropic.Option{}
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
	}

	router, err := provider.NewRouter(cfg, factories)
	if err != nil {
		return fmt.Errorf("creating router: %w", err)
	}

	// Phase 1: Supervisor (mayor role) decomposes the task.
	fmt.Printf("🧠 Phase 1: Supervisor analyzing task...\n")
	fmt.Printf("   Role: mayor → ")

	supervisorReq := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role:    provider.RoleSystem,
				Content: "You are a coding supervisor. Given a task, produce a clear, concise implementation plan with exactly ONE subtask for a worker to implement. Output ONLY the subtask description — no preamble, no numbering, just the task description the worker needs.",
			},
			{
				Role:    provider.RoleUser,
				Content: task,
			},
		},
	}

	supervisorResp, err := router.ChatCompletionForRole(ctx, "mayor", supervisorReq)
	if err != nil {
		return fmt.Errorf("supervisor request failed: %w", err)
	}

	subtask := strings.TrimSpace(supervisorResp.Message.Content)
	fmt.Printf("model=%s (%d tokens)\n", supervisorResp.Model, supervisorResp.Usage.TotalTokens)
	fmt.Printf("   Subtask: %s\n\n", truncate(subtask, 120))

	// Phase 2: Worker (polecat role) executes the subtask with streaming.
	fmt.Printf("🔨 Phase 2: Worker executing subtask (streaming)...\n")
	fmt.Printf("   Role: polecat → ")

	workerReq := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role:    provider.RoleSystem,
				Content: "You are a coding worker. Implement exactly what is asked. Output ONLY the code — no explanations, no markdown fences unless specifically requested.",
			},
			{
				Role:    provider.RoleUser,
				Content: subtask,
			},
		},
	}

	stream, err := router.StreamChatCompletionForRole(ctx, "polecat", workerReq)
	if err != nil {
		return fmt.Errorf("worker stream request failed: %w", err)
	}
	defer stream.Close()

	// Print model info from first chunk, then stream content.
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
			fmt.Printf("model=%s\n\n", chunk.Model)
			fmt.Printf("━━━ Worker Output ━━━\n")
			firstChunk = false
		}

		if chunk.Delta.Content != "" {
			fmt.Print(chunk.Delta.Content)
			totalContent.WriteString(chunk.Delta.Content)
		}

		if chunk.Done && chunk.Usage != nil {
			fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━\n")
			fmt.Printf("   Worker tokens: %d\n", chunk.Usage.TotalTokens)
		}
	}

	if firstChunk {
		// Never got model info — print what we have.
		fmt.Printf("(streaming)\n\n━━━ Worker Output ━━━\n")
		fmt.Print(totalContent.String())
		fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━\n")
	}

	fmt.Printf("\n✅ PoC complete: supervisor→worker round-trip via provider router\n")
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
