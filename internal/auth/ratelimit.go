package auth

import (
	"sync"
	"time"
)

// Limiter is an in-memory token bucket, which plan §4 explicitly says is
// sufficient here: one replica, ~25 users.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	burst   float64
	refill  float64 // tokens per second
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter allows burst attempts immediately, refilling at per/interval.
func NewLimiter(burst int, per time.Duration) *Limiter {
	return &Limiter{
		buckets: map[string]*bucket{},
		burst:   float64(burst),
		refill:  float64(burst) / per.Seconds(),
		now:     time.Now,
	}
}

// Allow consumes one token for key, reporting whether the attempt may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.refill
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Reset clears a key after a successful login, so a legitimate user who
// fat-fingered their password a few times is not left throttled.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// Sweep drops buckets that have fully refilled. Called periodically so the map
// cannot grow without bound from a distributed guessing attempt.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.refill >= l.burst {
			delete(l.buckets, k)
		}
	}
}
