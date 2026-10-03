// adr: 361 — tenant egress is default-deny: web ports, pinned DNS, per-VM
// rate limits.
package fcvm

import (
	"context"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestApplyTenantEgressPolicyPerPlan(t *testing.T) {
	for plan, want := range map[api.Plan][2]int{
		api.PlanFree: {10, 40}, api.PlanHobby: {20, 80}, api.PlanPro: {50, 200}, api.PlanScale: {100, 400},
	} {
		var nc netns.Config
		applyTenantEgressPolicy(&nc, plan, nil)
		if !reflect.DeepEqual(nc.EgressPorts, []uint16{80, 443}) || nc.EgressConnRate != want[0] || nc.EgressConnBurst != want[1] {
			t.Errorf("%s: ports=%v rate=%d burst=%d, want [80 443] %d %d", plan, nc.EgressPorts, nc.EgressConnRate, nc.EgressConnBurst, want[0], want[1])
		}
	}
	// An unknown plan still gets the port policy (Wake rejects it anyway).
	var nc netns.Config
	applyTenantEgressPolicy(&nc, api.Plan("mystery"), nil)
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

// vmmd re-checks the ports it enforces: forbidden and duplicate ports never
// reach the set even if apid were bypassed.
func TestTenantEgressPortsFiltersForbidden(t *testing.T) {
	got := tenantEgressPorts(api.PlanPro, []uint16{5432, 25, 443, 0, 22, 6379, 5432, 3333, 53})
	if !reflect.DeepEqual(got, []uint16{80, 443, 5432, 6379}) {
		t.Fatalf("tenantEgressPorts = %v, want [80 443 5432 6379]", got)
	}
}

// A plan downgrade leaves the stored list in place; vmmd enforces the
// current plan's allowance, not the one the ports were declared under.
func TestTenantEgressPortsCappedByPlan(t *testing.T) {
	extra := []uint16{5000, 5001, 5002, 5003, 5004, 5005, 5006, 5007, 5008, 5009}
	for _, tc := range []struct {
		plan api.Plan
		want int
	}{
		{api.PlanFree, 2}, {api.PlanHobby, 2}, {api.PlanPro, 2 + 8}, {api.PlanScale, 2 + len(extra)}, {api.Plan("mystery"), 2},
	} {
		if got := tenantEgressPorts(tc.plan, extra); len(got) != tc.want {
			t.Errorf("%s: %d ports %v, want %d", tc.plan, len(got), got, tc.want)
		}
	}
}

func TestWakeRendersDeclaredEgressPorts(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "egress-extra") })
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: "egress-extra", AppID: "app-extra", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, EgressMbit: 100, EgressPorts: []uint16{6379, 5432},
		Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if !run.ran("add element ip faas egress_ports { 80,443,5432,6379 }") || !run.ran("add element ip6 faas egress_ports { 80,443,5432,6379 }") {
		t.Fatal("wake did not render the declared extra ports into both families")
	}
}

// A live app's extra ports are swapped in one nft transaction per instance;
// an unchanged set runs nothing.
func TestUpdateEgressPortsPatchesLiveInstances(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "egress-live") })
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: "egress-live", AppID: "app-live", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, EgressMbit: 100, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	before := run.inputRuns
	if err := m.UpdateEgressPorts(t.Context(), "app-live", []uint16{5432, 25}); err != nil {
		t.Fatalf("UpdateEgressPorts: %v", err)
	}
	if run.inputRuns != before+1 {
		t.Fatalf("live update ran %d nft transactions, want 1", run.inputRuns-before)
	}
	want := "flush set ip faas egress_ports\nadd element ip faas egress_ports { 80,443,5432 }\n" +
		"flush set ip6 faas egress_ports\nadd element ip6 faas egress_ports { 80,443,5432 }\n"
	if string(run.input) != want {
		t.Fatalf("transaction:\n%s\nwant:\n%s", run.input, want)
	}
	m.mu.Lock()
	livePorts := append([]uint16(nil), m.live["egress-live"].Net.EgressPorts...)
	m.mu.Unlock()
	if !reflect.DeepEqual(livePorts, []uint16{80, 443, 5432}) {
		t.Fatalf("instance ports after update = %v", livePorts)
	}
	if err := m.UpdateEgressPorts(t.Context(), "app-live", []uint16{5432}); err != nil || run.inputRuns != before+1 {
		t.Fatalf("an unchanged set re-ran nft (runs %d, err %v)", run.inputRuns-before, err)
	}
}

// A prepared namespace serves apps with declared ports: only the port set
// is swapped, or the port set and the DNAT target together.
func TestPreparedNetworkRetargetCoversEgressPorts(t *testing.T) {
	prepared := netns.NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	prepared.EgressPorts = []uint16{80, 443}
	for _, tc := range []struct {
		name       string
		change     func(*netns.Config)
		wantDNAT   bool
		wantPorts  bool
		wantUsable bool
	}{
		{"ports only", func(c *netns.Config) { c.EgressPorts = []uint16{80, 443, 5432} }, false, true, true},
		{"ports and app port", func(c *netns.Config) { c.EgressPorts = []uint16{80, 443, 5432}; c.GuestAppPort = 3000 }, true, true, true},
		{"app port only", func(c *netns.Config) { c.GuestAppPort = 3000 }, true, false, true},
		{"ports and rate differ", func(c *netns.Config) { c.EgressPorts = []uint16{80, 443, 5432}; c.EgressConnRate = 99 }, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := prepared
			tc.change(&requested)
			cmds, ok := preparedNetworkRetargetCommands(prepared, requested)
			if ok != tc.wantUsable {
				t.Fatalf("usable = %v, want %v", ok, tc.wantUsable)
			}
			var dnat, ports bool
			for _, argv := range cmds {
				line := strings.Join(argv, " ")
				dnat = dnat || strings.Contains(line, "flush chain ip faas prerouting")
				ports = ports || strings.Contains(line, "flush set ip faas egress_ports")
			}
			if dnat != tc.wantDNAT || ports != tc.wantPorts {
				t.Fatalf("retarget DNAT=%v ports=%v, want %v %v: %v", dnat, ports, tc.wantDNAT, tc.wantPorts, cmds)
			}
		})
	}
}

// adr: 361 — vmmd reports an instance's fan-out with its plan's ceiling;
// nothing is reported before the first window or for a gone instance.
func TestManagerEgressFanoutCarriesPlanCeiling(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "egress-fanout") })
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: "egress-fanout", AppID: "app-fanout", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanHobby, EgressMbit: 100, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, ok := m.EgressFanout("egress-fanout"); ok {
		t.Fatal("fan-out reported before any window was observed")
	}
	m.RecordEgressFanout("egress-fanout", 37)
	m.RecordEgressFanout("gone", 99)
	got, ok := m.EgressFanout("egress-fanout")
	hobby, _ := api.LimitsFor(api.PlanHobby)
	if !ok || got.NewDestinationsPerMinute != 37 || got.Limit != int64(hobby.EgressNewDestinationsPerMinute) {
		t.Fatalf("EgressFanout = %+v, %v; want 37 with the Hobby ceiling %d", got, ok, hobby.EgressNewDestinationsPerMinute)
	}
	if _, ok := m.EgressFanout("gone"); ok {
		t.Fatal("a sample for an unknown instance must be dropped")
	}
}

// adr: 361 — job VMs run tenant code and get the same network policy as app
// instances. Without it the always-declared egress_ports set stays empty and
// the job can open no outbound TCP.
func TestBootJobAppliesTenantEgressPolicy(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "job-egress") })
	if _, err := m.BootJob(t.Context(), testJobBootRequest("job-egress")); err != nil {
		t.Fatalf("BootJob: %v", err)
	}
	hobby, _ := api.LimitsFor(api.PlanHobby)
	for _, want := range []string{
		"add element ip faas egress_ports { 80,443 }",
		"add element ip6 faas egress_ports { 80,443 }",
		fmt.Sprintf("limit rate over %d/second", hobby.EgressNewConnPerSecond),
	} {
		if !run.ran(want) {
			t.Errorf("job VM network did not render %q", want)
		}
	}
}
