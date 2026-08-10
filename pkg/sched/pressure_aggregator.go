package sched

import (
	"sort"
	"sync"
	"time"
)

// PressureAggregator (Tier A9 / ADR-087) is the in-process
// sliding-window per-app counter of WakeResult{AtCapacity: true}
// returns. The engine increments it at every AtCapacity return
// site via Engine.IncAtCapacity; the pressure-rebalancer polls
// PressuredApps on each sweep. In-process (not pg_notify-driven)
// because AtCapacity is a per-call outcome, not a row-write
// event — a pg_notify consumer would need to write every
// AtCapacity event to a table first, doubling the write path.
//
// Threading: a single sync.Mutex guards every per-app event
// slice. The slices are bounded by an event-count cap (window
// events + slack) so the aggregator's memory footprint is
// proportional to the number of apps currently pressured, not
// the lifetime wake count. The cap is conservative (a 60s window
// at 1000 events/s degrades to 60k stored events per app max —
// well below the per-schedd memory budget).
type PressureAggregator struct {
	// window is the sliding-window duration. Fixed at 60s — the
	// "events per 60s" surface is the stable cross-version
	// contract the §12 dashboard panel reads. Tunable in tests
	// only via NewPressureAggregatorForTest.
	window time.Duration
	// maxEventsPerApp caps the per-app stored-event slice so a
	// pathological flood (a misconfigured customer's 1M wakes/s
	// app) can't OOM the schedd. The cap is high enough that no
	// realistic Prestne customer bypasses PressuredApps.
	maxEventsPerApp int
	// now is the clock seam; production wires time.Now, tests
	// inject a frozen clock.
	now func() time.Time

	// mu guards events. Reads and writes are O(1) amortised; the
	// per-sweep PressuredApps call is O(apps × window-events) in
	// the worst case, but the per-app event count is bounded by
	// maxEventsPerApp so the per-sweep cost is bounded by
	// O(apps × maxEventsPerApp). Typical fleet: hundred apps, a
	// handful pressured per sweep — trivial.
	mu     sync.Mutex
	events map[string][]time.Time
}

// NewPressureAggregator returns the production aggregator. The
// window is fixed at 60s and the per-app event cap is 100_000
// (orders of magnitude above the threshold window — a flooded
// customer that genuinely sustains 1000 events/s for 60s
// re-trips the threshold 1000x, and the per-app cap simply
// stops tracking the absolute count past that point).
func NewPressureAggregator() *PressureAggregator {
	return NewPressureAggregatorForTest(60*time.Second, 100_000, time.Now)
}

// NewPressureAggregatorForTest is the test-constructor: window
// + maxEventsPerApp + now are all injectable. The sweep test
// uses window=1s and now=frozenClock to exercise the
// sliding-window GC without sleeping.
func NewPressureAggregatorForTest(window time.Duration, maxEventsPerApp int, now func() time.Time) *PressureAggregator {
	if window <= 0 {
		panic("sched: NewPressureAggregatorForTest: window must be > 0")
	}
	if maxEventsPerApp <= 0 {
		panic("sched: NewPressureAggregatorForTest: maxEventsPerApp must be > 0")
	}
	if now == nil {
		panic("sched: NewPressureAggregatorForTest: now must not be nil")
	}
	return &PressureAggregator{
		window:          window,
		maxEventsPerApp: maxEventsPerApp,
		now:             now,
		events:          make(map[string][]time.Time),
	}
}

// IncAtCapacity records an AtCapacity event for appID at the
// supplied time. The per-app slice is pruned to size
// maxEventsPerApp by dropping the oldest entry on overflow. The
// per-app slice is also pruned of entries outside the window
// (lazy GC on every write) so PressuredApps is fast on the
// sweep path.
func (a *PressureAggregator) IncAtCapacity(appID string, t time.Time) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.events == nil {
		a.events = make(map[string][]time.Time)
	}
	slice := a.events[appID]
	// Drop everything strictly older than t-window.
	cutoff := t.Add(-a.window)
	keep := slice[:0]
	for _, ts := range slice {
		if ts.After(cutoff) || ts.Equal(cutoff) {
			keep = append(keep, ts)
		}
	}
	slice = append(keep, t)
	// Cap on overflow: drop the oldest. The cap is extreme
	// (100k in production) so this branch is unreachable on a
	// realistic fleet; tests prove the cap behaviour.
	if len(slice) > a.maxEventsPerApp {
		slice = slice[len(slice)-a.maxEventsPerApp:]
	}
	a.events[appID] = slice
}

// Count returns the number of AtCapacity events recorded for
// appID within the window ending at t. Reads the per-app slice
// inline (no allocation) — the engine's per-tick debug path may
// call this; the watcher does not (it uses PressuredApps).
func (a *PressureAggregator) Count(appID string, t time.Time) int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	slice := a.events[appID]
	if len(slice) == 0 {
		return 0
	}
	cutoff := t.Add(-a.window)
	count := 0
	for _, ts := range slice {
		if ts.After(cutoff) || ts.Equal(cutoff) {
			count++
		}
	}
	return count
}

// PressuredApps returns the deterministic app-id list whose
// event count over the window ending at t is >= threshold. The
// output is sorted by app id ASC so the watcher sweeps in a
// stable order — prevents rare per-sweep-ordering drift between
// the metric label and the per-app work loop. Per-app slices
// are pruned to the window inline (the GC is cheap; we walk
// each app at most once per sweep).
func (a *PressureAggregator) PressuredApps(threshold int, t time.Time) []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := t.Add(-a.window)
	var out []string
	for appID, slice := range a.events {
		// Inline pruning: drop entries outside the window.
		keep := slice[:0]
		for _, ts := range slice {
			if ts.After(cutoff) || ts.Equal(cutoff) {
				keep = append(keep, ts)
			}
		}
		if len(keep) == 0 {
			delete(a.events, appID)
			continue
		}
		a.events[appID] = keep
		if len(keep) >= threshold {
			out = append(out, appID)
		}
	}
	sort.Strings(out)
	return out
}

// Reset clears the per-app event slice. Called by the engine
// after a successful reassign so the freshly-migrated app
// doesn't re-trip the threshold on the next sweep (the new
// owner may be at lower pressure — let the counter rebuild from
// scratch).
func (a *PressureAggregator) Reset(appID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.events, appID)
}

// ResetAll clears every per-app event slice. Called by the
// engine on shutdown (the watcher goroutine is bounded by ctx
// cancel, but a graceful shutdown path is cleaner with an
// explicit drop).
func (a *PressureAggregator) ResetAll() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = make(map[string][]time.Time)
}

// Now returns the aggregator's current clock value. Exposed so
// sibling components (the pressure-rebalancer watcher, the
// engine's debug paths) can anchor windowed reads to the same
// clock the aggregator uses internally — production wires
// time.Now, tests inject a frozen clock; reading wall-clock
// time.Now in a watcher would silently bypass the test seam
// and GC every seeded event once the wall clock advances past
// the window.
func (a *PressureAggregator) Now() time.Time {
	if a == nil {
		return time.Time{}
	}
	return a.now()
}
