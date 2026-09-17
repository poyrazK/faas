// migration_leaser.go — Leaser-shaped adapter for the per-vmmd
// migration tracker (Workstream B / issue #1184 / Task #63 /
// ADR-137).
//
// This is the hot-cache adapter around the existing migrationTracker. The
// source-side lease record is persisted by state.MigrationLeaseStore; keeping
// the cache here avoids a database round-trip for the paused-VM handle while
// the durable row supplies restart recovery.
//
// Why the adapter lives here rather than in pkg/sched: vmmdgrpc
// already owns the tracker (migration_handlers.go). Importing
// pkg/sched from pkg/vmmdgrpc for a one-method interface would
// couple the two packages unnecessarily; the Leaser-shaped
// surface here is a thin projection that satisfies the same
// Acquire / Renew / Release / Lookup contract.
//
// The cache is intentionally process-local; restart recovery is handled by
// the durable migration_leases row in the Server phase handlers and expiry
// loop. This adapter remains useful to callers that need the common lease
// shape without a database-backed VM handle.
package vmmdgrpc

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// migrationLeaser wraps *migrationTracker as sched.Leaser[any].
// Per-instance state lives in the tracker; the Leaser methods
// map Acquire → tracker.put, Lookup → tracker.get, Release →
// tracker.delete. Renew is a no-op for the in-memory tracker
// because the lease TTL is fixed at lease-token mint time; a
// future PG-backed variant will support TTL extension.
type migrationLeaser struct {
	tracker *migrationTracker
	mu      sync.Mutex // serialises Acquire; tracker has its own mutex per-method
}

// NewMigrationLeaser returns the hot-cache lease adapter. Production vmmd
// pairs it with state.MigrationLeaseStore through Server.WithMigrationStore;
// unit-test callers can continue to use the cache by itself.
func NewMigrationLeaser(tracker *migrationTracker) sched.Leaser[any] {
	return &migrationLeaser{tracker: tracker}
}

// Acquire mints a new lease for the given instance key and
// inserts a tracker entry. The handle returned (T = any) is the
// tracker entry itself, wrapped in a map-friendly form so
// callers that need the lease token can read it back. Today the
// sched lease API doesn't surface the handle; the migration
// path reads leaseToken via a separate field.
func (l *migrationLeaser) Acquire(ctx context.Context, key string, policy sched.LeasePolicy, ownerID string) (sched.LeaseToken, any, error) {
	if err := policy.Validate(); err != nil {
		return "", nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	m := &activeMigration{
		instanceID:     key,
		leaseToken:     mintLeaseToken(),
		createdAt:      now,
		leaseExpiresAt: now.Add(policy.TTL),
	}
	if err := l.tracker.put(m); err != nil {
		return "", nil, err
	}
	return sched.LeaseToken(m.leaseToken), m, nil
}

// Renew verifies the in-memory entry. Migration leases use a fixed TTL from
// Phase 1 rather than extending a paused-VM lease; durable expiry cleanup is
// the recovery path after a restart.
func (l *migrationLeaser) Renew(ctx context.Context, token sched.LeaseToken, ownerID string, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.tracker.findByLeaseToken(string(token))
	if ok {
		if time.Now().UTC().After(m.leaseExpiresAt) {
			return sched.ErrLeaseExpired
		}
		return nil
	}
	return sched.ErrLeaseNotFound
}

// Release deletes the tracker entry. Idempotent: a second
// Release on the same token returns sched.ErrLeaseNotFound,
// which the sched.Leaser contract explicitly tolerates.
func (l *migrationLeaser) Release(ctx context.Context, token sched.LeaseToken, ownerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tracker.deleteByLeaseToken(string(token)) {
		return nil
	}
	return sched.ErrLeaseNotFound
}

// Lookup is the read-only audit/idempotency check. Returns the
// instance key, lease_expires_at, owner_id, and ok=true if the
// token is still held.
func (l *migrationLeaser) Lookup(ctx context.Context, token sched.LeaseToken) (key string, expiresAt time.Time, ownerID string, ok bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, found := l.tracker.findByLeaseToken(string(token))
	if found {
		if time.Now().UTC().After(m.leaseExpiresAt) {
			return m.instanceID, m.leaseExpiresAt, "", false, nil
		}
		return m.instanceID, m.leaseExpiresAt, "vmmd-local", true, nil
	}
	return "", time.Time{}, "", false, nil
}
