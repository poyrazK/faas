// adr: 20261027090013 §6 — a registries-mode sandbox session VM boots with a
// DNS-gated network, egress on 443 only and its DNS pinned to the
// registries-only resolver; resolved addresses open only for registry
// names; and it never boots without the DNS gate.
package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func registriesSandboxWake(instance string) SandboxWakeRequest {
	return SandboxWakeRequest{Instance: instance, SandboxID: "sbx-" + instance, AccountID: "acct", Plan: api.PlanPro,
		BaseKey: "/base/runner-agent-amd64.ext4", LayerKey: "/base/sandbox-layer-v1-amd64.ext4", VcpuCount: 2, MemSizeMiB: 1024,
		CPUMillicores: 2000, Network: api.SandboxNetworkRegistries}
}

func TestRegistriesSandboxBootsDNSGatedOn443(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &sandboxTestVMM{fakeVMM: &fakeVMM{}})
	inst, err := m.WakeSandbox(t.Context(), registriesSandboxWake("sbx-net"))
	if err != nil {
		t.Fatalf("WakeSandbox: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "sbx-net") })
	if inst.Lease.Networkless || !inst.Lease.AgentSized || !inst.ExecutionOnly {
		t.Fatalf("lease = %+v, execution-only %v; want a networked, agent-sized execution-only VM", inst.Lease, inst.ExecutionOnly)
	}
	nc := inst.Net
	if !nc.SandboxRegistries || !nc.DNSGated || !slices.Equal(nc.EgressPorts, []uint16{443}) || nc.DatabaseEgress || len(nc.EgressAllowlist) != 0 {
		t.Fatalf("network = %+v; want registries, DNS-gated, 443 only", nc)
	}
	// §10: root in the guest sends only from the fixed MAC and GuestIP.
	if nc.GuestMAC != netns.SandboxGuestMAC || len(nc.GuestGuardCommands()) == 0 {
		t.Fatalf("guest MAC %q; want the guarded %q", nc.GuestMAC, netns.SandboxGuestMAC)
	}
	period := api.PlanPro.CPUPeriodUS()
	if got, err := leaseCPUMax(inst.Lease); err != nil || got != fmt.Sprintf("%d %d", 2*period, period) {
		t.Fatalf("cpu.max = %q, %v; want two whole vCPUs", got, err)
	}
	pinned := false
	for _, argv := range nc.NftCommands() {
		pinned = pinned || strings.Contains(strings.Join(argv, " "), "dnat to "+nc.HostBridgeIP.String()+":"+itoa(netns.SandboxRegistriesDNSPort))
	}
	if !pinned {
		t.Fatal("guest DNS is not pinned to the registries resolver")
	}

	source := nc.HostIP
	owner := resolvedOwner(t, m, source)
	addr := []netip.Addr{netip.MustParseAddr("198.51.100.20")}
	before := run.inputRuns
	for _, name := range []string{"", "example.com", "evilgithub.com"} {
		if err := m.AllowResolvedEgressNamed(t.Context(), source, owner, name, addr, time.Minute, 0); err != nil || run.inputRuns != before {
			t.Fatalf("%q opened egress (runs %d, err %v)", name, run.inputRuns-before, err)
		}
	}
	if err := m.AllowResolvedEgress(t.Context(), source, owner, addr, time.Minute, 0); err != nil || run.inputRuns != before {
		t.Fatalf("an unnamed answer opened egress (runs %d, err %v)", run.inputRuns-before, err)
	}
	if err := m.AllowResolvedEgressNamed(t.Context(), source, owner, "registry.npmjs.org", addr, time.Minute, 0); err != nil || run.inputRuns != before+1 ||
		!strings.Contains(string(run.input), "egress_resolved { 198.51.100.20") {
		t.Fatalf("a registry answer did not open egress (runs %d, err %v):\n%s", run.inputRuns-before, err, run.input)
	}
}

func TestRegistriesSandboxRefusesToBootWithoutTheDNSGate(t *testing.T) {
	m := newTestManager(&fakeInputRunner{}, &sandboxTestVMM{fakeVMM: &fakeVMM{}}).WithDNSGatedEgress(false)
	if _, err := m.WakeSandbox(t.Context(), registriesSandboxWake("sbx-off")); err == nil || !strings.Contains(err.Error(), "DNS-gated") {
		t.Fatalf("WakeSandbox = %v, want a refusal naming the DNS gate", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, live := m.live["sbx-off"]; live {
		t.Fatal("a refused sandbox stayed live")
	}
}

// The guard's MAC is the one Firecracker gives the guest.
func TestColdBootFixesTheGuardedGuestMAC(t *testing.T) {
	cfg := BuildColdBootConfig(ColdBootSpec{Tap: "tap0", GuestMAC: netns.SandboxGuestMAC, VcpuCount: 1, MemSizeMiB: 512}, 3)
	if len(cfg.NetworkInterfaces) != 1 || cfg.NetworkInterfaces[0].GuestMAC != netns.SandboxGuestMAC {
		t.Fatalf("interfaces %+v", cfg.NetworkInterfaces)
	}
	raw, err := json.Marshal(BuildColdBootConfig(ColdBootSpec{Tap: "tap0", VcpuCount: 1, MemSizeMiB: 512}, 3).NetworkInterfaces)
	if err != nil || strings.Contains(string(raw), "guest_mac") {
		t.Fatalf("an unguarded VM fixes a MAC: %s %v", raw, err)
	}
}
