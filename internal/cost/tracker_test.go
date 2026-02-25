package cost

import (
	"math"
	"sync"
	"testing"
)

// testPricing returns a deterministic pricing map for tests.
func testPricing() map[string]ModelPricing {
	return map[string]ModelPricing{
		"gpt-4o":      {PromptCostPer1M: 2.50, CompletionCostPer1M: 10.00},
		"gpt-4o-mini": {PromptCostPer1M: 0.15, CompletionCostPer1M: 0.60},
		"claude-sonnet-4-20250514": {PromptCostPer1M: 3.00, CompletionCostPer1M: 15.00},
	}
}

func TestRecord(t *testing.T) {
	tr := NewTracker(testPricing())

	rec := tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens:     500,
		CompletionTokens: 200,
		TotalTokens:      700,
	})

	if rec.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", rec.Provider, "openai")
	}
	if rec.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", rec.Model, "gpt-4o")
	}
	if rec.Role != "engineer" {
		t.Errorf("Role = %q, want %q", rec.Role, "engineer")
	}
	if rec.PromptTokens != 500 {
		t.Errorf("PromptTokens = %d, want 500", rec.PromptTokens)
	}
	if rec.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", rec.CompletionTokens)
	}
	if rec.TotalTokens != 700 {
		t.Errorf("TotalTokens = %d, want 700", rec.TotalTokens)
	}
	if rec.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}

	// Cost: (500/1M)*2.50 + (200/1M)*10.00 = 0.00125 + 0.002 = 0.00325
	expectedCost := 0.00325
	if math.Abs(rec.EstimatedCost-expectedCost) > 1e-10 {
		t.Errorf("EstimatedCost = %f, want %f", rec.EstimatedCost, expectedCost)
	}
}

func TestRecord_NoPricing(t *testing.T) {
	tr := NewTracker(testPricing())

	rec := tr.Record("ollama", "llama3", "engineer", Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	})

	if rec.EstimatedCost != 0.0 {
		t.Errorf("EstimatedCost = %f, want 0.0 for unknown model", rec.EstimatedCost)
	}
	if rec.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", rec.TotalTokens)
	}
}

func TestRecord_NilUsage(t *testing.T) {
	tr := NewTracker(testPricing())

	// Zero-value Usage -- should not panic
	rec := tr.Record("openai", "gpt-4o", "engineer", Usage{})

	if rec.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", rec.PromptTokens)
	}
	if rec.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", rec.CompletionTokens)
	}
	if rec.EstimatedCost != 0.0 {
		t.Errorf("EstimatedCost = %f, want 0.0", rec.EstimatedCost)
	}
}

func TestSummary(t *testing.T) {
	tr := NewTracker(testPricing())

	tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
	})
	tr.Record("anthropic", "claude-sonnet-4-20250514", "designer", Usage{
		PromptTokens: 2000, CompletionTokens: 1000, TotalTokens: 3000,
	})

	s := tr.Summary()

	if s.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", s.TotalRequests)
	}
	if s.TotalTokens != 4500 {
		t.Errorf("TotalTokens = %d, want 4500", s.TotalTokens)
	}
	if s.TotalPromptTokens != 3000 {
		t.Errorf("TotalPromptTokens = %d, want 3000", s.TotalPromptTokens)
	}
	if s.TotalCompletionTokens != 1500 {
		t.Errorf("TotalCompletionTokens = %d, want 1500", s.TotalCompletionTokens)
	}

	// gpt-4o: (1000/1M)*2.50 + (500/1M)*10.00 = 0.0025 + 0.005 = 0.0075
	// claude: (2000/1M)*3.00 + (1000/1M)*15.00 = 0.006 + 0.015 = 0.021
	expectedTotal := 0.0285
	if math.Abs(s.TotalCost-expectedTotal) > 1e-10 {
		t.Errorf("TotalCost = %f, want %f", s.TotalCost, expectedTotal)
	}
}

func TestSummaryByProvider(t *testing.T) {
	tr := NewTracker(testPricing())

	tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
	})
	tr.Record("openai", "gpt-4o-mini", "engineer", Usage{
		PromptTokens: 2000, CompletionTokens: 1000, TotalTokens: 3000,
	})
	tr.Record("anthropic", "claude-sonnet-4-20250514", "designer", Usage{
		PromptTokens: 500, CompletionTokens: 200, TotalTokens: 700,
	})

	s := tr.Summary()

	openai, ok := s.ByProvider["openai"]
	if !ok {
		t.Fatal("missing openai in ByProvider")
	}
	if openai.Requests != 2 {
		t.Errorf("openai.Requests = %d, want 2", openai.Requests)
	}
	if openai.Tokens != 4500 {
		t.Errorf("openai.Tokens = %d, want 4500", openai.Tokens)
	}

	anthropic, ok := s.ByProvider["anthropic"]
	if !ok {
		t.Fatal("missing anthropic in ByProvider")
	}
	if anthropic.Requests != 1 {
		t.Errorf("anthropic.Requests = %d, want 1", anthropic.Requests)
	}
}

func TestSummaryByModel(t *testing.T) {
	tr := NewTracker(testPricing())

	tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
	})
	tr.Record("openai", "gpt-4o", "designer", Usage{
		PromptTokens: 800, CompletionTokens: 300, TotalTokens: 1100,
	})

	s := tr.Summary()

	gpt4o, ok := s.ByModel["gpt-4o"]
	if !ok {
		t.Fatal("missing gpt-4o in ByModel")
	}
	if gpt4o.Requests != 2 {
		t.Errorf("gpt-4o.Requests = %d, want 2", gpt4o.Requests)
	}
	if gpt4o.Tokens != 2600 {
		t.Errorf("gpt-4o.Tokens = %d, want 2600", gpt4o.Tokens)
	}
}

func TestSummaryByRole(t *testing.T) {
	tr := NewTracker(testPricing())

	tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
	})
	tr.Record("openai", "gpt-4o", "designer", Usage{
		PromptTokens: 800, CompletionTokens: 300, TotalTokens: 1100,
	})
	tr.Record("anthropic", "claude-sonnet-4-20250514", "engineer", Usage{
		PromptTokens: 600, CompletionTokens: 200, TotalTokens: 800,
	})

	s := tr.Summary()

	eng, ok := s.ByRole["engineer"]
	if !ok {
		t.Fatal("missing engineer in ByRole")
	}
	if eng.Requests != 2 {
		t.Errorf("engineer.Requests = %d, want 2", eng.Requests)
	}
	if eng.Tokens != 2300 {
		t.Errorf("engineer.Tokens = %d, want 2300", eng.Tokens)
	}

	des, ok := s.ByRole["designer"]
	if !ok {
		t.Fatal("missing designer in ByRole")
	}
	if des.Requests != 1 {
		t.Errorf("designer.Requests = %d, want 1", des.Requests)
	}
}

func TestSummaryForRole(t *testing.T) {
	tr := NewTracker(testPricing())

	tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
	})
	tr.Record("openai", "gpt-4o", "designer", Usage{
		PromptTokens: 800, CompletionTokens: 300, TotalTokens: 1100,
	})
	tr.Record("anthropic", "claude-sonnet-4-20250514", "engineer", Usage{
		PromptTokens: 600, CompletionTokens: 200, TotalTokens: 800,
	})

	s := tr.SummaryForRole("engineer")

	if s.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", s.TotalRequests)
	}
	if s.TotalTokens != 2300 {
		t.Errorf("TotalTokens = %d, want 2300", s.TotalTokens)
	}
	if s.TotalPromptTokens != 1600 {
		t.Errorf("TotalPromptTokens = %d, want 1600", s.TotalPromptTokens)
	}
	if s.TotalCompletionTokens != 700 {
		t.Errorf("TotalCompletionTokens = %d, want 700", s.TotalCompletionTokens)
	}

	// Should only have providers/models used by engineer
	if _, ok := s.ByProvider["openai"]; !ok {
		t.Error("expected openai in filtered ByProvider")
	}
	if _, ok := s.ByProvider["anthropic"]; !ok {
		t.Error("expected anthropic in filtered ByProvider")
	}

	// designer data should not be present
	if _, ok := s.ByRole["designer"]; ok {
		t.Error("designer should not appear in engineer-filtered summary")
	}
}

func TestReset(t *testing.T) {
	tr := NewTracker(testPricing())

	tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
	})

	if len(tr.Records()) != 1 {
		t.Fatalf("Records() length = %d, want 1 before reset", len(tr.Records()))
	}

	tr.Reset()

	if len(tr.Records()) != 0 {
		t.Errorf("Records() length = %d, want 0 after reset", len(tr.Records()))
	}

	s := tr.Summary()
	if s.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0 after reset", s.TotalRequests)
	}
}

func TestConcurrentRecord(t *testing.T) {
	tr := NewTracker(testPricing())

	var wg sync.WaitGroup
	n := 100
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			tr.Record("openai", "gpt-4o", "engineer", Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			})
		}()
	}

	wg.Wait()

	records := tr.Records()
	if len(records) != n {
		t.Errorf("Records() length = %d, want %d after concurrent writes", len(records), n)
	}

	s := tr.Summary()
	if s.TotalRequests != n {
		t.Errorf("TotalRequests = %d, want %d", s.TotalRequests, n)
	}
	if s.TotalTokens != n*150 {
		t.Errorf("TotalTokens = %d, want %d", s.TotalTokens, n*150)
	}
}

func TestDefaultPricing(t *testing.T) {
	pricing := DefaultPricing()

	knownModels := []string{"gpt-4o", "gpt-4o-mini", "claude-sonnet-4-20250514", "claude-haiku-3.5"}
	for _, model := range knownModels {
		p, ok := pricing[model]
		if !ok {
			t.Errorf("DefaultPricing missing model %q", model)
			continue
		}
		if p.PromptCostPer1M <= 0 {
			t.Errorf("model %q PromptCostPer1M = %f, want > 0", model, p.PromptCostPer1M)
		}
		if p.CompletionCostPer1M <= 0 {
			t.Errorf("model %q CompletionCostPer1M = %f, want > 0", model, p.CompletionCostPer1M)
		}
	}
}

func TestCostCalculation(t *testing.T) {
	tr := NewTracker(testPricing())

	// 1000 prompt tokens at $2.50/1M = $0.0025
	rec := tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens:     1000,
		CompletionTokens: 0,
		TotalTokens:      1000,
	})

	expected := 0.0025
	if math.Abs(rec.EstimatedCost-expected) > 1e-10 {
		t.Errorf("EstimatedCost = %.10f, want %.10f", rec.EstimatedCost, expected)
	}

	// 1000 completion tokens at $10.00/1M = $0.01
	rec2 := tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens:     0,
		CompletionTokens: 1000,
		TotalTokens:      1000,
	})

	expected2 := 0.01
	if math.Abs(rec2.EstimatedCost-expected2) > 1e-10 {
		t.Errorf("EstimatedCost = %.10f, want %.10f", rec2.EstimatedCost, expected2)
	}

	// Combined: 1M prompt + 1M completion at gpt-4o pricing = $2.50 + $10.00 = $12.50
	rec3 := tr.Record("openai", "gpt-4o", "engineer", Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
		TotalTokens:      2_000_000,
	})

	expected3 := 12.50
	if math.Abs(rec3.EstimatedCost-expected3) > 1e-10 {
		t.Errorf("EstimatedCost = %.10f, want %.10f", rec3.EstimatedCost, expected3)
	}
}
