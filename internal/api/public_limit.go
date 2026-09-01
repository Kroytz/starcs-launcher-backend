package api

import (
	"net/http"
	"sync"
	"time"
)

const (
	publicReadRateWindow   = time.Minute
	maxPublicReadsPerIP    = 60
	publicRateLimitedCode  = 4014
)

type publicReadLimiter struct {
	mu       sync.Mutex
	attempts map[string]*slidingCounter
}

func newPublicReadLimiter() *publicReadLimiter {
	return &publicReadLimiter{attempts: make(map[string]*slidingCounter)}
}

func (l *publicReadLimiter) allow(ip string, now time.Time) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	return l.counter(ip).count(now, publicReadRateWindow) < maxPublicReadsPerIP
}

func (l *publicReadLimiter) record(ip string, now time.Time) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	l.counter(ip).add(now, publicReadRateWindow)
}

func (l *publicReadLimiter) counter(ip string) *slidingCounter {
	current, ok := l.attempts[ip]
	if !ok {
		current = &slidingCounter{}
		l.attempts[ip] = current
	}
	return current
}

func (l *publicReadLimiter) pruneLocked(now time.Time) {
	if len(l.attempts) < 4096 {
		return
	}
	for key, counter := range l.attempts {
		if counter.count(now, publicReadRateWindow) == 0 {
			delete(l.attempts, key)
		}
	}
}

func (h *Handler) allowPublicRead(w http.ResponseWriter, r *http.Request) bool {
	if h.publicLimiter == nil {
		return true
	}
	ip := clientIP(r)
	now := time.Now()
	if !h.publicLimiter.allow(ip, now) {
		h.writeError(w, http.StatusTooManyRequests, publicRateLimitedCode, "请求过于频繁，请稍后再试")
		return false
	}
	h.publicLimiter.record(ip, now)
	return true
}
