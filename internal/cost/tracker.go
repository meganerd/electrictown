// Package cost provides LLM request cost tracking and aggregation.
// It calculates estimated costs based on token usage and configurable
// per-model pricing, and provides summary breakdowns by provider, model,
// and role.
package cost

import (
	"sync"
	"time"
)

// ModelPricing defines cost per 1M tokens for a model.
type ModelPricing struct {
	PromptCostPer1M     float64 // cost per 1M prompt/input tokens
	CompletionCostPer1M float64 // cost per 1M completion/output tokens
}

// Usage mirrors provider.Usage for decoupling.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// RequestRecord captures the cost of a single LLM request.
type RequestRecord struct {
	Timestamp        time.Time
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	EstimatedCost    float64 // in USD
	Role             string  // which role made this request
}

// Summary provides aggregate cost stats.
type Summary struct {
	TotalRequests         int
	TotalTokens           int
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalCost             float64
	ByProvider            map[string]*ProviderSummary
	ByModel               map[string]*ModelSummary
	ByRole                map[string]*RoleSummary
}

// ProviderSummary aggregates stats for a single provider.
type ProviderSummary struct {
	Requests int
	Tokens   int
	Cost     float64
}

// ModelSummary aggregates stats for a single model.
type ModelSummary struct {
	Requests int
	Tokens   int
	Cost     float64
}

// RoleSummary aggregates stats for a single role.
type RoleSummary struct {
	Requests int
	Tokens   int
	Cost     float64
}

// Tracker records LLM request costs and provides aggregated summaries.
// It is safe for concurrent use.
type Tracker struct {
	pricing map[string]ModelPricing // keyed by model name
	records []RequestRecord
	mu      sync.RWMutex
}

// NewTracker creates a Tracker with the given per-model pricing.
func NewTracker(pricing map[string]ModelPricing) *Tracker {
	p := make(map[string]ModelPricing, len(pricing))
	for k, v := range pricing {
		p[k] = v
	}
	return &Tracker{
		pricing: p,
	}
}

// Record creates a RequestRecord from the given usage data, calculates cost,
// stores it, and returns the record. If the model has no configured pricing,
// EstimatedCost is 0.0.
func (t *Tracker) Record(provider, model, role string, usage Usage) *RequestRecord {
	var estimatedCost float64
	if p, ok := t.pricing[model]; ok {
		estimatedCost = (float64(usage.PromptTokens)/1_000_000)*p.PromptCostPer1M +
			(float64(usage.CompletionTokens)/1_000_000)*p.CompletionCostPer1M
	}

	rec := RequestRecord{
		Timestamp:        time.Now(),
		Provider:         provider,
		Model:            model,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		EstimatedCost:    estimatedCost,
		Role:             role,
	}

	t.mu.Lock()
	t.records = append(t.records, rec)
	t.mu.Unlock()

	return &rec
}

// Summary returns an aggregated summary across all recorded requests.
func (t *Tracker) Summary() *Summary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return buildSummary(t.records)
}

// SummaryForRole returns an aggregated summary filtered to a single role.
func (t *Tracker) SummaryForRole(role string) *Summary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	filtered := make([]RequestRecord, 0, len(t.records))
	for _, r := range t.records {
		if r.Role == role {
			filtered = append(filtered, r)
		}
	}
	return buildSummary(filtered)
}

// Records returns a copy of all recorded requests.
func (t *Tracker) Records() []RequestRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]RequestRecord, len(t.records))
	copy(out, t.records)
	return out
}

// Reset clears all recorded requests.
func (t *Tracker) Reset() {
	t.mu.Lock()
	t.records = nil
	t.mu.Unlock()
}

// buildSummary computes a Summary from a slice of records.
func buildSummary(records []RequestRecord) *Summary {
	s := &Summary{
		ByProvider: make(map[string]*ProviderSummary),
		ByModel:    make(map[string]*ModelSummary),
		ByRole:     make(map[string]*RoleSummary),
	}

	for _, r := range records {
		s.TotalRequests++
		s.TotalTokens += r.TotalTokens
		s.TotalPromptTokens += r.PromptTokens
		s.TotalCompletionTokens += r.CompletionTokens
		s.TotalCost += r.EstimatedCost

		// Provider
		ps, ok := s.ByProvider[r.Provider]
		if !ok {
			ps = &ProviderSummary{}
			s.ByProvider[r.Provider] = ps
		}
		ps.Requests++
		ps.Tokens += r.TotalTokens
		ps.Cost += r.EstimatedCost

		// Model
		ms, ok := s.ByModel[r.Model]
		if !ok {
			ms = &ModelSummary{}
			s.ByModel[r.Model] = ms
		}
		ms.Requests++
		ms.Tokens += r.TotalTokens
		ms.Cost += r.EstimatedCost

		// Role
		rs, ok := s.ByRole[r.Role]
		if !ok {
			rs = &RoleSummary{}
			s.ByRole[r.Role] = rs
		}
		rs.Requests++
		rs.Tokens += r.TotalTokens
		rs.Cost += r.EstimatedCost
	}

	return s
}

// DefaultPricing returns pricing for common models as of early 2025.
func DefaultPricing() map[string]ModelPricing {
	return map[string]ModelPricing{
		"gpt-4o":                    {PromptCostPer1M: 2.50, CompletionCostPer1M: 10.00},
		"gpt-4o-mini":               {PromptCostPer1M: 0.15, CompletionCostPer1M: 0.60},
		"claude-sonnet-4-20250514":  {PromptCostPer1M: 3.00, CompletionCostPer1M: 15.00},
		"claude-haiku-3.5":          {PromptCostPer1M: 0.80, CompletionCostPer1M: 4.00},
		// Ollama local models are free — no entry needed, cost defaults to 0.0
		// Gemini has different pricing tiers — add as needed
	}
}
