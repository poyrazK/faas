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
