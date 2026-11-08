// adr: 20261104143005 — the ledger indexes reservations by node, and service
// locks are dropped once nobody holds them.

package sched_test

import (
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// Measured admission adds only that node's reservations admitted after its
// measurement; the per-node index must agree with the full scan it replaced.
func TestMeasuredAdmissionCountsOnlyThatNodesNewReservations(t *testing.T) {
	m := &fakeMeasurement{mb: map[string]int{"n1": 1000, "n2": 2000}, ok: true}
	l, clock := newMeasuredLedger(m)
	m.asOf = clock.t
	clock.tick()

	onN1 := measuredScaleReq(1)
	onN2 := measuredScaleReq(2)
	onN2.NodeID = "n2"
	for _, r := range []sched.Request{onN1, onN2} {
		if err := l.Admit(r); err != nil {
			t.Fatalf("Admit %s: %v", r.Instance, err)
		}
	}
	addedN1 := l.AdmissionUsedMB("n1") - 1000
	addedN2 := l.AdmissionUsedMB("n2") - 2000
	if addedN1 <= 0 || addedN1 != addedN2 {
		t.Fatalf("added after measurement: n1 %d MB, n2 %d MB; want one reservation each", addedN1, addedN2)
	}

	l.Release(onN1.Instance)
	if got := l.AdmissionUsedMB("n1"); got != 1000 {
		t.Fatalf("n1 after release = %d MB, want the measurement 1000", got)
	}
	if got := l.AdmissionUsedMB("n2") - 2000; got != addedN2 {
		t.Fatalf("n2 changed with n1's release: %d MB, want %d", got, addedN2)
	}

	// A measurement taken after the admission covers it.
	m.asOf = clock.t.Add(time.Second)
	if got := l.AdmissionUsedMB("n2"); got != 2000 {
		t.Fatalf("n2 after a newer measurement = %d MB, want 2000", got)
	}
}

func TestKeyedLocksDropUnusedKeys(t *testing.T) {
	var k sched.KeyedLocks
	unlock := k.Lock("dep-1")
	if k.Size() != 1 {
		t.Fatalf("size while held = %d, want 1", k.Size())
	}

	// A waiter keeps the key until it is done too.
	var wg sync.WaitGroup
	acquired := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		release := k.Lock("dep-1")
		close(acquired)
		release()
	}()
	select {
	case <-acquired:
		t.Fatal("a second holder took a held lock")
	case <-time.After(20 * time.Millisecond):
	}
	unlock()
	unlock() // a second call is a no-op
	wg.Wait()
	if k.Size() != 0 {
		t.Fatalf("size after every holder left = %d, want 0", k.Size())
	}
}
