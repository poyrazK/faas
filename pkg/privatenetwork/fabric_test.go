package privatenetwork

import (
	"net/netip"
	"testing"
)

func TestBuildFabricPlanUsesStableAccountScopedBridge(t *testing.T) {
	spec, err := NewFabricSpecFromValues("acct-a", "prod", "fra1", "10.42.7.9/16")
	if err != nil {
		t.Fatalf("NewFabricSpecFromValues: %v", err)
	}
	plan, err := BuildFabricPlan(spec)
	if err != nil {
		t.Fatalf("BuildFabricPlan: %v", err)
	}
	if plan.Spec.CIDR != netip.MustParsePrefix("10.42.0.0/16") {
		t.Fatalf("plan CIDR = %s, want 10.42.0.0/16", plan.Spec.CIDR)
	}
	if plan.Gateway != netip.MustParseAddr("10.42.0.1") {
		t.Fatalf("gateway = %s, want 10.42.0.1", plan.Gateway)
	}
	if plan.GatewayCIDR != netip.MustParsePrefix("10.42.0.1/16") {
		t.Fatalf("gateway CIDR = %s, want 10.42.0.1/16", plan.GatewayCIDR)
	}
	if len(plan.BridgeName) > 15 || plan.BridgeName != BridgeName("acct-a", "prod") {
		t.Fatalf("bridge name = %q, want stable <= 15-byte name", plan.BridgeName)
	}
	if got := plan.Setup[1]; len(got) != 6 || got[0] != "ip" || got[1] != "addr" || got[2] != "replace" || got[3] != "10.42.0.1/16" {
		t.Fatalf("gateway setup command = %v", got)
	}
	if BridgeName("acct-a", "prod") == BridgeName("acct-b", "prod") {
		t.Fatal("bridge names for different accounts collided")
	}
	if got := plan.Teardown[0]; len(got) != 5 || got[0] != "ip" || got[1] != "link" || got[2] != "del" || got[4] != plan.BridgeName {
		t.Fatalf("teardown command = %v", got)
	}
}

func TestNewFabricSpecRejectsInvalidWireValues(t *testing.T) {
	tests := []struct {
		name string
		id   string
		reg  string
		cidr string
	}{
		{name: "missing network", id: "", reg: "fra1", cidr: "10.42.0.0/16"},
		{name: "invalid region", id: "prod", reg: "FRA1", cidr: "10.42.0.0/16"},
		{name: "public cidr", id: "prod", reg: "fra1", cidr: "192.0.2.0/24"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewFabricSpecFromValues("acct-a", tc.id, tc.reg, tc.cidr); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
