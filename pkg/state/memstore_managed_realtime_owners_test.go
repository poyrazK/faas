package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreManagedRealtimeOwnerLeaseLifecycle(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	first, err := m.ClaimManagedRealtimeConnectionOwner(ctx, "conn-1", "endpoint-1", "node-a", time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if first.LeaseToken == "" || first.NodeID != "node-a" {
		t.Fatalf("claim returned %+v", first)
	}
	if _, err := m.ClaimManagedRealtimeConnectionOwner(ctx, "conn-1", "endpoint-1", "node-b", time.Minute); !errors.Is(err, ErrManagedRealtimeOwnerConflict) {
		t.Fatalf("competing claim = %v, want conflict", err)
	}
	got, err := m.GetManagedRealtimeConnectionOwner(ctx, "conn-1", "endpoint-1")
	if err != nil || got.LeaseToken != first.LeaseToken {
		t.Fatalf("get = %+v, %v", got, err)
	}
	renewed, err := m.RenewManagedRealtimeConnectionOwner(ctx, "conn-1", "endpoint-1", first.LeaseToken, 2*time.Minute)
	if err != nil || !renewed.LeaseExpiresAt.After(first.LeaseExpiresAt) {
		t.Fatalf("renew = %+v, %v", renewed, err)
	}
	if _, err := m.RenewManagedRealtimeConnectionOwner(ctx, "conn-1", "endpoint-1", "wrong", time.Minute); !errors.Is(err, ErrManagedRealtimeOwnerConflict) {
		t.Fatalf("wrong-token renew = %v, want conflict", err)
	}
	if err := m.ReleaseManagedRealtimeConnectionOwner(ctx, "conn-1", first.LeaseToken); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := m.GetManagedRealtimeConnectionOwner(ctx, "conn-1", "endpoint-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after release = %v, want not found", err)
	}
}

func TestMemStoreManagedRealtimeOwnerPruneExpired(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.ClaimManagedRealtimeConnectionOwner(ctx, "expired-1", "endpoint-1", "node-a", time.Millisecond); err != nil {
		t.Fatalf("claim expired-1: %v", err)
	}
	if _, err := m.ClaimManagedRealtimeConnectionOwner(ctx, "expired-2", "endpoint-1", "node-a", time.Millisecond); err != nil {
		t.Fatalf("claim expired-2: %v", err)
	}
	if _, err := m.ClaimManagedRealtimeConnectionOwner(ctx, "live", "endpoint-1", "node-a", time.Minute); err != nil {
		t.Fatalf("claim live: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	removed, err := m.PruneExpiredManagedRealtimeConnectionOwners(ctx, 1)
	if err != nil || removed != 1 {
		t.Fatalf("prune limit=1 = (%d, %v), want (1, nil)", removed, err)
	}
	removed, err = m.PruneExpiredManagedRealtimeConnectionOwners(ctx, 10)
	if err != nil || removed != 1 {
		t.Fatalf("prune remainder = (%d, %v), want (1, nil)", removed, err)
	}
	if _, err := m.GetManagedRealtimeConnectionOwner(ctx, "live", "endpoint-1"); err != nil {
		t.Fatalf("live owner lookup = %v, want present", err)
	}
	if _, err := m.PruneExpiredManagedRealtimeConnectionOwners(ctx, 0); err == nil {
		t.Fatal("prune limit=0 unexpectedly succeeded")
	}
}
