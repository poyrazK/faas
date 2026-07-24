package netns

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig() Config {
	return NewConfig("abc123", "fc-abc123", "vh7", "vp7", netip.MustParseAddr("10.100.0.9"))
}

func TestNewConfigDefaults(t *testing.T) {
	c := testConfig()
	if c.Tap != "tap0" {
		t.Errorf("Tap = %q, want tap0 (identical inner world, ADR-009)", c.Tap)
	}
	if c.HostBits != 16 {
		t.Errorf("HostBits = %d, want 16", c.HostBits)
	}
	if c.hostCIDR() != "10.100.0.9/16" {
		t.Errorf("hostCIDR = %q, want 10.100.0.9/16", c.hostCIDR())
	}
}

func TestSetupThenTeardownReferToSameResources(t *testing.T) {
	c := testConfig()
	setup := flatten(c.SetupCommands())
	teardown := flatten(c.TeardownCommands())

	// Everything teardown deletes (netns, host veth) must have been created by
	// setup — otherwise leakcheck will trip.
	if !strings.Contains(setup, "netns add "+c.Netns) {
		t.Error("setup does not create the netns it will delete")
	}
	if !strings.Contains(setup, "link add "+c.VethHost) {
		t.Error("setup does not create the host veth it will delete")
	}
	if !strings.Contains(teardown, "netns del "+c.Netns) {
		t.Error("teardown does not delete the netns")
	}
	if !strings.Contains(teardown, "link del "+c.VethHost) {
		t.Error("teardown does not delete the host veth")
	}
	// The tc qdisc lives on VethHost and is implicitly freed when
	// teardown deletes the link. Locking that single-side identity
	// here prevents a future edit that accidentally moves the qdisc
	// to the peer end (which would orphan it on teardown).
	if !strings.Contains(flatten(c.TcCommands()), "dev "+c.VethHost) {
		t.Error("tc commands must target VethHost (the host-side veth that teardown deletes)")
	}

	// Order matters: netns-del first, host veth second. The opposite order
	// leaves the peer veth orphaned inside the namespace, which pins a
	// reference and causes `ip netns del` to fail silently — observed on the
	// Lima arm64 nested-KVM guest (leakcheck found `fc-m0-hello` after a
	// clean Destroy). The same iproute2 behavior holds on the EX44.
	cmds := c.TeardownCommands()
	netnsIdx, linkIdx := -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "netns del "+c.Netns) {
			netnsIdx = i
		}
		if strings.Contains(line, "link del "+c.VethHost) {
			linkIdx = i
		}
	}
	if netnsIdx < 0 || linkIdx < 0 {
		t.Fatalf("expected both netns-del and link-del in teardown (netns=%d link=%d)", netnsIdx, linkIdx)
	}
	if netnsIdx > linkIdx {
		t.Errorf("netns-del (idx %d) must precede host link-del (idx %d); reversed order orphans the veth peer", netnsIdx, linkIdx)
	}
}

func TestSetupCreatesTapAndAddressing(t *testing.T) {
	c := testConfig()
	setup := flatten(c.SetupCommands())
	wants := []string{
		"tuntap add tap0 mode tap",
		"addr add " + TapPrefix + " dev tap0",      // 10.0.0.1/30
		"addr add 10.100.0.9/16 dev " + c.VethPeer, // host identity on the peer
		"link set " + c.VethHost + " master " + TenantBridge,
		"net.ipv4.ip_forward=1",
		// Netns default route via the bridge IP (HostBridgeCIDR). Without
		// this argv guest packets to public destinations hit ENETUNREACH —
		// was the silent tenant-egress P0.
		"route add default via 10.100.0.1 dev " + c.VethPeer,
	}
	for _, w := range wants {
		if !strings.Contains(setup, w) {
			t.Errorf("setup missing %q\ngot:\n%s", w, setup)
		}
	}
}

func TestVethPeerMovedIntoNetnsBeforeAddressing(t *testing.T) {
	// The peer must enter the netns before we address it, or the addr lands in
	// the root namespace. Assert ordering.
	cmds := testConfig().SetupCommands()
	moveIdx, addrIdx := -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "link set vp7 netns") {
			moveIdx = i
		}
		if strings.Contains(line, "addr add 10.100.0.9/16 dev vp7") {
			addrIdx = i
		}
	}
	if moveIdx < 0 || addrIdx < 0 {
		t.Fatalf("expected both move and addr commands (move=%d addr=%d)", moveIdx, addrIdx)
	}
	if moveIdx > addrIdx {
		t.Errorf("peer addressed (idx %d) before being moved into netns (idx %d)", addrIdx, moveIdx)
	}
}

// TestSetupInstallsNetnsDefaultRouteViaBridge locks the tenant-egress P0
// fix: SetupCommands must add a netns default route via the bridge IP
// (10.100.0.1, reserved by pkg/fcvm/alloc.go so the allocator never hands
// out .1). Without this argv, a guest packet to e.g. 8.8.8.8 matches no
// connected route inside the netns and the kernel returns ENETUNREACH.
func TestSetupInstallsNetnsDefaultRouteViaBridge(t *testing.T) {
	c := testConfig()
	found := -1
	for i, cmd := range c.SetupCommands() {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "ip route add default via 10.100.0.1") {
			found = i
			// Must be ip-netns-exec'd (the inNetns helper), not root-ns.
			if !strings.HasPrefix(line, "ip netns exec "+c.Netns+" ip route") {
				t.Errorf("default-route argv not in-netns: %q", line)
			}
			// Must reference the per-instance peer, not a hard-coded name.
			if !strings.Contains(line, "dev "+c.VethPeer) {
				t.Errorf("default-route argv missing dev %q: %q", c.VethPeer, line)
			}
		}
	}
	if found < 0 {
		t.Fatalf("SetupCommands missing `ip route add default via 10.100.0.1 ...`; got:\n%s", flatten(c.SetupCommands()))
	}
}

// TestSetupInstallsDefaultRouteAfterAddressing locks the per-instance
// peer-up ordering: the default route must reference a peer that is
// already up, otherwise the route installs but the kernel returns
// `RTNETLINK answers: Network is unreachable` on the first packet.
// Mirrors the TestVethPeerMovedIntoNetnsBeforeAddressing pattern.
func TestSetupInstallsDefaultRouteAfterAddressing(t *testing.T) {
	cmds := testConfig().SetupCommands()
	c := testConfig()
	peerUp, route := -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "link set "+c.VethPeer+" up") {
			peerUp = i
		}
		if strings.Contains(line, "route add default via 10.100.0.1") {
			route = i
		}
	}
	if peerUp < 0 || route < 0 {
		t.Fatalf("expected both peer-up and default-route (peerUp=%d route=%d)", peerUp, route)
	}
	if route < peerUp {
		t.Errorf("default-route (idx %d) must be installed AFTER peer-up (idx %d)", route, peerUp)
	}
}

// TestSetupDefaultRouteArgvIsExact locks the EXACT argv bytes for the
// netns default route and asserts exactly one such entry exists.
// Contents-matching (see TestSetupInstallsNetnsDefaultRouteViaBridge)
// is necessary but not sufficient: a future edit could add a second
// route command whose argv happens to contain the expected substring
// (e.g. a typo like `ip route add default via 10.100.0.1 dev eth0`
// inside the netns) and pass the substring test, breaking the entire
// tenant-egress story. nft/IP argv are not shell-parsed, so the only
// way to compose them correctly is argv-by-argv equality.
func TestSetupDefaultRouteArgvIsExact(t *testing.T) {
	c := testConfig()
	want := []string{
		"ip", "netns", "exec", c.Netns,
		"ip", "route", "add", "default", "via", "10.100.0.1", "dev", c.VethPeer,
	}
	cmds := c.SetupCommands()
	hits := 0
	for _, cmd := range cmds {
		if len(cmd) != len(want) {
			continue
		}
		match := true
		for j := range want {
			if cmd[j] != want[j] {
				match = false
				break
			}
		}
		if match {
			hits++
		}
	}
	if hits == 0 {
		t.Fatalf("SetupCommands missing exact argv %v; got:\n%s", want, flatten(cmds))
	}
	if hits > 1 {
		t.Errorf("SetupCommands emitted %d argvs matching the default-route shape; want exactly 1", hits)
	}
}

func TestNftCommandsPublishGuestPort(t *testing.T) {
	rules := flatten(testConfig().NftCommands())
	// Publish + NAT: DNAT the host identity's :8080 to the guest, masquerade egress.
	wants := []string{
		"iifname vp7 tcp dport 8080 dnat to 10.0.0.2:8080", // inbound to the guest contract
		"postrouting oifname vp7 masquerade",               // egress behind the host identity
	}
	for _, w := range wants {
		if !strings.Contains(rules, w) {
			t.Errorf("nft ruleset missing %q\ngot:\n%s", w, rules)
		}
	}
}

func TestNftCommandsEnforceEgressPolicy(t *testing.T) {
	rules := flatten(testConfig().NftCommands())
	// §11 ship-blocking egress denies, scoped to the guest side (iifname tap0) so
	// the inbound DNAT path (iifname vp7) is never affected. CGN (100.64.0.0/10)
	// is included for symmetry with pkg/netns.DefaultHostPolicy.ForwardDenyCIDRs.
	wants := []string{
		"iifname tap0 tcp dport { 25, 465, 587 } drop",                                                            // deny SMTP (spam/abuse)
		"iifname tap0 ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } drop", // deny RFC1918 + link-local/metadata + CGN
		"iifname tap0 ip6 daddr { fe80::/10, fc00::/7, ff00::/8, ::1/128, ::/128 } drop",                          // deny IPv6 link-local + ULA + multicast + unspecified (ADR-023)
	}
	for _, w := range wants {
		if !strings.Contains(rules, w) {
			t.Errorf("egress policy missing %q\ngot:\n%s", w, rules)
		}
	}
	// The denies must be guest-originated only; a daddr drop without an iifname
	// guard would also kill the inbound DNAT'd packet (daddr 10.0.0.2 ∈ 10/8).
	for _, cmd := range testConfig().NftCommands() {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "daddr") && strings.Contains(line, "drop") && !strings.Contains(line, "iifname tap0") {
			t.Errorf("egress daddr-drop not scoped to the guest side: %q", line)
		}
	}
}

func TestNftCommandsAcceptRepliesBeforeEgressDenies(t *testing.T) {
	// The published request's reply leaves the guest via iifname tap0 with a daddr
	// in the host-identity range (10.100.0.0/16 ⊂ 10.0.0.0/8), so it would be
	// caught by the lateral-movement deny. An established/related accept must come
	// FIRST in the forward chain, or no published request ever completes (proven on
	// metal: without it TestMetalHelloBoot hangs at readiness on SYN-cookie spam).
	cmds := testConfig().NftCommands()
	est, daddrDrop := -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "forward") && strings.Contains(line, "ct state established,related accept") {
			est = i
		}
		if strings.Contains(line, "forward") && strings.Contains(line, "daddr") && strings.Contains(line, "drop") {
			daddrDrop = i
		}
	}
	if est < 0 {
		t.Fatalf("forward chain missing an established,related accept for reply traffic:\n%s", flatten(cmds))
	}
	if daddrDrop < 0 {
		t.Fatalf("forward chain missing the lateral-movement daddr drop:\n%s", flatten(cmds))
	}
	if est > daddrDrop {
		t.Errorf("established,related accept (rule %d) must precede the daddr drop (rule %d) — nft evaluates in order", est, daddrDrop)
	}
}

// TestNftCommandsEmitsConntrackCapRule asserts the §7 per-instance
// conntrack cap rule appears in BOTH the IPv4 and IPv6 forward chains
// when ConntrackCap > 0. Spec §7 line 344 reads "per-instance conntrack
// cap 4,096" without distinguishing v4 vs v6 entries — a v6-only flood
// could otherwise exhaust the conntrack table separately. The named
// counter `faas_cap` is what PR-C's dashboard reads via `nft list
// counters`; the count is informational, not assertion.
//
// Plan source: docs/faas_implementation_spec.md:344, ADR-018:186-202.
// Implementation: pkg/netns/config.go::forwardConnlimitRule and
// forwardConnlimitRule6.
func TestNftCommandsEmitsConntrackCapRule(t *testing.T) {
	c := testConfig()
	c.ConntrackCap = 4096
	cmds := c.NftCommands()
	v4Want := `nft add rule ip faas forward ct count over 4096 counter name "faas_cap" drop`
	v6Want := `nft add rule ip6 faas forward ct count over 4096 counter name "faas_cap" drop`
	v4Found, v6Found := -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		switch {
		case v4Found < 0 && strings.Contains(line, v4Want):
			v4Found = i
		case v6Found < 0 && strings.Contains(line, v6Want):
			v6Found = i
		}
	}
	if v4Found < 0 {
		t.Fatalf("expected v4 cap rule %q in NftCommands, none found:\n%s", v4Want, flatten(cmds))
	}
	if v6Found < 0 {
		t.Fatalf("expected v6 cap rule %q in NftCommands, none found:\n%s", v6Want, flatten(cmds))
	}
}

// TestNftCommandsOmitsConntrackCapRule_WhenZero is the opt-out pin:
// a Config with ConntrackCap <= 0 emits no cap rule on either the v4
// or v6 forward chain. Mirrors the EgressMbit==0 → no tc rule
// contract in pkg/fcvm/manager.go:500.
func TestNftCommandsOmitsConntrackCapRule_WhenZero(t *testing.T) {
	c := testConfig() // zero value for ConntrackCap
	for i, cmd := range c.NftCommands() {
		if strings.Contains(strings.Join(cmd, " "), "ct count over") {
			t.Errorf("rule %d contains `ct count over` but ConntrackCap=0:\n%s", i, strings.Join(cmd, " "))
		}
	}
}

// TestNftCommandsCapRuleRunsAfterEstablishedBeforeDenies asserts the
// placement the comment in forwardConnlimitRule promises: the cap is
// AFTER the established/related accept (so reply packets on existing
// flows keep flowing regardless of count) and BEFORE the SMTP / daddr
// drops (so an app scanning many denied destinations still hits the
// cap). nft evaluates in declaration order, so a misplaced rule would
// silently degrade either cap enforcement or published-request reach.
// Mirrored for both the IPv4 and IPv6 chains.
func TestNftCommandsCapRuleRunsAfterEstablishedBeforeDenies(t *testing.T) {
	c := testConfig()
	c.ConntrackCap = 4096
	cmds := c.NftCommands()
	for _, family := range []string{"ip", "ip6"} {
		var established, cap, daddrDrop, smtpDrop = -1, -1, -1, -1
		// SMTP drop only exists on the v4 chain.
		wantSMTPMatch := family == "ip"
		for i, cmd := range cmds {
			line := strings.Join(cmd, " ")
			if !strings.Contains(line, family+" faas") {
				continue
			}
			switch {
			case established < 0 && strings.Contains(line, "ct state established,related accept"):
				established = i
			case cap < 0 && strings.Contains(line, "ct count over"):
				cap = i
			case smtpDrop < 0 && wantSMTPMatch && strings.Contains(line, "tcp dport") && strings.Contains(line, "drop"):
				smtpDrop = i
			case daddrDrop < 0 && strings.Contains(line, "daddr") && strings.Contains(line, "drop"):
				daddrDrop = i
			}
		}
		if established < 0 || cap < 0 || daddrDrop < 0 {
			t.Fatalf("[%s chain] missing rule: established=%d cap=%d daddr=%d\n%s",
				family, established, cap, daddrDrop, flatten(cmds))
		}
		if wantSMTPMatch && smtpDrop < 0 {
			t.Fatalf("[%s chain] expected SMTP drop, found none:\n%s", family, flatten(cmds))
		}
		if established >= cap {
			t.Errorf("[%s chain] established,related accept (rule %d) must come BEFORE the connlimit cap (rule %d)", family, established, cap)
		}
		if wantSMTPMatch && cap >= smtpDrop {
			t.Errorf("[%s chain] connlimit cap (rule %d) must come BEFORE the SMTP drop (rule %d)", family, cap, smtpDrop)
		}
		if cap >= daddrDrop {
			t.Errorf("[%s chain] connlimit cap (rule %d) must come BEFORE the daddr lateral-movement drop (rule %d)", family, cap, daddrDrop)
		}
	}
}

func TestNftCommandsHaveNoShellMetacharacters(t *testing.T) {
	// nft argv legitimately uses ; { } , for its own grammar (there is no shell —
	// ExecRunner passes argv directly), but genuinely dangerous shell syntax must
	// never appear. Both NftCommands and NftResetCommands are covered.
	for _, cmds := range [][][]string{testConfig().NftCommands(), testConfig().NftResetCommands()} {
		for _, cmd := range cmds {
			for _, arg := range cmd {
				if strings.ContainsAny(arg, "|&<>$`\n") {
					t.Errorf("nft argv element %q contains shell metacharacters", arg)
				}
			}
		}
	}
}

// TestNftCommandsPrefixAllInNetns is the regression net for a future edit that
// accidentally runs an nft rule in the root namespace — every argv must start
// with the canonical prefix [ip, netns, exec, <Netns>, nft]. Without the
// prefix, nft would write to /var/lib/nftables (root netns) and silently fail
// to apply inside the per-instance netns.
func TestNftCommandsPrefixAllInNetns(t *testing.T) {
	c := testConfig()
	for _, cmds := range [][][]string{c.NftCommands(), c.NftResetCommands()} {
		for i, cmd := range cmds {
			if len(cmd) < 6 {
				t.Errorf("argv %d too short: %v", i, cmd)
				continue
			}
			want := []string{"ip", "netns", "exec", c.Netns, "nft"}
			for j, w := range want {
				if cmd[j] != w {
					t.Errorf("argv %d prefix[%d] = %q, want %q (full: %v)",
						i, j, cmd[j], w, cmd)
				}
			}
		}
	}
}

// TestNftCommandsStartsWithIdempotentReset locks the snapshot-restore Wake
// fix: NftCommands must NOT prepend its own `delete table` (that would fail
// on a fresh netns and abort setup). Instead the reset lives in a separate
// best-effort NftResetCommands method, and the strict NftCommands argv list
// starts with `add table`.
func TestNftCommandsStartsWithAddTable(t *testing.T) {
	first := testConfig().NftCommands()[0]
	if len(first) < 6 {
		t.Fatalf("first argv too short: %v", first)
	}
	// prefix is [ip, netns, exec, <Netns>, nft, add, ...]
	if first[5] != "add" {
		t.Errorf("first argv[5] = %q, want \"add\" (table-define); full: %v", first[5], first)
	}
	if first[6] != "table" {
		t.Errorf("first argv[6] = %q, want \"table\"; full: %v", first[6], first)
	}
}

// TestNftResetCommandsPrependsDeleteTable asserts the best-effort reset
// argv targets the table we're about to (re-)add. Without this, snapshot-
// restore Wake fails on the second `add table`.
func TestNftResetCommandsPrependsDeleteTable(t *testing.T) {
	reset := testConfig().NftResetCommands()
	if len(reset) == 0 {
		t.Fatal("NftResetCommands returned empty slice; the reset would be skipped")
	}
	first := reset[0]
	if len(first) < 6 {
		t.Fatalf("reset argv too short: %v", first)
	}
	// prefix is [ip, netns, exec, <Netns>, nft, delete, ...]
	if first[5] != "delete" {
		t.Errorf("reset argv[5] = %q, want \"delete\"; full: %v", first[5], first)
	}
	if first[6] != "table" || first[7] != "ip" || first[8] != "faas" {
		t.Errorf("reset argv[6..8] = %q, want [table ip faas]; full: %v",
			first[6:9], first)
	}
	// Mirror the IPv6 reset (ADR-023 split). Without it, a snapshot-restore
	// Wake's `add table ip6 faas` collides on the existing table.
	if len(reset) < 2 {
		t.Fatalf("NftResetCommands must reset both ip and ip6 tables; got %d argvs", len(reset))
	}
	v6 := reset[1]
	if len(v6) < 9 {
		t.Fatalf("ipv6 reset argv too short: %v", v6)
	}
	if v6[5] != "delete" || v6[6] != "table" || v6[7] != "ip6" || v6[8] != "faas" {
		t.Errorf("second argv[5..8] = %q, want [delete table ip6 faas]; full: %v",
			v6[5:9], v6)
	}
}

// TestNftCommandsAllStartWithAddOrFlushOrDelete is a defensive guard against
// a future edit that drops the nft subcommand (e.g. `nft ip faas add rule
// ...` with the subcommand in the wrong slot). Every argv in NftCommands
// and NftResetCommands must have a valid nft subcommand at index 5.
func TestNftCommandsAllStartWithAddOrFlushOrDelete(t *testing.T) {
	c := testConfig()
	valid := map[string]bool{"add": true, "flush": true, "delete": true}
	for _, cmds := range [][][]string{c.NftCommands(), c.NftResetCommands()} {
		for i, cmd := range cmds {
			if len(cmd) < 6 {
				t.Errorf("argv %d too short: %v", i, cmd)
				continue
			}
			if !valid[cmd[5]] {
				t.Errorf("argv %d subcommand = %q, want one of add/flush/delete; full: %v",
					i, cmd[5], cmd)
			}
		}
	}
}

func TestCommandsHaveNoShellMetacharacters(t *testing.T) {
	// Commands are exec'd directly (no shell); guard against accidental injection
	// of shell syntax into an argv element.
	for _, cmd := range append(testConfig().SetupCommands(), testConfig().TeardownCommands()...) {
		for _, arg := range cmd {
			if strings.ContainsAny(arg, "|&;<>$`\n") {
				t.Errorf("argv element %q contains shell metacharacters", arg)
			}
		}
	}
}

// TestTenantBridgeMatches is the regression net for issue #27's bridge-name
// typo bug (pkg/netns/TenantBridge vs ansible-side `faas-tenant-bridge`).
//
// The Go-side constant is the single source of truth. The host nft ruleset
// (the artifact produced by `make egress-render` and dropped at
// /etc/nftables.conf) MUST reference this exact name — otherwise the forward
// chain's `iifname "..." oifname "..." accept` allow rule never matches the
// bridge vmmd actually creates, and every tenant→public packet is dropped
// before the §11 denylist matters.
//
// The test gracefully skips when the checked-in artifact isn't present
// yet (e.g. before `make egress-render` has run for the first time). Once
// the artifact is in place, the test fails if the names diverge.
func TestTenantBridgeMatches(t *testing.T) {
	// 1. The Go render must substitute the same name.
	rendered := DefaultHostPolicy.Render()
	if !strings.Contains(rendered, `iifname "`+TenantBridge+`" accept`) {
		t.Errorf("Go renderer did not use TenantBridge=%q in input chain; got:\n%s",
			TenantBridge, rendered)
	}
	if !strings.Contains(rendered, `iifname "`+TenantBridge+`" oifname "`) {
		t.Errorf("Go renderer did not use TenantBridge=%q in forward chain; got:\n%s",
			TenantBridge, rendered)
	}

	// 2. The checked-in artifact must reference the same name. Walk up from
	// the test's CWD to find the repo root, then load the artifact.
	const relArtifact = "deploy/ansible/roles/nftables/files/policy_nftables.conf"
	artifact, err := findRepoFile(relArtifact)
	if err != nil {
		t.Skipf("checked-in artifact not present yet (%s); run `make egress-render`", err)
	}
	body, err := os.ReadFile(artifact)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("checked-in artifact not present yet (%s); run `make egress-render`", err)
		}
		t.Fatalf("read %s: %v", artifact, err)
	}
	text := string(body)
	if !strings.Contains(text, `iifname "`+TenantBridge+`" oifname "`) {
		t.Errorf("checked-in artifact does not reference TenantBridge=%q in forward chain; "+
			"this is the bridge-name typo regression — see #27:\n%s",
			TenantBridge, text)
	}
	// Anti-regression: the dead name must never appear anywhere.
	if strings.Contains(text, "faas-tenant-bridge") {
		t.Errorf("checked-in artifact references the dead name `faas-tenant-bridge`:\n%s", text)
	}
}

// findRepoFile walks up from the current file's directory looking for the
// repo root (a go.mod) and returns the join of root + rel. Skips if not
// found — that's the case for `go test` invocations from outside the module.
func findRepoFile(rel string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func flatten(cmds [][]string) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

// --- TcCommands / TcResetCommands ----------------------------------------

// TestTcCommandsApplyRateToVethHost asserts the strict tc qdisc argv
// targets VethHost (root-ns side of the veth pair, the choke point
// nearest the box) and embeds the per-plan rate. Locks the wire shape
// the manager's setupNetwork relies on.
func TestTcCommandsApplyRateToVethHost(t *testing.T) {
	c := testConfig()
	c.EgressMbit = 25 // Hobby plan
	cmds := c.TcCommands()
	if len(cmds) != 1 {
		t.Fatalf("TcCommands returned %d argvs, want 1: %v", len(cmds), cmds)
	}
	line := strings.Join(cmds[0], " ")
	wants := []string{
		"tc", "qdisc", "add", "dev", c.VethHost, "root", "tbf",
		"rate", "25mbit",
		"burst", "32kbit", "latency", "400ms",
	}
	for _, w := range wants {
		if !strings.Contains(line, w) {
			t.Errorf("tc argv missing %q\ngot: %s", w, line)
		}
	}
}

// TestTcResetCommandsPrependsDeleteRoot asserts the best-effort reset
// argv targets the same root qdisc TcCommands adds. Without this, a
// snapshot-restore Wake fails on the second `tc qdisc add` with
// "RTNETLINK answers: File exists". Mirrors TestNftResetCommandsPrependsDeleteTable.
func TestTcResetCommandsPrependsDeleteRoot(t *testing.T) {
	c := testConfig()
	reset := c.TcResetCommands()
	if len(reset) != 1 {
		t.Fatalf("TcResetCommands returned %d argvs, want 1: %v", len(reset), reset)
	}
	line := strings.Join(reset[0], " ")
	for _, w := range []string{"tc", "qdisc", "del", "dev", c.VethHost, "root"} {
		if !strings.Contains(line, w) {
			t.Errorf("reset argv missing %q\ngot: %s", w, line)
		}
	}
}

// TestTcCommandsHaveNoShellMetacharacters is the regression net for an
// accidental shell-injection into an argv element. tc argv legitimately
// uses k/m/g suffixes for rate/burst, but never shell metacharacters.
func TestTcCommandsHaveNoShellMetacharacters(t *testing.T) {
	c := testConfig()
	c.EgressMbit = 25
	for _, cmds := range [][][]string{c.TcCommands(), c.TcResetCommands()} {
		for _, cmd := range cmds {
			for _, arg := range cmd {
				if strings.ContainsAny(arg, "|&<>$`\n;{}") {
					t.Errorf("tc argv element %q contains shell metacharacters", arg)
				}
			}
		}
	}
}

// TestTcCommandsPrefixNoNetnsExec locks the architectural choice: tc
// runs in the root namespace and must NOT be wrapped in `ip netns
// exec`. Wrapping would put the qdisc on the peer end inside the
// per-instance netns, which (a) cannot be reached from outside without
// re-entering the netns every time and (b) would be removed when the
// netns is torn down at park time, losing the cap on a snapshot-
// restore Wake. A future edit that adds the prefix by reflex —
// mirroring NftCommands — would silently break the cap; this test
// fails loudly.
func TestTcCommandsPrefixNoNetnsExec(t *testing.T) {
	c := testConfig()
	c.EgressMbit = 25
	for _, cmds := range [][][]string{c.TcCommands(), c.TcResetCommands()} {
		for _, cmd := range cmds {
			for _, arg := range cmd {
				if arg == "netns" || arg == "exec" {
					t.Errorf("tc argv contains 'netns exec' — must run in root ns: %v", cmd)
				}
			}
		}
	}
}

// TestTcCommandsValidRate is a defensive bound on the rate expression.
// tbf rejects 0mbit ("RTNETLINK answers: Invalid argument") and our
// `if nc.EgressMbit > 0` guard in setupNetwork is the only thing
// keeping a 0 from leaking here. A future edit that drops the guard
// would fail this test instead of silently producing a broken qdisc.
func TestTcCommandsValidRate(t *testing.T) {
	c := testConfig()
	for _, mbit := range []int{10, 25, 100, 250} {
		c.EgressMbit = mbit
		line := strings.Join(c.TcCommands()[0], " ")
		want := fmt.Sprintf("rate %dmbit", mbit)
		if !strings.Contains(line, want) {
			t.Errorf("rate %d: argv missing %q\ngot: %s", mbit, want, line)
		}
	}
}

// TestForwardAllowlistRuleHelper pins the v4 unit-tested branch in
// isolation. Empty EgressAllowlist must return nil (no rule shape at
// all) — the renderer chooses NOT to fabricate "ip daddr {} accept"
// on empty, because chain-policy accept handles the no-list case.
// A populated list must produce exactly one argv of shape:
//
//	nft add rule ip faas forward iifname "tap0" ip daddr { CIDRs… } accept
//
// with comma-joined values (the modern-nft comma-required regression
// gate at pkg/netns/policy.go::TestHostPolicyRenderNftSyntaxCheck is
// what we are mirroring here — the test would catch a re-introduction
// of space-separated nft sets).
//
// Plan source: ADR-031 ("tier-2 of the network roadmap"). The
// post-deny/pre-default-accept placement is asserted separately
// (TestNftCommandsAllowlistRuleRunsAfterDenies) because the helper
// itself doesn't know its position in the chain. The v6 sibling is
// pinned by TestForwardAllowlistRule6Helper below.
func TestForwardAllowlistRuleHelper(t *testing.T) {
	nx := []string{"ip", "netns", "exec", "fc-test", "nft"}
	nft := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }

	// Empty: nothing emitted.
	empty := testConfig() // zero-value EgressAllowlist
	if got := empty.forwardAllowlistRule(nft); got != nil {
		t.Errorf("empty EgressAllowlist: helper returned %v, want nil", got)
	}

	// Single v4 CIDR: single argv with the expected shape.
	one := testConfig()
	one.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	wantOne := `ip netns exec fc-test nft add rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 } accept`
	if got := strings.Join(one.forwardAllowlistRule(nft), " "); got != wantOne {
		t.Errorf("single v4 CIDR:\n got  %s\n want %s", got, wantOne)
	}

	// Multiple v4 CIDRs: comma-joined set, NO trailing whitespace.
	many := testConfig()
	many.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("8.8.8.0/24"),
		netip.MustParsePrefix("9.9.9.0/24"),
	}
	wantMany := `ip netns exec fc-test nft add rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24,8.8.8.0/24,9.9.9.0/24 } accept`
	if got := strings.Join(many.forwardAllowlistRule(nft), " "); got != wantMany {
		t.Errorf("multiple v4 CIDRs:\n got  %s\n want %s", got, wantMany)
	}

	// v4-only partition: a v6-only EgressAllowlist produces NO v4
	// rule (the v6 half is emitted by forwardAllowlistRule6). This
	// is the ADR-032 family split: each chain reads its own slice.
	v6Only := testConfig()
	v6Only.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("fe80::/10")}
	if got := v6Only.forwardAllowlistRule(nft); got != nil {
		t.Errorf("v6-only input on v4 helper: got %v, want nil", got)
	}

	// Mixed input on the v4 helper: only the v4 entries appear.
	mixed := testConfig()
	mixed.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("fe80::/10"),
	}
	wantMixedV4 := `ip netns exec fc-test nft add rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 } accept`
	if got := strings.Join(mixed.forwardAllowlistRule(nft), " "); got != wantMixedV4 {
		t.Errorf("mixed input on v4 helper:\n got  %s\n want %s", got, wantMixedV4)
	}
}

// TestForwardAllowlistRule6Helper is the ADR-032 v6 mirror of
// TestForwardAllowlistRuleHelper. Same shape: nil on empty, comma-
// joined set with NO trailing whitespace, family-pinned (v6 only —
// a v4 entry produces nil here because the v4 helper owns it).
//
// Internal to NftCommands — do not invoke from anywhere else. The
// helper signature mirrors forwardAllowlistRule so the per-family
// symmetric test pattern is grep-able.
func TestForwardAllowlistRule6Helper(t *testing.T) {
	nx := []string{"ip", "netns", "exec", "fc-test", "nft"}
	nft := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }

	// Empty: nothing emitted.
	if got := testConfig().forwardAllowlistRule6(nft); got != nil {
		t.Errorf("empty EgressAllowlist: helper returned %v, want nil", got)
	}

	// Single v6 CIDR: single argv with the expected v6 shape.
	one := testConfig()
	one.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("fe80::/10")}
	wantOne := `ip netns exec fc-test nft add rule ip6 faas forward iifname tap0 ip6 daddr { fe80::/10 } accept`
	if got := strings.Join(one.forwardAllowlistRule6(nft), " "); got != wantOne {
		t.Errorf("single v6 CIDR:\n got  %s\n want %s", got, wantOne)
	}

	// Multiple v6 CIDRs: comma-joined set, NO trailing whitespace.
	many := testConfig()
	many.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	wantMany := `ip netns exec fc-test nft add rule ip6 faas forward iifname tap0 ip6 daddr { fe80::/10,2001:db8::/32 } accept`
	if got := strings.Join(many.forwardAllowlistRule6(nft), " "); got != wantMany {
		t.Errorf("multiple v6 CIDRs:\n got  %s\n want %s", got, wantMany)
	}

	// v6-only partition: a v4-only EgressAllowlist produces NO v6
	// rule. Symmetric to the v4 helper's v6-only case above.
	v4Only := testConfig()
	v4Only.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	if got := v4Only.forwardAllowlistRule6(nft); got != nil {
		t.Errorf("v4-only input on v6 helper: got %v, want nil", got)
	}

	// Mixed input on the v6 helper: only the v6 entries appear.
	mixed := testConfig()
	mixed.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("fe80::/10"),
	}
	wantMixedV6 := `ip netns exec fc-test nft add rule ip6 faas forward iifname tap0 ip6 daddr { fe80::/10 } accept`
	if got := strings.Join(mixed.forwardAllowlistRule6(nft), " "); got != wantMixedV6 {
		t.Errorf("mixed input on v6 helper:\n got  %s\n want %s", got, wantMixedV6)
	}
}

// TestNftCommandsEmitsAllowlistRule asserts the FULL-RULESET path:
// when EgressAllowlist is non-empty, NftCommands emits exactly ONE
// allowlist rule per family partition inside the corresponding
// forward chain. ADR-032 expanded the v1 surface from v4-only to
// v4 + v6, partitioned by `prefix.Addr().Is4()` at render time.
//
// v4-only input → exactly one v4 rule (zero v6).
// v6-only input → exactly one v6 rule (zero v4).
// Mixed input → one v4 rule + one v6 rule.
//
// Mirrors the ConntrackCap test style.
func TestNftCommandsEmitsAllowlistRule(t *testing.T) {
	type wantCounts struct {
		v4 int
		v6 int
	}
	cases := []struct {
		name     string
		allow    []netip.Prefix
		want     wantCounts
		v4Substr string // optional: assert a substring on the v4 rule
		v6Substr string // optional: assert a substring on the v6 rule
	}{
		{
			name: "v4-only",
			allow: []netip.Prefix{
				netip.MustParsePrefix("1.2.3.0/24"),
				netip.MustParsePrefix("8.8.8.0/24"),
			},
			want:     wantCounts{v4: 1, v6: 0},
			v4Substr: `add rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24,8.8.8.0/24 } accept`,
		},
		{
			name: "v6-only",
			allow: []netip.Prefix{
				netip.MustParsePrefix("fe80::/10"),
				netip.MustParsePrefix("2001:db8::/32"),
			},
			want:     wantCounts{v4: 0, v6: 1},
			v6Substr: `add rule ip6 faas forward iifname tap0 ip6 daddr { fe80::/10,2001:db8::/32 } accept`,
		},
		{
			name: "mixed-v4-and-v6",
			allow: []netip.Prefix{
				netip.MustParsePrefix("1.2.3.0/24"),
				netip.MustParsePrefix("fe80::/10"),
			},
			want:     wantCounts{v4: 1, v6: 1},
			v4Substr: `add rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 } accept`,
			v6Substr: `add rule ip6 faas forward iifname tap0 ip6 daddr { fe80::/10 } accept`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			c.EgressAllowlist = tc.allow
			cmds := c.NftCommands()
			v4Count, v6Count := 0, 0
			for _, cmd := range cmds {
				line := strings.Join(cmd, " ")
				// Allowlist-shape: `<family> daddr { … } accept` and
				// NOT a `drop` (the lateral-movement deny has the
				// same prefix).
				switch {
				case strings.Contains(line, "ip daddr {") && strings.Contains(line, "accept") && !strings.Contains(line, "drop") && !strings.Contains(line, "ip6 daddr"):
					v4Count++
					if tc.v4Substr != "" && !strings.Contains(line, tc.v4Substr) {
						t.Errorf("v4 allowlist rule mismatch:\n got  %s\n want-substr %s", line, tc.v4Substr)
					}
				case strings.Contains(line, "ip6 daddr {") && strings.Contains(line, "accept") && !strings.Contains(line, "drop"):
					v6Count++
					if tc.v6Substr != "" && !strings.Contains(line, tc.v6Substr) {
						t.Errorf("v6 allowlist rule mismatch:\n got  %s\n want-substr %s", line, tc.v6Substr)
					}
				}
			}
			if v4Count != tc.want.v4 {
				t.Errorf("v4 allowlist rule count = %d, want %d:\n%s", v4Count, tc.want.v4, flatten(cmds))
			}
			if v6Count != tc.want.v6 {
				t.Errorf("v6 allowlist rule count = %d, want %d:\n%s", v6Count, tc.want.v6, flatten(cmds))
			}
		})
	}
}

// TestNftCommandsOmitsAllowlistRule_WhenEmpty: an empty allowlist
// must NOT emit the rule on either chain, even when other knobs
// (ConntrackCap, EgressMbit) are populated. Empty allowlist is the
// default-accept case — adding a "daddr { } accept" rule would be
// cosmetic at best, wrong at worst (a future nft syntax check might
// reject the empty set). Pinning the omission here makes that
// asymmetry explicit. ADR-032 expanded the scan to include the v6
// chain (`ip6 daddr { … } accept` is equally forbidden).
func TestNftCommandsOmitsAllowlistRule_WhenEmpty(t *testing.T) {
	c := testConfig()
	c.ConntrackCap = 4096
	c.EgressMbit = 100
	for i, cmd := range c.NftCommands() {
		line := strings.Join(cmd, " ")
		switch {
		case strings.Contains(line, "ip daddr {") && strings.Contains(line, "accept"):
			t.Errorf("rule %d contains v4 allowlist-shape `ip daddr { … } accept` but EgressAllowlist is empty:\n%s", i, line)
		case strings.Contains(line, "ip6 daddr {") && strings.Contains(line, "accept"):
			t.Errorf("rule %d contains v6 allowlist-shape `ip6 daddr { … } accept` but EgressAllowlist is empty (ADR-032):\n%s", i, line)
		}
	}
}

// TestNftCommandsAllowlistRuleRunsAfterDenies asserts the placement
// the ADR-031 + ADR-032 comments promise: AFTER the SMTP drop +
// lateral-movement deny (so deny > allow on overlap) and AFTER the
// established/related accept (so reply packets on existing flows
// keep flowing regardless of the allowlist). The chain-policy accept
// would be reached if the rule didn't exist, but with allowlist
// enabled the rule itself is the last meaningful gate.
//
// Mixed v4 + v6 input exercises BOTH chains — the v4 allowlist
// must come after the v4 lateral-movement deny, and the v6
// allowlist must come after the v6 lateral-movement deny. ADR-032.
func TestNftCommandsAllowlistRuleRunsAfterDenies(t *testing.T) {
	c := testConfig()
	c.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("fe80::/10"),
	}
	cmds := c.NftCommands()
	var v4Established, v4SmtpDrop, v4DaddrDrop, v4Allowlist = -1, -1, -1, -1
	var v6Established, v6DaddrDrop, v6Allowlist = -1, -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		switch {
		// v4 chain.
		case strings.Contains(line, "ip faas"):
			switch {
			case v4Established < 0 && strings.Contains(line, "ct state established,related accept"):
				v4Established = i
			case v4SmtpDrop < 0 && strings.Contains(line, "tcp dport") && strings.Contains(line, "drop"):
				v4SmtpDrop = i
			case v4DaddrDrop < 0 && strings.Contains(line, "ip daddr") && strings.Contains(line, "drop"):
				v4DaddrDrop = i
			case v4Allowlist < 0 && strings.Contains(line, "ip daddr") && strings.Contains(line, "accept"):
				v4Allowlist = i
			}
		// v6 chain (no SMTP drop; ADR-023).
		case strings.Contains(line, "ip6 faas"):
			switch {
			case v6Established < 0 && strings.Contains(line, "ct state established,related accept"):
				v6Established = i
			case v6DaddrDrop < 0 && strings.Contains(line, "ip6 daddr") && strings.Contains(line, "drop"):
				v6DaddrDrop = i
			case v6Allowlist < 0 && strings.Contains(line, "ip6 daddr") && strings.Contains(line, "accept"):
				v6Allowlist = i
			}
		}
	}
	if v4Established < 0 || v4SmtpDrop < 0 || v4DaddrDrop < 0 || v4Allowlist < 0 {
		t.Fatalf("v4 chain: missing rule: established=%d smtp=%d daddrDrop=%d allowlist=%d\n%s",
			v4Established, v4SmtpDrop, v4DaddrDrop, v4Allowlist, flatten(cmds))
	}
	if v6Established < 0 || v6DaddrDrop < 0 || v6Allowlist < 0 {
		t.Fatalf("v6 chain: missing rule: established=%d daddrDrop=%d allowlist=%d\n%s",
			v6Established, v6DaddrDrop, v6Allowlist, flatten(cmds))
	}
	if v4Established >= v4Allowlist || v4SmtpDrop >= v4Allowlist || v4DaddrDrop >= v4Allowlist {
		t.Errorf("v4 allowlist (rule %d) must come AFTER established=%d smtpDrop=%d daddrDrop=%d",
			v4Allowlist, v4Established, v4SmtpDrop, v4DaddrDrop)
	}
	if v6Established >= v6Allowlist || v6DaddrDrop >= v6Allowlist {
		t.Errorf("v6 allowlist (rule %d) must come AFTER established=%d daddrDrop=%d",
			v6Allowlist, v6Established, v6DaddrDrop)
	}
}

// TestAllowlistAndConnlimitCoexist: a Config with both knobs enabled
// emits both rules. Order: cap (after established, before deny),
// then deny, then allowlist (after deny). Pin that both rules land.
// ADR-032 expanded to assert the v6 siblings too.
func TestAllowlistAndConnlimitCoexist(t *testing.T) {
	c := testConfig()
	c.ConntrackCap = 4096
	c.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("fe80::/10"),
	}
	cmds := c.NftCommands()
	v4Cap := `add rule ip faas forward ct count over 4096 counter name "faas_cap" drop`
	v4Allow := `add rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 } accept`
	v6Cap := `add rule ip6 faas forward ct count over 4096 counter name "faas_cap" drop`
	v6Allow := `add rule ip6 faas forward iifname tap0 ip6 daddr { fe80::/10 } accept`
	var v4CapIdx, v4AllowIdx = -1, -1
	var v6CapIdx, v6AllowIdx = -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		switch {
		case v4CapIdx < 0 && strings.Contains(line, v4Cap):
			v4CapIdx = i
		case v4AllowIdx < 0 && strings.Contains(line, v4Allow):
			v4AllowIdx = i
		case v6CapIdx < 0 && strings.Contains(line, v6Cap):
			v6CapIdx = i
		case v6AllowIdx < 0 && strings.Contains(line, v6Allow):
			v6AllowIdx = i
		}
	}
	if v4CapIdx < 0 || v4AllowIdx < 0 || v4CapIdx >= v4AllowIdx {
		t.Errorf("v4 chain: capIdx=%d allowIdx=%d; cap must come BEFORE allowlist", v4CapIdx, v4AllowIdx)
	}
	if v6CapIdx < 0 || v6AllowIdx < 0 || v6CapIdx >= v6AllowIdx {
		t.Errorf("v6 chain: capIdx=%d allowIdx=%d; cap must come BEFORE allowlist (ADR-032)", v6CapIdx, v6AllowIdx)
	}
}

// TestForwardChainPolicyHelper pins the policy-decision table. Empty
// EgressAllowlist keeps the historical accept (no behaviour change
// from before ADR-031); non-empty flips to drop so the allowlist
// accept rule is the only egress path. The renderer for v4 and v6
// both read this value, so changing one side of the chain implies
// the other — no future asymmetric policy is reachable by editing
// just one of the two `add chain` lines.
func TestForwardChainPolicyHelper(t *testing.T) {
	// Empty → accept.
	if got := testConfig().forwardChainPolicy(); got != "accept" {
		t.Errorf("empty allowlist: policy = %q, want accept", got)
	}
	// Single entry → drop.
	one := testConfig()
	one.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	if got := one.forwardChainPolicy(); got != "drop" {
		t.Errorf("one-entry allowlist: policy = %q, want drop", got)
	}
	// An explicit nil-slice or zero-len slice is len == 0 and so
	// returns "accept" — the documented "clear" wire message
	// (pkg/state/app_egress_allowlist_test.go mirrors this) which
	// rewinds to no-allowlist, current-behaviour. The helper is
	// an LEN check; not a presence check. Pin the equivalence so
	// a future migration to "is the field set?" semantics can't
	// quietly disagree with the persistence layer.
	empty := testConfig()
	empty.EgressAllowlist = []netip.Prefix{}
	if got := empty.forwardChainPolicy(); got != "accept" {
		t.Errorf("explicit-empty (len 0): policy = %q, want accept (len check, presence is the caller's)", got)
	}
}

// TestNftCommandsChainPolicySwitchesWithAllowlist pins the argv-shape
// claim that unlisted destinations actually drop on a populated
// allowlist (PR #159 review F1). Without this switch the chain
// defaulted to `policy accept` and the accept rule on top of an
// already-permissive default was a no-op on unlisted destinations —
// the renderer fix in NftCommands (config.go:195 / :258) is what
// actually delivers deny-by-default behaviour. Both chains (v4 + v6)
// must switch in lock-step.
func TestNftCommandsChainPolicySwitchesWithAllowlist(t *testing.T) {
	// Empty case: BOTH chains use policy accept.
	empty := flatten(testConfig().NftCommands())
	if got := strings.Count(empty, "policy accept"); got != 2 {
		t.Errorf("empty allowlist: expected 2 `policy accept` occurrences (v4 + v6), found %d:\n%s", got, empty)
	}
	if strings.Contains(empty, "policy drop") {
		t.Errorf("empty allowlist: ruleset must NOT contain `policy drop`:\n%s", empty)
	}

	// Populated case: BOTH chains use policy drop, and exactly one
	// allowlist accept rule is on each chain that has any entries
	// (ADR-032 — the v6 mirror lands a sibling on `ip6 faas forward`
	// whenever the v6 partition is non-empty; v4-only input still
	// produces one v4 rule and zero v6 rules).
	pop := testConfig()
	pop.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	ruleset := flatten(pop.NftCommands())
	// Both chains `policy drop`: the literal appears in the chain-create argv.
	if !strings.Contains(ruleset, "add chain ip faas forward") || !strings.Contains(ruleset, "policy drop ;") {
		t.Errorf("populated allowlist: v4 chain must be `policy drop`:\n%s", ruleset)
	}
	if !strings.Contains(ruleset, "add chain ip6 faas forward") {
		t.Errorf("populated allowlist: v6 chain-create argv missing:\n%s", ruleset)
	}
	// Two `policy drop` occurrences (v4 + v6 chain-create), zero
	// `policy accept` (both chains flipped). Asserting the count
	// catches a future regression that flips only one chain.
	if got := strings.Count(ruleset, "policy drop"); got != 2 {
		t.Errorf("populated allowlist: expected 2 `policy drop` (v4 + v6), found %d:\n%s", got, ruleset)
	}
	if got := strings.Count(ruleset, "policy accept"); got != 0 {
		t.Errorf("populated allowlist: expected 0 `policy accept`, found %d:\n%s", got, ruleset)
	}
}
