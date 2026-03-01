// Package pool provides a parallel worker pool that dispatches subtasks
// across multiple model aliases using the provider Router and Balancer.
package pool

import (
	"context"
	"fmt"
	"sync"

	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/role"
)

// WorkerPool dispatches subtasks concurrently across a pool of model aliases.
// It uses a Balancer for round-robin assignment and the Router for request routing.
type WorkerPool struct {
	router   *provider.Router
	balancer *provider.Balancer
	aliases  []string // pool model aliases
}

// New creates a WorkerPool with the given router, balancer, and pool model aliases.
func New(router *provider.Router, balancer *provider.Balancer, aliases []string) *WorkerPool {
	return &WorkerPool{
		router:   router,
		balancer: balancer,
		aliases:  aliases,
	}
}

// ExecuteAll dispatches subtasks concurrently across pool members. Each subtask
// is assigned a model alias via the Balancer (round-robin). Concurrency is bounded
// to min(len(subtasks), len(aliases)) goroutines. Results are returned in subtask
// order. Per-worker errors do not abort other workers — failed subtasks are reported
// in the result with a non-empty Error field.
func (wp *WorkerPool) ExecuteAll(ctx context.Context, subtasks []string, systemPrompt string) []role.WorkerResult {
	n := len(subtasks)
	results := make([]role.WorkerResult, n)

	// Bounded concurrency: min(subtasks, pool size).
	maxConcurrency := len(wp.aliases)
	if n < maxConcurrency {
		maxConcurrency = n
	}
	sem := make(chan struct{}, maxConcurrency)

	var wg sync.WaitGroup
	for i, subtask := range subtasks {
		wg.Add(1)
		go func(idx int, task string) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			alias := wp.balancer.Select("pool", wp.aliases)

			req := &provider.ChatRequest{
				Model: alias,
				Messages: []provider.Message{
					{Role: provider.RoleSystem, Content: systemPrompt},
					{Role: provider.RoleUser, Content: task},
				},
			}

			resp, err := wp.router.ChatCompletion(ctx, req)
			if err != nil {
				// Retry once on transient failure.
				resp, err = wp.router.ChatCompletion(ctx, req)
			}

			result := role.WorkerResult{
				Role:    alias,
				Subtask: task,
			}
			if err != nil {
				result.Response = fmt.Sprintf("error: %v", err)
			} else {
				result.Response = resp.Message.Content
				result.Tokens = resp.Usage.TotalTokens
			}

			results[idx] = result
		}(i, subtask)
	}

	wg.Wait()
	return results
}
