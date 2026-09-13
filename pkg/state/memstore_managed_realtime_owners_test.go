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
