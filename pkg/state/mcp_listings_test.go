package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkMCPListings publishes, republishes (bumping the version), reads
// across accounts and unpublishes a listing.
func checkMCPListings(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	listings := st.(state.MCPListingStore)
	if _, err := listings.GetMCPListing(ctx, accountID, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unpublished get = %v, want ErrNotFound", err)
	}
	in := state.MCPListing{AccountID: accountID, AppID: appID, Title: "Search", Summary: "Find things", Endpoint: "https://search.example/mcp",
		Tools: []state.MCPListingTool{{Name: "search", Description: "Search"}}, RegistryName: "io.github.acme/search"}
	first, err := listings.PutMCPListing(ctx, in)
	if err != nil || first.Version != 1 || first.PublishedAt.IsZero() || len(first.Tools) != 1 {
		t.Fatalf("publish = %+v, %v", first, err)
	}
	in.Tools, in.ShowUsage = nil, true
	second, err := listings.PutMCPListing(ctx, in)
	if err != nil || second.Version != 2 || len(second.Tools) != 0 || !second.ShowUsage {
		t.Fatalf("republish = %+v, %v", second, err)
	}
	got, err := listings.GetMCPListing(ctx, accountID, appID)
	if err != nil || got.Version != 2 || got.RegistryName != "io.github.acme/search" || got.Tools == nil {
		t.Fatalf("get = %+v, %v", got, err)
	}
	other := "00000000-0000-0000-0000-000000000001"
	if _, err := listings.GetMCPListing(ctx, other, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("other account get = %v", err)
	}
	if n, err := listings.CountOpenMCPListingCustomers(ctx, accountID, appID); err != nil || n != 0 {
		t.Fatalf("open customers = %d, %v; want 0", n, err)
	}
	if err := listings.DeleteMCPListing(ctx, accountID, appID); err != nil {
		t.Fatal(err)
	}
	if err := listings.DeleteMCPListing(ctx, accountID, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second unpublish = %v, want ErrNotFound", err)
	}
}

// adr: 20261126175800
func TestMemStoreMCPListings(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkMCPListings(t, ctx, store, accountID, appID)
}

// adr: 20261126175800
func TestPgStoreMCPListings(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkMCPListings(t, ctx, store, accountID, appID)
}
