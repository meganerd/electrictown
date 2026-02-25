package provider

import (
	"crypto/rand"
	"math/big"
	"sync"
	"sync/atomic"
)

// Strategy determines how requests are distributed across backends.
type Strategy string

const (
	// StrategyRoundRobin distributes requests evenly in order.
	StrategyRoundRobin Strategy = "round-robin"

	// StrategyRandom selects a backend at random using crypto/rand.
	StrategyRandom Strategy = "random"

	// StrategyLeastLoad selects the backend with the fewest in-flight requests.
	// Reserved for future implementation; currently falls back to round-robin.
	StrategyLeastLoad Strategy = "least-load"
)

// WeightedOption pairs a backend value with a relative weight for weighted
// selection. A weight of 0 is valid but means the option is never selected
// unless all weights are 0 (in which case uniform random selection is used).
type WeightedOption struct {
	Value  string
	Weight int
}

// Balancer distributes requests across multiple backends using a configurable
// strategy. It is safe for concurrent use.
type Balancer struct {
	strategy Strategy
	counters sync.Map // map[string]*atomic.Uint64 — per-group counters
}

// NewBalancer creates a Balancer with the given strategy.
func NewBalancer(strategy Strategy) *Balancer {
	return &Balancer{
		strategy: strategy,
	}
}

// Select picks one backend from the list for the given group.
//
// For round-robin: uses an atomic counter per group, modulo the number of
// backends, guaranteeing even distribution and lock-free concurrency.
//
// For random: uses crypto/rand for unbiased selection.
//
// Returns an empty string if backends is empty.
func (b *Balancer) Select(group string, backends []string) string {
	if len(backends) == 0 {
		return ""
	}
	if len(backends) == 1 {
		return backends[0]
	}

	switch b.strategy {
	case StrategyRandom:
		return backends[cryptoRandIntn(len(backends))]
	case StrategyRoundRobin, StrategyLeastLoad:
		// LeastLoad falls back to round-robin until implemented.
		counter := b.getCounter(group)
		idx := counter.Add(1) - 1 // 0-indexed
		return backends[idx%uint64(len(backends))]
	default:
		// Unknown strategy: fall back to round-robin.
		counter := b.getCounter(group)
		idx := counter.Add(1) - 1
		return backends[idx%uint64(len(backends))]
	}
}

// SelectWeighted picks one backend from the weighted options using crypto/rand.
// The probability of selecting an option is proportional to its weight relative
// to the total weight. If all weights are zero, uniform random selection is used.
//
// Returns an empty string if options is empty.
func (b *Balancer) SelectWeighted(group string, options []WeightedOption) string {
	if len(options) == 0 {
		return ""
	}

	totalWeight := 0
	for _, opt := range options {
		totalWeight += opt.Weight
	}

	// If all weights are zero, fall back to uniform selection.
	if totalWeight == 0 {
		return options[cryptoRandIntn(len(options))].Value
	}

	target := cryptoRandIntn(totalWeight)
	cumulative := 0
	for _, opt := range options {
		cumulative += opt.Weight
		if target < cumulative {
			return opt.Value
		}
	}

	// Should not reach here, but return last option as safety.
	return options[len(options)-1].Value
}

// getCounter returns the atomic counter for a group, creating it if needed.
func (b *Balancer) getCounter(group string) *atomic.Uint64 {
	if v, ok := b.counters.Load(group); ok {
		return v.(*atomic.Uint64)
	}
	counter := &atomic.Uint64{}
	actual, _ := b.counters.LoadOrStore(group, counter)
	return actual.(*atomic.Uint64)
}

// cryptoRandIntn returns a cryptographically random int in [0, n).
// Panics if n <= 0.
func cryptoRandIntn(n int) int {
	max := big.NewInt(int64(n))
	val, err := rand.Int(rand.Reader, max)
	if err != nil {
		// crypto/rand failure is catastrophic; panic is appropriate.
		panic("balancer: crypto/rand failed: " + err.Error())
	}
	return int(val.Int64())
}
