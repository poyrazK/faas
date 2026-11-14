package netns

// adr: 20261104120000

import (
	"net/netip"
	"strings"
	"testing"
)

func TestPrivatePolicyRechecksOriginalTupleBeforeEstablished(t *testing.T) {
	c := peeringRuntimeConfig()
	c.PrivateNetworkServiceAddress = netip.MustParseAddr("10.42.0.9")
	c.PrivateNetworkFirewallRules = []PrivateNetworkFirewallRule{
		{Direction: "ingress", Protocol: "tcp", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/25")}, Ports: []PrivateNetworkFirewallPortRange{{Start: 9000, End: 9000}}},
		{Direction: "egress", Protocol: "udp", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.128/25")}, Ports: []PrivateNetworkFirewallPortRange{{Start: 53, End: 53}}},
	}
	var script strings.Builder
	for _, cmd := range c.NftCommands() {
		script.WriteString(strings.Join(cmd, " ") + "\n")
	}
	for _, required := range []string{
		"hook forward priority -5", // regular established/related accept is priority 0
		"ct original ip saddr 10.0.0.2 jump egress",
		"ct original ip saddr != 10.0.0.2 jump ingress",
		"ct original ip daddr != { 10.42.0.2,10.42.0.9 } drop",
		"ct status != dnat drop",
		"ingress ct original ip saddr { 10.42.0.0/25 } ct protocol tcp ct original proto-dst 9000 return",
		"egress ct original ip daddr { 10.42.0.128/25 } ct protocol udp ct original proto-dst 53 return",
		"faas_private_policy egress drop", "faas_private_policy ingress drop",
	} {
		if !strings.Contains(script.String(), required) {
			t.Errorf("missing revocation rule %q", required)
		}
	}
	for _, cmd := range c.NftResetCommands() {
		if strings.Contains(strings.Join(cmd, " "), "faas_private_policy") {
			t.Fatal("replacement loses historical destination classification")
		}
	}
	// After detach the preserved history still dispatches both directions,
	// but no authorization remains and a cached DNAT cannot grant access.
	c.PrivateNetworkCIDRs, c.PrivateNetworkAllowedCIDRs, c.PrivateNetworkFirewallRules = nil, nil, nil
	c.PrivateVethPeer = ""
	for _, cmd := range c.privateConntrackPolicyCommands(func(args ...string) []string { return args }) {
		if strings.HasSuffix(strings.Join(cmd, " "), "return") && (cmd[4] == "egress" || cmd[4] == "ingress") {
			t.Fatalf("detach retains authorization: %v", cmd)
		}
	}
}

func TestGuestSourceValidationPrecedesConntrack(t *testing.T) {
	for _, c := range []Config{peeringRuntimeConfig(), NewConfig("source", "fc-source", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))} {
		var script strings.Builder
		for _, cmd := range c.NftCommands() {
			script.WriteString(strings.Join(cmd, " ") + "\n")
		}
		for _, required := range []string{"guest_source { type filter hook prerouting priority raw", "guest_source iifname tap0 ip saddr != 10.0.0.2 drop"} {
			if !strings.Contains(script.String(), required) {
				t.Errorf("missing source validation: %s", required)
			}
		}
	}
}

func TestPrivateConntrackBaselineUsesPublishedPort(t *testing.T) {
	c := peeringRuntimeConfig()
	c.GuestAppPort = 9090
	var script strings.Builder
	for _, cmd := range c.NftCommands() {
		script.WriteString(strings.Join(cmd, " ") + "\n")
	}
	if !strings.Contains(script.String(), "ingress ct protocol tcp ct original proto-dst 8080 return") {
		t.Fatal("baseline checks translated guest port instead of published port")
	}
	if strings.Contains(script.String(), "ingress ct protocol tcp ct original proto-dst 9090") {
		t.Fatal("custom guest port replaced original conntrack port")
	}
}

func TestPrivateARPNotificationFollowsAddressBinding(t *testing.T) {
	c := peeringRuntimeConfig()
	c.PrivateNetworkServiceAddress = netip.MustParseAddr("10.42.0.9")
	vip, notify, up := -1, -1, -1
	for i, argv := range c.PrivateNetworkSetupCommands() {
		line := strings.Join(argv, " ")
		if strings.Contains(line, "addr replace 10.42.0.9/32") {
			vip = i
		}
		if strings.Contains(line, ".arp_notify=1") {
			notify = i
		}
		if strings.Contains(line, "ip link set "+c.PrivateVethPeer+" up") {
			up = i
		}
	}
	if vip < 0 || notify <= vip || up <= notify {
		t.Fatalf("VIP and notification must precede NETDEV_UP: vip=%d notify=%d up=%d", vip, notify, up)
	}
	commands := c.PrivateNetworkAnnounceCommands("02:01:02:03:04:05")
	if len(commands) != 2 || !strings.Contains(strings.Join(commands[0], " "), ".arp_notify=1") || !strings.HasSuffix(strings.Join(commands[1], " "), "ip link set dev "+c.PrivateVethPeer+" address 02:01:02:03:04:05") {
		t.Fatalf("promotion does not trigger NETDEV_CHANGEADDR after notification: %v", commands)
	}
}
