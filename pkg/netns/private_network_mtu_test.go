package netns

import (
	"strings"
	"testing"
)

func TestPrivateNetworkMTUClampsBothDirectionsBeforeEstablishedAccept(t *testing.T) {
	config := peeringRuntimeConfig()
	config.PrivateNetworkMTU = 1230
	commands := config.NftCommands()
	var script strings.Builder
	for _, command := range commands {
		script.WriteString(strings.Join(command, " ") + "\n")
	}
	text := script.String()
	for _, direction := range []string{"iifname", "oifname"} {
		match := direction + " " + config.PrivateVethPeer + " tcp flags & ( syn | rst ) == syn tcp option maxseg size > 1190 tcp option maxseg size set 1190"
		index := strings.Index(text, match)
		if index < 0 || index > strings.Index(text, "ct state established,related accept") {
			t.Fatalf("missing private MSS reduction before early accept: %s", match)
		}
	}
	var topology strings.Builder
	for _, command := range config.PrivateNetworkSetupCommands() {
		topology.WriteString(strings.Join(command, " ") + "\n")
	}
	if strings.Count(topology.String(), "mtu 1230") != 2 {
		t.Fatalf("private setup did not configure both endpoints: %s", topology.String())
	}
	if strings.Contains(topology.String(), "tap0 mtu") {
		t.Fatal("private MTU mutated the guest's fixed network world")
	}
}
