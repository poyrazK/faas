// adr: 20261027090013 §10 — a guest whose tenant is root sends from its own
// MAC and GuestIP only: the tap ingress guard drops other MACs, other IPv4
// sources, ARP claims for other addresses and every other protocol, and VMs
// without a fixed MAC get no guard and no extra commands.
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func TestGuestGuardOnlyForAFixedMAC(t *testing.T) {
	c := NewConfigWithBridge("sandbox-sb", "fc-sb", "vh0", "vp0", netip.MustParseAddr("10.100.0.9"), netip.MustParseAddr("10.100.0.1"))
	if cmds := c.GuestGuardCommands(); cmds != nil {
		t.Fatalf("guard without a MAC: %v", cmds)
	}
	unguarded := len(c.NftResetCommands())
	for _, line := range renderedLines(c) {
		if strings.Contains(line, GuestGuardTable) {
			t.Fatalf("an unguarded VM renders the guard: %q", line)
		}
	}
	c.GuestMAC = SandboxGuestMAC
	var guard []string
	for _, argv := range c.GuestGuardCommands() {
		guard = append(guard, strings.Join(argv, " "))
	}
	all := strings.Join(guard, "\n")
	for _, want := range []string{
		"ip netns exec fc-sb nft add table netdev " + GuestGuardTable,
		"type filter hook ingress device " + c.Tap + " priority -500 ; policy drop ;",
		"ether saddr != " + SandboxGuestMAC + " counter name " + GuestSpoofCounter + " drop",
		"ether type ip ip saddr " + GuestIP + " accept",
		"ether type arp arp saddr ether " + SandboxGuestMAC + " arp saddr ip " + GuestIP + " accept",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("guard is missing %q:\n%s", want, all)
		}
	}
	// The MAC check comes first; the accepts after it; a counted drop last.
	if !strings.HasSuffix(guard[len(guard)-1], "counter name "+GuestSpoofCounter+" drop") ||
		strings.Index(all, "ether saddr !=") > strings.Index(all, "ip saddr "+GuestIP) {
		t.Fatalf("guard order:\n%s", all)
	}
	rendered := strings.Join(renderedLines(c), "\n")
	if !strings.Contains(rendered, "netdev "+GuestGuardTable) {
		t.Fatal("NftCommands does not install the guard")
	}
	if got := len(c.NftResetCommands()); got != unguarded+1 {
		t.Fatalf("reset has %d commands, want %d", got, unguarded+1)
	}
}
