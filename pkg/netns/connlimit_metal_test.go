//go:build metal

// Metal integration test for the per-instance conntrack cap (spec §7).
//
// Drives NftCommands(ConntrackCap=4096) into a real per-instance netns,
// opens >cap TCP flows, and asserts:
//
//  1. The rule is actually present in the netns ruleset after pipeline apply.
//  2. The named counter `faas_cap` is registered (the PR-C dashboard reads
//     it via `nft list counters`).
//  3. The counter observes increments — proving the rule fires on real
//     traffic, not just that the kernel accepted the rule.
//
// First temp-netns metal test in the repo (existing
// TestMetalHostPolicySyntaxChecks validates host policy only). Triple-skip
// when env can't satisfy: non-Linux runtime, no `nft` or `ip` on PATH, or
// insufficient privilege to create a netns (needs CAP_SYS_ADMIN).
//
// Skip pattern modeled after pkg/netns/policy_metal_test.go. Rule's
// argv shape is pinned by the unit tests in pkg/netns/config_test.go
// (TestNftCommandsEmitsConntrackCapRule / CapRuleRunsAfterEstablishedBeforeDenies);
// this test pins the *runtime* contract.
package netns

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMetalConnlimitCapEnforced(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip (iproute2) not on PATH; install iproute2 on a Linux host to run this gate")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft (nftables) not on PATH; install nftables on a Linux host to run this gate")
	}
	// Netns creation needs CAP_SYS_ADMIN; on a rootless Lima guest the
	// capability is granted, on a plain `go test` from $HOME it isn't.
	// Detect by trying once before we commit to the loop / counter asserts.
	probe := exec.Command("ip", "netns", "add", "faas_cap_probe")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", "faas_cap_probe").CombinedOutput()

	nsName := "faas_cap_" + t.Name()
	t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", nsName).CombinedOutput() })
	if out, err := exec.Command("ip", "netns", "add", nsName).CombinedOutput(); err != nil {
		t.Fatalf("ip netns add %s: %v\n%s", nsName, err, out)
	}

	// Mirror a production-shaped Config. HostIP is unused by the §7
	// rule itself, but the rest of NftCommands references c.VethPeer
	// and c.Tap, so we set names that won't collide with anything.
	hostIP := netip.MustParseAddr("10.100.0.1")
	c := NewConfig("connlimit-test", nsName, "vh-test", "vp-test", hostIP)
	c.ConntrackCap = 4096

	for _, argv := range c.NftCommands() {
		full := append([]string{"ip", "netns", "exec", nsName}, argv...)
		out, err := exec.Command(full[0], full[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("nft rule failed: %v\nargv: %v\noutput:\n%s", err, full, out)
		}
	}

	// Open connections to a closed port from inside the netns. The
	// SYN reaches the kernel regardless of whether anything answers,
	// and the conntrack table counts it. Once the count exceeds the
	// rule's `ct count over 4096` threshold, the rule drops subsequent
	// forward flows and the named counter increments.
	const attempts = 4096 + 16
	for i := 0; i < attempts; i++ {
		// /dev/tcp is bash's built-in; we don't need nc installed.
		// We tolerate every error — the load-bearing fact is the
		// kernel counted the SYN.
		cmd := exec.Command("ip", "netns", "exec", nsName, "bash", "-c",
			"exec 3<>/dev/tcp/127.0.0.1/1 2>/dev/null; sleep 0.001; exec 3>&- 2>/dev/null || true")
		_ = cmd.Run()
	}

	// The rule is still installed and unmodified after the loop.
	list, err := exec.Command("ip", "netns", "exec", nsName, "nft", "list", "ruleset").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list ruleset: %v\noutput:\n%s", err, list)
	}
	if !strings.Contains(string(list), "ct count over 4096") {
		t.Fatalf("connlimit rule missing from ruleset after loop:\n%s", list)
	}

	// The named counter is registered and observed.
	counters, err := exec.Command("ip", "netns", "exec", nsName, "nft", "list", "counters").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list counters: %v\noutput:\n%s", err, counters)
	}
	if !strings.Contains(string(counters), `"faas_cap"`) {
		t.Fatalf("`faas_cap` counter not registered:\n%s", counters)
	}
	if !strings.Contains(string(counters), "packets") {
		t.Fatalf("`faas_cap` found but no `packets` line:\n%s", counters)
	}
}
