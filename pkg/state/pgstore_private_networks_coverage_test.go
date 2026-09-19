package state_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_PrivateNetworkCoverage keeps the Postgres-backed private-network
// surface in the shard-2a coverage budget. The MemStore lifecycle test covers
// the same contract in-process, but without this integration exercise the
// newly added pgstore implementation contributes a large untested block to
// pkg/state's aggregate coverage.
func TestPg_PrivateNetworkCoverage(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "pg-private-network-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	suffix := uuid.NewString()[:8]
	network, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: acct.ID,
		Name:      "net-" + suffix,
		Region:    "fra1",
		CIDR:      netip.MustParsePrefix("10.80.0.0/28"),
		FirewallRules: []api.PrivateNetworkFirewallRule{{
			Direction: "ingress",
			Protocol:  "tcp",
			CIDRs:     []string{"10.80.0.0/28"},
			Ports:     []string{"443"},
		}},
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetwork: %v", err)
	}
	if network.Status != api.PrivateNetworkStatusReady || network.CIDR.String() != "10.80.0.0/28" || len(network.FirewallRules) != 1 {
		t.Fatalf("created network = %+v", network)
	}

	if got, err := s.GetPrivateNetwork(ctx, acct.ID, network.ID); err != nil || got.ID != network.ID || len(got.FirewallRules) != 1 || got.FirewallRules[0].Ports[0] != "443" {
		t.Fatalf("GetPrivateNetwork = %+v, %v", got, err)
	}
	if got, err := s.ListPrivateNetworks(ctx, acct.ID); err != nil || len(got) != 1 || got[0].ID != network.ID || len(got[0].FirewallRules) != 1 {
		t.Fatalf("ListPrivateNetworks = %+v, %v", got, err)
	}
	updated, err := s.UpdatePrivateNetworkFirewallPolicy(ctx, acct.ID, network.ID,
		[]netip.Prefix{netip.MustParsePrefix("10.80.0.0/28")},
		[]api.PrivateNetworkFirewallRule{{Direction: "egress", Protocol: "udp", Ports: []string{"53"}}})
	if err != nil || len(updated.FirewallRules) != 1 || updated.FirewallRules[0].Direction != "egress" {
		t.Fatalf("UpdatePrivateNetworkFirewallPolicy = %+v, %v", updated, err)
	}

	if _, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: acct.ID, Name: network.Name, Region: "fra1", CIDR: netip.MustParsePrefix("10.81.0.0/28"),
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate network name = %v, want conflict", err)
	}
	if _, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: acct.ID, Name: "overlap-" + suffix, Region: "fra1", CIDR: netip.MustParsePrefix("10.80.0.0/28"),
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping network = %v, want conflict", err)
	}
	otherRegion, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: acct.ID, Name: "other-" + suffix, Region: "fra2", CIDR: netip.MustParsePrefix("10.80.0.0/28"),
	})
	if err != nil {
		t.Fatalf("same CIDR in another region: %v", err)
	}

	address, err := s.AllocatePrivateNetworkAddress(ctx, acct.ID, network.ID, " app ", " owner-1 ")
	if err != nil {
		t.Fatalf("AllocatePrivateNetworkAddress: %v", err)
	}
	if address.Address.String() != "10.80.0.2" || address.OwnerType != "app" || address.OwnerID != "owner-1" {
		t.Fatalf("allocated address = %+v", address)
	}
	replayed, err := s.AllocatePrivateNetworkAddress(ctx, acct.ID, network.ID, "app", "owner-1")
	if err != nil || replayed.ID != address.ID {
		t.Fatalf("replayed allocation = %+v, %v", replayed, err)
	}
	if _, err := s.AllocatePrivateNetworkAddress(ctx, acct.ID, network.ID, "", "owner-2"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid owner = %v, want invalid argument", err)
	}
	if _, err := s.AllocatePrivateNetworkAddress(ctx, acct.ID, "missing-network", "app", "owner-2"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing network allocation = %v, want not found", err)
	}

	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "net-app-" + suffix, Type: state.AppTypeApp})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := s.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: acct.ID, AppID: app.ID, NetworkID: network.ID, Region: network.Region,
	}); err != nil {
		t.Fatalf("UpsertAppPrivateNetworkAttachment: %v", err)
	}
	if err := s.DeletePrivateNetwork(ctx, acct.ID, network.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete attached network = %v, want conflict", err)
	}
	if err := s.DeleteAppPrivateNetworkAttachment(ctx, acct.ID, app.ID); err != nil {
		t.Fatalf("DeleteAppPrivateNetworkAttachment: %v", err)
	}

	if err := s.ReleasePrivateNetworkAddress(ctx, acct.ID, network.ID, "app", "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing address release = %v, want not found", err)
	}
	if err := s.ReleasePrivateNetworkAddress(ctx, acct.ID, network.ID, "app", "owner-1"); err != nil {
		t.Fatalf("ReleasePrivateNetworkAddress: %v", err)
	}
	if err := s.ReleasePrivateNetworkAddress(ctx, acct.ID, network.ID, "app", "owner-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate address release = %v, want not found", err)
	}
	if err := s.DeletePrivateNetwork(ctx, acct.ID, network.ID); err != nil {
		t.Fatalf("DeletePrivateNetwork: %v", err)
	}
	if _, err := s.GetPrivateNetwork(ctx, acct.ID, network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted network = %v, want not found", err)
	}
	if err := s.DeletePrivateNetwork(ctx, acct.ID, network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate network delete = %v, want not found", err)
	}
	if err := s.DeletePrivateNetwork(ctx, acct.ID, otherRegion.ID); err != nil {
		t.Fatalf("DeletePrivateNetwork other region: %v", err)
	}
}
