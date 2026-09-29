// adr: 361 — tenant egress is default-deny: web ports, pinned DNS, per-VM
// rate limits.
package fcvm

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestApplyTenantEgressPolicyPerPlan(t *testing.T) {
	for plan, want := range map[api.Plan][2]int{
		api.PlanFree: {10, 40}, api.PlanHobby: {20, 80}, api.PlanPro: {50, 200}, api.PlanScale: {100, 400},
	} {
		var nc netns.Config
		applyTenantEgressPolicy(&nc, plan)
		if !reflect.DeepEqual(nc.EgressPorts, []uint16{80, 443}) || nc.EgressConnRate != want[0] || nc.EgressConnBurst != want[1] {
			t.Errorf("%s: ports=%v rate=%d burst=%d, want [80 443] %d %d", plan, nc.EgressPorts, nc.EgressConnRate, nc.EgressConnBurst, want[0], want[1])
		}
	}
	// An unknown plan still gets the port policy (Wake rejects it anyway).
	var nc netns.Config
	applyTenantEgressPolicy(&nc, api.Plan("mystery"))
	if len(nc.EgressPorts) == 0 {
		t.Fatal("unknown plan left the guest without a port policy")
	}
}

// Every tenant wake renders the default-deny policy into the instance's
// netns: the port set, the plan's rate limit and the DNS pin.
func TestWakeRendersTenantEgressPolicy(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "egress-wake") })
	inst, err := m.Wake(t.Context(), WakeRequest{
		Instance: "egress-wake", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanFree, EgressMbit: 10, Snapshot: usableSnapshot(),
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if !reflect.DeepEqual(inst.Net.EgressPorts, []uint16{80, 443}) || inst.Net.EgressConnRate != 10 {
		t.Fatalf("instance network plan = ports %v rate %d, want [80 443] 10", inst.Net.EgressPorts, inst.Net.EgressConnRate)
	}
	for _, want := range []string{
		"add element ip faas egress_ports { 80,443 }",
		"tcp dport != @egress_ports",
		"meta l4proto != tcp",
		"limit rate over 10/second burst 40 packets",
		"guest_dns",
	} {
		if !run.ran(want) {
			t.Errorf("wake did not render %q", want)
		}
	}
}
