package fcvm

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"
)

func TestLeaseDerivationIsUnique(t *testing.T) {
	// Distinct slots must yield distinct uid, gid, host IP, and iface names.
	// This is invariant §6.2-5 at the derivation level.
	seenUID := map[int]int{}
	seenIP := map[netip.Addr]int{}
	seenVeth := map[string]int{}
	for slot := 0; slot < MaxSlots; slot++ {
		l := leaseForSlot(fmt.Sprintf("inst-%d", slot), slot)
		if l.UID != l.GID {
			t.Fatalf("slot %d: uid %d != gid %d", slot, l.UID, l.GID)
		}
		if l.UID < JailUIDBase || l.UID > JailUIDMax {
			t.Fatalf("slot %d: uid %d out of range [%d,%d]", slot, l.UID, JailUIDBase, JailUIDMax)
		}
		if prev, dup := seenUID[l.UID]; dup {
			t.Fatalf("uid %d collides between slot %d and %d", l.UID, prev, slot)
		}
		if prev, dup := seenIP[l.HostIP]; dup {
			t.Fatalf("host IP %s collides between slot %d and %d", l.HostIP, prev, slot)
		}
		if prev, dup := seenVeth[l.VethHost]; dup {
			t.Fatalf("veth %s collides between slot %d and %d", l.VethHost, prev, slot)
		}
		seenUID[l.UID] = slot
		seenIP[l.HostIP] = slot
		seenVeth[l.VethHost] = slot
	}
}

func TestHostIPRangeAndReservations(t *testing.T) {
	// Slot 0 starts at .0.2 so the bridge (10.100.0.1) and network (.0.0) are
	// never leased.
	if got := hostIPForSlot(0); got.String() != "10.100.0.2" {
		t.Fatalf("slot 0 host IP = %s, want 10.100.0.2", got)
	}
	bridge := netip.MustParseAddr("10.100.0.1")
	network := netip.MustParseAddr("10.100.0.0")
	prefix := netip.MustParsePrefix("10.100.0.0/16")
	for slot := 0; slot < MaxSlots; slot++ {
		ip := hostIPForSlot(slot)
		if ip == bridge || ip == network {
			t.Fatalf("slot %d leased reserved address %s", slot, ip)
		}
		if !prefix.Contains(ip) {
			t.Fatalf("slot %d host IP %s escaped %s", slot, ip, prefix)
		}
	}
}

func TestIfaceNamesWithinKernelLimit(t *testing.T) {
	// Linux caps interface names at 15 bytes; the highest slot must still fit.
	l := leaseForSlot("x", MaxSlots-1)
	for _, name := range []string{l.VethHost, l.VethPeer} {
		if len(name) > 15 {
			t.Errorf("iface name %q is %d bytes, exceeds kernel limit of 15", name, len(name))
		}
	}
}

func TestAcquireReleaseRecyclesWithoutLiveDuplicates(t *testing.T) {
	a := NewAllocator()
	live := map[int]string{} // slot -> instance currently holding it

	acquire := func(inst string) {
		l, err := a.Acquire(inst)
		if err != nil {
			t.Fatalf("acquire %s: %v", inst, err)
		}
		if other, dup := live[l.Slot]; dup {
			t.Fatalf("slot %d held by both %s and %s", l.Slot, other, inst)
		}
		live[l.Slot] = inst
	}
	release := func(inst string) {
		for slot, holder := range live {
			if holder == inst {
				delete(live, slot)
				break
			}
		}
		if err := a.Release(inst); err != nil {
			t.Fatalf("release %s: %v", inst, err)
		}
	}

	for i := 0; i < 500; i++ {
		acquire(fmt.Sprintf("inst-%d", i))
	}
	if a.InUse() != 500 {
		t.Fatalf("InUse=%d want 500", a.InUse())
	}
	// Release half, then acquire a fresh batch that must reuse freed slots
	// without ever duplicating one still live.
	for i := 0; i < 250; i++ {
		release(fmt.Sprintf("inst-%d", i))
	}
	for i := 500; i < 750; i++ {
		acquire(fmt.Sprintf("inst-%d", i))
	}
	if a.InUse() != 500 {
		t.Fatalf("InUse=%d want 500 after recycle", a.InUse())
	}
}

func TestAcquireRejectsDuplicateInstance(t *testing.T) {
	a := NewAllocator()
	if _, err := a.Acquire("dup"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Acquire("dup"); err == nil {
		t.Error("acquiring the same instance twice should error")
	}
}

func TestReleaseUnknownErrors(t *testing.T) {
	a := NewAllocator()
	if err := a.Release("ghost"); err == nil {
		t.Error("releasing an unknown instance should error (leak signal)")
	}
}

func TestAcquireExhaustion(t *testing.T) {
	a := NewAllocator()
	for i := 0; i < MaxSlots; i++ {
		if _, err := a.Acquire(fmt.Sprintf("i%d", i)); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	if _, err := a.Acquire("one-too-many"); err == nil {
		t.Error("acquiring past MaxSlots should error")
	}
}

// TestConcurrentAcquireNoCollision is the property test for §6.2-5: many
// goroutines acquiring at once must never receive the same slot/uid/IP.
func TestConcurrentAcquireNoCollision(t *testing.T) {
	a := NewAllocator()
	const n = 2000
	leases := make([]Lease, n)
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := a.Acquire(fmt.Sprintf("c-%d", i))
			errs[i], leases[i] = err, l
		}(i)
	}
	wg.Wait()

	seenSlot := map[int]bool{}
	seenUID := map[int]bool{}
	seenIP := map[netip.Addr]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("acquire %d: %v", i, errs[i])
		}
		l := leases[i]
		if seenSlot[l.Slot] || seenUID[l.UID] || seenIP[l.HostIP] {
			t.Fatalf("collision at instance %d: slot=%d uid=%d ip=%s", i, l.Slot, l.UID, l.HostIP)
		}
		seenSlot[l.Slot], seenUID[l.UID], seenIP[l.HostIP] = true, true, true
	}
	if a.InUse() != n {
		t.Fatalf("InUse=%d want %d", a.InUse(), n)
	}
}
