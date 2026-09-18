package state

// adr: 184 — operator-managed reserved public IP inventory and tenant claims.

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/networkip"
)

func TestMemStoreReservedIPInventoryClaimLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	accountID := uuid.NewString()
	otherAccountID := uuid.NewString()
	inventory, err := store.UpsertReservedIPInventory(ctx, ReservedIPInventory{
		Region:      "fra1",
		Address:     netip.MustParseAddr("198.51.100.88"),
		ProviderRef: "provider-ip-88",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Status != networkip.InventoryAvailable || inventory.LeaseID != "" {
		t.Fatalf("created inventory = %#v", inventory)
	}

	lease, err := store.ClaimReservedIP(ctx, accountID, inventory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != networkip.StatusAvailable || lease.AccountID != accountID || lease.Address != inventory.Address {
		t.Fatalf("claimed lease = %#v", lease)
	}
	claimed, err := store.GetReservedIPInventory(ctx, inventory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != networkip.InventoryClaimed || claimed.LeaseID != lease.ID {
		t.Fatalf("claimed inventory = %#v", claimed)
	}
	if retry, err := store.ClaimReservedIP(ctx, accountID, inventory.ID); err != nil || retry.ID != lease.ID {
		t.Fatalf("idempotent claim = %#v, %v", retry, err)
	}
	if _, err := store.ClaimReservedIP(ctx, otherAccountID, inventory.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-account claim error = %v, want conflict", err)
	}

	appID := uuid.NewString()
	if _, err := store.AssignReservedIP(ctx, accountID, lease.ID, appID, "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseReservedIPClaim(ctx, accountID, lease.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("release assigned claim error = %v, want conflict", err)
	}
	// The connector clears the workload assignment before the tenant releases
	// its claim back to the operator pool.
	if err := store.ReleaseReservedIP(ctx, accountID, lease.ID, appID); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseReservedIPClaim(ctx, accountID, lease.ID); err != nil {
		t.Fatal(err)
	}
	released, err := store.GetReservedIPInventory(ctx, inventory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != networkip.InventoryAvailable || released.LeaseID != "" {
		t.Fatalf("released inventory = %#v", released)
	}
	if _, err := store.GetReservedIP(ctx, accountID, lease.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released lease lookup = %v, want not found", err)
	}
}

func TestMemStoreReservedIPInventoryValidationAndFilters(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	first, err := store.UpsertReservedIPInventory(ctx, ReservedIPInventory{Region: "fra1", Address: netip.MustParseAddr("203.0.113.90"), ProviderRef: "provider-ip-90"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertReservedIPInventory(ctx, ReservedIPInventory{Region: "ams1", Address: first.Address, ProviderRef: "provider-ip-other"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate address error = %v, want conflict", err)
	}
	if _, err := store.UpsertReservedIPInventory(ctx, ReservedIPInventory{Region: "ams1", Address: netip.MustParseAddr("203.0.113.91"), ProviderRef: first.ProviderRef}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate provider ref error = %v, want conflict", err)
	}
	if _, err := store.UpsertReservedIPInventory(ctx, ReservedIPInventory{Region: "fra1", Address: netip.MustParseAddr("10.0.0.90")}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("private address error = %v, want invalid argument", err)
	}
	retired, err := store.UpsertReservedIPInventory(ctx, ReservedIPInventory{Region: "fra1", Address: netip.MustParseAddr("203.0.113.92"), Status: networkip.InventoryRetired})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimReservedIP(ctx, uuid.NewString(), retired.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("retired claim error = %v, want conflict", err)
	}
	items, err := store.ListReservedIPInventory(ctx, "fra1", networkip.InventoryAvailable)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("available fra1 inventory = %#v", items)
	}
}
