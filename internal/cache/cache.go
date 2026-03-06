// Package cache provides an in-memory LLM response cache for deduplicating
// identical prompts across build/fix iterations.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// Cache is a concurrent-safe in-memory key-value store for LLM responses.
type Cache struct {
	m sync.Map
}

// New creates an empty Cache.
func New() *Cache {
	return &Cache{}
}

// Get returns the cached value and true if the key exists, or ("", false) on miss.
func (c *Cache) Get(key string) (string, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return "", false
	}
	return v.(string), true
}

// Set stores a value under the given key, overwriting any previous value.
func (c *Cache) Set(key string, value string) {
	c.m.Store(key, value)
}

// Key builds a deterministic SHA-256 hex digest from the concatenated parts,
// separated by null bytes to prevent collisions between ("a","b") and ("ab","").
func Key(parts ...string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}

// Len returns the number of entries in the cache.
func (c *Cache) Len() int {
	n := 0
	c.m.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
