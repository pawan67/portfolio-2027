package metrics

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Counter tracks served requests so the panel can show a request rate.
type Counter struct{ n atomic.Uint64 }

// Middleware counts every request that reaches the handler.
func (c *Counter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.n.Add(1)
		next.ServeHTTP(w, r)
	})
}

// Total returns the count since process start.
func (c *Counter) Total() uint64 { return c.n.Load() }

// Hub samples once per interval and fans the result out to every connected
// browser. Sampling is centralised deliberately: CPU percent is a delta between
// consecutive /proc/stat reads, so per-connection sampling would give each
// client a different and wrong answer.
type Hub struct {
	collector *Collector
	counter   *Counter
	interval  time.Duration
	maxSubs   int

	mu        sync.Mutex
	subs      map[chan Snapshot]struct{}
	latest    Snapshot
	hasLatest bool
	prevTotal uint64
	prevAt    time.Time
}

// NewHub wires a collector and request counter to a broadcast loop.
func NewHub(collector *Collector, counter *Counter, interval time.Duration, maxSubs int) *Hub {
	return &Hub{
		collector: collector,
		counter:   counter,
		interval:  interval,
		maxSubs:   maxSubs,
		subs:      map[chan Snapshot]struct{}{},
	}
}

// Run samples until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	// Establish the CPU and request-rate baselines without publishing them.
	// CPU percent is a delta between two /proc/stat reads, so the very first
	// read has nothing to compare against and would report a confident 0%.
	// Better to show the skeleton for one interval than a wrong number.
	h.prime(time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.sample(now)
		}
	}
}

// prime reads once to set the deltas, discarding the result.
func (h *Hub) prime(now time.Time) {
	total := h.counter.Total()
	h.collector.Collect(now, total, 0)

	h.mu.Lock()
	h.prevTotal, h.prevAt = total, now
	h.mu.Unlock()
}

func (h *Hub) sample(now time.Time) {
	total := h.counter.Total()

	h.mu.Lock()
	perSec := 0.0
	if !h.prevAt.IsZero() {
		if elapsed := now.Sub(h.prevAt).Seconds(); elapsed > 0 {
			perSec = round1(float64(total-h.prevTotal) / elapsed)
		}
	}
	h.prevTotal, h.prevAt = total, now
	h.mu.Unlock()

	snapshot := h.collector.Collect(now, total, perSec)

	h.mu.Lock()
	h.latest, h.hasLatest = snapshot, true
	for ch := range h.subs {
		// Never block the sampler on a slow reader: drop this tick for them.
		// The next one is two seconds away and carries the same shape of data.
		select {
		case ch <- snapshot:
		default:
		}
	}
	h.mu.Unlock()
}

// Latest returns the most recent sample, if there has been one.
func (h *Hub) Latest() (Snapshot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.latest, h.hasLatest
}

// Subscribe registers a listener. The returned function must be called to
// release it. It reports false when the hub is at capacity.
func (h *Hub) Subscribe() (<-chan Snapshot, func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.subs) >= h.maxSubs {
		return nil, nil, false
	}

	// Buffered so a subscriber that is mid-write does not miss the next tick.
	ch := make(chan Snapshot, 1)
	h.subs[ch] = struct{}{}

	var once sync.Once
	release := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
	return ch, release, true
}

// Subscribers reports the current listener count.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
