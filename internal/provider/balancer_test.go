package provider

import (
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Round-Robin Tests
// ---------------------------------------------------------------------------

func TestRoundRobin(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin)
	backends := []string{"a", "b", "c"}

	counts := map[string]int{}
	for i := 0; i < 9; i++ {
		pick := b.Select("test-group", backends)
		counts[pick]++
	}

	for _, backend := range backends {
		if counts[backend] != 3 {
			t.Errorf("expected backend %q selected 3 times, got %d", backend, counts[backend])
		}
	}
}

func TestRoundRobin_SingleBackend(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin)
	backends := []string{"only-one"}

	for i := 0; i < 10; i++ {
		pick := b.Select("single", backends)
		if pick != "only-one" {
			t.Errorf("expected only-one, got %s", pick)
		}
	}
}

func TestRoundRobin_ConcurrentSafe(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin)
	backends := []string{"x", "y", "z"}

	var wg sync.WaitGroup
	results := make([]string, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = b.Select("concurrent", backends)
		}(i)
	}
	wg.Wait()

	// Every result must be one of the valid backends.
	valid := map[string]bool{"x": true, "y": true, "z": true}
	for i, r := range results {
		if !valid[r] {
			t.Errorf("goroutine %d returned invalid backend %q", i, r)
		}
	}

	// With 100 calls across 3 backends, each should appear at least once.
	counts := map[string]int{}
	for _, r := range results {
		counts[r]++
	}
	for _, backend := range backends {
		if counts[backend] == 0 {
			t.Errorf("backend %q was never selected across 100 concurrent calls", backend)
		}
	}
}

func TestRoundRobin_IndependentGroups(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin)
	backends := []string{"a", "b"}

	// Group "g1" and "g2" should have independent counters.
	pick1 := b.Select("g1", backends)
	pick2 := b.Select("g2", backends)

	// Both should start at index 0 -> "a".
	if pick1 != "a" {
		t.Errorf("g1 first pick: expected a, got %s", pick1)
	}
	if pick2 != "a" {
		t.Errorf("g2 first pick: expected a, got %s", pick2)
	}

	// Second call to g1 should advance to "b", while g2 stays independent.
	pick1b := b.Select("g1", backends)
	if pick1b != "b" {
		t.Errorf("g1 second pick: expected b, got %s", pick1b)
	}
}

// ---------------------------------------------------------------------------
// Random Tests
// ---------------------------------------------------------------------------

func TestRandom(t *testing.T) {
	b := NewBalancer(StrategyRandom)
	backends := []string{"alpha", "beta", "gamma"}

	counts := map[string]int{}
	const iterations = 300
	for i := 0; i < iterations; i++ {
		pick := b.Select("random-group", backends)
		counts[pick]++
	}

	// Every backend must be selected at least once in 300 iterations.
	// The probability of any single backend not being selected in 300 trials
	// with 3 options is (2/3)^300, which is astronomically small.
	valid := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	for _, backend := range backends {
		if counts[backend] == 0 {
			t.Errorf("backend %q was never selected in %d random iterations", backend, iterations)
		}
	}
	// Verify no invalid values were returned.
	for k := range counts {
		if !valid[k] {
			t.Errorf("unexpected backend %q returned from random selection", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Weighted Selection Tests
// ---------------------------------------------------------------------------

func TestSelectWeighted(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin) // strategy doesn't matter for weighted
	options := []WeightedOption{
		{Value: "heavy", Weight: 7},
		{Value: "medium", Weight: 2},
		{Value: "light", Weight: 1},
	}

	counts := map[string]int{}
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		pick := b.SelectWeighted("weighted-group", options)
		counts[pick]++
	}

	// With weights 7:2:1 over 1000 iterations:
	// "heavy" should get roughly 700 (accept 500-900)
	// "medium" should get roughly 200 (accept 50-400)
	// "light" should get roughly 100 (accept 10-300)
	if counts["heavy"] < 500 || counts["heavy"] > 900 {
		t.Errorf("expected heavy ~700, got %d", counts["heavy"])
	}
	if counts["medium"] < 50 || counts["medium"] > 400 {
		t.Errorf("expected medium ~200, got %d", counts["medium"])
	}
	if counts["light"] < 10 || counts["light"] > 300 {
		t.Errorf("expected light ~100, got %d", counts["light"])
	}
}

func TestSelectWeighted_AllZeroWeight(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin)
	options := []WeightedOption{
		{Value: "a", Weight: 0},
		{Value: "b", Weight: 0},
	}

	// When all weights are zero, should fall back to uniform selection.
	pick := b.SelectWeighted("zero-weight", options)
	if pick != "a" && pick != "b" {
		t.Errorf("expected a or b, got %q", pick)
	}
}

func TestSelectWeighted_Empty(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin)
	pick := b.SelectWeighted("empty", nil)
	if pick != "" {
		t.Errorf("expected empty string for empty weighted options, got %q", pick)
	}
}

// ---------------------------------------------------------------------------
// Edge Cases
// ---------------------------------------------------------------------------

func TestSelect_EmptyBackends(t *testing.T) {
	b := NewBalancer(StrategyRoundRobin)

	pick := b.Select("empty", nil)
	if pick != "" {
		t.Errorf("expected empty string for nil backends, got %q", pick)
	}

	pick = b.Select("empty", []string{})
	if pick != "" {
		t.Errorf("expected empty string for empty backends, got %q", pick)
	}
}

func TestSelect_EmptyBackends_Random(t *testing.T) {
	b := NewBalancer(StrategyRandom)

	pick := b.Select("empty-rand", nil)
	if pick != "" {
		t.Errorf("expected empty string for nil backends with random strategy, got %q", pick)
	}
}

// ---------------------------------------------------------------------------
// Strategy Validation
// ---------------------------------------------------------------------------

func TestNewBalancer_Strategies(t *testing.T) {
	strategies := []Strategy{StrategyRoundRobin, StrategyRandom, StrategyLeastLoad}
	for _, s := range strategies {
		b := NewBalancer(s)
		if b == nil {
			t.Errorf("NewBalancer(%q) returned nil", s)
		}
	}
}
