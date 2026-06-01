package notify

import (
	"context"
	"sync"
	"time"
)

// tokenBucket is a minimal refilling rate limiter used to respect per-channel
// send caps (e.g. WeCom bot: 20 messages/minute).
type tokenBucket struct {
	mu       sync.Mutex
	capacity int
	tokens   int
	interval time.Duration
	last     time.Time
}

func newTokenBucket(capacity int, interval time.Duration) *tokenBucket {
	return &tokenBucket{
		capacity: capacity,
		tokens:   capacity,
		interval: interval,
	}
}

// wait blocks until a token is available or ctx is done. It refills the full
// bucket once per interval (coarse but sufficient for chat-bot limits).
func (b *tokenBucket) wait(ctx context.Context) {
	for {
		if b.tryTake() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.interval / time.Duration(b.capacity)):
		}
	}
}

func (b *tokenBucket) tryTake() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := nowFunc()
	if b.last.IsZero() || now.Sub(b.last) >= b.interval {
		b.tokens = b.capacity
		b.last = now
	}
	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

// nowFunc is indirected so tests can control time; production uses the clock.
var nowFunc = time.Now
