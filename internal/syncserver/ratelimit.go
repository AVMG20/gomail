package syncserver

import (
	"sync"
	"time"
)

// limiter counts attempts per key in a fixed window: at most max attempts
// per window, then callers wait for the window to end.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string]*window
	swept  time.Time // when finished windows were last forgotten
	sweeps int       // tests
}

type window struct {
	start time.Time
	n     int
}

func newLimiter(max int, per time.Duration) *limiter {
	return &limiter{max: max, window: per, now: time.Now, hits: map[string]*window{}}
}

// allow records an attempt for key; when over the limit it returns false
// and how long until the next attempt is allowed.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// Forget finished windows once a window (a scan per window, not per
	// attempt).
	if now.Sub(l.swept) >= l.window {
		for k, w := range l.hits {
			if now.Sub(w.start) >= l.window {
				delete(l.hits, k)
			}
		}
		l.swept = now
		l.sweeps++
	}
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) >= l.window {
		l.hits[key] = &window{start: now, n: 1}
		return true, 0
	}
	if w.n >= l.max {
		return false, w.start.Add(l.window).Sub(now)
	}
	w.n++
	return true, 0
}

// reset forgets key (a successful login clears its failures).
func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
