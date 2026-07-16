package netns

import (
	"net/netip"
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

func flatten(cmds [][]string) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}
