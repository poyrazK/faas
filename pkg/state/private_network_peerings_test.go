package state

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestMemStorePrivateNetworkPeeringLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	for _, network := range []PrivateNetwork{
		{ID: "frontend", AccountID: "acct-1", Name: "frontend", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/20")},
		{ID: "data", AccountID: "acct-1", Name: "data", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.16.0/20")},
	} {
		if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}
	created, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{
		ID: "peer-frontend-data", AccountID: "acct-1", LeftNetworkID: "data", RightNetworkID: "frontend", Region: "fra1",
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetworkPeering: %v", err)
	}
	if created.LeftNetworkID != "data" || created.RightNetworkID != "frontend" || created.Status != "pending" {
		t.Fatalf("created = %+v, want canonical pending peering", created)
	}
	listed, err := store.ListPrivateNetworkPeerings(ctx, "acct-1", "frontend")
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListPrivateNetworkPeerings = %+v, %v", listed, err)
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{ID: "peer-reverse", AccountID: "acct-1", LeftNetworkID: "frontend", RightNetworkID: "data", Region: "fra1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reverse duplicate error = %v, want conflict", err)
	}
	if err := store.DeletePrivateNetwork(ctx, "acct-1", "frontend"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete network with peering error = %v, want conflict", err)
	}
	if err := store.DeletePrivateNetworkPeering(ctx, "acct-1", created.ID); err != nil {
		t.Fatalf("DeletePrivateNetworkPeering: %v", err)
	}
	if err := store.DeletePrivateNetwork(ctx, "acct-1", "frontend"); err != nil {
		t.Fatalf("DeletePrivateNetwork after peering: %v", err)
	}
}

func TestMemStorePrivateNetworkPeeringRejectsUnsafeRelationships(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	for _, network := range []PrivateNetwork{
		{ID: "left", AccountID: "acct-1", Name: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.50.0.0/20")},
		{ID: "right", AccountID: "acct-1", Name: "right", Region: "ams1", CIDR: netip.MustParsePrefix("10.50.16.0/20")},
	} {
		if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{ID: "peer-cross-region", AccountID: "acct-1", LeftNetworkID: "left", RightNetworkID: "right", Region: "fra1"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cross-region error = %v, want invalid argument", err)
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{ID: "peer-foreign", AccountID: "acct-2", LeftNetworkID: "left", RightNetworkID: "right", Region: "fra1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account error = %v, want not found", err)
	}
}
