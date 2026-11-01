// adr: 1027 — a database VM that outlives vmmd keeps its slot: the next
// vmmd adopts the exact slot, never a duplicate, and releases it as usual.
package fcvm

import (
	"strconv"
	"testing"
)

func TestAllocatorAdoptTakesBackAFreeSlot(t *testing.T) {
	a := NewAllocator()
	lease, err := a.Adopt("db-x", 42)
	if err != nil || lease.Slot != 42 || lease.UID != JailUIDBase+42 || a.InUse() != 1 {
		t.Fatalf("adopt = %+v, %v (in use %d)", lease, err, a.InUse())
	}
	if _, err := a.Adopt("db-y", 42); err == nil {
		t.Fatal("adopted a slot twice")
	}
	if _, err := a.Adopt("db-x", 43); err == nil {
		t.Fatal("adopted a second slot for one instance")
	}
	for range MaxSlots - 1 {
		l, err := a.Acquire("other-" + strconv.Itoa(len(a.byInstance)))
		if err != nil {
			t.Fatal(err)
		}
		if l.Slot == 42 {
			t.Fatal("acquire handed out the adopted slot")
		}
	}
	if err := a.Release("db-x"); err != nil {
		t.Fatal(err)
	}
	if l, err := a.Acquire("db-x"); err != nil || l.Slot != 42 {
		t.Fatalf("after release = %+v, %v", l, err)
	}
	for _, bad := range []int{-1, MaxSlots} {
		if _, err := NewAllocator().Adopt("db-z", bad); err == nil {
			t.Errorf("adopted slot %d", bad)
		}
	}
}
