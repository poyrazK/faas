package gateway

// Durable edge health (ADR-641, H5-6). The wake outcome a gateway observes is
// process-local, so a restarted gateway, or a compute gateway that did not run
// the app's last wake, answered 503 "last wake status: unknown" for every
// parked app until real traffic woke it. The app's most recent instance is
// the outcome every gateway can see.

import (
	"context"
	"sync"
	"time"
)

// HealthOutcomeLookup returns the state of the app's most recently started
// instance; found is false when the app has never had one.
type HealthOutcomeLookup func(ctx context.Context, appID string) (state string, found bool, err error)

const (
	// healthOutcomeTTL bounds the lookup to one query per app per gateway in
	// each window, whatever the monitor cadence.
	healthOutcomeTTL = 15 * time.Second
	// healthOutcomeTimeout keeps a slow database from stalling a probe.
	healthOutcomeTimeout = 2 * time.Second
)

type healthOutcomeEntry struct {
	snapshot healthSnapshot
	ok       bool
	at       time.Time
}

type healthOutcomeCache struct {
	lookup HealthOutcomeLookup
	now    func() time.Time
	mu     sync.Mutex
	byApp  map[string]healthOutcomeEntry
}

// WithHealthOutcomeLookup arms the durable edge health source. nil keeps the
// process-local wake outcome only.
func (h *Handler) WithHealthOutcomeLookup(lookup HealthOutcomeLookup) *Handler {
	if lookup == nil {
		h.healthOutcomes = nil
		return h
	}
	h.healthOutcomes = &healthOutcomeCache{lookup: lookup, now: time.Now, byApp: map[string]healthOutcomeEntry{}}
	return h
}

// snapshotFor maps the last instance state to an edge answer. Only a FAILED
// instance is unhealthy: a parked or stopped app wakes on its next request.
// ok is false when there is no instance or the lookup failed, so the caller
// falls back to the local wake outcome.
func (c *healthOutcomeCache) snapshotFor(ctx context.Context, appID string) (healthSnapshot, bool) {
	if c == nil || appID == "" {
		return healthSnapshot{}, false
	}
	now := c.now()
	c.mu.Lock()
	entry, cached := c.byApp[appID]
	c.mu.Unlock()
	if cached && now.Sub(entry.at) < healthOutcomeTTL {
		return entry.snapshot, entry.ok
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), healthOutcomeTimeout)
	defer cancel()
	instanceState, found, err := c.lookup(lookupCtx, appID)
	entry = healthOutcomeEntry{at: now}
	switch {
	case err != nil || !found:
	case instanceState == "failed":
		entry.snapshot, entry.ok = healthSnapshot{reason: "last_instance_failed"}, true
	default:
		entry.snapshot, entry.ok = healthSnapshot{ready: true, reason: instanceState}, true
	}
	c.mu.Lock()
	c.byApp[appID] = entry
	c.mu.Unlock()
	return entry.snapshot, entry.ok
}
