//go:build linux && metal

// adr: 531 — real nft setup and transaction rollback in isolated netns.
package fcvm

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalEgressCircuitDualFamilyAtomicReplacement(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires private mount and network namespaces")
	}
	run := wire.ExecRunner{}
	for _, argv := range [][]string{{"ip", "link", "add", netns.TenantBridge, "type", "bridge"}, {"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge}, {"ip", "link", "set", netns.TenantBridge, "up"}} {
		if err := run.Run(t.Context(), argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	nc := netns.NewConfig("circuitmetal", "fc-circuitmetal", "vh-circuit", "vp-circuit", netip.MustParseAddr("10.100.0.2"))
	nc.EgressCircuitEnabled = true
	m := newTestManager(run, &fakeVMM{}).WithEgressCircuitBreaker(true)
	t.Cleanup(func() {
		for _, argv := range nc.TeardownCommands() {
			_ = run.Run(context.Background(), argv)
		}
	})
	if err := m.setupNetwork(t.Context(), nc); err != nil {
		t.Fatal(err)
	}
	if err := m.registerEgressCircuitNetwork(t.Context(), "app", nc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.unregisterEgressCircuitNetwork(nc.Instance) })
	targets := []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432), circuitTarget("2001:db8::1", 5432)}
	if err := m.UpdateEgressCircuit(t.Context(), "app", targets); err != nil {
		t.Fatal(err)
	}
	assertSets := func(wantElements bool) {
		t.Helper()
		for _, family := range []struct{ name, element string }{{"ip", "203.0.113.9 . 5432"}, {"ip6", "2001:db8::1 . 5432"}} {
			out, err := exec.CommandContext(t.Context(), "ip", "netns", "exec", nc.Netns, "nft", "list", "set", family.name, "faas", netns.EgressCircuitSetName).CombinedOutput()
			if err != nil || strings.Contains(string(out), family.element) != wantElements {
				t.Fatalf("family=%s elements=%v err=%v rules=%s", family.name, wantElements, err, out)
			}
		}
	}
	assertSets(true)
	bad := append(nc.EgressCircuitSetCommands(nil), []string{"ip", "netns", "exec", nc.Netns, "nft", "add", "element", "ip6", "faas", "missing_set", "{ 2001:db8::2 . 5432 }"})
	if err := m.runNftCommands(t.Context(), nc.Netns, bad); err == nil {
		t.Fatal("invalid nft batch accepted")
	}
	assertSets(true) // both flushes roll back when any command fails
	if err := m.UpdateEgressCircuit(t.Context(), "app", nil); err != nil {
		t.Fatal(err)
	}
	assertSets(false)
}
