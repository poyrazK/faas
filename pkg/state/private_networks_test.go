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

func TestMemStorePrivateNetworkValidationAndErrorPaths(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	valid := PrivateNetwork{AccountID: "acct-1", Name: "prod", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/28")}

	for name, network := range map[string]PrivateNetwork{
		"missing account": {Name: "prod", Region: "fra1", CIDR: valid.CIDR},
		"missing name":    {AccountID: "acct-1", Region: "fra1", CIDR: valid.CIDR},
		"missing region":  {AccountID: "acct-1", Name: "prod", CIDR: valid.CIDR},
		"bad name":        {AccountID: "acct-1", Name: "UPPER", Region: "fra1", CIDR: valid.CIDR},
		"bad region":      {AccountID: "acct-1", Name: "prod", Region: "EU WEST", CIDR: valid.CIDR},
		"bad id":          {ID: "not valid", AccountID: "acct-1", Name: "prod", Region: "fra1", CIDR: valid.CIDR},
		"bad cidr":        {AccountID: "acct-1", Name: "prod", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/30")},
		"bad status":      {AccountID: "acct-1", Name: "prod", Region: "fra1", CIDR: valid.CIDR, Status: "pending"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.CreatePrivateNetwork(ctx, network); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("CreatePrivateNetwork error = %v, want invalid argument", err)
			}
		})
	}

	first, err := store.CreatePrivateNetwork(ctx, valid)
	if err != nil {
		t.Fatalf("CreatePrivateNetwork: %v", err)
	}
	if _, err := store.CreatePrivateNetwork(ctx, PrivateNetwork{AccountID: "acct-1", Name: "zeta", Region: "fra1", CIDR: netip.MustParsePrefix("10.43.0.0/28")}); err != nil {
		t.Fatalf("CreatePrivateNetwork zeta: %v", err)
	}
	if _, err := store.CreatePrivateNetwork(ctx, PrivateNetwork{AccountID: "acct-1", Name: "alpha", Region: "fra2", CIDR: netip.MustParsePrefix("10.44.0.0/28")}); err != nil {
		t.Fatalf("CreatePrivateNetwork alpha: %v", err)
	}
	networks, err := store.ListPrivateNetworks(ctx, "acct-1")
	if err != nil || len(networks) != 3 || networks[0].Name != "alpha" || networks[2].Name != "zeta" {
		t.Fatalf("ListPrivateNetworks = %+v, err=%v", networks, err)
	}
	if _, err := store.GetPrivateNetwork(ctx, "other-account", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPrivateNetwork wrong account = %v, want not found", err)
	}

	if _, err := store.AllocatePrivateNetworkAddress(ctx, "acct-1", first.ID, "", "owner"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid owner type error = %v, want invalid argument", err)
	}
	if _, err := store.AllocatePrivateNetworkAddress(ctx, "other-account", first.ID, "app", "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong account allocation = %v, want not found", err)
	}
	if err := store.ReleasePrivateNetworkAddress(ctx, "acct-1", first.ID, "app", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing release = %v, want not found", err)
	}

	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, AppPrivateNetworkAttachment{
		AccountID: "acct-1", AppID: "app-1", NetworkID: first.ID, Region: first.Region, Status: "pending",
	}); err != nil {
		t.Fatalf("UpsertAppPrivateNetworkAttachment: %v", err)
	}
	if err := store.DeletePrivateNetwork(ctx, "acct-1", first.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete attached network = %v, want conflict", err)
	}
	if err := store.DeleteAppPrivateNetworkAttachment(ctx, "acct-1", "app-1"); err != nil {
		t.Fatalf("DeleteAppPrivateNetworkAttachment: %v", err)
	}
	if err := store.DeletePrivateNetwork(ctx, "other-account", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete wrong account = %v, want not found", err)
	}

	if _, _, ok := firstPrivateNetworkAddress(netip.MustParsePrefix("2001:db8::/64")); ok {
		t.Fatal("IPv6 address unexpectedly accepted")
	}
	if _, _, ok := firstPrivateNetworkAddress(netip.MustParsePrefix("10.0.0.0/31")); ok {
		t.Fatal("/31 network unexpectedly accepted")
	}
	if _, ok := allocatePrivateNetworkAddress(netip.MustParsePrefix("10.0.0.0/31"), nil); ok {
		t.Fatal("allocation from /31 unexpectedly succeeded")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.CreatePrivateNetwork(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create = %v", err)
	}
	if _, err := store.GetPrivateNetwork(cancelled, "acct-1", first.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get = %v", err)
	}
	if _, err := store.ListPrivateNetworks(cancelled, "acct-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list = %v", err)
	}
	if err := store.DeletePrivateNetwork(cancelled, "acct-1", first.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete = %v", err)
	}
	if _, err := store.AllocatePrivateNetworkAddress(cancelled, "acct-1", first.ID, "app", "owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled allocate = %v", err)
	}
	if err := store.ReleasePrivateNetworkAddress(cancelled, "acct-1", first.ID, "app", "owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled release = %v", err)
	}
}
