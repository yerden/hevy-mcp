package oauth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rate limits. Two independent buckets:
//
//   - /oauth/authorize POST is limited per client IP. Each submission
//     triggers a live GET /v1/user/info against Hevy to validate the
//     pasted API key, so unbounded traffic turns the server into a
//     credential-tester proxy.
//   - /mcp is limited per authenticated user (keyed by a hash of the
//     decrypted Hevy API key). Guards against a runaway loop from a
//     legitimate token — or a leaked one — hammering Hevy through us.
const (
	authorizeRatePerMin = 5
	authorizeBurst      = 5

	mcpRatePerMin = 60
	mcpBurst      = 20

	// Idle limiter entries are dropped after this window to keep the
	// per-key map bounded across the process lifetime.
	rateLimiterIdleEvict = 10 * time.Minute
	// Opportunistic sweep threshold: only walk the map to evict when it
	// grows past this. Keeps the hot path O(1) in the common case.
	rateLimiterSweepAt = 1024
)

// tokenBucket is a plain refill-on-read token bucket. Not safe for
// concurrent use; the enclosing keyRateLimiter provides the mutex.
type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

func (b *tokenBucket) allow(now time.Time, ratePerSec, burst float64) bool {
	if elapsed := now.Sub(b.lastRefill).Seconds(); elapsed > 0 {
		b.tokens += elapsed * ratePerSec
		if b.tokens > burst {
			b.tokens = burst
		}
		b.lastRefill = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// keyRateLimiter tracks one token bucket per opaque string key. Used
// both per-IP (authorize) and per-user (mcp); the caller decides the
// key domain.
type keyRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	ratePerS float64
	burst    float64
	now      func() time.Time
}

func newKeyRateLimiter(ratePerMin, burst int, now func() time.Time) *keyRateLimiter {
	return &keyRateLimiter{
		buckets:  make(map[string]*tokenBucket),
		ratePerS: float64(ratePerMin) / 60.0,
		burst:    float64(burst),
		now:      now,
	}
}

func (l *keyRateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: l.burst, lastRefill: n}
		l.buckets[key] = b
	}
	allowed := b.allow(n, l.ratePerS, l.burst)
	if len(l.buckets) > rateLimiterSweepAt {
		for k, v := range l.buckets {
			if n.Sub(v.lastRefill) > rateLimiterIdleEvict {
				delete(l.buckets, k)
			}
		}
	}
	return allowed
}

// clientIP returns the best-effort client IP for the request, honoring the
// Fly-Client-IP header the Fly proxy injects, then X-Forwarded-For, then
// falling back to r.RemoteAddr with the port stripped.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("Fly-Client-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
