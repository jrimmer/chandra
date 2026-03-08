package ratelimit_test

import (
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/ratelimit"
)

func TestLimiter_AllowsUpToRate(t *testing.T) {
	l := ratelimit.New(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("user1") {
			t.Fatalf("expected Allow=true on call %d, got false", i+1)
		}
	}
	if l.Allow("user1") {
		t.Fatal("expected Allow=false after rate exceeded, got true")
	}
}

func TestLimiter_IndependentKeys(t *testing.T) {
	l := ratelimit.New(2, time.Minute)
	l.Allow("a")
	l.Allow("a")
	if l.Allow("a") {
		t.Fatal("a should be exhausted")
	}
	// b has its own bucket — should still have capacity
	if !l.Allow("b") {
		t.Fatal("b should not be limited")
	}
}

func TestLimiter_WindowReset(t *testing.T) {
	l := ratelimit.New(1, 50*time.Millisecond)
	if !l.Allow("u") {
		t.Fatal("first call should succeed")
	}
	if l.Allow("u") {
		t.Fatal("second call should be blocked")
	}
	time.Sleep(60 * time.Millisecond) // wait for window reset
	if !l.Allow("u") {
		t.Fatal("call after window reset should succeed")
	}
}

func TestLimiter_Disabled(t *testing.T) {
	l := ratelimit.New(0, time.Minute)
	for i := 0; i < 100; i++ {
		if !l.Allow("user") {
			t.Fatal("disabled limiter should always allow")
		}
	}
}

func TestLimiter_Remaining(t *testing.T) {
	l := ratelimit.New(5, time.Minute)
	if r := l.Remaining("u"); r != 5 {
		t.Fatalf("want remaining=5 before any calls, got %d", r)
	}
	l.Allow("u")
	l.Allow("u")
	if r := l.Remaining("u"); r != 3 {
		t.Fatalf("want remaining=3 after 2 calls, got %d", r)
	}
}
