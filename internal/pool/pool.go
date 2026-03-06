// Package pool provides a parallel worker pool that dispatches subtasks
// across multiple model aliases using the provider Router and Balancer.
package pool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/role"
)

// WorkerPool dispatches subtasks concurrently across a pool of model aliases.
// It uses a Balancer for round-robin assignment and the Router for request routing.
type WorkerPool struct {
	router     *provider.Router
	balancer   *provider.Balancer
	aliases    []string                           // pool model aliases
	onComplete func(idx int, r role.WorkerResult) // optional per-worker completion hook
}

// New creates a WorkerPool with the given router, balancer, and pool model aliases.
func New(router *provider.Router, balancer *provider.Balancer, aliases []string) *WorkerPool {
	return &WorkerPool{
		router:   router,
		balancer: balancer,
		aliases:  aliases,
	}
}

// SetProgressHook registers a callback invoked when each worker finishes.
// The callback receives the subtask index and the completed WorkerResult.
// Safe to call concurrently — the caller is responsible for synchronizing any
// shared state accessed inside the hook.
func (wp *WorkerPool) SetProgressHook(fn func(idx int, r role.WorkerResult)) {
	wp.onComplete = fn
}

// ExecuteDAG dispatches subtasks respecting dependency ordering. Tasks are
// grouped into execution waves via topological sort — each wave runs in
// parallel, and completed task outputs are injected into dependent tasks'
// prompts as context. Falls back to ExecuteAll behavior when deps is empty.
func (wp *WorkerPool) ExecuteDAG(ctx context.Context, subtasks []string, deps map[int][]int, systemPrompt string) ([]role.WorkerResult, error) {
	if !HasDependencies(deps) {
		return wp.ExecuteAll(ctx, subtasks, systemPrompt), nil
	}

	waves, err := TopoSort(len(subtasks), deps)
	if err != nil {
		return nil, err
	}

	results := make([]role.WorkerResult, len(subtasks))
	for _, wave := range waves {
		// Build prompts for this wave, injecting completed dependency outputs.
		waveSubtasks := make([]string, len(wave))
		waveIndices := make([]int, len(wave))
		for i, taskIdx := range wave {
			prompt := StripDepMarkers(subtasks[taskIdx])
			// Prepend outputs from dependencies as context.
			if depList, ok := deps[taskIdx]; ok && len(depList) > 0 {
				var ctx strings.Builder
				ctx.WriteString("## Context from completed subtasks\n\n")
				for _, depIdx := range depList {
					if results[depIdx].Response != "" {
						fmt.Fprintf(&ctx, "--- Subtask %d output ---\n%s\n\n", depIdx+1, results[depIdx].Response)
					}
				}
				prompt = ctx.String() + "---\n\n" + prompt
			}
			waveSubtasks[i] = prompt
			waveIndices[i] = taskIdx
		}

		// Execute this wave in parallel.
		waveResults := wp.ExecuteAll(ctx, waveSubtasks, systemPrompt)
		for i, r := range waveResults {
			r.Subtask = subtasks[waveIndices[i]] // preserve original subtask text
			results[waveIndices[i]] = r
		}
	}
	return results, nil
}

// ExecuteDAGWithModels dispatches subtasks respecting dependency ordering, using
// per-subtask model overrides and optional fallback chains. When models[i] is
// non-empty, it overrides the balancer selection for subtask i. When fallbacks[i]
// is non-nil, those aliases are tried in order if the primary model fails.
// Falls back to ExecuteDAG behavior when models is nil.
func (wp *WorkerPool) ExecuteDAGWithModels(ctx context.Context, subtasks []string, deps map[int][]int, models []string, fallbacks [][]string, systemPrompt string) ([]role.WorkerResult, error) {
	if !HasDependencies(deps) {
		return wp.ExecuteAllWithModels(ctx, subtasks, models, fallbacks, systemPrompt), nil
	}

	waves, err := TopoSort(len(subtasks), deps)
	if err != nil {
		return nil, err
	}

	results := make([]role.WorkerResult, len(subtasks))
	for _, wave := range waves {
		waveSubtasks := make([]string, len(wave))
		waveModels := make([]string, len(wave))
		waveFallbacks := make([][]string, len(wave))
		waveIndices := make([]int, len(wave))
		for i, taskIdx := range wave {
			prompt := StripDepMarkers(subtasks[taskIdx])
			if depList, ok := deps[taskIdx]; ok && len(depList) > 0 {
				var ctx strings.Builder
				ctx.WriteString("## Context from completed subtasks\n\n")
				for _, depIdx := range depList {
					if results[depIdx].Response != "" {
						fmt.Fprintf(&ctx, "--- Subtask %d output ---\n%s\n\n", depIdx+1, results[depIdx].Response)
					}
				}
				prompt = ctx.String() + "---\n\n" + prompt
			}
			waveSubtasks[i] = prompt
			waveIndices[i] = taskIdx
			if models != nil && taskIdx < len(models) {
				waveModels[i] = models[taskIdx]
			}
			if fallbacks != nil && taskIdx < len(fallbacks) {
				waveFallbacks[i] = fallbacks[taskIdx]
			}
		}

		waveResults := wp.ExecuteAllWithModels(ctx, waveSubtasks, waveModels, waveFallbacks, systemPrompt)
		for i, r := range waveResults {
			r.Subtask = subtasks[waveIndices[i]]
			results[waveIndices[i]] = r
		}
	}
	return results, nil
}

// ExecuteAllWithModels dispatches subtasks concurrently, using per-subtask model
// overrides and optional fallback chains. When models[i] is non-empty, it is
// used instead of the balancer selection. When fallbacks[i] is non-nil, those
// aliases are tried in order if the primary model fails. When models is nil or
// models[i] is empty, falls back to the pool balancer. This enables specialist
// routing where different subtasks use different models with resilient fallbacks.
func (wp *WorkerPool) ExecuteAllWithModels(ctx context.Context, subtasks []string, models []string, fallbacks [][]string, systemPrompt string) []role.WorkerResult {
	n := len(subtasks)
	results := make([]role.WorkerResult, n)

	maxConcurrency := len(wp.aliases)
	if n < maxConcurrency {
		maxConcurrency = n
	}
	if maxConcurrency == 0 {
		maxConcurrency = 1
	}
	sem := make(chan struct{}, maxConcurrency)

	var wg sync.WaitGroup
	for i, subtask := range subtasks {
		wg.Add(1)
		go func(idx int, task string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Use per-subtask model override if provided, otherwise balancer.
			alias := ""
			if models != nil && idx < len(models) && models[idx] != "" {
				alias = models[idx]
			} else {
				alias = wp.balancer.Select("pool", wp.aliases)
			}

			req := &provider.ChatRequest{
				Model: alias,
				Messages: []provider.Message{
					{Role: provider.RoleSystem, Content: systemPrompt},
					{Role: provider.RoleUser, Content: task},
				},
			}

			// Use fallback-aware routing when fallbacks are configured for this subtask.
			var fb []string
			if fallbacks != nil && idx < len(fallbacks) {
				fb = fallbacks[idx]
			}

			start := time.Now()
			var resp *provider.ChatResponse
			var err error
			if len(fb) > 0 {
				resp, err = wp.router.ChatCompletionWithFallbacks(ctx, req, fb)
			} else {
				resp, err = wp.router.ChatCompletion(ctx, req)
				if err != nil {
					// Retry once on transient failure (existing behavior).
					resp, err = wp.router.ChatCompletion(ctx, req)
				}
			}
			elapsed := time.Since(start)

			result := role.WorkerResult{
				Role:    alias,
				Subtask: task,
				Elapsed: elapsed,
			}
			if err != nil {
				result.Response = fmt.Sprintf("error: %v", err)
				provider.DumpFailedRequest(alias, req.Messages, err)
			} else {
				result.Response = resp.Message.Content
				result.Tokens = resp.Usage.TotalTokens
			}

			results[idx] = result

			if wp.onComplete != nil {
				wp.onComplete(idx, result)
			}
		}(i, subtask)
	}

	wg.Wait()
	return results
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

			start := time.Now()
			resp, err := wp.router.ChatCompletion(ctx, req)
			if err != nil {
				// Retry once on transient failure.
				resp, err = wp.router.ChatCompletion(ctx, req)
			}
			elapsed := time.Since(start)

			result := role.WorkerResult{
				Role:    alias,
				Subtask: task,
				Elapsed: elapsed,
			}
			if err != nil {
				result.Response = fmt.Sprintf("error: %v", err)
				// Dump failed request for offline debugging.
				provider.DumpFailedRequest(alias, req.Messages, err)
			} else {
				result.Response = resp.Message.Content
				result.Tokens = resp.Usage.TotalTokens
			}

			results[idx] = result

			if wp.onComplete != nil {
				wp.onComplete(idx, result)
			}
		}(i, subtask)
	}

	wg.Wait()
	return results
}
