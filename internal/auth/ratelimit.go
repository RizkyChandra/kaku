package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// bcrypt already costs ~100ms per attempt; 5 failures per 15 minutes stops
	// sustained stuffing from one address while leaving room for a user who
	// mistypes a few times.
	ipFailures = 5
	// Deliberately far higher than the IP limit. The email counter exists so a
	// distributed attack on one known account still hits a wall, but it is also
	// the one counter an attacker can drive for someone else's account, so it
	// must not be cheap to weaponise: at 50 it costs 50 attempts to lock an
	// account out for 15 minutes, and the IP limit means those attempts have to
	// come from at least 10 addresses. The tradeoff is accepted knowingly —
	// a small lockout window beats an unbounded stuffing budget.
	emailFailures = 50
	failWindow    = 15 * time.Minute
	// A full sweep runs at most this often so a flood of unique keys cannot
	// make every failure an O(n) scan.
	sweepEvery = time.Minute
)

// Limiter counts recent failed logins per client IP and per submitted email.
// It answers only "allowed or not" — callers must return the same generic
// failure either way, or being limited would reveal that an account exists.
type Limiter struct {
	mu        sync.Mutex
	hits      map[string]entry
	lastSweep time.Time
	now       func() time.Time
}

// Fixed bucket rather than a sliding window: half the code, and the only cost
// is that up to 2x the limit can land across a window boundary.
type entry struct {
	n     int
	reset time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{hits: map[string]entry{}, now: time.Now}
}

// Allow reports whether a login attempt may proceed.
func (l *Limiter) Allow(ip, email string) bool {
	// ponytail: one global mutex. Fine up to tens of thousands of logins/sec;
	// shard by key hash if a lock profile ever says otherwise.
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	return l.count("i:"+ip, now) < ipFailures && l.count("e:"+key(email), now) < emailFailures
}

// Fail records a failed attempt against both keys.
func (l *Limiter) Fail(ip, email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	l.bump("i:"+ip, now)
	l.bump("e:"+key(email), now)
}

// Succeed clears the email counter. The IP counter is deliberately left alone:
// clearing it would let anyone holding one valid account reset their own budget
// between guesses.
func (l *Limiter) Succeed(email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, "e:"+key(email))
}

func (l *Limiter) count(k string, now time.Time) int {
	e, ok := l.hits[k]
	if !ok || now.After(e.reset) {
		return 0
	}
	return e.n
}

func (l *Limiter) bump(k string, now time.Time) {
	e, ok := l.hits[k]
	if !ok || now.After(e.reset) {
		e = entry{reset: now.Add(failWindow)}
	}
	e.n++
	l.hits[k] = e
}

// sweep drops expired entries so an attacker sending unique emails cannot grow
// the map without bound.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	for k, e := range l.hits {
		if now.After(e.reset) {
			delete(l.hits, k)
		}
	}
}

func key(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// ClientIP returns the address to key the limiter on. chi's middleware.RealIP
// has already rewritten RemoteAddr from X-Forwarded-For by the time a handler
// runs, so read it from there and never from the header directly — a forged
// header would otherwise hand an attacker a fresh key per request. That also
// means the IP counter is only as trustworthy as the proxy in front of Kaku;
// the email counter is the backstop when there is none.
func ClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
