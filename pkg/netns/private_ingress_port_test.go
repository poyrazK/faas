package netns

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestPrivateIngressUsesTranslatedGuestPort(t *testing.T) {
	for _, guestPort := range []int{8080, 3000} {
		for _, firewall := range []bool{false, true} {
			t.Run(fmt.Sprintf("port=%d/firewall=%t", guestPort, firewall), func(t *testing.T) {
				c := NewConfig("app", "fc-app", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
				c.GuestAppPort = guestPort
				c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
				c.PrivateNetworkAllowedCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/25")}
				c.PrivateNetworkBridge, c.PrivateVethHost, c.PrivateVethPeer = "gpn-br", "gpn-h", "gpn-p"
				c.PrivateNetworkAddress = netip.MustParseAddr("10.42.0.2")
				if firewall {
					c.PrivateNetworkFirewallRules = []PrivateNetworkFirewallRule{{Direction: "ingress", Protocol: "tcp", Ports: []PrivateNetworkFirewallPortRange{{Start: 8080, End: 8080}}}}
				}
				want := "tcp dport " + strconv.Itoa(guestPort) + " ct status dnat accept"
				if firewall {
					want = "meta l4proto tcp ct original proto-dst 8080 accept"
				}
				for _, commands := range [][][]string{c.NftCommands(), c.PrivateNetworkNftCommands()} {
					var lines []string
					found := false
					for _, argv := range commands {
						line := strings.Join(argv, " ")
						lines = append(lines, line)
						found = found || strings.Contains(line, "forward iifname gpn-p") && strings.Contains(line, want)
					}
					if !strings.Contains(strings.Join(lines, "\n"), "iifname gpn-p ip daddr 10.42.0.2 tcp dport 8080 dnat to 10.0.0.2:"+strconv.Itoa(guestPort)) {
						t.Fatal("private DNAT did not select the guest port")
					}
					if !found {
						t.Fatalf("private ingress has no matching accept for translated port: %s", want)
					}
				}
			})
		}
	}
}
