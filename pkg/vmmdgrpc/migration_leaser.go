// migration_leaser.go — Leaser-shaped adapter for the per-vmmd
// migration tracker (Workstream B / issue #1184 / Task #63 /
// ADR-137).
//
// Today this is an in-memory adapter around the existing
// migrationTracker. The Task #63 goal is to give cmd/vmmd a
// single lease primitive it can swap: today the in-memory
// tracker works but loses state across vmmd restart; the PG-
// backed variant (staged for a follow-up migration) survives
// restarts. Wrapping the existing tracker as Leaser[T] keeps
// cmd/vmmd wiring uniform with cmd/schedd's job leaser; the
// swap to PG is a constructor change only.
//
// Why the adapter lives here rather than in pkg/sched: vmmdgrpc
// already owns the tracker (migration_handlers.go). Importing
// pkg/sched from pkg/vmmdgrpc for a one-method interface would
// couple the two packages unnecessarily; the Leaser-shaped
// surface here is a thin projection that satisfies the same
// Acquire / Renew / Release / Lookup contract.
//
// Why in-memory today: the per-vmmd restart loss is bounded by
// the vmmd lifetime (vmmd runs as a long-lived daemon under
// systemd). A vmmd that loses state will see a new owner send
// Phase-3 with a lease_token the local tracker never issued;
// Phase-3 returns errNoLease, the new owner retries with the
// Phase-1 ack timeout, and the migration lands cleanly. The
// PG-backed variant eliminates even that small window.
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

// NewMigrationLeaser returns a sched.Leaser[any] backed by the
// per-vmmd in-memory migration tracker. cmd/vmmd wires this as
// the migration lease source today; a follow-up constructor
// (NewPGMigrationLeaser) lands in the same slot when migration
// 00586+ stages the PG-backed variant.
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

// Renew is the in-memory no-op. The TTL is fixed at Acquire
// time (tracker.put stamps leaseExpiresAt from the policy);
// Renew just verifies the entry still exists so callers get a
// clear error if vmmd restart already lost the lease.
func (l *migrationLeaser) Renew(ctx context.Context, token sched.LeaseToken, ownerID string, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	lt := string(token)
	for _, m := range l.tracker.state {
		if m.leaseToken == lt {
			if time.Now().UTC().After(m.leaseExpiresAt) {
				return sched.ErrLeaseExpired
			}
			return nil
		}
	}
	return sched.ErrLeaseNotFound
}

// Release deletes the tracker entry. Idempotent: a second
// Release on the same token returns sched.ErrLeaseNotFound,
// which the sched.Leaser contract explicitly tolerates.
func (l *migrationLeaser) Release(ctx context.Context, token sched.LeaseToken, ownerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	lt := string(token)
	for instanceID, m := range l.tracker.state {
		if m.leaseToken == lt {
			l.tracker.delete(instanceID)
			return nil
		}
	}
	return sched.ErrLeaseNotFound
}

// Lookup is the read-only audit/idempotency check. Returns the
// instance key, lease_expires_at, owner_id, and ok=true if the
// token is still held.
func (l *migrationLeaser) Lookup(ctx context.Context, token sched.LeaseToken) (key string, expiresAt time.Time, ownerID string, ok bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lt := string(token)
	for instanceID, m := range l.tracker.state {
		if m.leaseToken == lt {
			if time.Now().UTC().After(m.leaseExpiresAt) {
				return instanceID, m.leaseExpiresAt, "", false, nil
			}
			return instanceID, m.leaseExpiresAt, "vmmd-local", true, nil
		}
	}
	return "", time.Time{}, "", false, nil
}
