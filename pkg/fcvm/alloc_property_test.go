package fcvm

// Property-based test for invariant §6.2-5: two live instances never share an IP,
// netns, jail uid/gid, or interface name. TestConcurrentAcquireNoCollision proves
// this for a single simultaneous burst; this fuzz target adds the dimension that
// matters just as much in production — interleaved acquire/release/recycle churn,
// where a freed slot is handed to a new instance and must not alias one still
// live. CLAUDE.md: "Invariants — enforce with property-based tests, never delete."

import (
	"fmt"
	"net/netip"
	"testing"
)

// instancePool bounds the id space so releases and re-acquires collide on the
// same instance ids, exercising the recycle path. It stays far below MaxSlots so
// the allocator never legitimately exhausts.
const instancePool = 16

func FuzzAllocatorNoLiveCollision(f *testing.F) {
	f.Add([]byte{0x00, 0x01, 0x02, 0x81, 0x00}) // acquire a few, release one, reacquire
	f.Add([]byte{0x00, 0x80, 0x00, 0x80, 0x00}) // acquire/release the same id repeatedly
	f.Add(make([]byte, 64))                     // 64 acquires across the 16-id pool

	f.Fuzz(func(t *testing.T, ops []byte) {
		a := NewAllocator()
		live := map[string]Lease{} // instance id -> its current lease

		for i, b := range ops {
			inst := fmt.Sprintf("i%d", int(b)%instancePool)
			// High bit selects release; otherwise acquire.
			if b&0x80 != 0 {
				releaseOp(t, a, live, inst, i)
			} else {
				acquireOp(t, a, live, inst, i)
			}
			assertNoLiveCollision(t, live, i)
			if got := a.InUse(); got != len(live) {
				t.Fatalf("step %d: InUse=%d, tracked live=%d", i, got, len(live))
			}
		}
	})
}

func acquireOp(t *testing.T, a *Allocator, live map[string]Lease, inst string, step int) {
	t.Helper()
	if _, held := live[inst]; held {
		// Double-acquire without an intervening release must be rejected.
		if _, err := a.Acquire(inst); err == nil {
			t.Fatalf("step %d: re-acquiring live instance %q should error", step, inst)
		}
		return
	}
	l, err := a.Acquire(inst)
	if err != nil {
		// With a 16-id pool the box is never full, so any error is a bug.
		t.Fatalf("step %d: acquire %q: %v", step, inst, err)
	}
	live[inst] = l
}

func releaseOp(t *testing.T, a *Allocator, live map[string]Lease, inst string, step int) {
	t.Helper()
	if _, held := live[inst]; held {
		if err := a.Release(inst); err != nil {
			t.Fatalf("step %d: release %q: %v", step, inst, err)
		}
		delete(live, inst)
		return
	}
	// Releasing an unknown instance must error (the leak signal).
	if err := a.Release(inst); err == nil {
		t.Fatalf("step %d: releasing unknown instance %q should error", step, inst)
	}
}

// assertNoLiveCollision checks that across all currently-live leases, every
// uniqueness-bearing field is distinct — the literal statement of §6.2-5.
func assertNoLiveCollision(t *testing.T, live map[string]Lease, step int) {
	t.Helper()
	slots := map[int]string{}
	uids := map[int]string{}
	ips := map[netip.Addr]string{}
	names := map[string]string{} // veth + netns names share one flat namespace on the host

	for inst, l := range live {
		if l.UID != l.GID {
			t.Fatalf("step %d: %s uid %d != gid %d", step, inst, l.UID, l.GID)
		}
		if prev, dup := slots[l.Slot]; dup {
			t.Fatalf("step %d: slot %d shared by live %s and %s", step, l.Slot, prev, inst)
		}
		if prev, dup := uids[l.UID]; dup {
			t.Fatalf("step %d: uid %d shared by live %s and %s", step, l.UID, prev, inst)
		}
		if prev, dup := ips[l.HostIP]; dup {
			t.Fatalf("step %d: host IP %s shared by live %s and %s", step, l.HostIP, prev, inst)
		}
		for _, name := range []string{l.VethHost, l.VethPeer, l.Netns} {
			if prev, dup := names[name]; dup {
				t.Fatalf("step %d: iface/netns name %q shared by live %s and %s", step, name, prev, inst)
			}
			names[name] = inst
		}
		slots[l.Slot] = inst
		uids[l.UID] = inst
		ips[l.HostIP] = inst
	}
}
