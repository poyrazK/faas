package sched

import (
	"testing"
)

// adr: 164
func TestAppSpecPrivateNetworkCIDRsWire(t *testing.T) {
	got := (AppSpec{PrivateNetworkCIDRs: []string{"10.42.0.0/16", "192.168.10.0/24"}}).toProto()
	if len(got.GetPrivateNetworkCidrs()) != 2 || got.GetPrivateNetworkCidrs()[0] != "10.42.0.0/16" {
		t.Fatalf("private network CIDRs did not round-trip: %v", got.GetPrivateNetworkCidrs())
	}
}
