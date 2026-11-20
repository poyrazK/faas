package fcvm

import (
	"net/netip"
	"testing"
)

// A retired record whose slot a live VM reused must not claim the live VM's
// slot-named host interfaces; its per-instance namespace stays checked.
func TestNativeRetiredLeaseNetworkSlotReuse(t *testing.T) {
	retired := Lease{Instance: "88f33064", Slot: 1, Netns: "fc-88f33064", VethHost: "vh1", VethPeer: "vp1", HostIP: netip.MustParseAddr("10.100.0.6")}
	live := Lease{Instance: "3f340541", Slot: 1, Netns: "fc-3f340541", VethHost: "vh1", VethPeer: "vp1", HostIP: netip.MustParseAddr("10.100.0.6")}
	other := Lease{Instance: "61b47c28", Slot: 0, Netns: "fc-61b47c28", VethHost: "vh0", VethPeer: "vp0", HostIP: netip.MustParseAddr("10.100.0.2")}
	cases := []struct {
		name      string
		records   []nativeLaunchRecord
		wantVeths bool
	}{
		{name: "slot free: interfaces checked", records: []nativeLaunchRecord{{Lease: retired, ResourcesRemoved: true}, {Lease: other}}, wantVeths: true},
		{name: "slot reused by live VM: interfaces skipped", records: []nativeLaunchRecord{{Lease: retired, ResourcesRemoved: true}, {Lease: live}}},
		{name: "slot reused by another retired VM: interfaces checked", records: []nativeLaunchRecord{{Lease: retired, ResourcesRemoved: true}, {Lease: live, ResourcesRemoved: true}}, wantVeths: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nc := nativeRetiredLeaseNetwork(retired, nativeSlotsHeldByUnremoved(tc.records))
			if nc.Netns != retired.Netns {
				t.Fatalf("netns=%q want %q", nc.Netns, retired.Netns)
			}
			hasVeths := nc.VethHost != "" && nc.VethPeer != "" && nc.PrivateVethHost != "" && nc.PrivateVethPeer != ""
			noVeths := nc.VethHost == "" && nc.VethPeer == "" && nc.PrivateVethHost == "" && nc.PrivateVethPeer == ""
			if tc.wantVeths && !hasVeths || !tc.wantVeths && !noVeths {
				t.Fatalf("veths=(%q %q %q %q) want present=%v", nc.VethHost, nc.VethPeer, nc.PrivateVethHost, nc.PrivateVethPeer, tc.wantVeths)
			}
		})
	}
}
