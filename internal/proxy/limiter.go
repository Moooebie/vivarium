package proxy

import (
	"sync"
	"time"
)

// limiter enforces per-key request-per-minute limits with a token bucket.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
	rpm    int
}

func newLimiter() *limiter {
	return &limiter{buckets: map[string]*bucket{}, now: time.Now}
}

// allow reports whether a request may proceed and, when denied, how long the
// caller should wait. An rpm of zero or less disables limiting.
func (l *limiter) allow(key string, rpm int) (bool, time.Duration) {
	if rpm <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	rate := float64(rpm) / 60.0

	b, ok := l.buckets[key]
	if !ok || b.rpm != rpm {
		b = &bucket{tokens: float64(rpm), last: now, rpm: rpm}
		l.buckets[key] = b
	} else {
		b.tokens = min(float64(rpm), b.tokens+now.Sub(b.last).Seconds()*rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	retry := time.Duration((1 - b.tokens) / rate * float64(time.Second))
	return false, retry
}
