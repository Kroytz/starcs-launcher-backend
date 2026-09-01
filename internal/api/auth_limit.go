package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	authRateWindow        = time.Minute
	maxAuthAttemptsPerIP  = 20
	maxAuthFailuresPerKey = 10
	authRateLimitedCode   = 4013
)

type slidingCounter struct {
	events []time.Time
}

func (c *slidingCounter) add(now time.Time, window time.Duration) int {
	cutoff := now.Add(-window)
	kept := c.events[:0]
	for _, event := range c.events {
		if event.After(cutoff) {
			kept = append(kept, event)
		}
	}
	c.events = append(kept, now)
	return len(c.events)
}

func (c *slidingCounter) count(now time.Time, window time.Duration) int {
	cutoff := now.Add(-window)
	kept := c.events[:0]
	for _, event := range c.events {
		if event.After(cutoff) {
			kept = append(kept, event)
		}
	}
	c.events = kept
	return len(c.events)
}

type authRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*slidingCounter
	failures map[string]*slidingCounter
}

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{
		attempts: make(map[string]*slidingCounter),
		failures: make(map[string]*slidingCounter),
	}
}

func (l *authRateLimiter) allow(ip string, steamID uint64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)

	if ip != "" {
		if l.counter(l.attempts, "ip:"+ip).count(now, authRateWindow) >= maxAuthAttemptsPerIP {
			return false
		}
	}
	steamKey := "steam:" + strconv.FormatUint(steamID, 10)
	if l.counter(l.failures, steamKey).count(now, authRateWindow) >= maxAuthFailuresPerKey {
		return false
	}
	if ip != "" {
		ipFailKey := "ipfail:" + ip
		if l.counter(l.failures, ipFailKey).count(now, authRateWindow) >= maxAuthFailuresPerKey {
			return false
		}
	}
	return true
}

func (l *authRateLimiter) recordAttempt(ip string, now time.Time) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	l.counter(l.attempts, "ip:"+ip).add(now, authRateWindow)
}

func (l *authRateLimiter) recordFailure(ip string, steamID uint64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	steamKey := "steam:" + strconv.FormatUint(steamID, 10)
	l.counter(l.failures, steamKey).add(now, authRateWindow)
	if ip != "" {
		l.counter(l.failures, "ipfail:"+ip).add(now, authRateWindow)
	}
}

func (l *authRateLimiter) counter(bucket map[string]*slidingCounter, key string) *slidingCounter {
	current, ok := bucket[key]
	if !ok {
		current = &slidingCounter{}
		bucket[key] = current
	}
	return current
}

func (l *authRateLimiter) pruneLocked(now time.Time) {
	if len(l.attempts)+len(l.failures) < 2048 {
		return
	}
	for key, counter := range l.attempts {
		if counter.count(now, authRateWindow) == 0 {
			delete(l.attempts, key)
		}
	}
	for key, counter := range l.failures {
		if counter.count(now, authRateWindow) == 0 {
			delete(l.failures, key)
		}
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
