package state_test

// adr: 184 — operator-managed reserved public IP inventory and tenant claims.

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/networkip"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreReservedIPInventoryClaimLifecycle(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "reserved-ip-inventory-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherAccount, err := store.CreateAccount(ctx, "reserved-ip-inventory-other-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}

	inventory, err := store.UpsertReservedIPInventory(ctx, state.ReservedIPInventory{
		Region:      "fra1",
		Address:     netip.MustParseAddr("198.51.100.89"),
		ProviderRef: "provider-ip-89",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Status != networkip.InventoryAvailable || inventory.LeaseID != "" {
		t.Fatalf("created inventory = %#v", inventory)
	}
	if _, err := store.GetReservedIPInventory(ctx, inventory.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ListReservedIPInventory(ctx, "fra1", networkip.InventoryAvailable); err != nil || len(got) != 1 {
		t.Fatalf("ListReservedIPInventory = %#v, %v", got, err)
	}

	lease, err := store.ClaimReservedIP(ctx, account.ID, inventory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != networkip.StatusAvailable || lease.AccountID != account.ID || lease.Address != inventory.Address {
		t.Fatalf("claimed lease = %#v", lease)
	}
	if retry, err := store.ClaimReservedIP(ctx, account.ID, inventory.ID); err != nil || retry.ID != lease.ID {
		t.Fatalf("idempotent claim = %#v, %v", retry, err)
	}
	if _, err := store.ClaimReservedIP(ctx, otherAccount.ID, inventory.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cross-account claim error = %v, want conflict", err)
	}
	claimed, err := store.GetReservedIPInventory(ctx, inventory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != networkip.InventoryClaimed || claimed.LeaseID != lease.ID {
		t.Fatalf("claimed inventory = %#v", claimed)
	}

	if err := store.ReleaseReservedIPClaim(ctx, account.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	released, err := store.GetReservedIPInventory(ctx, inventory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != networkip.InventoryAvailable || released.LeaseID != "" {
		t.Fatalf("released inventory = %#v", released)
	}
	if _, err := store.GetReservedIP(ctx, account.ID, lease.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("released lease lookup = %v, want not found", err)
	}
}

func TestPgStoreReservedIPInventoryValidationAndUniqueness(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	first, err := store.UpsertReservedIPInventory(ctx, state.ReservedIPInventory{
		Region:      "fra1",
		Address:     netip.MustParseAddr("203.0.113.93"),
		ProviderRef: "provider-ip-93",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertReservedIPInventory(ctx, state.ReservedIPInventory{Region: "ams1", Address: first.Address, ProviderRef: "provider-ip-other"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate address error = %v, want conflict", err)
	}
	if _, err := store.UpsertReservedIPInventory(ctx, state.ReservedIPInventory{Region: "ams1", Address: netip.MustParseAddr("203.0.113.94"), ProviderRef: first.ProviderRef}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate provider ref error = %v, want conflict", err)
	}
	if _, err := store.GetReservedIPInventory(ctx, "not-a-uuid"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid inventory ID error = %v, want invalid argument", err)
	}
	if _, err := store.ListReservedIPInventory(ctx, "bad region", ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid region error = %v, want invalid argument", err)
	}
	if _, err := store.ListReservedIPInventory(ctx, "fra1", "bogus"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid status error = %v, want invalid argument", err)
	}
	retired, err := store.UpsertReservedIPInventory(ctx, state.ReservedIPInventory{Region: "fra1", Address: netip.MustParseAddr("203.0.113.95"), Status: networkip.InventoryRetired})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimReservedIP(ctx, uuid.NewString(), retired.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retired claim error = %v, want conflict", err)
	}
	if err := store.ReleaseReservedIPClaim(ctx, uuid.NewString(), first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("wrong lease ID release error = %v, want not found", err)
	}
}
