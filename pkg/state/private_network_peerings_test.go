package state

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
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

func TestMemStorePrivateNetworkPeeringValidationAndOrdering(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	for _, network := range []PrivateNetwork{
		{ID: "alpha", AccountID: "acct-1", Name: "alpha", Region: "fra1", CIDR: netip.MustParsePrefix("10.70.0.0/20")},
		{ID: "beta", AccountID: "acct-1", Name: "beta", Region: "fra1", CIDR: netip.MustParsePrefix("10.70.16.0/20")},
		{ID: "gamma", AccountID: "acct-1", Name: "gamma", Region: "fra1", CIDR: netip.MustParsePrefix("10.70.32.0/20")},
	} {
		if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}
	// Bypass CreatePrivateNetwork's own overlap guard so the peering guard is
	// exercised independently.
	store.mu.Lock()
	store.privateNetworks["overlap"] = PrivateNetwork{ID: "overlap", AccountID: "acct-1", Name: "overlap", Region: "fra1", CIDR: netip.MustParsePrefix("10.70.0.0/21")}
	store.mu.Unlock()

	invalid := []PrivateNetworkPeering{
		{},
		{AccountID: "acct-1", LeftNetworkID: "alpha", RightNetworkID: "beta", Region: "fra1", Status: "bogus"},
		{ID: "bad id", AccountID: "acct-1", LeftNetworkID: "alpha", RightNetworkID: "beta", Region: "fra1"},
		{AccountID: "acct-1", LeftNetworkID: "bad id", RightNetworkID: "beta", Region: "fra1"},
		{AccountID: "acct-1", LeftNetworkID: "alpha", RightNetworkID: "bad id", Region: "fra1"},
		{AccountID: "acct-1", LeftNetworkID: "alpha", RightNetworkID: "beta", Region: "bad region"},
	}
	for _, peering := range invalid {
		if _, err := store.CreatePrivateNetworkPeering(ctx, peering); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid peering %+v error = %v, want invalid argument", peering, err)
		}
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{
		AccountID: "acct-1", LeftNetworkID: "alpha", RightNetworkID: "overlap", Region: "fra1",
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("overlapping CIDRs error = %v, want invalid argument", err)
	}

	first, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{
		ID: "peer-alpha-beta", AccountID: "acct-1", LeftNetworkID: "beta", RightNetworkID: "alpha", Region: "fra1",
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetworkPeering(first): %v", err)
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{
		ID: first.ID, AccountID: "acct-1", LeftNetworkID: "beta", RightNetworkID: "gamma", Region: "fra1",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate ID error = %v, want conflict", err)
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{
		ID: "peer-alpha-gamma", AccountID: "acct-1", LeftNetworkID: "gamma", RightNetworkID: "alpha", Region: "fra1", Status: api.PrivateNetworkPeeringStatusReady,
	}); err != nil {
		t.Fatalf("CreatePrivateNetworkPeering(second): %v", err)
	}
	listed, err := store.ListPrivateNetworkPeerings(ctx, "acct-1", "")
	if err != nil || len(listed) != 2 || listed[0].LeftNetworkID != "alpha" || listed[1].LeftNetworkID != "alpha" {
		t.Fatalf("ordered peerings = %+v, %v", listed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.CreatePrivateNetworkPeering(canceled, PrivateNetworkPeering{
		AccountID: "acct-1", LeftNetworkID: "alpha", RightNetworkID: "beta", Region: "fra1",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create error = %v, want context canceled", err)
	}
}

func TestMemStorePrivateNetworkPeeringReadsAndGuards(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	for _, network := range []PrivateNetwork{
		{ID: "alpha", AccountID: "acct-1", Name: "alpha", Region: "fra1", CIDR: netip.MustParsePrefix("10.60.0.0/20")},
		{ID: "beta", AccountID: "acct-1", Name: "beta", Region: "fra1", CIDR: netip.MustParsePrefix("10.60.16.0/20")},
	} {
		if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}
	created, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{
		AccountID: "acct-1", LeftNetworkID: "beta", RightNetworkID: "alpha", Region: "fra1",
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetworkPeering generated ID: %v", err)
	}
	if !strings.HasPrefix(created.ID, "peer-") || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("generated peering = %+v, want ID and timestamps", created)
	}
	got, err := store.GetPrivateNetworkPeering(ctx, "acct-1", created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("GetPrivateNetworkPeering = %+v, %v", got, err)
	}
	all, err := store.ListPrivateNetworkPeerings(ctx, "acct-1", "")
	if err != nil || len(all) != 1 || all[0].LeftNetworkID != "alpha" {
		t.Fatalf("ListPrivateNetworkPeerings(all) = %+v, %v", all, err)
	}
	if _, err := store.GetPrivateNetworkPeering(ctx, "acct-1", "peer-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing peering error = %v, want not found", err)
	}
	if err := store.DeletePrivateNetworkPeering(ctx, "acct-1", "peer-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing peering error = %v, want not found", err)
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, PrivateNetworkPeering{
		ID: "peer-self", AccountID: "acct-1", LeftNetworkID: "alpha", RightNetworkID: "alpha", Region: "fra1",
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("self peering error = %v, want invalid argument", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListPrivateNetworkPeerings(canceled, "acct-1", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list error = %v, want context canceled", err)
	}
	if _, err := store.GetPrivateNetworkPeering(canceled, "acct-1", created.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled get error = %v, want context canceled", err)
	}
	if err := store.DeletePrivateNetworkPeering(canceled, "acct-1", created.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delete error = %v, want context canceled", err)
	}
}
