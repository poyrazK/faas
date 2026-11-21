// adr: 20261107122928 amendment 2 — a tenant's br-tenants port does not take
// the broadcast and multicast other namespaces send.
package netns

import (
	"slices"
	"testing"
)

func TestSetupTurnsTenantPortFloodingOff(t *testing.T) {
	c := testConfig()
	cmds := c.SetupCommands()
	idx := func(argv []string) int {
		return slices.IndexFunc(cmds, func(s []string) bool { return slices.Equal(s, argv) })
	}
	master := idx([]string{"ip", "link", "set", c.VethHost, "master", TenantBridge})
	flood := idx(TenantPortFloodOffCommand(c.VethHost))
	up := idx([]string{"ip", "link", "set", c.VethHost, "up"})
	if master < 0 || flood < 0 || up < 0 {
		t.Fatalf("setup lacks master (%d), flood off (%d) or up (%d): %v", master, flood, up, cmds)
	}
	if master >= flood || flood >= up {
		t.Fatalf("flood off at %d must follow the bridge join (%d) and precede link up (%d)", flood, master, up)
	}
}

func TestTenantPortFloodOffCommand(t *testing.T) {
	got := TenantPortFloodOffCommand("vh7")
	want := []string{"ip", "link", "set", "dev", "vh7", "type", "bridge_slave", "mcast_flood", "off", "bcast_flood", "off"}
	if !slices.Equal(got, want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	// Unknown unicast must keep flooding: the host reaches a lease whose
	// bridge entry aged out that way.
	if slices.Contains(got, "flood") {
		t.Fatalf("command turns off unknown unicast flooding: %v", got)
	}
}
