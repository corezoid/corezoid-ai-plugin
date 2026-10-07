package main

import (
	"crypto/sha256"
	"sync"
)

// hostedDefaultMaxConcurrentPerToken caps the tool calls one caller may have
// in flight on a replica (run-task can wait on a process for minutes), so one
// account cannot occupy every worker. The key is the caller's token, not the
// source IP: AI platforms call on behalf of many users from shared addresses.
const hostedDefaultMaxConcurrentPerToken = 4

// tokenLimiter counts in-flight requests per token; only hashes are kept.
type tokenLimiter struct {
	max      int
	mu       sync.Mutex
	inFlight map[[sha256.Size]byte]int
}

// newTokenLimiter returns nil (no limit) for max < 0; 0 means the default.
func newTokenLimiter(max int) *tokenLimiter {
	if max < 0 {
		return nil
	}
	if max == 0 {
		max = hostedDefaultMaxConcurrentPerToken
	}
	return &tokenLimiter{max: max, inFlight: map[[sha256.Size]byte]int{}}
}

// acquire takes a slot for token without waiting; release returns it.
func (l *tokenLimiter) acquire(token string) (release func(), ok bool) {
	if l == nil {
		return func() {}, true
	}
	key := sha256.Sum256([]byte(token))
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key] >= l.max {
		return nil, false
	}
	l.inFlight[key]++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.inFlight[key] <= 1 {
			delete(l.inFlight, key)
			return
		}
		l.inFlight[key]--
	}, true
}
