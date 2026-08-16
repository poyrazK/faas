// Gap #3 wiring tests — verify SetHostIPBase (the per-host
// bridge CIDR override) correctly seeds the slot allocator so
// every per-VM /30 lease is carved from the right /16. PR
// scale-out tier-1 residual.

package fcvm

import (
	"net/netip"
	"testing"
)

// TestSetHostIPBase_ReservesBridgeAddress verifies that after
// SetHostIPBase(10.101.0.0), slot 0 yields 10.101.0.2 (the
// bridge .1 and network .0 are reserved by hostIPOffset = 2).
func TestSetHostIPBase_ReservesBridgeAddress(t *testing.T) {
	saved := hostIPBase
	defer SetHostIPBase(saved)

	SetHostIPBase(netip.MustParseAddr("10.101.0.0"))
	if got := hostIPForSlot(0); got.String() != "10.101.0.2" {
		t.Fatalf("slot 0 with base 10.101.0.0: got %s, want 10.101.0.2", got)
	}
}

// TestSetHostIPBase_ConsecutiveAcquire verifies that three
// consecutive slots map to .2 / .3 / .4 of the new /16 —
// the operator's override is byte-faithful. PR scale-out
// tier-1 residual (Gap #3 wiring).
func TestSetHostIPBase_ConsecutiveAcquire(t *testing.T) {
	saved := hostIPBase
	defer SetHostIPBase(saved)

	SetHostIPBase(netip.MustParseAddr("10.200.0.0"))
	want := []string{"10.200.0.2", "10.200.0.3", "10.200.0.4"}
	for i, w := range want {
		if got := hostIPForSlot(i); got.String() != w {
			t.Fatalf("slot %d: got %s, want %s", i, got, w)
		}
	}
}

// TestSetHostIPBase_BridgeAddrIsReserved guards the .1
// invariant — the bridge IP must never be handed out as a
// per-VM lease (the per-netns default route points at .1, so
// a duplicate would silently break wake-and-proxy).
func TestSetHostIPBase_BridgeAddrIsReserved(t *testing.T) {
	saved := hostIPBase
	defer SetHostIPBase(saved)

	SetHostIPBase(netip.MustParseAddr("10.42.0.0"))
	// Walk the first 16 slots; none may equal the bridge IP.
	bridge := netip.MustParseAddr("10.42.0.1")
	for slot := 0; slot < 16; slot++ {
		if got := hostIPForSlot(slot); got == bridge {
			t.Fatalf("slot %d: allocator handed out the bridge IP %s", slot, bridge)
		}
	}
}
