package privatenetwork

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func testFabricTransportSpec() FabricTransportSpec {
	return FabricTransportSpec{
		Fabric: FabricSpec{
			AccountID: "acct-a",
			NetworkID: "net-prod",
			Region:    "fra1",
			CIDR:      netip.MustParsePrefix("10.42.0.0/16"),
		},
		OverlayInterface: "tailscale0",
		LocalAddress:     netip.MustParseAddr("100.64.0.10"),
		PeerAddresses: []netip.Addr{
			netip.MustParseAddr("100.64.0.12"),
			netip.MustParseAddr("100.64.0.11"),
		},
	}
}

func TestBuildFabricTransportPlanIsDeterministicAndSortsPeers(t *testing.T) {
	first, err := BuildFabricTransportPlan(testFabricTransportSpec())
	if err != nil {
		t.Fatalf("BuildFabricTransportPlan: %v", err)
	}
	secondSpec := testFabricTransportSpec()
	secondSpec.PeerAddresses = []netip.Addr{
		netip.MustParseAddr("100.64.0.11"),
		netip.MustParseAddr("100.64.0.12"),
	}
	second, err := BuildFabricTransportPlan(secondSpec)
	if err != nil {
		t.Fatalf("BuildFabricTransportPlan (reordered): %v", err)
	}
	if first.LinkName != second.LinkName || first.VNI != second.VNI || !reflect.DeepEqual(first.Setup, second.Setup) || !reflect.DeepEqual(first.PeerSync, second.PeerSync) {
		t.Fatalf("transport plan changed with peer order: first=%+v second=%+v", first, second)
	}
	if first.LinkName == BridgeName("acct-a", "net-prod") {
		t.Fatalf("transport link %q must be distinct from bridge", first.LinkName)
	}
	if len(first.Setup) != 5 {
		t.Fatalf("setup commands = %d, want create + master + up + 2 FDB entries", len(first.Setup))
	}
	if len(first.PeerSync) != 3 {
		t.Fatalf("peer sync commands = %d, want flush + 2 FDB entries", len(first.PeerSync))
	}
	if got := first.PeerSync[1][len(first.PeerSync[1])-1]; got != "100.64.0.11" {
		t.Fatalf("first FDB peer = %q, want 100.64.0.11", got)
	}
	if got := first.PeerSync[2][len(first.PeerSync[2])-1]; got != "100.64.0.12" {
		t.Fatalf("second FDB peer = %q, want 100.64.0.12", got)
	}
	if got := first.Setup[0]; got[len(got)-1] != "nolearning" {
		t.Fatalf("VXLAN setup = %v, want nolearning", got)
	}
}

func TestBuildFabricTransportPlanRejectsUnsafeTopology(t *testing.T) {
	tests := []struct {
		name string
		edit func(*FabricTransportSpec)
		want string
	}{
		{name: "missing interface", edit: func(s *FabricTransportSpec) { s.OverlayInterface = "" }, want: "overlay interface"},
		{name: "invalid interface", edit: func(s *FabricTransportSpec) { s.OverlayInterface = "tailscale/0" }, want: "unsupported character"},
		{name: "invalid local", edit: func(s *FabricTransportSpec) { s.LocalAddress = netip.Addr{} }, want: "local address"},
		{name: "self peer", edit: func(s *FabricTransportSpec) { s.PeerAddresses = []netip.Addr{s.LocalAddress} }, want: "local address"},
		{name: "duplicate peer", edit: func(s *FabricTransportSpec) {
			s.PeerAddresses = []netip.Addr{netip.MustParseAddr("100.64.0.11"), netip.MustParseAddr("100.64.0.11")}
		}, want: "duplicated"},
		{name: "ipv6 peer", edit: func(s *FabricTransportSpec) { s.PeerAddresses = []netip.Addr{netip.MustParseAddr("2001:db8::2")} }, want: "IPv4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := testFabricTransportSpec()
			tt.edit(&spec)
			_, err := BuildFabricTransportPlan(spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseFabricTransportPeersAndDetectsDrift(t *testing.T) {
	observed := ParseFabricTransportPeers([]byte("00:00:00:00:00:00 dev gpx-abcd dst 100.64.0.12 self permanent\n" +
		"00:00:00:00:00:00 dst 100.64.0.11 dev gpx-abcd\n" +
		"00:00:00:00:00:00 dst not-an-ip dev gpx-abcd\n"))
	wantObserved := []netip.Addr{netip.MustParseAddr("100.64.0.11"), netip.MustParseAddr("100.64.0.12")}
	if !reflect.DeepEqual(observed, wantObserved) {
		t.Fatalf("observed peers = %v, want %v", observed, wantObserved)
	}

	missing, stale := FabricPeerDrift([]netip.Addr{netip.MustParseAddr("100.64.0.11"), netip.MustParseAddr("100.64.0.13")}, observed)
	if want := []netip.Addr{netip.MustParseAddr("100.64.0.13")}; !reflect.DeepEqual(missing, want) {
		t.Fatalf("missing peers = %v, want %v", missing, want)
	}
	if want := []netip.Addr{netip.MustParseAddr("100.64.0.12")}; !reflect.DeepEqual(stale, want) {
		t.Fatalf("stale peers = %v, want %v", stale, want)
	}
}
