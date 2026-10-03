// adr: 493 — environment intent and runtime ownership contracts.
package fcvm

import (
	"reflect"
	"testing"
)

func TestNativeRecoverySlotsExcludeEveryObservedUIDAndDuplicateID(t *testing.T) {
	a := NewAllocator()
	leas := []Lease{leaseForSlot("survived", 0), leaseForSlot("survived", 2), leaseForSlot("other-survivor", 3)}
	if err := a.reserveRecovered(leas); err != nil {
		t.Fatal(err)
	}
	if err := a.reserveRecovered(leas); err != nil || a.InUse() != 3 {
		t.Fatalf("idempotent recovery count=%d err=%v", a.InUse(), err)
	}
	if _, err := a.Acquire("survived"); err == nil {
		t.Fatal("an untracked survivor could acquire another boot lease")
	}
	if _, err := a.reserveNetwork("survived"); err == nil {
		t.Fatal("a survivor borrowed a prepared-network identity")
	}
	if err := a.Release("survived"); err == nil || a.InUse() != 3 {
		t.Fatal("ordinary release bypassed native recovery")
	}
	newLease, err := a.Acquire("replacement")
	if err != nil || newLease.Slot != 1 || a.InUse() != 4 {
		t.Fatalf("replacement lease=%+v count=%d err=%v", newLease, a.InUse(), err)
	}
	if err := a.releaseRecovered("survived"); err != nil || a.InUse() != 2 {
		t.Fatalf("confirmed release count=%d err=%v", a.InUse(), err)
	}
	next, err := a.Acquire("next")
	if err != nil || next.Slot != 0 && next.Slot != 2 {
		t.Fatalf("recovered slots were not returned: lease=%+v err=%v", next, err)
	}
}

func TestNativeRecoverySlotsRejectConflictsAtomically(t *testing.T) {
	for _, conflict := range []string{"live_slot", "live_id", "prepared_slot", "prepared_id", "recovered_slot", "duplicate_slot", "corrupt", "missing_free"} {
		t.Run(conflict, func(t *testing.T) {
			a := NewAllocator()
			request := []Lease{leaseForSlot("survivor-a", 5), leaseForSlot("survivor-b", 6)}
			switch conflict {
			case "live_slot":
				_, _ = a.Acquire("live")
				request[1] = leaseForSlot("survivor-b", 0)
			case "live_id":
				_, _ = a.Acquire("survivor-b")
			case "prepared_slot":
				_, _ = a.reserveNetwork("prepared")
				request[1] = leaseForSlot("survivor-b", 0)
			case "prepared_id":
				_, _ = a.reserveNetwork("survivor-b")
			case "recovered_slot":
				if err := a.reserveRecovered([]Lease{leaseForSlot("already-recovered", 6)}); err != nil {
					t.Fatal(err)
				}
			case "duplicate_slot":
				request[1] = leaseForSlot("survivor-b", 5)
			case "corrupt":
				request[1].UID++
			case "missing_free":
				for i, slot := range a.free {
					if slot == 6 {
						a.free = append(a.free[:i], a.free[i+1:]...)
						break
					}
				}
			}
			beforeFree := append([]int(nil), a.free...)
			beforeRecovered := make(map[int]string)
			for slot, id := range a.recovered {
				beforeRecovered[slot] = id
			}
			count := a.InUse()
			if err := a.reserveRecovered(request); err == nil || count != a.InUse() || !reflect.DeepEqual(beforeFree, a.free) {
				t.Fatalf("conflicting recovery mutated allocator: err=%v count=%d -> %d", err, count, a.InUse())
			}
			for slot, id := range a.recovered {
				if beforeRecovered[slot] != id {
					t.Fatal("conflicting recovery changed a quarantine")
				}
			}
		})
	}
}
