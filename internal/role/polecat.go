// Package role defines agent roles for the electrictown orchestrator.
// Each role (mayor, polecat, crew, etc.) encapsulates behavior for a specific
// agent type, using the provider router for model-agnostic LLM access.
package role

import (
	"context"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

const defaultPolecatRole = "polecat"

const defaultSystemPrompt = "You are a coding worker. Implement exactly what is asked. " +
	"Output ONLY the code -- no explanations, no markdown fences unless requested."

// Polecat represents a worker agent that executes coding tasks.
// It is provider-agnostic -- it uses the router to talk to whatever
// model is configured for the "polecat" role (or any custom worker role).
type Polecat struct {
	router       *provider.Router
	tracker      *cost.Tracker // optional, nil-safe
	role         string        // role name, defaults to "polecat"
	systemPrompt string        // configurable system prompt
}

// Option configures a Polecat during construction.
type Option func(*Polecat)

// WithRole sets a custom role name for the polecat worker.
// The role name determines which model config is used via the router.
func WithRole(name string) Option {
	return func(p *Polecat) {
		p.role = name
	}
}

// WithSystemPrompt overrides the default system prompt.
func WithSystemPrompt(prompt string) Option {
	return func(p *Polecat) {
		p.systemPrompt = prompt
	}
}

// WithCostTracker attaches a cost tracker for recording token usage.
func WithCostTracker(t *cost.Tracker) Option {
	return func(p *Polecat) {
		p.tracker = t
	}
}

// NewPolecat creates a polecat worker with the given router and options.
func NewPolecat(router *provider.Router, opts ...Option) *Polecat {
	p := &Polecat{
		router:       router,
		role:         defaultPolecatRole,
		systemPrompt: defaultSystemPrompt,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SystemPrompt returns the current system prompt.
func (p *Polecat) SystemPrompt() string {
	return p.systemPrompt
}

// Role returns the polecat's configured role name.
func (p *Polecat) Role() string {
	return p.role
}

// Execute sends a task string to the worker's configured model and returns
// the full response. The system prompt is automatically prepended. Uses
// ChatCompletionForRole which handles fallbacks automatically.
func (p *Polecat) Execute(ctx context.Context, task string) (*provider.ChatResponse, error) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: p.systemPrompt},
		{Role: provider.RoleUser, Content: task},
	}

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := p.router.ChatCompletionForRole(ctx, p.role, req)
	if err != nil {
		return nil, err
	}

	p.recordCost(resp)
	return resp, nil
}

// ExecuteStream sends a task and returns a streaming response. The system
// prompt is automatically prepended. Uses StreamChatCompletionForRole which
// handles fallbacks automatically.
func (p *Polecat) ExecuteStream(ctx context.Context, task string) (provider.ChatStream, error) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: p.systemPrompt},
		{Role: provider.RoleUser, Content: task},
	}

	req := &provider.ChatRequest{
		Messages: messages,
		Stream:   true,
	}

	return p.router.StreamChatCompletionForRole(ctx, p.role, req)
}

// ExecuteWithContext allows passing conversation context (previous messages)
// for multi-turn worker sessions. The system prompt is automatically prepended
// before the provided history.
func (p *Polecat) ExecuteWithContext(ctx context.Context, history []provider.Message) (*provider.ChatResponse, error) {
	messages := make([]provider.Message, 0, len(history)+1)
	messages = append(messages, provider.Message{
		Role:    provider.RoleSystem,
		Content: p.systemPrompt,
	})
	messages = append(messages, history...)

	req := &provider.ChatRequest{
		Messages: messages,
	}

	resp, err := p.router.ChatCompletionForRole(ctx, p.role, req)
	if err != nil {
		return nil, err
	}

	p.recordCost(resp)
	return resp, nil
}

// recordCost records token usage if a cost tracker is attached.
// Safe to call when tracker is nil.
func (p *Polecat) recordCost(resp *provider.ChatResponse) {
	if p.tracker == nil || resp == nil {
		return
	}
	p.tracker.Record(
		"", // provider name not available from response directly
		resp.Model,
		p.role,
		cost.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	)
}
