package fcvm

import (
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

func TestPublicForwardTargetRetainsOriginalCapturedGeneration(t *testing.T) {
	original := netns.PublicForwardTarget{HostIP: netip.MustParseAddr("10.100.0.2"), BridgeIP: netip.MustParseAddr("10.100.0.1"), Veth: "vh-original", IfIndex: 41, AppPort: 9000}
	m := &Manager{live: map[string]*Instance{"instance": {publicForwardTarget: &original}}}
	got, err := m.PublicForwardTarget("instance")
	if err != nil || got == &original || *got != original {
		t.Fatalf("captured route: %+v %v", got, err)
	}
	got.IfIndex = 42
	got.HostIP = netip.MustParseAddr("10.100.0.3")
	next, err := m.PublicForwardTarget("instance")
	if err != nil || *next != original {
		t.Fatalf("caller mutated original attempt: %+v %v", next, err)
	}
	delete(m.live, "instance")
	if route, err := m.PublicForwardTarget("instance"); route != nil || err == nil {
		t.Fatal("retired instance retained a forwarding target")
	}
}
