// migration_leaser_test.go — coverage for the Leaser[T]-shaped
// adapter around the per-vmmd migration tracker (Task #63).
// Pins the Acquire / Lookup / Renew / Release surface so a
// future PG-backed variant can drop in as a constructor change.
package vmmdgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// TestMigrationLeaser_AcquireLookupRelease — the happy-path
// surface cmd/vmmd uses to mint, audit, and free a migration
// lease token.
func TestMigrationLeaser_AcquireLookupRelease(t *testing.T) {
	tracker := newMigrationTracker()
	l := NewMigrationLeaser(tracker)
	policy := sched.LeasePolicy{TTL: 60 * time.Second}

	token, _, err := l.Acquire(context.Background(), "i-1", policy, "vmmd-local")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if token == "" {
		t.Fatal("Acquire returned empty token")
	}
	// Lookup must see the lease.
	key, exp, owner, ok, err := l.Lookup(context.Background(), token)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ok {
		t.Errorf("Lookup ok=false; want true")
	}
	if key != "i-1" {
		t.Errorf("Lookup key=%q, want i-1", key)
	}
	if owner == "" {
		t.Errorf("Lookup owner is empty; want vmmd-local")
	}
	if exp.IsZero() {
		t.Errorf("Lookup expires_at is zero; want lease-policy TTL anchor")
	}
	// Release.
	if err := l.Release(context.Background(), token, "vmmd-local"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Second Release must surface sched.ErrLeaseNotFound (the
	// Leaser contract's idempotency guarantee — not a panic).
	if err := l.Release(context.Background(), token, "vmmd-local"); err != sched.ErrLeaseNotFound {
		t.Errorf("second Release err=%v, want sched.ErrLeaseNotFound", err)
	}
}

// TestMigrationLeaser_InvalidPolicy — Acquire rejects zero-TTL
// so callers can't mint a lease that immediately expires.
func TestMigrationLeaser_InvalidPolicy(t *testing.T) {
	tracker := newMigrationTracker()
	l := NewMigrationLeaser(tracker)
	_, _, err := l.Acquire(context.Background(), "i-1", sched.LeasePolicy{}, "vmmd-local")
	if err == nil {
		t.Errorf("Acquire(zero TTL) = nil err; want ErrInvalidLeasePolicy")
	}
}

// TestMigrationLeaser_LookupUnknown — unknown token returns
// ok=false without surfacing an error (the audit-log-friendly
// path).
func TestMigrationLeaser_LookupUnknown(t *testing.T) {
	tracker := newMigrationTracker()
	l := NewMigrationLeaser(tracker)
	_, _, _, ok, err := l.Lookup(context.Background(), sched.LeaseToken("does-not-exist"))
	if err != nil {
		t.Errorf("Lookup err=%v, want nil", err)
	}
	if ok {
		t.Errorf("Lookup ok=true for unknown token; want false")
	}
}