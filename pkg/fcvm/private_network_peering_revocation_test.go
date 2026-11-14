// adr: 20261104160000
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

type peeringRevocationRunner struct {
	fakeInputRunner
	active           string
	ambiguousFailure bool
}

func (r *peeringRevocationRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	if err := r.fakeInputRunner.RunInput(ctx, argv, input); err != nil {
		return err
	}
	if strings.Contains(string(input), "flush table inet faas_peering") {
		r.active = string(input)
		if r.ambiguousFailure {
			return errors.New("lost acknowledgement after kernel commit")
		}
	}
	return nil
}

func peeringRevocationFixture(t *testing.T) (*Manager, *peeringRevocationRunner, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) {
	t.Helper()
	r := &peeringRevocationRunner{}
	m := newTestManager(r, &fakeVMM{})
	networks := []privatenetwork.FabricSpec{
		{AccountID: "router", NetworkID: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")},
		{AccountID: "router", NetworkID: "right", Region: "fra1", CIDR: netip.MustParsePrefix("10.43.0.0/24")},
		{AccountID: "router", NetworkID: "unrelated", Region: "fra1", CIDR: netip.MustParsePrefix("10.44.0.0/24")},
	}
	for _, spec := range networks {
		if err := m.ReconcilePrivateNetworkFabric(t.Context(), spec.AccountID, spec.NetworkID, spec.Region, spec.CIDR); err != nil {
			t.Fatal(err)
		}
	}
	routes := []privatenetwork.PeeringRoute{
		{FromNetworkID: "left", DestinationNetwork: "right", DestinationCIDR: networks[1].CIDR},
		{FromNetworkID: "right", DestinationNetwork: "left", DestinationCIDR: networks[0].CIDR},
		{FromNetworkID: "left", DestinationNetwork: "unrelated", DestinationCIDR: networks[2].CIDR},
		{FromNetworkID: "unrelated", DestinationNetwork: "left", DestinationCIDR: networks[0].CIDR},
	}
	if err := m.ReconcilePrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes); err != nil {
		t.Fatal(err)
	}
	return m, r, networks, routes
}

func TestPrivatePeeringRevocationSurvivesUnrelatedFabricFailure(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "all", true: "partial"}[partial], func(t *testing.T) {
			m, r, networks, routes := peeringRevocationFixture(t)
			r.fakeRunner.failOn = "addr replace 10.44.0.1/24"
			var desired []privatenetwork.PeeringRoute
			if partial {
				desired = routes[2:]
			}
			err := m.ReconcilePrivateNetworkPeerings(t.Context(), "router", "fra1", networks, desired)
			if partial && err == nil || !partial && err != nil {
				t.Fatalf("unexpected repair result: %v", err)
			}
			if strings.Contains(r.active, "10.43.0.0/24") || strings.Count(r.active, "add rule inet faas_peering forward") != len(desired) {
				t.Fatalf("revoked permissions survived repair failure: %s", r.active)
			}
		})
	}
}

func TestPrivatePeeringRestrictionNeverAddsRoutes(t *testing.T) {
	m, r, networks, routes := peeringRevocationFixture(t)
	if err := m.RestrictPrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes[:2]); err != nil {
		t.Fatal(err)
	}
	before := len(r.commands)
	if err := m.RestrictPrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes); err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != before || strings.Count(r.active, "add rule inet faas_peering forward") != 2 || strings.Contains(r.active, "10.44.0.0/24") {
		t.Fatal("restrictive phase repaired links or restored revoked permission")
	}
	// A restart cannot assume that any preexisting kernel permission is current.
	m.privateNetworkPeeringPolicies = nil
	if err := m.RestrictPrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.active, "add rule inet faas_peering forward") {
		t.Fatal("unknown prior policy activated routes during restriction")
	}
}

func TestPrivatePeeringCIDRChangeRequiresRepair(t *testing.T) {
	m, r, networks, routes := peeringRevocationFixture(t)
	networks[0].CIDR = netip.MustParsePrefix("10.45.0.0/24")
	routes[1].DestinationCIDR = networks[0].CIDR
	routes[3].DestinationCIDR = networks[0].CIDR
	if err := m.RestrictPrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.active, "add rule inet faas_peering forward") {
		t.Fatal("CIDR change added permissions before endpoint repair")
	}
}

func TestPrivatePeeringRestrictionForgetsAmbiguousCommit(t *testing.T) {
	m, r, networks, routes := peeringRevocationFixture(t)
	r.ambiguousFailure = true
	if err := m.RestrictPrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes[:2]); err == nil {
		t.Fatal("lost acknowledgement was reported as success")
	}
	r.ambiguousFailure = false
	if err := m.RestrictPrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.active, "add rule inet faas_peering forward") {
		t.Fatal("retry restored unconfirmed permissions before repair")
	}
	if err := m.ReconcilePrivateNetworkPeerings(t.Context(), "router", "fra1", networks, routes); err != nil {
		t.Fatal(err)
	}
	if strings.Count(r.active, "add rule inet faas_peering forward") != len(routes) {
		t.Fatal("successful repair did not activate desired permissions")
	}
}
