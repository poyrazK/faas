package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func TestSetupCommandsInstallPrivateRoutesBeforeDefault(t *testing.T) {
	c := NewConfig("app", "fc-app", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16"), netip.MustParsePrefix("192.168.10.0/24")}
	cmds := c.SetupCommands()
	joined := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		joined = append(joined, strings.Join(cmd, " "))
	}
	firstRoute := -1
	defaultRoute := -1
	for i, command := range joined {
		if strings.Contains(command, "ip route replace 10.42.0.0/16") {
			firstRoute = i
		}
		if strings.Contains(command, "ip route add default") {
			defaultRoute = i
		}
	}
	if firstRoute < 0 || defaultRoute < 0 || firstRoute >= defaultRoute {
		t.Fatalf("private route/default ordering invalid: %v", joined)
	}
}

func TestNftCommandsPrivateNetworkAcceptPrecedesDeny(t *testing.T) {
	c := NewConfig("app", "fc-app", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	cmds := c.NftCommands()
	private := -1
	deny := -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "ip daddr { 10.42.0.0/16 }") {
			private = i
		}
		if strings.Contains(line, "ip daddr 10.0.0.0/8") && strings.HasSuffix(line, "drop") {
			deny = i
		}
	}
	if private < 0 || deny < 0 || private >= deny {
		t.Fatalf("private accept does not precede lateral deny: private=%d deny=%d", private, deny)
	}
}

// adr: 009
func TestGregalePrivateSideLinkPreservesPublicVeth(t *testing.T) {
	c := NewConfig("app", "fc-app", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	c.PrivateNetworkBridge = "gpn-abc123"
	c.PrivateVethHost = "gpn-h00001"
	c.PrivateVethPeer = "gpn-p00001"
	c.PrivateNetworkAddress = netip.MustParseAddr("10.42.0.2")
	cmds := c.SetupCommands()
	joined := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		joined = append(joined, strings.Join(cmd, " "))
	}
	if !containsCommand(joined, "ip link set veth-host master br-tenants") {
		t.Fatalf("public veth was not retained: %v", joined)
	}
	if !containsCommand(joined, "ip link set gpn-h00001 master gpn-abc123") {
		t.Fatalf("private veth was not attached to gpn bridge: %v", joined)
	}
	if !containsCommand(joined, "ip netns exec fc-app ip addr add 10.42.0.2/16 dev gpn-p00001") {
		t.Fatalf("private member address was not programmed: %v", joined)
	}
	if !containsCommand(joined, "ip netns exec fc-app ip route replace 10.42.0.0/16 dev gpn-p00001 src 10.42.0.2") {
		t.Fatalf("private route did not use side-link: %v", joined)
	}
}

// adr: 009
func TestNftCommandsPrivateSideLinkPublishesStableAddress(t *testing.T) {
	c := NewConfig("app", "fc-app", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	c.PrivateNetworkBridge = "gpn-abc123"
	c.PrivateVethHost = "gpn-h00001"
	c.PrivateVethPeer = "gpn-p00001"
	c.PrivateNetworkAddress = netip.MustParseAddr("10.42.0.2")
	cmds := c.NftCommands()
	joined := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		joined = append(joined, strings.Join(cmd, " "))
	}
	if !containsCommand(joined, "iifname gpn-p00001 ip daddr 10.42.0.2 tcp dport 8080 dnat to 10.0.0.2:8080") {
		t.Fatalf("private DNAT rule missing: %v", joined)
	}
	if !containsCommand(joined, "oifname gpn-p00001 ip saddr 10.0.0.2 snat to 10.42.0.2") {
		t.Fatalf("private SNAT rule missing: %v", joined)
	}
}

func TestNftCommandsPrivatePolicyRestrictsIngressAndEgress(t *testing.T) {
	c := NewConfig("app", "fc-app", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	c.PrivateNetworkAllowedCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.8.0/24")}
	c.PrivateNetworkBridge = "gpn-abc123"
	c.PrivateVethHost = "gpn-h00001"
	c.PrivateVethPeer = "gpn-p00001"
	c.PrivateNetworkAddress = netip.MustParseAddr("10.42.0.2")
	joined := make([]string, 0)
	for _, cmd := range c.NftCommands() {
		joined = append(joined, strings.Join(cmd, " "))
	}
	if !containsCommand(joined, "iifname tap0 ip daddr { 10.42.8.0/24 } accept") {
		t.Fatalf("policy egress rule missing: %v", joined)
	}
	if !containsCommand(joined, "iifname gpn-p00001 ip saddr { 10.42.8.0/24 } ip daddr 10.0.0.2 tcp dport 8080 accept") {
		t.Fatalf("policy ingress source restriction missing: %v", joined)
	}
	if !containsCommand(joined, "iifname gpn-p00001 drop") {
		t.Fatalf("policy ingress terminal drop missing: %v", joined)
	}
}

func TestNftCommandsPrivateFirewallRulesFailClosed(t *testing.T) {
	c := NewConfig("app", "fc-app", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	c.PrivateNetworkAllowedCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.8.0/24")}
	c.PrivateNetworkBridge = "gpn-abc123"
	c.PrivateVethHost = "gpn-h00001"
	c.PrivateVethPeer = "gpn-p00001"
	c.PrivateNetworkAddress = netip.MustParseAddr("10.42.0.2")
	c.PrivateNetworkFirewallRules = []PrivateNetworkFirewallRule{
		{Direction: "ingress", Protocol: "tcp", Ports: []PrivateNetworkFirewallPortRange{{Start: 443, End: 443}}},
		{Direction: "egress", Protocol: "udp", Ports: []PrivateNetworkFirewallPortRange{{Start: 53, End: 53}}},
	}
	joined := make([]string, 0)
	for _, cmd := range c.NftCommands() {
		joined = append(joined, strings.Join(cmd, " "))
	}
	if !containsCommand(joined, "iifname tap0 ip daddr { 10.42.8.0/24 } udp dport 53 accept") {
		t.Fatalf("protocol-aware egress rule missing: %v", joined)
	}
	if !containsCommand(joined, "iifname gpn-p00001 ip saddr { 10.42.8.0/24 } ip daddr 10.0.0.2 tcp dport 443 accept") {
		t.Fatalf("protocol-aware ingress rule missing: %v", joined)
	}
	if !containsCommand(joined, "iifname tap0 ip daddr { 10.42.8.0/24 } drop") || !containsCommand(joined, "iifname gpn-p00001 drop") {
		t.Fatalf("firewall rules did not install terminal drops: %v", joined)
	}
}

// adr: 009
func TestPrivateNetworkNftCommandsAreAdditive(t *testing.T) {
	c := NewConfig("app", "fc-app", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	c.PrivateNetworkBridge = "gpn-abc123"
	c.PrivateVethHost = "gpn-h00001"
	c.PrivateVethPeer = "gpn-p00001"
	c.PrivateNetworkAddress = netip.MustParseAddr("10.42.0.2")
	cmds := c.PrivateNetworkNftCommands()
	if len(cmds) != 3 {
		t.Fatalf("private live patch emitted %d commands, want 3: %v", len(cmds), cmds)
	}
	if !strings.Contains(strings.Join(cmds[2], " "), "insert rule ip faas forward") {
		t.Fatalf("private ingress rule must be inserted before deny rules: %v", cmds[2])
	}
}

func containsCommand(commands []string, want string) bool {
	for _, command := range commands {
		if strings.Contains(command, want) {
			return true
		}
	}
	return false
}
