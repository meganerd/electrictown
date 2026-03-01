package role

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

// Score evaluates a worker's output and returns a quality score (1-10) and brief note.
// It asks the reviewer model to respond with SCORE: N and REASON: text lines.
// Returns score=0 on parse failure.
func (w *Reviewer) Score(ctx context.Context, subtask, response string) (score int, note string, err error) {
	prompt := fmt.Sprintf(
		"You are a code quality reviewer. Score this worker output for a coding subtask.\n\n"+
			"Subtask:\n%s\n\nOutput:\n%s\n\n"+
			"Respond ONLY with exactly two lines:\nSCORE: N\nREASON: one-line explanation\n"+
			"(N is 1-10; 1=completely wrong, 10=perfect)",
		subtask, response,
	)
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "You are a concise code quality reviewer. Output only SCORE: N and REASON: text."},
		{Role: provider.RoleUser, Content: prompt},
	}
	req := &provider.ChatRequest{Messages: messages}
	resp, callErr := w.router.ChatCompletionForRole(ctx, w.role, req)
	if callErr != nil {
		return 0, "", callErr
	}
	w.recordCost(resp)
	score, note = parseScoreResponse(resp.Message.Content)
	return score, note, nil
}

// parseScoreResponse extracts SCORE and REASON from a reviewer response.
func parseScoreResponse(text string) (int, string) {
	var score int
	var note string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SCORE:") {
			s := strings.TrimSpace(strings.TrimPrefix(line, "SCORE:"))
			// Strip "/10" suffix if present.
			s = strings.TrimSuffix(s, "/10")
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				score = n
			}
		} else if strings.HasPrefix(line, "REASON:") {
			note = strings.TrimSpace(strings.TrimPrefix(line, "REASON:"))
		}
	}
	return score, note
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
