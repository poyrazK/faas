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
