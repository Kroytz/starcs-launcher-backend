package api

import (
	"testing"
	"time"
)

func TestAuthRateLimiterBlocksAfterIPAttempts(t *testing.T) {
	limiter := newAuthRateLimiter()
	now := time.Now()
	const ip = "203.0.113.10"
	const steamID = uint64(76561198000000001)

	for i := 0; i < maxAuthAttemptsPerIP; i++ {
		if !limiter.allow(ip, steamID, now) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		limiter.recordAttempt(ip, now)
	}
	if limiter.allow(ip, steamID, now) {
		t.Fatal("ip attempt budget should be exhausted")
	}
	if !limiter.allow("203.0.113.11", steamID, now) {
		t.Fatal("other ip should still be allowed")
	}
}

func TestAuthRateLimiterBlocksAfterSteamFailures(t *testing.T) {
	limiter := newAuthRateLimiter()
	now := time.Now()
	const ip = "203.0.113.20"
	const steamID = uint64(76561198000000002)

	for i := 0; i < maxAuthFailuresPerKey; i++ {
		if !limiter.allow(ip, steamID, now) {
			t.Fatalf("failure %d should be allowed before recording", i+1)
		}
		limiter.recordAttempt(ip, now)
		limiter.recordFailure(ip, steamID, now)
	}
	if limiter.allow(ip, steamID, now) {
		t.Fatal("steam failure budget should be exhausted")
	}
	if !limiter.allow("203.0.113.21", steamID+1, now.Add(time.Second)) {
		t.Fatal("other ip and steam id should still be allowed")
	}
	if limiter.allow("203.0.113.21", steamID, now.Add(time.Second)) {
		t.Fatal("same steam id should remain blocked across ips")
	}
}
