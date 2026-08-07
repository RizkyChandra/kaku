package auth

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// newTestLimiter returns a limiter with a clock the test drives.
func newTestLimiter() (*Limiter, *time.Time) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewLimiter()
	l.now = func() time.Time { return now }
	return l, &now
}

func TestIPThreshold(t *testing.T) {
	l, _ := newTestLimiter()
	for i := range ipFailures {
		if !l.Allow("1.2.3.4", "a@example.com") {
			t.Fatalf("blocked after %d failures, want allowed", i)
		}
		l.Fail("1.2.3.4", "a@example.com")
	}
	if l.Allow("1.2.3.4", "a@example.com") {
		t.Fatalf("allowed after %d failures, want blocked", ipFailures)
	}
	// A different address is unaffected: the email counter is far higher, so
	// one attacker cannot lock the real user out from their own IP.
	if !l.Allow("5.6.7.8", "a@example.com") {
		t.Fatal("other IP blocked, want allowed")
	}
}

func TestEmailThreshold(t *testing.T) {
	l, _ := newTestLimiter()
	for i := range emailFailures {
		l.Fail(fmt.Sprintf("10.0.0.%d", i), "a@example.com")
	}
	if l.Allow("9.9.9.9", "a@example.com") {
		t.Fatal("allowed after email limit, want blocked")
	}
	if !l.Allow("9.9.9.9", "b@example.com") {
		t.Fatal("other email blocked, want allowed")
	}
}

func TestSucceedResetsEmail(t *testing.T) {
	l, _ := newTestLimiter()
	l.Fail("1.2.3.4", "A@Example.com")
	l.Fail("1.2.3.4", "a@example.com")
	l.Succeed("a@example.com")
	if got := l.count("e:a@example.com", l.now()); got != 0 {
		t.Fatalf("email count %d after success, want 0", got)
	}
	// The IP counter deliberately survives a success.
	if got := l.count("i:1.2.3.4", l.now()); got != 2 {
		t.Fatalf("ip count %d, want 2", got)
	}
}

func TestWindowExpires(t *testing.T) {
	l, now := newTestLimiter()
	for range ipFailures {
		l.Fail("1.2.3.4", "a@example.com")
	}
	if l.Allow("1.2.3.4", "a@example.com") {
		t.Fatal("allowed while limited")
	}
	*now = now.Add(failWindow + time.Second)
	if !l.Allow("1.2.3.4", "a@example.com") {
		t.Fatal("blocked after window expired, want allowed")
	}
}

func TestEvictsExpired(t *testing.T) {
	l, now := newTestLimiter()
	for i := range 100 {
		l.Fail("1.2.3.4", fmt.Sprintf("u%d@example.com", i))
	}
	before := len(l.hits)
	if before < 100 {
		t.Fatalf("map has %d entries, want >= 100", before)
	}
	*now = now.Add(failWindow + sweepEvery)
	l.Fail("1.2.3.4", "fresh@example.com")
	if len(l.hits) != 2 { // only the fresh IP and email entries remain
		t.Fatalf("map has %d entries after sweep, want 2 (was %d)", len(l.hits), before)
	}
}

func TestConcurrent(t *testing.T) {
	l := NewLimiter()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 100 {
				email := fmt.Sprintf("u%d@example.com", j)
				l.Allow("1.2.3.4", email)
				l.Fail("1.2.3.4", email)
				l.Succeed(email)
			}
		}()
	}
	wg.Wait()
}

func TestClientIP(t *testing.T) {
	for addr, want := range map[string]string{
		"1.2.3.4:5678": "1.2.3.4",
		"[::1]:5678":   "::1",
		"1.2.3.4":      "1.2.3.4", // RealIP sets a bare address when it rewrites
	} {
		r := &http.Request{RemoteAddr: addr}
		if got := ClientIP(r); got != want {
			t.Errorf("ClientIP(%q) = %q, want %q", addr, got, want)
		}
	}
}
