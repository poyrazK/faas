// adr: 169
package netns

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestNftCommandsAdmitGuestServiceProxyOnlyOnHostBridge(t *testing.T) {
	config := NewConfigWithBridge(
		"instance-1", "fc-instance-1", "veth-host", "veth-peer",
		netip.MustParseAddr("10.100.0.7"), netip.MustParseAddr("10.100.0.1"),
	)
	commands := config.NftCommands()
	want := []string{"iifname", "tap0", "ip", "daddr", "10.100.0.1", "tcp", "dport", strconv.Itoa(ServiceProxyPort), "accept"}
	var found bool
	for _, command := range commands {
		if containsSequence(command, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("service proxy admission rule not found in nft commands")
	}
	for _, protocol := range []string{"udp", "tcp"} {
		wantDNS := []string{"iifname", "tap0", "ip", "daddr", "10.100.0.1", protocol, "dport", strconv.Itoa(ServiceDiscoveryDNSPort), "accept"}
		if !containsSequenceInCommands(commands, wantDNS) {
			t.Fatalf("service discovery DNS %s admission rule not found", protocol)
		}
	}

	joined := make([]string, len(commands))
	for i, command := range commands {
		joined[i] = strings.Join(command, " ")
	}
	serviceRule := "ip daddr 10.100.0.1 tcp dport " + strconv.Itoa(ServiceProxyPort) + " accept"
	serviceIndex, lateralIndex := -1, -1
	for i, command := range joined {
		if strings.Contains(command, serviceRule) {
			serviceIndex = i
		}
		if strings.Contains(command, "ip daddr 10.0.0.0/8") && strings.Contains(command, " drop") && lateralIndex == -1 {
			lateralIndex = i
		}
	}
	if serviceIndex == -1 || lateralIndex == -1 || serviceIndex >= lateralIndex {
		t.Fatalf("service proxy rule index=%d lateral deny index=%d; want service rule first", serviceIndex, lateralIndex)
	}
}

func TestServiceProxyHTTPSFirewallIsOptIn(t *testing.T) {
	config := NewConfigWithBridge("instance-1", "fc-instance-1", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.7"), netip.MustParseAddr("10.100.0.1"))
	want := []string{"iifname", "tap0", "ip", "daddr", "10.100.0.1", "tcp", "dport", strconv.Itoa(ServiceProxyHTTPSPort), "accept"}
	if containsSequenceInCommands(config.NftCommands(), want) {
		t.Fatal("guest :443 admission enabled without CA opt-in")
	}
	config.ServiceProxyHTTPS = true
	if !containsSequenceInCommands(config.NftCommands(), want) {
		t.Fatal("guest :443 admission absent after opt-in")
	}
}

func containsSequenceInCommands(commands [][]string, want []string) bool {
	for _, command := range commands {
		if containsSequence(command, want) {
			return true
		}
	}
	return false
}

func containsSequence(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// adr: 384 Both ports are specific bridge exceptions, never a private-range
// allowlist. Existing callers remain reachable while new Fetch callers work.
func TestServiceProxyCanonicalAndLegacyFirewallScope(t *testing.T) {
	bridge := netip.MustParseAddr("172.19.0.1")
	config := NewConfigWithBridge("instance-ports", "fc-instance-ports", "veth-host", "veth-peer", netip.MustParseAddr("172.19.0.7"), bridge)
	commands := config.NftCommands()
	for _, port := range []int{ServiceProxyPort, LegacyServiceProxyPort} {
		want := []string{"iifname", "tap0", "ip", "daddr", bridge.String(), "tcp", "dport", strconv.Itoa(port), "accept"}
		if !containsSequenceInCommands(commands, want) {
			t.Fatalf("reserved port %d is absent for bridge %s", port, bridge)
		}
		for _, command := range commands {
			line := strings.Join(command, " ")
			if strings.Contains(line, "tcp dport "+strconv.Itoa(port)+" accept") && !strings.Contains(line, "ip daddr "+bridge.String()) {
				t.Fatalf("reserved port %d admitted away from local bridge: %s", port, line)
			}
		}
	}
}

func TestQualificationOnlyNetworkAdmitsOnlyCanonicalProxyAndDNS(t *testing.T) {
	bridge := netip.MustParseAddr("10.100.0.1")
	config := NewConfigWithBridge("qualification-1", "fc-qualification-1", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.7"), bridge)
	config.QualificationOnly = true
	config.ServiceProxyHTTPS = true
	config.ServiceAddressCIDR = netip.MustParsePrefix("198.19.0.0/16")
	config.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}
	config.EgressPorts = []uint16{80, 443, 5432}
	config.OperatorExceptions = []netip.Prefix{netip.MustParsePrefix("10.66.0.0/16")}
	config.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("192.168.201.0/24")}

	commands := config.NftCommands()
	joined := make([]string, len(commands))
	for i, command := range commands {
		joined[i] = strings.Join(command, " ")
	}
	for _, family := range []string{"ip", "ip6"} {
		want := "add chain " + family + " faas forward { type filter hook forward priority filter ; policy drop ; }"
		if !containsSequenceInCommands(commands, strings.Fields(want)) {
			t.Fatalf("qualification %s forward chain is not drop-by-default", family)
		}
	}

	wantProxy := []string{"iifname", "tap0", "ip", "daddr", bridge.String(), "tcp", "dport", strconv.Itoa(ServiceProxyPort), "accept"}
	if !containsSequenceInCommands(commands, wantProxy) {
		t.Fatalf("qualification proxy admission missing for port %d", ServiceProxyPort)
	}
	wantHTTPS := []string{"iifname", "tap0", "ip", "daddr", bridge.String(), "tcp", "dport", strconv.Itoa(ServiceProxyHTTPSPort), "accept"}
	if !containsSequenceInCommands(commands, wantHTTPS) {
		t.Fatalf("configured qualification HTTPS proxy admission missing for port %d", ServiceProxyHTTPSPort)
	}
	config.ServiceProxyHTTPS = false
	if containsSequenceInCommands(config.NftCommands(), wantHTTPS) {
		t.Fatal("qualification HTTPS proxy admission ignored the private-CA opt-in")
	}
	config.ServiceProxyHTTPS = true
	for _, protocol := range []string{"udp", "tcp"} {
		want := []string{"iifname", "tap0", "ip", "daddr", bridge.String(), protocol, "dport", strconv.Itoa(ServiceDiscoveryDNSPort), "accept"}
		if !containsSequenceInCommands(commands, want) {
			t.Fatalf("qualification DNS admission missing for %s", protocol)
		}
	}
	for _, forbidden := range []string{
		"tcp dport " + strconv.Itoa(LegacyServiceProxyPort) + " accept",
		"ip daddr { 8.8.8.0/24 } tcp dport != 25 accept",
		"ip daddr 198.19.0.0/16 meta l4proto tcp accept",
		"ip daddr 192.168.201.0/24",
		"ip saddr 10.66.0.0/16 accept",
		"tcp dport != @egress_ports",
	} {
		for _, line := range joined {
			if strings.Contains(line, forbidden) {
				t.Fatalf("qualification network emitted forbidden policy %q in %q", forbidden, line)
			}
		}
	}
	for _, line := range joined {
		if !strings.Contains(line, "add rule ") || !strings.Contains(line, " forward ") || !strings.HasSuffix(line, " accept") {
			continue
		}
		if strings.Contains(line, "ct state established,related accept") ||
			strings.Contains(line, "ip daddr "+bridge.String()+" tcp dport "+strconv.Itoa(ServiceProxyPort)+" accept") ||
			strings.Contains(line, "ip daddr "+bridge.String()+" tcp dport "+strconv.Itoa(ServiceProxyHTTPSPort)+" accept") ||
			strings.Contains(line, "ip daddr "+bridge.String()+" udp dport "+strconv.Itoa(ServiceDiscoveryDNSPort)+" accept") ||
			strings.Contains(line, "ip daddr "+bridge.String()+" tcp dport "+strconv.Itoa(ServiceDiscoveryDNSPort)+" accept") {
			continue
		}
		t.Fatalf("qualification network emitted an unscoped forward accept: %q", line)
	}
}
