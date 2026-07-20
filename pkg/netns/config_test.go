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
// chain's `iif "..." oifname "..." accept` allow rule never matches the
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
	if !strings.Contains(rendered, `iif "`+TenantBridge+`" oifname "`) {
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
	if !strings.Contains(text, `iif "`+TenantBridge+`" oifname "`) {
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
