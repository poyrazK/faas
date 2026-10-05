// adr: 568 — unknown managed networks cannot grant a reusable native slot.
package fcvm

import "testing"

func TestNativeRecoveryNetworkInventoryRefusesUnknownOrNetworklessResources(t *testing.T) {
	l := leaseForSlot("owned", 3)
	host, peer := privateVethNames(l.Slot)
	if err := checkNativeNetworkInventory([]Lease{l}, []string{l.Netns, "operator"}, []string{l.VethHost, l.VethPeer, host, peer, "eth0", "gpn-bridge"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vh4", "vp4", "gpn-h00004", "gpn-p00004", "vh0003"} {
		if err := checkNativeNetworkInventory([]Lease{l}, nil, []string{name}); err == nil {
			t.Fatalf("unowned %s accepted", name)
		}
	}
	for _, name := range []string{"fc-unknown", "fc-prepared-unknown"} {
		if err := checkNativeNetworkInventory([]Lease{l}, []string{name}, nil); err == nil {
			t.Fatalf("unowned %s accepted", name)
		}
	}
	l.Networkless = true
	if err := checkNativeNetworkInventory([]Lease{l}, []string{l.Netns}, nil); err == nil {
		t.Fatal("networkless frame adopted a namespace")
	}
	if err := checkNativeNetworkInventory([]Lease{l}, nil, []string{l.VethPeer}); err == nil {
		t.Fatal("networkless frame adopted a veth")
	}
}
