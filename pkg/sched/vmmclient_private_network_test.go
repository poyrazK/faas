package sched

import (
	"testing"
)

// adr: 164
// adr: 009
func TestAppSpecPrivateNetworkCIDRsWire(t *testing.T) {
	got := (AppSpec{PrivateNetworkCIDRs: []string{"10.42.0.0/16", "192.168.10.0/24"}, PrivateNetworkAllowedCIDRs: []string{"10.42.8.0/24"}, PrivateNetworkID: "net-1", PrivateNetworkAddress: "10.42.0.2"}).toProto()
	if len(got.GetPrivateNetworkCidrs()) != 2 || got.GetPrivateNetworkCidrs()[0] != "10.42.0.0/16" {
		t.Fatalf("private network CIDRs did not round-trip: %v", got.GetPrivateNetworkCidrs())
	}
	if got.GetPrivateNetworkId() != "net-1" || got.GetPrivateNetworkAddress() != "10.42.0.2" {
		t.Fatalf("Gregale attachment identity did not cross vmmd wire: id=%q address=%q", got.GetPrivateNetworkId(), got.GetPrivateNetworkAddress())
	}
	if len(got.GetPrivateNetworkAllowedCidrs()) != 1 || got.GetPrivateNetworkAllowedCidrs()[0] != "10.42.8.0/24" {
		t.Fatalf("private network policy did not cross vmmd wire: %v", got.GetPrivateNetworkAllowedCidrs())
	}
}
