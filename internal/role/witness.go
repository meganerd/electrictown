package role

import (
	"context"
	"fmt"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

const defaultReviewerRole = "reviewer"

const defaultWitnessSystemPrompt = "You are a code reviewer. Analyze the provided code for correctness, " +
	"security issues, performance problems, and adherence to best practices. " +
	"Provide specific, actionable feedback organized by severity."

// Witness represents a code reviewer/validator agent that reviews work output
// for correctness, security issues, and quality. It is provider-agnostic --
// it uses the router to talk to whatever model is configured for the "reviewer"
// role (or any custom reviewer role).
type Reviewer struct {
	router       *provider.Router
	tracker      *cost.Tracker // optional, nil-safe
	role         string        // role name, defaults to "reviewer"
	systemPrompt string        // configurable system prompt
}

// WitnessOption configures a Witness during construction.
type WitnessOption func(*Reviewer)

// WithReviewerRole sets a custom role name for the witness reviewer.
// The role name determines which model config is used via the router.
func WithReviewerRole(name string) WitnessOption {
	return func(w *Reviewer) {
		w.role = name
	}
}

// WithWitnessSystemPrompt overrides the default system prompt.
func WithWitnessSystemPrompt(prompt string) WitnessOption {
	return func(w *Reviewer) {
		w.systemPrompt = prompt
	}
}

// WithWitnessCostTracker attaches a cost tracker for recording token usage.
func WithWitnessCostTracker(t *cost.Tracker) WitnessOption {
	return func(w *Reviewer) {
		w.tracker = t
	}
}

// NewReviewer creates a witness reviewer with the given router and options.
func NewReviewer(router *provider.Router, opts ...WitnessOption) *Reviewer {
	w := &Reviewer{
		router:       router,
		role:         defaultReviewerRole,
		systemPrompt: defaultWitnessSystemPrompt,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// SystemPrompt returns the current system prompt.
func (w *Reviewer) SystemPrompt() string {
	return w.systemPrompt
}

// Role returns the witness's configured role name.
func (w *Reviewer) Role() string {
	return w.role
}

// Review sends code to the reviewer's configured model for analysis and returns
// the full review response. The system prompt is automatically prepended.
func (w *Reviewer) Review(ctx context.Context, code string) (*provider.ChatResponse, error) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: w.systemPrompt},
		{Role: provider.RoleUser, Content: fmt.Sprintf("Review the following code:\n\n%s", code)},
	}

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := w.router.ChatCompletionForRole(ctx, w.role, req)
	if err != nil {
		return nil, err
	}

	w.recordCost(resp)
	return resp, nil
}

// ReviewWithContext reviews code with the original task context, allowing the
// reviewer to assess whether the implementation correctly addresses the task.
func (w *Reviewer) ReviewWithContext(ctx context.Context, task string, code string) (*provider.ChatResponse, error) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: w.systemPrompt},
		{Role: provider.RoleUser, Content: fmt.Sprintf("Original task:\n%s\n\nCode to review:\n%s", task, code)},
	}

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := w.router.ChatCompletionForRole(ctx, w.role, req)
	if err != nil {
		return nil, err
	}

	w.recordCost(resp)
	return resp, nil
}

// Validate checks output against acceptance criteria, determining whether the
// output meets the specified requirements.
func (w *Reviewer) Validate(ctx context.Context, criteria string, output string) (*provider.ChatResponse, error) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: w.systemPrompt},
		{Role: provider.RoleUser, Content: fmt.Sprintf("Acceptance criteria:\n%s\n\nOutput to validate:\n%s", criteria, output)},
	}

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := w.router.ChatCompletionForRole(ctx, w.role, req)
	if err != nil {
		return nil, err
	}

	w.recordCost(resp)
	return resp, nil
}

// recordCost records token usage if a cost tracker is attached.
// Safe to call when tracker is nil.
func (w *Reviewer) recordCost(resp *provider.ChatResponse) {
	if w.tracker == nil || resp == nil {
		return
	}
	w.tracker.Record(
		"", // provider name not available from response directly
		resp.Model,
		w.role,
		cost.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	)
}
