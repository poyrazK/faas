//go:build linux

// adr: 20261105103000
package fcvm

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// The child uses private network/mount namespaces; no host bridge or guest is
// touched. Exercise actual nft JSON expiry units and renderer handle capture.
func TestEgressRecoveryKernel(t *testing.T) {
	if os.Getenv("FAAS_EGRESS_RECOVERY_KERNEL_TEST") != "1" {
		t.Skip("opt-in isolated kernel networking acceptance")
	}
	if os.Getenv("FAAS_EGRESS_RECOVERY_KERNEL_INNER") != "1" {
		cmd := exec.CommandContext(t.Context(), "unshare", "--net", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestEgressRecoveryKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "FAAS_EGRESS_RECOVERY_KERNEL_INNER=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated egress recovery acceptance: %v\n%s", err, output)
		}
		return
	}
	run := privateKernelRunner{}
	if err := os.MkdirAll("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mount", "-t", "tmpfs", "tmpfs", "/run/netns"}, {"ip", "netns", "add", "fc-egress-recovery"}} {
		if err := run.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "netns", "del", "fc-egress-recovery"}) })
	nc := netns.NewConfig("egress-recovery", "fc-egress-recovery", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	applyTenantEgressPolicy(&nc, api.PlanPro, []uint16{81, 5432})
	nc.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/64")}
	if err := runAtomicNftPolicy(t.Context(), run, nc.Netns, nc.NftCommands()); err != nil {
		t.Fatal(err)
	}
	addresses := []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}
	commands := nc.ResolvedEgressAddCommands(addresses, 20*time.Second)
	commands = append(commands, nc.DatabaseEgressAddCommands(addresses, 5432, 25*time.Second)...)
	if err := runAtomicNftPolicy(t.Context(), run, nc.Netns, commands); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(run)
	inst := &Instance{Lease: Lease{Instance: nc.Instance}, AppID: "app", Plan: api.PlanPro, Net: netns.NewConfig(nc.Instance, nc.Netns, nc.VethHost, nc.VethPeer, nc.HostIP)}
	if err := m.restoreAdoptedEgress(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	if len(inst.resolvedEgress) != 2 || len(inst.databaseEgress) != 2 || !inst.Net.DNSGated || inst.Net.EgressConnRate == 0 {
		t.Fatal("live kernel policy was not recovered")
	}
	for _, addr := range addresses {
		if remaining := time.Until(inst.resolvedEgress[addr]); remaining <= 0 || remaining > 20*time.Second {
			t.Fatalf("kernel expiry changed units or renewed: %s", remaining)
		}
	}
	for _, family := range []string{"ip", "ip6"} {
		if handle, err := listChainHandles(t.Context(), run, nc.Netns, family, "faas", "forward"); err != nil || handle == 0 {
			t.Fatalf("real %s public allowlist handle=%d err=%v", family, handle, err)
		}
	}
	// Renew existing keys together with absent keys. Linux 6.8 acknowledges
	// add-existing without extending it, so verify the actual kernel expiry.
	addresses = append(addresses, netip.MustParseAddr("203.0.113.20"), netip.MustParseAddr("2001:db8::20"))
	commands = nc.ResolvedEgressRefreshCommands(addresses, time.Minute)
	commands = append(commands, nc.DatabaseEgressRefreshCommands(addresses, 5432, time.Minute)...)
	if err := runAtomicNftPolicy(t.Context(), run, nc.Netns, commands); err != nil {
		t.Fatal(err)
	}
	if err := m.restoreAdoptedEgress(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	for _, addr := range addresses {
		for _, until := range []time.Time{inst.resolvedEgress[addr], inst.databaseEgress[databaseEgressKey{addr, 5432}]} {
			if remaining := time.Until(until); remaining < 50*time.Second || remaining > time.Minute {
				t.Fatalf("kernel did not renew %s: %s", addr, remaining)
			}
		}
	}
}
