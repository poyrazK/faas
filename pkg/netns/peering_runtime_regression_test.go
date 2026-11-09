package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func peeringRuntimeConfig() Config {
	c := NewConfig("audit3", "fc-audit3", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkBridge = "gpn-audit3"
	c.PrivateVethHost = "gpn-h"
	c.PrivateVethPeer = "gpn-p"
	c.PrivateNetworkAddress = netip.MustParseAddr("10.42.0.2")
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24"), netip.MustParsePrefix("10.43.0.0/24")}
	return c
}
func TestPrivateNetworkPeeringRoutesUseARoutedNextHop(t *testing.T) {
	c := peeringRuntimeConfig()
	for _, argv := range c.PrivateNetworkRouteCommands() {
		command := strings.Join(argv, " ")
		if strings.Contains(command, "10.43.0.0/24") && !strings.Contains(command, " via ") {
			t.Fatalf("off-subnet peer route requires ARP for unreachable remote members instead of a gateway: %s", command)
		}
	}
}
func TestPrivateNetworkOmittedFirewallCIDRsStayWithinOriginalNetwork(t *testing.T) {
	c := peeringRuntimeConfig()
	c.PrivateNetworkFirewallRules = []PrivateNetworkFirewallRule{{Direction: "egress", Protocol: "tcp", Ports: []PrivateNetworkFirewallPortRange{{Start: 443, End: 443}}}}
	for _, argv := range c.ForwardPrivateNetworkFirewallRules(func(args ...string) []string { return args }) {
		command := strings.Join(argv, " ")
		if strings.Contains(command, "10.43.0.0/24") {
			t.Fatalf("adding peering silently broadens an omitted-CIDR firewall rule beyond its network: %s", command)
		}
	}
}
