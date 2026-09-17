package state

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStorePrivateNetworkLifecycleAndAddressAllocation(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	network, err := store.CreatePrivateNetwork(ctx, PrivateNetwork{
		AccountID: "acct-1", Name: "prod", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/28"),
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetwork: %v", err)
	}
	if network.ID == "" || network.Status != api.PrivateNetworkStatusReady || network.CIDR.String() != "10.42.0.0/28" {
		t.Fatalf("network = %+v", network)
	}
	if _, err := store.CreatePrivateNetwork(ctx, PrivateNetwork{AccountID: "acct-1", Name: "prod", Region: "fra1", CIDR: netip.MustParsePrefix("10.43.0.0/28")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name err = %v, want conflict", err)
	}
	if _, err := store.CreatePrivateNetwork(ctx, PrivateNetwork{AccountID: "acct-1", Name: "overlap", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/28")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlap err = %v, want conflict", err)
	}
	first, err := store.AllocatePrivateNetworkAddress(ctx, "acct-1", network.ID, "app", "app-1")
	if err != nil {
		t.Fatalf("AllocatePrivateNetworkAddress: %v", err)
	}
	if first.Address.String() != "10.42.0.2" {
		t.Fatalf("first address = %s, want 10.42.0.2", first.Address)
	}
	replayed, err := store.AllocatePrivateNetworkAddress(ctx, "acct-1", network.ID, "app", "app-1")
	if err != nil || replayed.ID != first.ID || replayed.Address != first.Address {
		t.Fatalf("replayed address = %+v, err=%v", replayed, err)
	}
	second, err := store.AllocatePrivateNetworkAddress(ctx, "acct-1", network.ID, "app", "app-2")
	if err != nil || second.Address.String() != "10.42.0.3" {
		t.Fatalf("second address = %+v, err=%v", second, err)
	}
	if err := store.ReleasePrivateNetworkAddress(ctx, "acct-1", network.ID, "app", "app-1"); err != nil {
		t.Fatalf("ReleasePrivateNetworkAddress: %v", err)
	}
	if err := store.DeletePrivateNetwork(ctx, "acct-1", network.ID); err != nil {
		t.Fatalf("delete network: %v", err)
	}
	if _, err := store.GetPrivateNetwork(ctx, "acct-1", network.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted network err = %v, want not found", err)
	}
}

func TestValidatePrivateNetworkCIDR(t *testing.T) {
	for _, value := range []string{"10.42.1.5/24", "172.16.1.5/16", "192.168.10.5/28"} {
		prefix, err := api.ValidatePrivateNetworkCIDR(value)
		if err != nil {
			t.Errorf("ValidatePrivateNetworkCIDR(%q): %v", value, err)
		}
		if prefix.String() == value {
			t.Errorf("ValidatePrivateNetworkCIDR(%q) did not canonicalize", value)
		}
	}
	for _, value := range []string{"10.0.0.0/30", "10.42.0.0/15", "10.42.0.0/29", "8.8.8.0/24", "fd00::/64"} {
		if _, err := api.ValidatePrivateNetworkCIDR(value); err == nil {
			t.Errorf("ValidatePrivateNetworkCIDR(%q) unexpectedly succeeded", value)
		}
	}
}
