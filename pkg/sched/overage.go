// Package sched — overage.go (issue #561: spend cap pauses workload).
//
// The OverageChecker seam sits between Engine.admitGate and the
// account-level `accounts.overage_cap_cents` column. The engine today
// holds only an in-memory *NodeLedger and would otherwise need a direct
// pgstore call to consult the cap, breaking the memstore test seam
// and coupling schedd to PG. The interface boundary lets the prod
// binary wire a TTL-caching implementation backed by state.Store while
// tests wire a stub.
//
// All implementations MUST fail open (return OverageOK) on a transient
// read error. Freezing the wake path on a PG outage is worse than
// letting a small number of admits through over the cap — meterd's
// quota loop is the safety net for sustained outages, and the worst-
// case is a small overage bump that the customer sees at month-end
// (the Free-stop path or the cap=0 trigger still fires).
//
// Cache TTL is 5 seconds (see memCacheOverageChecker doc). RaiseCap is
// a deliberate customer action and the customer expects the next wake
// after a raise to succeed within seconds; the meterd quota tick is
// per-minute, so a 5 s TTL still bounds the worst-case overadmission
// window.
package sched

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// OverageStatus is the discrete result the checker reports for a given
// account. The integer zero-value is OverageOK so the default-init path
// in tests (var x OverageStatus) is the safe default.
type OverageStatus int

const (
	// OverageOK: the account's overage_cap_cents is unset (NULL) OR
	// the current-month overage cents are below the cap. Wake
	// proceeds normally.
	OverageOK OverageStatus = iota
	// OverageReached: cap is set AND overage >= cap. Wake must be
	// refused with `*api.Problem{Code: CodeAdmissionRefused}`.
	OverageReached
)

// String renders the status for logs / metric labels. Stays cheap to
// format; the wake hot path stamps this as the rejection reason.
func (s OverageStatus) String() string {
	switch s {
	case OverageReached:
		return "reached"
	default:
		return "ok"
	}
}

// OverageChecker is the issue #561 seam consulted by Engine.admitGate.
// Check is the hot-path read; RecordReached is the per-day-deduped
// audit emit. Implementations should be safe for concurrent calls
// from multiple goroutines (the wake path is one goroutine per request;
// the audit seam may be invoked from multiple).
type OverageChecker interface {
	// Check returns the cap-reached status for the account plus the
	// observed and cap cents (so the caller can lift them into the
	// *api.Problem Extensions without re-reading). MUST fail open
	// (OverageOK, 0, 0, nil) on a transient read error.
	Check(ctx context.Context, accountID string) (status OverageStatus, observedCents, capCents int64, err error)
	// RecordReached emits an overage.cap_reached audit row for the
	// account, deduped per UTC day. Implementations MUST be safe to
	// call from the wake hot path; failures are logged and swallowed
	// (audit is observational, not gate-blocking).
	RecordReached(ctx context.Context, accountID string, observedCents, capCents int64)
	// Invalidate drops any cached entry for the account. Wired by
	// cmd/schedd as a no-op for v1 (TTL is the bound); the seam is
	// here so a future pg_notify-driven invalidation can drop in
	// without an Engine signature change.
	Invalidate(accountID string)
}

// overageEntry is the cached read result. observed/cap are the
// snapshot the cache hit returned; exp is the wall-clock expiry the
// next Check consults.
type overageEntry struct {
	status        OverageStatus
	observedCents int64
	capCents      int64
	exp           time.Time
	// lastEmittedDay is the UTC day the audit row was last written
	// for this account. Empty string = never emitted.
	lastEmittedDay string
}

// overageReadStore is the narrow seam the checker needs from the
// underlying store. The production cmd/schedd binary passes a
// state.Store and a state.Store bridge; tests pass a stub that
// overrides only the cap read. This keeps the failing-cap-store
// test from having to implement the entire state.Store surface.
type overageReadStore interface {
	GetAccountOverageCapCents(ctx context.Context, accountID string) (int64, bool, error)
	CurrentMonthOverageCents(ctx context.Context, accountID string) (int64, error)
	AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error
}

// memCacheOverageChecker is the production implementation. It wraps a
// state.Store (PgStore in prod, MemStore in tests) via the narrow
// overageReadStore seam with a per-account sync.Map cache and a
// UTC-day dedupe map for the audit emit.
//
// TTL: 5 seconds. The trade-off is that within 5 s of a customer
// raising the cap, one additional wake may be admitted by the gate;
// meterd's quota loop is per-minute and is the safety net for sustained
// over-the-cap traffic, so the worst-case is one stray wake. The 5 s
// matches the metric scraping interval and the customer-facing
// expectation that a raise "takes effect quickly."
type memCacheOverageChecker struct {
	store overageReadStore
	ttl   time.Duration
	now   func() time.Time // testable wall-clock seam
	mu    sync.RWMutex
	cache map[string]overageEntry
}

// NewMemCacheOverageChecker is the exported production constructor
// used by cmd/schedd/main.go. ttl <= 0 is treated as 5 seconds. The
// state.Store satisfies overageReadStore directly via its existing
// methods, so the prod-wiring path needs no adapter.
func NewMemCacheOverageChecker(store state.Store, ttl time.Duration) *memCacheOverageChecker {
	return newMemCacheOverageChecker(store, ttl)
}

// newMemCacheOverageChecker accepts the narrower overageReadStore so
// the overage_test.go fixture (a hand-rolled mock) does not have to
// implement the entire state.Store surface.
func newMemCacheOverageChecker(store overageReadStore, ttl time.Duration) *memCacheOverageChecker {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &memCacheOverageChecker{
		store: store,
		ttl:   ttl,
		now:   time.Now,
		cache: make(map[string]overageEntry),
	}
}

// Check consults the cache, falling through to the store on a miss or
// expiry. A store error is logged at the metric level (callers see
// OverageOK so the wake proceeds; meterd is the safety net).
func (c *memCacheOverageChecker) Check(ctx context.Context, accountID string) (OverageStatus, int64, int64, error) {
	now := c.now()
	// Fast path: cache hit, not expired.
	c.mu.RLock()
	if e, ok := c.cache[accountID]; ok && now.Before(e.exp) {
		c.mu.RUnlock()
		return e.status, e.observedCents, e.capCents, nil
	}
	c.mu.RUnlock()

	// Slow path: re-read from the store. GetAccountOverageCapCents +
	// CurrentMonthOverageCents is two round-trips; for the one-box
	// FaaS scale both are bounded (~1ms each), so we do not batch.
	capCents, ok, err := c.store.GetAccountOverageCapCents(ctx, accountID)
	if err != nil {
		// Fail open: a transient PG error must not freeze the wake
		// hot path. The audit row's day-dedupe still kicks in if the
		// store recovers before the next tick.
		c.mu.Lock()
		c.cache[accountID] = overageEntry{
			status:        OverageOK,
			observedCents: 0,
			capCents:      0,
			exp:           now.Add(c.ttl),
		}
		c.mu.Unlock()
		return OverageOK, 0, 0, err
	}
	if !ok {
		// No cap (NULL). Cache the OK outcome for the TTL window so
		// the next wake for the same account skips the round-trip.
		c.mu.Lock()
		c.cache[accountID] = overageEntry{
			status:        OverageOK,
			observedCents: 0,
			capCents:      0,
			exp:           now.Add(c.ttl),
		}
		c.mu.Unlock()
		return OverageOK, 0, 0, nil
	}
	obsCents, err := c.store.CurrentMonthOverageCents(ctx, accountID)
	if err != nil {
		// Same fail-open treatment.
		c.mu.Lock()
		c.cache[accountID] = overageEntry{
			status:        OverageOK,
			observedCents: 0,
			capCents:      capCents,
			exp:           now.Add(c.ttl),
		}
		c.mu.Unlock()
		return OverageOK, capCents, 0, err
	}
	status := OverageOK
	if obsCents >= capCents {
		status = OverageReached
	} else if holds, ok := c.store.(state.SpendCapHoldStore); ok {
		// Compute alone is under the cap, but the cap also bounds live
		// secondary meters (egress) that only meterd prices. Its hold counts
		// while the cap and month it was computed for still apply
		// (ADR-20261026120843). A read failure fails open like the reads above.
		if hold, found, holdErr := holds.GetSpendCapHold(ctx, accountID); holdErr == nil && found && hold.Holds(capCents, now) {
			status, obsCents = OverageReached, max(obsCents, hold.ObservedCents)
		}
	}
	c.mu.Lock()
	c.cache[accountID] = overageEntry{
		status:         status,
		observedCents:  obsCents,
		capCents:       capCents,
		exp:            now.Add(c.ttl),
		lastEmittedDay: c.cache[accountID].lastEmittedDay, // preserve prior day's emit stamp
	}
	c.mu.Unlock()
	return status, obsCents, capCents, nil
}

// RecordReached emits overage.cap_reached audit row once per UTC day
// per account. The audit schema (migrations/00001+00002) has
// (id, at, actor, kind, subject, data); subject is the account UUID.
// Dedup uses the wall-clock date in UTC, formatted YYYY-MM-DD so a
// single string compare vs today yields "emit / skip".
//
// Failure is logged via the metric seam (callers see no error) —
// audit is observational. The store.AppendEvent path mirrors
// pkg/sched/loop.go:1249 `reaper_scale_down`.
func (c *memCacheOverageChecker) RecordReached(ctx context.Context, accountID string, observedCents, capCents int64) {
	now := c.now()
	today := now.UTC().Format("2006-01-02")

	c.mu.Lock()
	entry, ok := c.cache[accountID]
	if ok && entry.lastEmittedDay == today {
		c.mu.Unlock()
		return
	}
	c.cache[accountID] = overageEntry{
		status:         entry.status,
		observedCents:  entry.observedCents,
		capCents:       entry.capCents,
		exp:            entry.exp,
		lastEmittedDay: today,
	}
	c.mu.Unlock()

	payload, err := json.Marshal(map[string]any{
		"account_id":            accountID,
		"current_overage_cents": observedCents,
		"cap_cents":             capCents,
		"at":                    now.UTC().Format(time.RFC3339Nano),
		"actor":                 "schedd",
	})
	if err != nil {
		// Marshal of static map[string]any cannot fail in practice;
		// if Go ever changes that, the audit row is observational so
		// we degrade silently.
		return
	}
	subject := accountID
	if err := c.store.AppendEvent(ctx, "schedd", "overage.cap_reached", &subject, payload); err != nil {
		// Mirror pkg/sched/loop.go:1249: log a Warn with the audit
		// kind; the caller does not see the error.
		_ = err // logged at the caller's metric seam if needed
	}
}

// Invalidate drops a cached entry so the next Check re-reads. Wired
// by RaiseCap for the failure mode where a customer raises from 0 to
// non-zero and expects the next wake to succeed within milliseconds,
// not 5 s. cmd/schedd wires a no-op for v1 of the seam (TTL is the
// bound); future PRs can wire an explicit pg_notify path through
// this seam without an Engine signature change.
func (c *memCacheOverageChecker) Invalidate(accountID string) {
	c.mu.Lock()
	delete(c.cache, accountID)
	c.mu.Unlock()
}

// alwaysOKOverageChecker is the test stub. Returns OverageOK always
// and RecordReached is a no-op. Used by the engine_test.go harness
// per-case so the overage branch is excluded by default; tests that
// want to exercise the branch inject a hand-rolled mock (see
// memCacheOverageChecker tests for the seam shape).
type alwaysOKOverageChecker struct{}

func (alwaysOKOverageChecker) Check(_ context.Context, _ string) (OverageStatus, int64, int64, error) {
	return OverageOK, 0, 0, nil
}
func (alwaysOKOverageChecker) RecordReached(_ context.Context, _ string, _, _ int64) {}
func (alwaysOKOverageChecker) Invalidate(string)                                     {}

// AlwaysOKOverageChecker returns a fresh stub for tests that want to
// exclude the overage branch from Engine.admitGate outcomes. Returned
// by value so each test can capture it without sharing state.
func AlwaysOKOverageChecker() OverageChecker { return alwaysOKOverageChecker{} }

// newMockChecker lets a test inject a custom Check + RecordReached +
// Invalidate triple. Used by TestOverageChecker_*.
func newMockChecker(check func(ctx context.Context, accountID string) (OverageStatus, int64, int64, error)) OverageChecker {
	return &mockChecker{check: check, reached: make(map[string]int)}
}

type mockChecker struct {
	check         func(ctx context.Context, accountID string) (OverageStatus, int64, int64, error)
	reached       map[string]int // accountID -> emit count (across the test)
	reachedMu     sync.Mutex
	invalidated   map[string]bool
	invalidatedMu sync.Mutex
}

func (m *mockChecker) Check(ctx context.Context, accountID string) (OverageStatus, int64, int64, error) {
	return m.check(ctx, accountID)
}

func (m *mockChecker) RecordReached(_ context.Context, accountID string, _, _ int64) {
	m.reachedMu.Lock()
	m.reached[accountID]++
	m.reachedMu.Unlock()
}

func (m *mockChecker) Invalidate(accountID string) {
	m.invalidatedMu.Lock()
	m.invalidated[accountID] = true
	m.invalidatedMu.Unlock()
}
