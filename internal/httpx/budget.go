package httpx

import (
	"context"
	"sync"
	"time"
)

// Budget is a token bucket for APIs that meter quota units per user, such
// as Gmail's 15,000 units per user per minute. Callers spend units up front
// and wait when the bucket runs dry, so concurrent workers share one pace.
type Budget struct {
	mu    sync.Mutex
	rate  float64 // units per second
	burst float64
	avail float64
	last  time.Time
}

// NewBudget creates a full bucket refilling at perSecond up to burst.
func NewBudget(perSecond, burst float64) *Budget {
	return &Budget{rate: perSecond, burst: burst, avail: burst, last: time.Now()}
}

// Take spends n units, waiting (via sleep) until they are covered.
func (b *Budget) Take(ctx context.Context, n float64, sleep func(context.Context, time.Duration) error) error {
	b.mu.Lock()
	now := time.Now()
	b.avail = min(b.burst, b.avail+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	b.avail -= n
	var wait time.Duration
	if b.avail < 0 {
		wait = time.Duration(-b.avail / b.rate * float64(time.Second))
	}
	b.mu.Unlock()
	if wait == 0 {
		return nil
	}
	if sleep == nil {
		sleep = defaultSleep
	}
	return sleep(ctx, wait)
}
