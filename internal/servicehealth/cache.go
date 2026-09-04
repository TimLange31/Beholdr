package servicehealth

import (
	"context"
	"sync"
	"time"
)

// reportCache collapses concurrent and repeated evaluations of the same
// (workload, window) into one Prometheus round trip.
//
// Two things make this necessary rather than an optimisation. The UI polls the
// metrics endpoint on a timer, once per open browser tab, and Beholdr has no
// authentication of its own — whatever sits in front of the ingress decides who
// reaches it, and any of them can hold the page open. And a single uncached
// report is ten or more range and instant queries. Without this, a handful of
// engineers watching the same services during an incident is a sustained load
// spike on the Prometheus they are relying on to diagnose it.
type reportCache struct {
	ttl        time.Duration
	maxEntries int

	mu       sync.Mutex
	entries  map[string]*cacheEntry
	inflight map[string]*flight
}

type cacheEntry struct {
	report  Report
	expires time.Time
}

// flight is one in-progress evaluation. Late arrivals wait on done rather than
// starting a second identical evaluation.
type flight struct {
	done   chan struct{}
	report Report
	err    error
}

func newReportCache(ttl time.Duration, maxEntries int) *reportCache {
	return &reportCache{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    map[string]*cacheEntry{},
		inflight:   map[string]*flight{},
	}
}

func (c *reportCache) do(ctx context.Context, key string, evaluate func(context.Context) (Report, error)) (Report, error) {
	if c == nil || c.ttl < 0 {
		return evaluate(ctx)
	}

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && time.Now().Before(entry.expires) {
		report := entry.report
		c.mu.Unlock()
		return report, nil
	}
	if existing, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-existing.done:
			return existing.report, existing.err
		case <-ctx.Done():
			// This caller gave up; the shared evaluation continues for the
			// others rather than being cancelled out from under them.
			return Report{}, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	// The evaluation deliberately does not inherit this caller's context: it is
	// shared, so one client disconnecting must not cancel the work the others
	// are waiting on. Timeouts come from the Prometheus client instead.
	f.report, f.err = evaluate(context.WithoutCancel(ctx))
	close(f.done)

	c.mu.Lock()
	delete(c.inflight, key)
	if f.err == nil {
		c.evictLocked()
		c.entries[key] = &cacheEntry{report: f.report, expires: time.Now().Add(c.ttl)}
	}
	c.mu.Unlock()

	return f.report, f.err
}

// evictLocked keeps the map bounded. Expired entries go first; if that is not
// enough, the soonest-to-expire entries are dropped. The key space is
// (workloads × windows), so this is a guard against an unbounded cluster rather
// than a hot-path concern.
func (c *reportCache) evictLocked() {
	if len(c.entries) < c.maxEntries {
		return
	}
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
	for len(c.entries) >= c.maxEntries {
		var oldestKey string
		var oldest time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.expires.Before(oldest) {
				oldestKey, oldest = key, entry.expires
			}
		}
		if oldestKey == "" {
			return
		}
		delete(c.entries, oldestKey)
	}
}
