//go:build linux || darwin

// adr: 20261104143000
package fcvm

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

func TestNativePrivateRouterPolicyUsesFabricAuthority(t *testing.T) {
	for _, stdin := range []bool{false, true} {
		t.Run(map[bool]string{false: "command", true: "stdin"}[stdin], func(t *testing.T) {
			m, v, _ := nativeManagerFixture(t)
			m.vmm = v
			command := &fakeRunner{}
			input := &fakeInputRunner{}
			m.run = command
			if stdin {
				m.run = input
			}
			left := privatenetwork.FabricSpec{AccountID: "acct-router", NetworkID: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")}
			right := privatenetwork.FabricSpec{AccountID: left.AccountID, NetworkID: "right", Region: left.Region, CIDR: netip.MustParsePrefix("10.43.0.0/24")}
			for _, spec := range []privatenetwork.FabricSpec{left, right} {
				if err := m.ReconcilePrivateNetworkFabric(t.Context(), spec.AccountID, spec.NetworkID, spec.Region, spec.CIDR); err != nil {
					t.Fatal(err)
				}
			}
			routes := []privatenetwork.PeeringRoute{
				{FromNetworkID: left.NetworkID, DestinationNetwork: right.NetworkID, DestinationCIDR: right.CIDR},
				{FromNetworkID: right.NetworkID, DestinationNetwork: left.NetworkID, DestinationCIDR: left.CIDR},
			}
			if err := m.ReconcilePrivateNetworkPeerings(t.Context(), left.AccountID, left.Region, []privatenetwork.FabricSpec{left, right}, routes); err != nil {
				t.Fatal(err)
			}
			namespace := privatenetwork.RouterNamespace(left.AccountID, left.Region)
			var policy string
			if stdin {
				policy = string(input.input)
				if strings.Join(input.inputArgv, " ") != "ip netns exec "+namespace+" nft -f -" {
					t.Fatalf("wrong router transaction: %v", input.inputArgv)
				}
			} else {
				last := command.commands[len(command.commands)-1]
				if len(last) != 6 {
					t.Fatalf("policy was not one nft transaction: %v", last)
				}
				policy = last[5]
			}
			if !strings.Contains(policy, "flush table inet faas_peering") || strings.Count(policy, "add rule inet faas_peering forward") != 2 {
				t.Fatalf("missing complete bidirectional router policy: %s", policy)
			}
			if err := m.ReconcilePrivateNetworkPeerings(t.Context(), left.AccountID, left.Region, nil, nil); err != nil {
				t.Fatalf("revocation without any VM owner failed: %v", err)
			}
			// Shared fabric authority must not become a fallback for VM policy.
			if err := m.runAtomicNetworkPolicy(t.Context(), "fc-unowned", nil); err == nil || !strings.Contains(err.Error(), "original lease scope") {
				t.Fatalf("unowned per-instance policy was admitted: %v", err)
			}
		})
	}
}

func TestNativePrivateRouterPolicyFailureAndRetry(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	m.vmm = v
	r := &policyReplacementRunner{failReplacement: true}
	m.run = r
	if err := m.ReconcilePrivateNetworkPeerings(t.Context(), "acct-router", "fra1", nil, nil); err == nil {
		t.Fatal("failed router transaction was acknowledged")
	}
	if len(m.privateNetworkRouters) != 0 {
		t.Fatal("failed router initialization was published")
	}
	r.failReplacement = false
	if err := m.ReconcilePrivateNetworkPeerings(t.Context(), "acct-router", "fra1", nil, nil); err != nil {
		t.Fatal(err)
	}
	before := r.inputRuns
	wrong := [][]string{{"ip", "netns", "exec", "fc-unowned", "nft", "flush", "ruleset"}}
	if err := m.runPrivateRouterPolicy(t.Context(), "acct-router", "fra1", wrong); err == nil || r.inputRuns != before {
		t.Fatalf("router authority accepted an unrelated namespace: %v", err)
	}
}
