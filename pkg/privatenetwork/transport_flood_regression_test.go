package privatenetwork

import (
	"net/netip"
	"strings"
	"testing"
)

func TestFabricVXLANFloodRulesUseKernelSupportedOperation(t *testing.T) {
	p, err := BuildFabricTransportPlan(FabricTransportSpec{Fabric: FabricSpec{AccountID: "acct-audit3", NetworkID: "prod", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")}, OverlayInterface: "tailscale0", LocalAddress: netip.MustParseAddr("100.64.0.10"), PeerAddresses: []netip.Addr{netip.MustParseAddr("100.64.0.11")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range append(p.Setup, p.PeerSync...) {
		command := strings.Join(argv, " ")
		if strings.HasPrefix(command, "bridge fdb replace 00:00:00:00:00:00") {
			t.Fatalf("Linux vxlan_fdb_update_create/existing rejects NLM_F_REPLACE for zero/multicast MAC with EOPNOTSUPP: %s", command)
		}
	}
}

func TestFabricVXLANPeerRepairUsesObservedDelta(t *testing.T) {
	observed := ParseFabricTransportPeers([]byte("00:00:00:00:00:00 dst 100.64.0.11 self permanent\n" +
		"00:00:00:00:00:00 dst 100.64.0.12 self permanent\n" +
		"02:aa:bb:cc:dd:ee dst 100.64.0.13 self permanent\n"))
	expected := []netip.Addr{netip.MustParseAddr("100.64.0.11"), netip.MustParseAddr("100.64.0.14")}
	commands := FabricTransportPeerCommands("gpx-test", expected, observed)
	if len(commands) != 2 {
		t.Fatalf("peer repair = %v, want one stale deletion and one missing append", commands)
	}
	if strings.Join(commands[0], " ") != "bridge fdb del 00:00:00:00:00:00 dev gpx-test dst 100.64.0.12" {
		t.Fatalf("wrong stale peer deletion: %v", commands[0])
	}
	if strings.Join(commands[1], " ") != "bridge fdb append 00:00:00:00:00:00 dev gpx-test dst 100.64.0.14" {
		t.Fatalf("wrong missing peer append: %v", commands[1])
	}
	if commands := FabricTransportPeerCommands("gpx-test", expected, expected); len(commands) != 0 {
		t.Fatal("converged roster emitted duplicate appends")
	}
}
