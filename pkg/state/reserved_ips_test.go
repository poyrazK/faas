package state

// adr: 171 — provider-neutral reserved public IP lease state and assignment lifecycle.

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/networkip"
)

func TestMemStoreReservedIPLeaseLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	accountID := uuid.NewString()
	appID := uuid.NewString()
	lease, err := store.UpsertReservedIP(ctx, ReservedIP{
		AccountID: accountID,
		Region:    "fra1",
		Address:   netip.MustParseAddr("198.51.100.24"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != networkip.StatusAvailable || lease.Generation != 0 {
		t.Fatalf("created lease = %#v", lease)
	}

	assigned, err := store.AssignReservedIP(ctx, accountID, lease.ID, appID, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Status != networkip.StatusPending || assigned.AppID != appID || assigned.Generation != 1 {
		t.Fatalf("assigned lease = %#v", assigned)
	}
	assigned, err = store.UpdateReservedIPStatus(ctx, accountID, lease.ID, networkip.StatusAssigned, "route ready", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Status != networkip.StatusAssigned || assigned.StatusDetail != "route ready" || assigned.Generation != 2 {
		t.Fatalf("ready lease = %#v", assigned)
	}
	if err := store.ReleaseReservedIP(ctx, accountID, lease.ID, appID); err != nil {
		t.Fatal(err)
	}
	released, err := store.GetReservedIP(ctx, accountID, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != networkip.StatusAvailable || released.AppID != "" || released.Generation != 3 {
		t.Fatalf("released lease = %#v", released)
	}
}

func TestMemStoreReservedIPLeaseConflictsAndRegionFilter(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	accountID := uuid.NewString()
	first, err := store.UpsertReservedIP(ctx, ReservedIP{AccountID: accountID, Region: "fra1", Address: netip.MustParseAddr("203.0.113.10")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertReservedIP(ctx, ReservedIP{AccountID: uuid.NewString(), Region: "ams1", Address: first.Address}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate address error = %v, want conflict", err)
	}
	if _, err := store.UpsertReservedIP(ctx, ReservedIP{AccountID: accountID, Region: "fra1", Address: netip.MustParseAddr("10.0.0.2")}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("private address error = %v, want invalid argument", err)
	}
	second, err := store.UpsertReservedIP(ctx, ReservedIP{AccountID: accountID, Region: "ams1", Address: netip.MustParseAddr("203.0.113.11")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignReservedIP(ctx, accountID, first.ID, uuid.NewString(), "node-a"); err != nil {
		t.Fatal(err)
	}
	otherApp := uuid.NewString()
	if _, err := store.AssignReservedIP(ctx, accountID, second.ID, otherApp, "node-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignReservedIP(ctx, accountID, first.ID, otherApp, "node-c"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reassign owned lease error = %v, want conflict", err)
	}
	leases, err := store.ListReservedIPs(ctx, accountID, "fra1")
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].ID != first.ID {
		t.Fatalf("fra1 leases = %#v", leases)
	}
}
