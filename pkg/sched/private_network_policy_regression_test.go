package sched_test

// adr: 20261103194727

import (
	"context"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type reviewPolicyRouter struct {
	allowed []netip.Prefix
	rules   []api.PrivateNetworkFirewallRule
	calls   int
}

func (r *reviewPolicyRouter) UpdatePrivateNetwork(_ context.Context, _, _ string, _ []netip.Prefix) error {
	r.calls++
	r.allowed = nil
	r.rules = nil
	return nil
}
func (r *reviewPolicyRouter) UpdatePrivateNetworkAttachment(_ context.Context, _, _, _ string, _ netip.Addr, _ []netip.Prefix) error {
	r.calls++
	r.allowed = nil
	r.rules = nil
	return nil
}
func (r *reviewPolicyRouter) UpdatePrivateNetworkAttachmentWithPolicy(_ context.Context, _, _, _ string, _ netip.Addr, _, allowed []netip.Prefix) error {
	r.calls++
	r.allowed = allowed
	r.rules = nil
	return nil
}
func (r *reviewPolicyRouter) UpdatePrivateNetworkAttachmentWithFirewall(_ context.Context, _, _, _ string, _ netip.Addr, _, allowed []netip.Prefix, rules []api.PrivateNetworkFirewallRule) error {
	r.calls++
	r.allowed = allowed
	r.rules = rules
	return nil
}
func reviewNetworkFixture(t *testing.T) (*state.MemStore, state.App, state.AppPrivateNetworkAttachment) {
	t.Helper()
	t.Setenv("FAAS_PRIVATE_NETWORK_FABRIC_ENABLED", "1")
	ctx := context.Background()
	store := state.NewMemStore()
	_, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{ID: "review-net", AccountID: "acct-review", Name: "review", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/16"), AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.8.0/24")}, FirewallRules: []api.PrivateNetworkFirewallRule{{Direction: "egress", Protocol: "tcp", Ports: []string{"443"}}}})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: "acct-review", AppID: "app-review", NetworkID: "review-net", Region: "fra1", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusReady})
	if err != nil {
		t.Fatal(err)
	}
	return store, state.App{ID: "app-review", AccountID: "acct-review"}, attachment
}
func TestPrivateNetworkColdWakeRetainsNetworkCIDRBaseline(t *testing.T) {
	store, app, _ := reviewNetworkFixture(t)
	p := sched.ProjectPrivateNetworkForTest(store, app)
	if p.PrivateNetworkID == "" {
		t.Fatal("fixture did not enable fabric")
	}
	if len(p.PrivateNetworkAllowedCIDRs) != 1 || p.PrivateNetworkAllowedCIDRs[0] != "10.42.8.0/24" {
		t.Fatalf("cold wake lost network baseline: destinations=%v allowed=%v rules=%v", p.PrivateNetworkCIDRs, p.PrivateNetworkAllowedCIDRs, p.PrivateNetworkFirewallRules)
	}
}
func TestPrivateNetworkPeeringReplayRetainsNetworkFirewall(t *testing.T) {
	store, app, _ := reviewNetworkFixture(t)
	_, err := store.CreateInstance(context.Background(), app.ID, "dep-review", string(state.StateRunning), 256, "node-review", "")
	if err != nil {
		t.Fatal(err)
	}
	r := &reviewPolicyRouter{}
	applier := sched.NewPrivateNetworkPeeringRouteApplierWithRouteApplier(store, sched.NewPrivateNetworkRouteApplier(store, r, nil).WithPeeringStore(store), nil)
	if err := applier.Apply(context.Background(), app.AccountID, "fra1", nil); err != nil {
		t.Fatal(err)
	}
	if r.calls == 0 {
		t.Fatal("fixture made no update")
	}
	if len(r.rules) != 1 || len(r.allowed) != 1 {
		t.Fatalf("peering replay removed network baseline: calls=%d allowed=%v rules=%v", r.calls, r.allowed, r.rules)
	}
}
