// Package kafkalag tracks per-app Kafka consumer lag so the scheduler can
// scale on it (ADR-198).
//
// segmentio/kafka-go's Reader.Lag() returns -1 for a consumer-group reader
// and the poller always sets a GroupID, so lag cannot be queried — it can
// only be learned from the high_water_mark stamped on each fetched message.
// That makes lag a per-partition observation that arrives as a side effect of
// dispatching, which is what this package exists to accumulate.
//
// It is a LEAF package. The schedd loop (pkg/sched) writes samples and
// pkg/sched/targets reads them; targets cannot import pkg/sched, so the
// shared state has to sit below both. Same constraint that put the ADR-194
// arbiter in pkg/sched/scalesignal.
package kafkalag

import (
	"sync"
	"time"
)

// DefaultFreshness bounds how long a sample stands in for the current
// backlog.
//
// A frozen reading is the hazard this defends against: if a schedd stops
// dispatching a trigger — broker outage, crashed poller, revoked partition
// assignment — the last lag it saw never changes. Treating that as current
// would pin the fleet at whatever the backlog was when the world stopped,
// indefinitely, and bill for it. Two minutes is long enough to survive an
// idle topic between dispatch ticks and short enough that a wedged poller
// stops driving capacity well before a human notices.
const DefaultFreshness = 2 * time.Minute

type sample struct {
	lag int64
	at  time.Time
}

// Tracker accumulates the freshest lag sample per (app, partition).
//
// Safe for concurrent use: the dispatch loop writes while the targets
// trigger reads.
type Tracker struct {
	mu        sync.RWMutex
	freshness time.Duration
	// byApp[appID][partitionKey] is the newest sample seen for that
	// partition. Partition keys are opaque strings so a future source
	// with a compound identity (topic+partition) needs no schema change.
	byApp map[string]map[string]sample
}

// New returns a Tracker with the given freshness bound. Zero or negative
// selects DefaultFreshness.
func New(freshness time.Duration) *Tracker {
	if freshness <= 0 {
		freshness = DefaultFreshness
	}
	return &Tracker{
		freshness: freshness,
		byApp:     map[string]map[string]sample{},
	}
}

// Observe records the backlog remaining on one partition at time now.
//
// Callers pass the value consumerLagFor already computes — high water mark
// minus the consumed offset minus one — so the arithmetic has exactly one
// definition in the tree.
func (t *Tracker) Observe(appID, partition string, lag int64, now time.Time) {
	if t == nil || appID == "" || lag < 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	parts, ok := t.byApp[appID]
	if !ok {
		parts = map[string]sample{}
		t.byApp[appID] = parts
	}
	// Last write wins rather than max: a later fetch on the same
	// partition is a strictly better estimate of what is still behind,
	// and keeping the maximum would make a drained partition look busy
	// forever.
	parts[partition] = sample{lag: lag, at: now}
}

// LagForApp returns the app's total backlog across every partition with a
// fresh sample, and whether any fresh sample exists at all.
//
// A partition that has gone quiet is deliberately excluded rather than
// carried forward. Partitions stop producing records because they have been
// drained, so a drained partition contributes zero — dropping it from the
// sum is the accurate answer, not a gap in the data.
//
// The boolean is what ADR-194's arbiter needs: false means "no reading",
// which never scales the app and never suppresses a sibling signal that does
// have one.
func (t *Tracker) LagForApp(appID string, now time.Time) (int64, bool) {
	if t == nil {
		return 0, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	parts, ok := t.byApp[appID]
	if !ok || len(parts) == 0 {
		return 0, false
	}
	var total int64
	fresh := false
	for _, s := range parts {
		if now.Sub(s.at) > t.freshness {
			continue
		}
		fresh = true
		total += s.lag
	}
	if !fresh {
		return 0, false
	}
	return total, true
}

// Forget drops every sample for an app. Called when an app's Kafka triggers
// are removed or disabled, so a stale backlog cannot outlive the trigger that
// produced it.
func (t *Tracker) Forget(appID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.byApp, appID)
	t.mu.Unlock()
}

// Sweep drops apps whose every sample has expired. The freshness bound in
// LagForApp already makes stale samples invisible; this only reclaims the map
// entries, so it is a memory concern rather than a correctness one and can
// run on a slow timer.
func (t *Tracker) Sweep(now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for appID, parts := range t.byApp {
		for key, s := range parts {
			if now.Sub(s.at) > t.freshness {
				delete(parts, key)
			}
		}
		if len(parts) == 0 {
			delete(t.byApp, appID)
		}
	}
}
