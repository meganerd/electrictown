package provider

import (
	"context"
	"fmt"
	"sync"
)

// ProviderFactory creates a Provider from a ProviderConfig.
// Each provider type registers a factory at init time.
type ProviderFactory func(cfg ProviderConfig) (Provider, error)

// Router routes chat requests to the appropriate provider based on config.
// It manages provider instances and handles model alias resolution.
type Router struct {
	config    *Config
	providers map[string]Provider // keyed by provider config name
	mu        sync.RWMutex
}

// NewRouter creates a router from config and a set of provider factories.
// The factories map provider type names (e.g., "openai") to their constructors.
func NewRouter(cfg *Config, factories map[string]ProviderFactory) (*Router, error) {
	r := &Router{
		config:    cfg,
		providers: make(map[string]Provider),
	}
	// Initialize all configured providers.
	for name, pc := range cfg.Providers {
		factory, ok := factories[pc.Type]
		if !ok {
			return nil, fmt.Errorf("router: unknown provider type %q for provider %q", pc.Type, name)
		}
		p, err := factory(pc)
		if err != nil {
			return nil, fmt.Errorf("router: initializing provider %q: %w", name, err)
		}
		r.providers[name] = p
	}
	return r, nil
}

// ChatCompletion routes a request to the appropriate provider based on the
// model field in the request. The model field can be a direct model name
// (prefixed with provider, e.g., "openai/gpt-4") or a model alias from config.
func (r *Router) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	p, model, err := r.resolve(req.Model)
	if err != nil {
		return nil, err
	}
	req.Model = model
	return p.ChatCompletion(ctx, req)
}

// StreamChatCompletion routes a streaming request to the appropriate provider.
func (r *Router) StreamChatCompletion(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	p, model, err := r.resolve(req.Model)
	if err != nil {
		return nil, err
	}
	req.Model = model
	return p.StreamChatCompletion(ctx, req)
}

// ChatCompletionForRole routes a request using the role's configured model.
func (r *Router) ChatCompletionForRole(ctx context.Context, role string, req *ChatRequest) (*ChatResponse, error) {
	pc, model, err := r.config.ResolveRole(role)
	if err != nil {
		return nil, err
	}
	p, err := r.providerFor(pc)
	if err != nil {
		return nil, err
	}
	req.Model = model
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return r.tryFallbacks(ctx, role, req, err)
	}
	return resp, nil
}

// StreamChatCompletionForRole routes a streaming request using the role's configured model.
func (r *Router) StreamChatCompletionForRole(ctx context.Context, role string, req *ChatRequest) (ChatStream, error) {
	pc, model, err := r.config.ResolveRole(role)
	if err != nil {
		return nil, err
	}
	p, err := r.providerFor(pc)
	if err != nil {
		return nil, err
	}
	req.Model = model
	return p.StreamChatCompletion(ctx, req)
}

// ListAllModels returns models from all configured providers.
func (r *Router) ListAllModels(ctx context.Context) ([]Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var all []Model
	for _, p := range r.providers {
		models, err := p.ListModels(ctx)
		if err != nil {
			continue // skip providers that fail to list
		}
		all = append(all, models...)
	}
	return all, nil
}

// resolve maps a model reference to a provider instance and actual model name.
func (r *Router) resolve(modelRef string) (Provider, string, error) {
	// First try as a config model alias.
	pc, model, err := r.config.ResolveModel(modelRef)
	if err == nil {
		p, err := r.providerFor(pc)
		if err != nil {
			return nil, "", err
		}
		return p, model, nil
	}
	// Not an alias — try as a direct "provider/model" reference.
	for name, p := range r.providers {
		if modelRef == name || len(modelRef) > len(name)+1 && modelRef[:len(name)+1] == name+"/" {
			actualModel := modelRef
			if len(modelRef) > len(name)+1 {
				actualModel = modelRef[len(name)+1:]
			}
			return p, actualModel, nil
		}
	}
	return nil, "", fmt.Errorf("router: cannot resolve model %q", modelRef)
}

// providerFor finds the provider instance matching a ProviderConfig.
func (r *Router) providerFor(pc ProviderConfig) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, cfg := range r.config.Providers {
		if cfg.Type == pc.Type && cfg.BaseURL == pc.BaseURL && cfg.APIKey == pc.APIKey {
			if p, ok := r.providers[name]; ok {
				return p, nil
			}
		}
	}
	return nil, fmt.Errorf("router: no provider instance matches config")
}

// tryFallbacks attempts fallback models for a role after the primary fails.
func (r *Router) tryFallbacks(ctx context.Context, role string, req *ChatRequest, primaryErr error) (*ChatResponse, error) {
	fallbacks := r.config.FallbacksForRole(role)
	if len(fallbacks) == 0 {
		return nil, primaryErr
	}

	errCode := ClassifyError(primaryErr)
	// Only fall back on retryable errors.
	switch errCode {
	case ErrRateLimit, ErrContextWindow, ErrServerError, ErrTimeout:
		// These are worth retrying with a different model.
	default:
		return nil, primaryErr
	}

	for _, fb := range fallbacks {
		pc, model, err := r.config.ResolveModel(fb)
		if err != nil {
			continue
		}
		p, err := r.providerFor(pc)
		if err != nil {
			continue
		}
		req.Model = model
		resp, err := p.ChatCompletion(ctx, req)
		if err == nil {
			return resp, nil
		}
	}
	return nil, fmt.Errorf("router: all fallbacks exhausted for role %q (primary error: %w)", role, primaryErr)
}
