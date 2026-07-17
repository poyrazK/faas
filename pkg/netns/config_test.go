package netns

import (
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

func TestNftDNATTargetsGuestPort(t *testing.T) {
	rules := testConfig().NftDNAT()
	if !strings.Contains(rules, "dnat to 10.0.0.2:8080") {
		t.Errorf("DNAT should target the guest :8080 contract, got:\n%s", rules)
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
