package cache

import (
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	c := New()
	if c.Len() != 0 {
		t.Errorf("new cache Len() = %d, want 0", c.Len())
	}
}

func TestSetGet(t *testing.T) {
	c := New()
	c.Set("k1", "hello")
	v, ok := c.Get("k1")
	if !ok {
		t.Fatal("expected hit for k1")
	}
	if v != "hello" {
		t.Errorf("Get(k1) = %q, want %q", v, "hello")
	}
}

func TestGetMiss(t *testing.T) {
	c := New()
	_, ok := c.Get("nonexistent")
	if ok {
		t.Error("expected miss for nonexistent key")
	}
}

func TestKey_Deterministic(t *testing.T) {
	k1 := Key("role", "prompt-text")
	k2 := Key("role", "prompt-text")
	if k1 != k2 {
		t.Errorf("same inputs produced different keys: %q vs %q", k1, k2)
	}
}

func TestKey_DifferentInputs(t *testing.T) {
	k1 := Key("role", "prompt-a")
	k2 := Key("role", "prompt-b")
	if k1 == k2 {
		t.Error("different inputs produced the same key")
	}
}

func TestKey_OrderMatters(t *testing.T) {
	k1 := Key("a", "b")
	k2 := Key("b", "a")
	if k1 == k2 {
		t.Error("Key(a,b) == Key(b,a), expected different keys")
	}
}

func TestKey_BoundaryCollision(t *testing.T) {
	// "ab" + "" vs "a" + "b" — null separator prevents collision.
	k1 := Key("ab", "")
	k2 := Key("a", "b")
	if k1 == k2 {
		t.Error("Key(ab,\"\") == Key(a,b), null separator failed")
	}
}

func TestLen(t *testing.T) {
	c := New()
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3")
	if c.Len() != 3 {
		t.Errorf("Len() = %d, want 3", c.Len())
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := Key("goroutine", string(rune(i)))
			c.Set(k, "value")
			c.Get(k)
		}(i)
	}
	wg.Wait()
	// No panic = pass. Verify we stored entries.
	if c.Len() == 0 {
		t.Error("expected non-zero entries after concurrent writes")
	}
}
