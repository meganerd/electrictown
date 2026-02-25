// et-poc-parallel demonstrates electrictown's multi-worker parallel execution:
// a frontier model (supervisor) decomposes a task into N subtasks, then N worker
// goroutines execute those subtasks concurrently via StreamChatCompletion.
//
// Usage:
//
//	go run cmd/et-poc-parallel/main.go [config-path] [task-description]
//
// Examples:
//
//	go run cmd/et-poc-parallel/main.go electrictown.yaml "Write three Go utility functions"
//	go run cmd/et-poc-parallel/main.go  # uses defaults
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/provider/anthropic"
	"github.com/meganerd/electrictown/internal/provider/gemini"
	"github.com/meganerd/electrictown/internal/provider/ollama"
	"github.com/meganerd/electrictown/internal/provider/openai"
)

const defaultTask = "Write three small Go utility functions: (1) a function to check if a string is a palindrome, (2) a function to find the maximum value in an integer slice, (3) a function to count words in a string"

// workerResult holds the output (or error) from a single worker goroutine.
type workerResult struct {
	Index   int
	Subtask string
	Output  string
	Err     error
}

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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	fmt.Printf("\u26a1 electrictown Multi-Worker PoC\n")
	fmt.Printf("\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\u2501\n")
	fmt.Printf("\U0001f4cb Config: %s\n", configPath)
	fmt.Printf("\U0001f4cb Task:   %s\n\n", task)

	cfg, err := provider.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build provider factories — all four supported types.
	factories := map[string]provider.ProviderFactory{
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

	router, err := provider.NewRouter(cfg, factories)
	if err != nil {
		return fmt.Errorf("creating router: %w", err)
	}

	// ── Phase 1: Supervisor decomposes the task ──────────────────────────
	fmt.Printf("\U0001f9e0 Supervisor decomposing task into subtasks...\n")

	supervisorReq := &provider.ChatRequest{
		Messages: []provider.Message{
			{
				Role: provider.RoleSystem,
				Content: "You are a coding supervisor. Given a task, decompose it into individual subtasks — one per line. " +
					"Output ONLY the subtask list as a numbered list (e.g., \"1. ...\"), one subtask per line. " +
					"No preamble, no summary, no extra commentary. Each line must be a complete, self-contained task description " +
					"that a worker can implement independently.",
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

	subtasks := parseSubtasks(supervisorResp.Message.Content)
	if len(subtasks) == 0 {
		return fmt.Errorf("supervisor produced no parseable subtasks from response:\n%s", supervisorResp.Message.Content)
	}

	fmt.Printf("   Found %d subtasks\n\n", len(subtasks))
	for i, st := range subtasks {
		fmt.Printf("   %d. %s\n", i+1, truncate(st, 100))
	}
	fmt.Println()

	// ── Phase 2: Fan out workers in parallel ─────────────────────────────
	n := len(subtasks)
	fmt.Printf("\U0001f528 Dispatching %d workers in parallel...\n\n", n)

	results := make([]workerResult, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i, st := range subtasks {
		go func(idx int, subtask string) {
			defer wg.Done()
			results[idx] = executeWorker(ctx, router, idx, subtask)
		}(i, st)
	}

	wg.Wait()

	// ── Phase 3: Print results ───────────────────────────────────────────
	succeeded := 0
	failed := 0

	for _, r := range results {
		label := fmt.Sprintf("Worker %d", r.Index+1)
		summary := truncate(r.Subtask, 60)

		if r.Err != nil {
			failed++
			fmt.Printf("\u2501\u2501\u2501 %s: %s \u2501\u2501\u2501\n", label, summary)
			fmt.Printf("[ERROR] %v\n\n", r.Err)
		} else {
			succeeded++
			fmt.Printf("\u2501\u2501\u2501 %s: %s \u2501\u2501\u2501\n", label, summary)
			fmt.Println(r.Output)
			fmt.Println()
		}
	}

	fmt.Printf("\u2705 All %d workers complete (%d succeeded, %d failed)\n", n, succeeded, failed)
	return nil
}

// executeWorker sends a single subtask to a polecat-role worker via streaming
// and collects the full response.
func executeWorker(ctx context.Context, router *provider.Router, index int, subtask string) workerResult {
	req := &provider.ChatRequest{
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

	stream, err := router.StreamChatCompletionForRole(ctx, "polecat", req)
	if err != nil {
		return workerResult{Index: index, Subtask: subtask, Err: fmt.Errorf("stream request failed: %w", err)}
	}
	defer stream.Close()

	var buf strings.Builder
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return workerResult{Index: index, Subtask: subtask, Err: fmt.Errorf("stream read error: %w", err)}
		}
		if chunk.Delta.Content != "" {
			buf.WriteString(chunk.Delta.Content)
		}
	}

	return workerResult{
		Index:   index,
		Subtask: subtask,
		Output:  strings.TrimSpace(buf.String()),
	}
}

// parseSubtasks splits the supervisor's response into individual subtask strings.
// It handles numbered lists (1. / 1) / 1: ), bullet lists (- / * ), and plain lines.
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

		// Strip numbering prefixes: "1.", "1)", "1:"
		cleaned := numberPrefix.ReplaceAllString(trimmed, "")
		// Strip bullet prefixes: "- ", "* "
		cleaned = bulletPrefix.ReplaceAllString(cleaned, "")
		cleaned = strings.TrimSpace(cleaned)

		if cleaned == "" {
			continue
		}

		// Skip lines that look like headers or meta-commentary rather than tasks.
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
