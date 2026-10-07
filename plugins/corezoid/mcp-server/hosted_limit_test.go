package main

import "testing"

// One token over its budget is refused at once; other tokens and the same
// token after a call finishes are not affected.
func TestHostedTokenLimiter(t *testing.T) {
	l := newTokenLimiter(2)
	r1, ok1 := l.acquire("a")
	r2, ok2 := l.acquire("a")
	if !ok1 || !ok2 {
		t.Fatal("calls within the budget refused")
	}
	if _, ok := l.acquire("a"); ok {
		t.Fatal("third concurrent call for one token accepted")
	}
	rb, okb := l.acquire("b")
	if !okb {
		t.Fatal("another token refused")
	}
	rb()
	r1()
	if _, ok := l.acquire("a"); !ok {
		t.Fatal("freed slot not reusable")
	}
	r2()

	if newTokenLimiter(-1) != nil {
		t.Fatal("negative limit must disable the limiter")
	}
	if rel, ok := (*tokenLimiter)(nil).acquire("x"); !ok || rel == nil {
		t.Fatal("disabled limiter must admit")
	}
	if newTokenLimiter(0).max != hostedDefaultMaxConcurrentPerToken {
		t.Fatal("zero must mean the default")
	}
}
