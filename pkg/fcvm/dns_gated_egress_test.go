// adr: 373 — DNS-gated egress; 20261026150000 — bound managed database egress.
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func wakeGated(t *testing.T, m *Manager, instance, app string) netip.Addr {
	t.Helper()
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: instance, AppID: app, BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, EgressMbit: 100, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.live[instance].Net.DNSGated {
		t.Fatal("tenant instance is not DNS-gated")
	}
	return m.live[instance].Net.HostIP
}

func TestAllowResolvedEgressAddsOncePerTTL(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	source := wakeGated(t, m, "dns-a", "app-dns")
	addrs := []netip.Addr{netip.MustParseAddr("198.51.100.10"), netip.MustParseAddr("2001:db8::7")}

	before := run.inputRuns
	if err := m.AllowResolvedEgress(t.Context(), source, addrs, 30*time.Second, 0); err != nil {
		t.Fatalf("AllowResolvedEgress: %v", err)
	}
	if run.inputRuns != before+1 {
		t.Fatalf("nft transactions = %d, want 1", run.inputRuns-before)
	}
	got := string(run.input)
	for _, want := range []string{
		"add element ip faas egress_resolved { 198.51.100.10 timeout 600s }", // 30s clamped up to the floor
		"add element ip6 faas egress_resolved { 2001:db8::7 timeout 600s }",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("transaction:\n%s\nmissing %q", got, want)
		}
	}
	if err := m.AllowResolvedEgress(t.Context(), source, addrs[:1], 30*time.Second, 0); err != nil || run.inputRuns != before+1 {
		t.Fatalf("a repeat within half the TTL re-ran nft (runs %d, err %v)", run.inputRuns-before, err)
	}
	// A longer TTL extends the element.
	if err := m.AllowResolvedEgress(t.Context(), source, addrs[:1], time.Hour, 0); err != nil || run.inputRuns != before+2 ||
		!strings.Contains(string(run.input), "198.51.100.10 timeout 3600s") {
		t.Fatalf("a longer TTL did not extend the element (runs %d, err %v):\n%s", run.inputRuns-before, err, run.input)
	}
	if err := m.AllowResolvedEgress(t.Context(), netip.MustParseAddr("10.100.99.99"), addrs, time.Minute, 0); !errors.Is(err, ErrResolvedEgressNoInstance) {
		t.Fatalf("unknown source = %v, want ErrResolvedEgressNoInstance", err)
	}
}

// ADR 20261026150000: a bound database's name opens its port to its addresses
// on a plan without extra ports, in the same transaction as the DNS gate, and
// a new instance of the app is seeded with the pairs.
func TestAllowResolvedEgressOpensTheBoundDatabasePort(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: "db-a", AppID: "app-db", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanHobby, EgressMbit: 100, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "db-a") })
	m.mu.Lock()
	nc := m.live["db-a"].Net
	m.mu.Unlock()
	if !nc.DatabaseEgress || slices.Contains(nc.EgressPorts, 5432) {
		t.Fatalf("hobby tenant: database egress %v, ports %v", nc.DatabaseEgress, nc.EgressPorts)
	}
	addr := netip.MustParseAddr("198.51.100.20")
	before := run.inputRuns
	if err := m.AllowResolvedEgress(t.Context(), nc.HostIP, []netip.Addr{addr}, time.Minute, 5432); err != nil {
		t.Fatalf("AllowResolvedEgress: %v", err)
	}
	got := string(run.input)
	if run.inputRuns != before+1 || !strings.Contains(got, "add element ip faas egress_resolved { 198.51.100.20 timeout 600s }") ||
		!strings.Contains(got, "add element ip faas egress_db { 198.51.100.20 . 5432 timeout 600s }") {
		t.Fatalf("one transaction with both sets wanted (runs %d):\n%s", run.inputRuns-before, got)
	}
	if err := m.AllowResolvedEgress(t.Context(), nc.HostIP, []netip.Addr{addr}, time.Minute, 5432); err != nil || run.inputRuns != before+1 {
		t.Fatalf("a fresh pair re-ran nft (runs %d, err %v)", run.inputRuns-before, err)
	}
	if err := m.AllowResolvedEgress(t.Context(), nc.HostIP, []netip.Addr{addr}, time.Minute, 6543); err != nil || run.inputRuns != before+2 ||
		strings.Contains(string(run.input), "egress_resolved") || !strings.Contains(string(run.input), "egress_db { 198.51.100.20 . 6543") {
		t.Fatalf("a second database port must add only its pair (runs %d, err %v):\n%s", run.inputRuns-before, err, run.input)
	}
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: "db-b", AppID: "app-db", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanHobby, EgressMbit: 100, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "db-b") })
	if seed := string(run.input); !strings.Contains(seed, "egress_db { 198.51.100.20 . 5432 timeout 600s }") && !strings.Contains(seed, "egress_db { 198.51.100.20 . 6543 timeout 600s }") {
		t.Fatalf("the next instance was not seeded with the database pairs:\n%s", seed)
	}
	m.mu.Lock()
	seeded := len(m.live["db-b"].databaseEgress)
	m.mu.Unlock()
	if seeded != 2 {
		t.Fatalf("seeded pairs = %d, want 2", seeded)
	}
}

// The DNS-gating switch leaves bound databases reachable.
func TestDatabaseEgressWithoutDNSGating(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{}).WithDNSGatedEgress(false)
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: "db-off", AppID: "app-db-off", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanHobby, EgressMbit: 100, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "db-off") })
	m.mu.Lock()
	source := m.live["db-off"].Net.HostIP
	m.mu.Unlock()
	if err := m.AllowResolvedEgress(t.Context(), source, []netip.Addr{netip.MustParseAddr("198.51.100.30")}, time.Minute, 5432); err != nil {
		t.Fatalf("AllowResolvedEgress: %v", err)
	}
	if got := string(run.input); strings.Contains(got, "egress_resolved") || !strings.Contains(got, "egress_db { 198.51.100.30 . 5432") {
		t.Fatalf("ungated instance transaction:\n%s", got)
	}
}

// A new instance of the same app starts with the app's recent resolutions.
func TestResolvedEgressSeedsNextInstance(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	source := wakeGated(t, m, "dns-first", "app-seed")
	if err := m.AllowResolvedEgress(t.Context(), source, []netip.Addr{netip.MustParseAddr("203.0.113.9")}, time.Minute, 0); err != nil {
		t.Fatalf("AllowResolvedEgress: %v", err)
	}
	wakeGated(t, m, "dns-second", "app-seed")
	if !strings.Contains(string(run.input), "add element ip faas egress_resolved { 203.0.113.9 timeout 600s }") {
		t.Fatalf("second instance was not seeded; last transaction:\n%s", run.input)
	}
	m.mu.Lock()
	_, seeded := m.live["dns-second"].resolvedEgress[netip.MustParseAddr("203.0.113.9")]
	m.mu.Unlock()
	if !seeded {
		t.Fatal("seeded address not recorded on the new instance")
	}
}

func TestClampResolvedTTL(t *testing.T) {
	for _, tc := range []struct{ in, want time.Duration }{
		{0, 10 * time.Minute}, {5 * time.Minute, 10 * time.Minute}, {30 * time.Minute, 30 * time.Minute}, {48 * time.Hour, time.Hour},
	} {
		if got := clampResolvedTTL(tc.in); got != tc.want {
			t.Errorf("clamp(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// The operator's switch turns DNS gating off for new VMs and leaves the
// rest of the egress policy in place.
func TestDNSGatedEgressOffSwitch(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{}).WithDNSGatedEgress(false)
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: "dns-off", AppID: "app-off", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, EgressMbit: 100, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "dns-off") })
	m.mu.Lock()
	gated := m.live["dns-off"].Net.DNSGated
	m.mu.Unlock()
	if gated || run.ran("egress_resolved") {
		t.Fatal("DNS gating rendered with the switch off")
	}
	if !run.ran("add element ip faas egress_ports { 80,443 }") {
		t.Fatal("the switch must not remove the port policy")
	}
}
