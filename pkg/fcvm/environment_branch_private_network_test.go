// adr: 20261031193635
package fcvm

import (
	"context"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// branchPolicyByDeployment answers each deployment's branch policy.
type branchPolicyByDeployment struct {
	policies map[string]state.EnvironmentBranchEgressPolicy
	reads    map[string]int
}

func (s *branchPolicyByDeployment) EnvironmentBranchEgress(_ context.Context, deploymentID string) (state.EnvironmentBranchEgressPolicy, error) {
	s.reads[deploymentID]++
	policy, ok := s.policies[deploymentID]
	if !ok {
		return state.EnvironmentBranchEgressPolicy{}, state.ErrNotFound
	}
	return policy, nil
}

func (s *branchPolicyByDeployment) EnvironmentBranchDatabaseEndpoints(context.Context, string) ([]state.EnvironmentBranchDatabaseEndpoint, error) {
	return nil, nil
}

// Section 6: a branch instance stays off its app's private network unless
// its branch joins it, at the wake and on every replay of the attachment, and
// a joined branch instance never owns the app's service address.
func TestEnvironmentBranchInstancesStayOffPrivateNetworksUnlessJoined(t *testing.T) {
	store := state.NewMemStore()
	network, err := store.CreatePrivateNetwork(t.Context(), state.PrivateNetwork{ID: "net-branches", Name: "branches", AccountID: "account", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAppPrivateNetworkAttachment(t.Context(), state.AppPrivateNetworkAttachment{AccountID: "account", AppID: "app", NetworkID: network.ID, Region: "fra1", Status: "ready", CIDRs: []netip.Prefix{network.CIDR}}); err != nil {
		t.Fatal(err)
	}
	policies := &branchPolicyByDeployment{reads: map[string]int{}, policies: map[string]state.EnvironmentBranchEgressPolicy{
		"dep-prod": {Scope: "default"},
		"dep-off":  {Mode: state.EnvironmentBranchEgressLive, EnvironmentID: "env-off", Scope: "pr-1", PrivateNetworks: state.EnvironmentBranchPrivateNetworksOff},
		"dep-join": {Mode: state.EnvironmentBranchEgressLive, EnvironmentID: "env-join", Scope: "pr-2", PrivateNetworks: state.EnvironmentBranchPrivateNetworksJoin},
	}}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithEnvironmentBranchEgress(policies).WithPrivateNetworkMembers(func(ctx context.Context, req PrivateNetworkMemberRequest) (PrivateNetworkMemberIdentity, error) {
		return store.EnsurePrivateNetworkMember(ctx, req.AccountID, req.AppID, req.NetworkID, req.InstanceID, req.Eligible)
	}, store.ReleasePrivateNetworkMember)
	wake := func(id, deployment string) *Instance {
		t.Helper()
		// schedd sends the app's attachment with every wake, and marks
		// non-production deployments, branches included, StaticEgressExcluded.
		inst, err := m.Wake(t.Context(), WakeRequest{Instance: id, AccountID: "account", AppID: "app", DeploymentID: deployment,
			BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot(),
			PrivateNetworkID: network.ID, PrivateNetworkAddress: "10.42.0.2", PrivateNetworkCIDRs: []string{network.CIDR.String()},
			StaticEgressExcluded: deployment != "dep-prod"})
		if err != nil {
			t.Fatalf("wake %s: %v", id, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		return inst
	}
	prod, off, join := wake("prod", "dep-prod"), wake("branch-off", "dep-off"), wake("branch-join", "dep-join")
	offAgain := wake("branch-off-2", "dep-off") // the cached policy keeps it off too

	if !prod.Net.PrivateNetworkServiceAddress.IsValid() || prod.PrivateNetworkExcluded {
		t.Fatalf("production instance: service=%v excluded=%v", prod.Net.PrivateNetworkServiceAddress, prod.PrivateNetworkExcluded)
	}
	if !join.Net.PrivateNetworkAddress.IsValid() || join.Net.PrivateNetworkServiceAddress.IsValid() || join.PrivateNetworkExcluded {
		t.Fatalf("joined branch instance: address=%v service=%v excluded=%v", join.Net.PrivateNetworkAddress, join.Net.PrivateNetworkServiceAddress, join.PrivateNetworkExcluded)
	}
	offInstances := []*Instance{off, offAgain}
	for _, inst := range offInstances {
		if !inst.PrivateNetworkExcluded || inst.Net.PrivateNetworkAddress.IsValid() || inst.Net.PrivateNetworkBridge != "" || len(inst.Net.PrivateNetworkCIDRs) != 0 {
			t.Fatalf("%s joined the network: excluded=%v address=%v bridge=%q cidrs=%v", inst.Lease.Instance, inst.PrivateNetworkExcluded,
				inst.Net.PrivateNetworkAddress, inst.Net.PrivateNetworkBridge, inst.Net.PrivateNetworkCIDRs)
		}
	}
	if policies.reads["dep-off"] != 1 {
		t.Fatalf("off branch policy reads = %d, want 1 (cached)", policies.reads["dep-off"])
	}

	// A replay of the attachment reaches production and the joined branch only.
	if err := m.UpdatePrivateNetworkAttachment(t.Context(), "app", network.ID, netip.MustParseAddr("10.42.0.2"), []netip.Prefix{network.CIDR}); err != nil {
		t.Fatalf("replay attachment: %v", err)
	}
	if err := m.UpdatePrivateNetwork(t.Context(), "app", []netip.Prefix{network.CIDR}); err != nil {
		t.Fatalf("replay destinations: %v", err)
	}
	for _, inst := range offInstances {
		m.mu.Lock()
		nc := inst.Net
		m.mu.Unlock()
		if nc.PrivateNetworkAddress.IsValid() || nc.PrivateNetworkBridge != "" || len(nc.PrivateNetworkCIDRs) != 0 {
			t.Fatalf("a replay put %s on the network: address=%v bridge=%q cidrs=%v", inst.Lease.Instance, nc.PrivateNetworkAddress, nc.PrivateNetworkBridge, nc.PrivateNetworkCIDRs)
		}
	}
	m.mu.Lock()
	joined := join.Net.PrivateNetworkAddress.IsValid()
	m.mu.Unlock()
	if !joined {
		t.Fatal("a replay took the joined branch instance off the network")
	}
}
