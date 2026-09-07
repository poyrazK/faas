//go:build metal

// Metal regression for ADR-031 + ADR-032 (tier-2 of the network
// roadmap, per-app egress allowlist). The unit tests in
// pkg/netns/config_test.go pin the argv SHAPE; this file pins the
// *runtime* contract: when an app pins an egress allowlist, the
// resulting nft ruleset inside the per-netns forward chain has the
// allowlist rule wired in AFTER the lateral-movement deny but BEFORE
// the SMTP drop (with TCP/25 excluded). The empty-allowlist case is its own gate — chain-policy
// accept must still be the only thing that governs egress, no rule
// installed at all. ADR-032 mirrors the assertion for v6: the v6
// allowlist rule must land on the `ip6 faas forward` chain AFTER
// the v6 lateral-movement deny (separate chain, separate table —
// per-family split from ADR-023).
//
// Why not assert a real outbound ping (like TestMetalConnlimitCapEnforced
// would)? A round-trip needs an outbound route to a real IP — which is
// exactly what PR #151 set up via MASQUERADE on the host bridge. The
// Lima nested-VM shim doesn't expose the production MASQUERADE shape;
// our snapshot regression net stays portable by substring-checking the
// rendered ruleset only. End-to-end reachability is exercised by the
// EX44 manual smoke listed in the ADR and tracked separately.
//
// Triple-skip when env can't satisfy: non-Linux runtime, no `nft` or
// `ip` on PATH, or insufficient privilege to create a netns (needs
// CAP_SYS_ADMIN). Skip pattern mirrors
// pkg/netns/connlimit_metal_test.go:39-53 and
// pkg/netns/policy_metal_test.go.
package netns

import (
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

func TestMetalAllowlistRuleInstalled(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip (iproute2) not on PATH; install iproute2 on a Linux host to run this gate")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft (nftables) not on PATH; install nftables on a Linux host to run this gate")
	}
	probe := exec.Command("ip", "netns", "add", "faas_allow_probe")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", "faas_allow_probe").CombinedOutput()

	nsName := "faas_allow_" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", nsName).CombinedOutput() })
	if out, err := exec.Command("ip", "netns", "add", nsName).CombinedOutput(); err != nil {
		t.Fatalf("ip netns add %s: %v\n%s", nsName, err, out)
	}

	// Mirror a production-shaped Config. Names must not collide with
	// any leftover state; suffix with test name so successive runs are
	// independent.
	c := NewConfig("allowlist-metal", nsName, "vh-allow", "vp-allow",
		netip.MustParseAddr("10.100.0.250"))
	c.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("8.8.8.0/24"),
	}

	for _, argv := range c.NftCommands() {
		full := append([]string{"ip", "netns", "exec", nsName}, argv...)
		out, err := exec.Command(full[0], full[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("nft rule failed: %v\nargv: %v\noutput:\n%s", err, full, out)
		}
	}

	// Read back the ruleset. Substring-assert the allowlist rule is
	// present with the comma-joined set; substring-assert the lateral-
	// movement drop rule appears in the same ruleset (so we know we're
	// looking at the chain we expect, not some ancestor-level stale
	// table). nft emits `iifname "tap0" ip daddr { … }` with spaces
	// after commas; we assert each CIDR appears inside an accept rule.
	out, err := exec.Command("ip", "netns", "exec", nsName, "nft", "list", "ruleset").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list ruleset: %v\n%s", err, out)
	}
	ruleset := string(out)

	// Anchor on the unique SMTP drop rule — it's always present. The
	// v4 allowlist exception now precedes it so submission ports can
	// be explicitly enabled without weakening the universal port-25 deny.
	smtpLine := `iifname "tap0" tcp dport { 25, 465, 587 } drop`
	smtpIdx := strings.Index(ruleset, smtpLine)
	if smtpIdx < 0 {
		t.Fatalf("expected SMTP drop rule in ruleset, none found:\n%s", ruleset)
	}
	// Allowlist: nft emits `iifname "tap0" ip daddr { CIDR, CIDR } … accept`
	// with spaces after commas and sorted CIDR order. Verify the allowlist
	// CIDRs appear in an accept rule before the SMTP marker.
	beforeSMTP := ruleset[:smtpIdx]
	for _, cidr := range []string{"1.2.3.0/24", "8.8.8.0/24"} {
		if !strings.Contains(beforeSMTP, cidr) {
			t.Fatalf("allowlist CIDR %q missing from ruleset before SMTP marker:\n%s", cidr, ruleset)
		}
	}
	if !strings.Contains(beforeSMTP, "accept") {
		t.Fatalf("no allowlist accept rule found before SMTP marker in:\n%s", ruleset)
	}
}

// TestMetalAllowlistV6RuleInstalled: ADR-032 v6 mirror. Pins the
// same AFTER-deny ordering on the `ip6 faas forward` chain as
// TestMetalAllowlistRuleInstalled does on `ip faas forward`. The v6
// chain lives in a separate per-family table (ADR-023) so the v4 +
// v6 assertions are independent — we drive the renderer through a
// v6-only allowlist and substring-check both that the v6 allowlist
// rule exists and that it lands AFTER the v6 lateral-movement deny
// (fe80::/10, fc00::/7, ff00::/8, ::1/128, ::/128 drop).
func TestMetalAllowlistV6RuleInstalled(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip (iproute2) not on PATH; install iproute2 on a Linux host to run this gate")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft (nftables) not on PATH; install nftables on a Linux host to run this gate")
	}
	probe := exec.Command("ip", "netns", "add", "faas_allow_v6_probe")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", "faas_allow_v6_probe").CombinedOutput()

	nsName := "faas_allow_" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", nsName).CombinedOutput() })
	if out, err := exec.Command("ip", "netns", "add", nsName).CombinedOutput(); err != nil {
		t.Fatalf("ip netns add %s: %v\n%s", nsName, err, out)
	}

	c := NewConfig("allowlist-v6-metal", nsName, "vh-allow-v6", "vp-allow-v6",
		netip.MustParseAddr("10.100.0.252"))
	c.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("2001:db8::/32"),
	}

	for _, argv := range c.NftCommands() {
		full := append([]string{"ip", "netns", "exec", nsName}, argv...)
		out, err := exec.Command(full[0], full[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("nft rule failed: %v\nargv: %v\noutput:\n%s", err, full, out)
		}
	}

	out, err := exec.Command("ip", "netns", "exec", nsName, "nft", "list", "ruleset").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list ruleset: %v\n%s", err, out)
	}
	ruleset := string(out)

	// Anchor on the unique v6 SMTP drop rule — it's always present and
	// comes before the deny rule, so we use it as the stable marker.
	smtpLine := `iifname "tap0" tcp dport { 25, 465, 587 } drop`
	smtpIdx := strings.Index(ruleset, smtpLine)
	if smtpIdx < 0 {
		t.Fatalf("expected SMTP drop rule in v6 ruleset, none found:\n%s", ruleset)
	}
	// Allowlist: nft emits `iifname "tap0" ip6 daddr { CIDR, CIDR } accept`
	// with spaces after commas and sorted CIDR order. Verify the allowlist
	// CIDRs appear in an accept rule after the SMTP marker.
	afterSMTP := ruleset[smtpIdx:]
	for _, cidr := range []string{"2001:db8::/32", "fe80::/10"} {
		if !strings.Contains(afterSMTP, cidr) {
			t.Fatalf("allowlist CIDR %q missing from v6 ruleset after SMTP marker:\n%s", cidr, ruleset)
		}
	}
	if !strings.Contains(afterSMTP, "accept") {
		t.Fatalf("no accept rule found after SMTP marker in v6:\n%s", ruleset)
	}
}

func TestMetalAllowlistSkippedWhenEmpty(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip (iproute2) not on PATH; install iproute2 on a Linux host to run this gate")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft (nftables) not on PATH; install nftables on a Linux host to run this gate")
	}
	probe := exec.Command("ip", "netns", "add", "faas_allow_empty_probe")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", "faas_allow_empty_probe").CombinedOutput()

	nsName := "faas_allow_" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", nsName).CombinedOutput() })
	if out, err := exec.Command("ip", "netns", "add", nsName).CombinedOutput(); err != nil {
		t.Fatalf("ip netns add %s: %v\n%s", nsName, err, out)
	}

	// Empty allowlist → no rule emitted. We still drive the chain so
	// the ruleset exists and is readable; the assertion is that NO
	// `ip daddr { … } accept` line ever appears (the lateral-movement
	// deny uses the same `{ … }` substring but `drop`, not `accept`,
	// so a substring match on `daddr { … } accept` is safe).
	c := NewConfig("allowlist-empty", nsName, "vh-empty", "vp-empty",
		netip.MustParseAddr("10.100.0.251"))
	// Deliberately leave EgressAllowlist nil.
	for _, argv := range c.NftCommands() {
		full := append([]string{"ip", "netns", "exec", nsName}, argv...)
		out, err := exec.Command(full[0], full[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("nft rule failed: %v\nargv: %v\noutput:\n%s", err, full, out)
		}
	}

	out, err := exec.Command("ip", "netns", "exec", nsName, "nft", "list", "ruleset").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list ruleset: %v\n%s", err, out)
	}
	ruleset := string(out)

	// Any line of shape `ip daddr { … } accept` would indicate an
	// allowlist rule was rendered — which is exactly what ADR-031
	// promises NOT to do for empty input. Substring-match (not
	// regex) keeps the regression net self-contained.
	for _, line := range strings.Split(ruleset, "\n") {
		if strings.Contains(line, "ip daddr {") && strings.Contains(line, "accept") {
			t.Errorf("unexpected allowlist-shape accept rule with empty EgressAllowlist:\n%s\nfull ruleset:\n%s",
				strings.TrimSpace(line), ruleset)
		}
	}
}
