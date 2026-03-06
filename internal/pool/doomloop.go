package pool

import (
	"crypto/sha256"
	"encoding/hex"
)

// DoomLoop tracks response hashes across retries to detect when a worker
// produces identical output repeatedly (wasting tokens without progress).
type DoomLoop struct {
	hashes map[string]int // hash → count
}

// NewDoomLoop creates a new doom-loop detector.
func NewDoomLoop() *DoomLoop {
	return &DoomLoop{hashes: make(map[string]int)}
}

// Check records a response and returns true if the same response has been
// seen before (i.e., the worker is in a doom loop producing identical output).
func (d *DoomLoop) Check(response string) bool {
	h := hash(response)
	d.hashes[h]++
	return d.hashes[h] > 1
}

// hash returns a hex-encoded SHA-256 digest of s.
func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
