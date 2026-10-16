//go:build metal

// Metal regression for tier-2 PR-B (ADR-031 + ADR-033): the
// in-place netns nft patch that closes the live-instance drift
// gap. The unit tests in pkg/fcvm/manager_test.go pin the
// argv SHAPE for UpdateEgressAllowlist; this file pins the
// RUNTIME contract: starting with allowlist [A], patching to
// [B] in-place, the per-netns forward chain ends with [B]
// (NOT [A]) and the chain is still well-formed (no dangling
// rules, the chain policy is still accept).
//
// Why an in-place patch and not a Wake re-render? Because Track
// B is the load-bearing track: the contract is "PATCH /v1/apps/
// {slug} updates the LIVE running netns without a cold wake
// tax". A metal test that exercises the renderer + nft together
// in a real netns is the only way to prove the in-place path
// works end-to-end (handle capture → delete-by-handle →
// add-at-same-offset) — unit tests stub the runner.
//
// Triple-skip when env can't satisfy: non-Linux runtime, no
// `nft` or `ip` on PATH, or insufficient privilege to create a
// netns (CAP_SYS_ADMIN). Pattern mirrors the existing
// allowlist_metal_test.go in the same package.

package netns

import (
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// runNet runs an `ip netns exec <ns>` command and returns
// combined output + error. Skips the surrounding subtest on
// CAP_SYS_ADMIN probe failure (mirrors the existing
// triple-skip pattern in allowlist_metal_test.go).
func runNet(t *testing.T, ns, cmd string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"netns", "exec", ns, cmd}, args...)
	out, err := exec.Command("ip", full...).CombinedOutput()
	return string(out), err
}

// listChainForFamily returns the nft ruleset filtered to one
// chain. Empty string means the chain does not exist (the test
// fails on that — Wake is expected to have created it).
func listChainForFamily(t *testing.T, ns, table, family, chain string) string {
	t.Helper()
	out, err := runNet(t, ns, "nft", "list", "chain", family, table, chain)
	if err != nil {
		return ""
	}
	return out
}

// handleFromListChainOutput scans the nft `-a list chain` output
// for the rule line containing the given substring and returns
// the trailing `# handle N` integer as a string. Returns the
// empty string if no match.
//
// The handle extraction is regex-anchored on `# handle \d+` so
// the rule body's argument tokens (which may contain the word
// "handle" in a future renderer extension) can't collide with
// the comment-anchored handle. The match is intended to be
// positional — the handle is the LAST `# handle N` token on
// the matching line, which is what `nft -a` emits.
func handleFromListChainOutput(ruleset, lineContains string) string {
	re := regexp.MustCompile(`# handle (\d+)`)
	for _, line := range strings.Split(ruleset, "\n") {
		if !strings.Contains(line, lineContains) {
			continue
		}
		match := re.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if _, err := strconv.ParseUint(match[1], 10, 64); err != nil {
			continue
		}
		return match[1]
	}
	return ""
}

func TestMetalEgressAllowlist_LivePatch(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip (iproute2) not on PATH; install iproute2 on a Linux host to run this gate")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft (nftables) not on PATH; install nftables on a Linux host to run this gate")
	}
	probe := exec.Command("ip", "netns", "add", "faas_livepatch_probe")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", "faas_livepatch_probe").CombinedOutput()

	nsName := "faas_livepatch_" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", nsName).CombinedOutput() })
	if out, err := exec.Command("ip", "netns", "add", nsName).CombinedOutput(); err != nil {
		t.Fatalf("ip netns add %s: %v\n%s", nsName, err, out)
	}

	// Stage 1: render a Wake with allowlist [1.2.3.0/24] and
	// install the ruleset into the netns. This mirrors what
	// pkg/fcvm.Manager.Wake does.
	c := NewConfig("livepatch", nsName, "vh-live", "vp-live",
		netip.MustParseAddr("10.100.0.245"))
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	for _, argv := range c.NftCommands() {
		full := append([]string{"ip", "netns", "exec", nsName}, argv...)
		if out, err := exec.Command(full[0], full[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("Wake rule failed: %v\nargv: %v\noutput:\n%s", err, full, out)
		}
	}

	// Confirm the initial allowlist rule is installed.
	initial := listChainForFamily(t, nsName, "faas", "ip", "forward")
	if !strings.Contains(initial, "ip daddr { 1.2.3.0/24 } accept") {
		t.Fatalf("expected initial allowlist rule in chain, got:\n%s", initial)
	}

	// Stage 2: simulate UpdateEgressAllowlist([8.8.8.0/24]) the way
	// pkg/fcvm does it: capture the accept's handle and replace that
	// rule in place, so it keeps its position before the DNS gate,
	// SMTP and terminal allowlist drops (H10-36: delete-then-add
	// appended it after them, where it never matched).
	listOut, err := runNet(t, nsName, "nft", "-a", "list", "chain", "ip", "faas", "forward")
	if err != nil {
		t.Fatalf("nft -a list chain: %v\n%s", err, listOut)
	}
	handle := handleFromListChainOutput(listOut, "ip daddr { 1.2.3.0/24 } accept")
	if handle == "" {
		t.Fatalf("could not find allowlist rule handle in:\n%s", listOut)
	}
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
	replace := allowlistReplaceArgv(t, c, "ip", "8.8.8.0/24", handle)
	if out, err := exec.Command(replace[0], replace[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("nft replace rule: %v\nargv: %v\noutput:\n%s", err, replace, out)
	}

	// Stage 3: assert the chain now has [8.8.8.0/24] and
	// no longer has [1.2.3.0/24]. This is the load-bearing
	// in-place drift close.
	patched := listChainForFamily(t, nsName, "faas", "ip", "forward")
	if !strings.Contains(patched, "ip daddr { 8.8.8.0/24 } accept") {
		t.Errorf("patched chain missing new allowlist:\n%s", patched)
	}
	if strings.Contains(patched, "ip daddr { 1.2.3.0/24 } accept") {
		t.Errorf("patched chain still has old allowlist — delete-by-handle did not take effect:\n%s", patched)
	}
	// Chain policy must still be accept (the renderer
	// re-runs every arg, but only the rule body changes —
	// `add counter accept` is the policy line and we must
	// not have introduced a `drop` or removed the policy).
	if !strings.Contains(patched, "policy accept") {
		t.Errorf("chain policy regressed after live patch:\n%s", patched)
	}
	assertAcceptBeforeTerminalDrop(t, patched, "ip daddr { 8.8.8.0/24 } accept")
}

// TestMetalEgressAllowlist_LivePatchV6 mirrors the v4 patch for
// the v6 chain. ADR-032 keeps the two chains in separate
// per-family tables; the in-place patch is family-local.
func TestMetalEgressAllowlist_LivePatchV6(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip (iproute2) not on PATH; install iproute2 on a Linux host to run this gate")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft (nftables) not on PATH; install nftables on a Linux host to run this gate")
	}
	probe := exec.Command("ip", "netns", "add", "faas_livepatch_v6_probe")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", "faas_livepatch_v6_probe").CombinedOutput()

	nsName := "faas_livepatch_v6_" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", nsName).CombinedOutput() })
	if out, err := exec.Command("ip", "netns", "add", nsName).CombinedOutput(); err != nil {
		t.Fatalf("ip netns add %s: %v\n%s", nsName, err, out)
	}

	c := NewConfig("livepatch-v6", nsName, "vh-live-v6", "vp-live-v6",
		netip.MustParseAddr("10.100.0.246"))
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")}
	for _, argv := range c.NftCommands() {
		full := append([]string{"ip", "netns", "exec", nsName}, argv...)
		if out, err := exec.Command(full[0], full[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("Wake rule failed: %v\nargv: %v\noutput:\n%s", err, full, out)
		}
	}
	listOut, err := runNet(t, nsName, "nft", "-a", "list", "chain", "ip6", "faas", "forward")
	if err != nil {
		t.Fatalf("nft -a list chain (v6): %v\n%s", err, listOut)
	}
	handle := handleFromListChainOutput(listOut, "ip6 daddr { 2001:db8::/32 } accept")
	if handle == "" {
		t.Fatalf("could not find v6 allowlist rule handle in:\n%s", listOut)
	}
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("fe80::/10")}
	replace := allowlistReplaceArgv(t, c, "ip6", "fe80::/10", handle)
	if out, err := exec.Command(replace[0], replace[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("nft replace rule (v6): %v\nargv: %v\noutput:\n%s", err, replace, out)
	}
	patched := listChainForFamily(t, nsName, "faas", "ip6", "forward")
	if !strings.Contains(patched, "ip6 daddr { fe80::/10 } accept") {
		t.Errorf("patched v6 chain missing new allowlist:\n%s", patched)
	}
	if strings.Contains(patched, "ip6 daddr { 2001:db8::/32 } accept") {
		t.Errorf("patched v6 chain still has old allowlist:\n%s", patched)
	}
	if !strings.Contains(patched, "policy accept") {
		t.Errorf("v6 chain policy regressed after live patch:\n%s", patched)
	}
	assertAcceptBeforeTerminalDrop(t, patched, "ip6 daddr { fe80::/10 } accept")
}

// allowlistReplaceArgv renders c's allowlist accept for cidr and turns it
// into the one-transaction `nft insert rule … handle <h> <spec> ; delete rule
// … handle <h>` pkg/fcvm runs on a live patch (swapRuleArgv: nftables 1.0.9
// aborts on `replace rule` with an anonymous set).
func allowlistReplaceArgv(t *testing.T, c Config, family, cidr, handle string) []string {
	t.Helper()
	for _, argv := range c.NftCommands() {
		if argv[len(argv)-1] != "accept" || !strings.Contains(strings.Join(argv, " "), " daddr { "+cidr+" }") {
			continue
		}
		for i := 0; i+5 <= len(argv); i++ {
			if argv[i] == "add" && argv[i+1] == "rule" && argv[i+2] == family {
				out := append([]string{}, argv[:i]...)
				out = append(out, "insert", "rule", family, "faas", "forward", "handle", handle)
				out = append(out, argv[i+5:]...)
				return append(out, ";", "delete", "rule", family, "faas", "forward", "handle", handle)
			}
		}
	}
	t.Fatalf("no %s allowlist accept for %s in NftCommands", family, cidr)
	return nil
}

// assertAcceptBeforeTerminalDrop fails when the allowlist accept no longer
// precedes the chain's terminal allowlist drop: a patched rule after it
// never matches (H10-36).
func assertAcceptBeforeTerminalDrop(t *testing.T, chain, accept string) {
	t.Helper()
	acceptAt, dropAt := -1, -1
	for i, line := range strings.Split(chain, "\n") {
		switch {
		case strings.Contains(line, accept):
			acceptAt = i
		case strings.Contains(line, EgressDenyCounterAllowlist) && strings.Contains(line, "drop"):
			dropAt = i
		}
	}
	if acceptAt < 0 || dropAt >= 0 && acceptAt > dropAt {
		t.Errorf("allowlist accept (line %d) must precede the terminal allowlist drop (line %d):\n%s", acceptAt, dropAt, chain)
	}
}
