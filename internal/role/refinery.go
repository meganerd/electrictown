package role

import (
	"context"
	"fmt"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

const defaultRefineryRole = "refinery"

const defaultRefinerySystemPrompt = "You are a code refinery agent. Take the provided code or content " +
	"and improve its quality: fix bugs, improve naming, add error handling where missing, optimize " +
	"performance, and ensure consistent style. Return the refined version with brief comments " +
	"explaining significant changes."

// Refinery represents an output polisher/synthesizer agent that takes raw
// output and refines it -- improving code quality, documentation, formatting,
// and coherence. It is provider-agnostic and uses the router to talk to
// whatever model is configured for the "refinery" role.
type Refinery struct {
	router       *provider.Router
	tracker      *cost.Tracker // optional, nil-safe
	role         string        // role name, defaults to "refinery"
	systemPrompt string        // configurable system prompt
}

// RefineryOption configures a Refinery during construction.
type RefineryOption func(*Refinery)

// WithRefineryRole sets a custom role name for the refinery agent.
// The role name determines which model config is used via the router.
func WithRefineryRole(name string) RefineryOption {
	return func(r *Refinery) {
		r.role = name
	}
}

// WithRefinerySystemPrompt overrides the default system prompt.
func WithRefinerySystemPrompt(prompt string) RefineryOption {
	return func(r *Refinery) {
		r.systemPrompt = prompt
	}
}

// WithRefineryCostTracker attaches a cost tracker for recording token usage.
func WithRefineryCostTracker(t *cost.Tracker) RefineryOption {
	return func(r *Refinery) {
		r.tracker = t
	}
}

// NewRefinery creates a refinery agent with the given router and options.
func NewRefinery(router *provider.Router, opts ...RefineryOption) *Refinery {
	r := &Refinery{
		router:       router,
		role:         defaultRefineryRole,
		systemPrompt: defaultRefinerySystemPrompt,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// SystemPrompt returns the current system prompt.
func (r *Refinery) SystemPrompt() string {
	return r.systemPrompt
}

// Role returns the refinery's configured role name.
func (r *Refinery) Role() string {
	return r.role
}

// Refine sends input content to the refinery model for quality improvement.
// The system prompt is automatically prepended.
func (r *Refinery) Refine(ctx context.Context, input string) (*provider.ChatResponse, error) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: r.systemPrompt},
		{Role: provider.RoleUser, Content: input},
	}

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := r.router.ChatCompletionForRole(ctx, r.role, req)
	if err != nil {
		return nil, err
	}

	r.recordCost(resp)
	return resp, nil
}

// RefineWithFeedback sends input content along with specific improvement
// instructions to the refinery model. Both the original content and the
// feedback are included in the user message.
func (r *Refinery) RefineWithFeedback(ctx context.Context, input string, feedback string) (*provider.ChatResponse, error) {
	userContent := fmt.Sprintf("Content to refine:\n\n%s\n\nImprovement instructions:\n\n%s", input, feedback)

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: r.systemPrompt},
		{Role: provider.RoleUser, Content: userContent},
	}

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := r.router.ChatCompletionForRole(ctx, r.role, req)
	if err != nil {
		return nil, err
	}

	r.recordCost(resp)
	return resp, nil
}

// Summarize sends verbose content to the refinery model and produces a
// concise summary. Uses a summarization-specific user prompt.
func (r *Refinery) Summarize(ctx context.Context, content string) (*provider.ChatResponse, error) {
	userContent := fmt.Sprintf("Produce a concise summary of the following content:\n\n%s", content)

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: r.systemPrompt},
		{Role: provider.RoleUser, Content: userContent},
	}

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := r.router.ChatCompletionForRole(ctx, r.role, req)
	if err != nil {
		return nil, err
	}

	r.recordCost(resp)
	return resp, nil
}

// recordCost records token usage if a cost tracker is attached.
// Safe to call when tracker is nil.
func (r *Refinery) recordCost(resp *provider.ChatResponse) {
	if r.tracker == nil || resp == nil {
		return
	}
	r.tracker.Record(
		"", // provider name not available from response directly
		resp.Model,
		r.role,
		cost.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	)
}
