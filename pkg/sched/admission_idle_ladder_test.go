// adr: 20261107122928 — under measured admission, instances vmmd holds at the
// idle ladder's freeze level leave the cap on reservations, up to twice the
// ceiling, and the placement binding carries the same discount to the durable
// guard.
package sched_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// fakeLadderFrozen is the instances a node last reported at the freeze level.
type fakeLadderFrozen struct {
	ids  map[string]struct{}
	asOf time.Time
	ok   bool
}

func (f *fakeLadderFrozen) read(string) (map[string]struct{}, time.Time, bool) {
	return f.ids, f.asOf, f.ok
}

func ladderIDs(n int) map[string]struct{} {
	ids := make(map[string]struct{}, n)
	for i := range n {
		ids[fmt.Sprintf("i%d", i)] = struct{}{}
	}
	return ids
}

func TestIdleLadderAdmissionLeavesTheFreezeLevelOutOfTheCap(t *testing.T) {
	m := &fakeMeasurement{mb: map[string]int{}}
	l, clock := newMeasuredLedger(m)
	refresh := func(admitted int) {
		m.mb["n1"], m.asOf, m.ok = admitted*260, clock.now(), true
	}
	admitted, err := admitUntilRefused(t, l, clock, refresh)
	if admitted != 16 || !strings.Contains(err.Error(), "beyond the overcommit allowance") {
		t.Fatalf("admitted %d (%v), want 16 refused by the reservation cap", admitted, err)
	}
	ladder := &fakeLadderFrozen{ids: ladderIDs(4), asOf: clock.now(), ok: true}
	l.EnableIdleLadderAdmission(ladder.read)
	if got, want := l.ReservationDiscountMB("n1"), 4*api.BillableRAMMB(1024); got != want {
		t.Fatalf("discount = %d MB, want %d for four 1 GiB VMs at the freeze level", got, want)
	}
	more := 0
	for i := admitted; i < 100; i++ {
		clock.tick()
		refresh(i)
		clock.tick()
		if err := l.Admit(measuredScaleReq(i)); err != nil {
			break
		}
		more++
	}
	// 4 × 1032 MB left out of a 17,186 MB cap fits four more 1 GiB VMs.
	if more != 4 {
		t.Fatalf("admitted %d more with four VMs at the freeze level, want 4", more)
	}
}

func TestIdleLadderDiscountRules(t *testing.T) {
	m := &fakeMeasurement{mb: map[string]int{}}
	l, clock := newMeasuredLedger(m)
	refresh := func(admitted int) {
		m.mb["n1"], m.asOf, m.ok = admitted*260, clock.now(), true
	}
	admitted, _ := admitUntilRefused(t, l, clock, refresh)
	ladder := &fakeLadderFrozen{ids: ladderIDs(admitted), asOf: clock.now(), ok: true}
	l.EnableIdleLadderAdmission(ladder.read)
	if got, want := l.ReservationDiscountMB("n1"), admitted*api.BillableRAMMB(1024); got != want {
		t.Fatalf("discount for %d VMs = %d MB, want %d", admitted, got, want)
	}
	// With all of them left out the node takes more, and once those reach the
	// freeze level too the discount stops at twice the ceiling.
	for i := admitted; i < 100; i++ {
		clock.tick()
		refresh(i)
		clock.tick()
		if err := l.Admit(measuredScaleReq(i)); err != nil {
			admitted = i
			break
		}
	}
	ladder.ids, ladder.asOf = ladderIDs(admitted), clock.now()
	if got, want := l.ReservationDiscountMB("n1"), measuredCeilingMB*api.IdleLadderReservationDiscountMaxPercent/100; got != want {
		t.Fatalf("discount for %d VMs = %d MB, want the %d MB bound", admitted, got, want)
	}
	ladder.asOf = clock.now().Add(-time.Hour)
	if got := l.ReservationDiscountMB("n1"); got != 0 {
		t.Fatalf("discount from a sample older than the bookings = %d MB, want 0", got)
	}
	ladder.asOf, ladder.ok = clock.now(), false
	if got := l.ReservationDiscountMB("n1"); got != 0 {
		t.Fatalf("discount without a fresh sample = %d MB, want 0", got)
	}
	ladder.ok, m.ok = true, false
	if got := l.ReservationDiscountMB("n1"); got != 0 {
		t.Fatalf("discount without a fresh measurement = %d MB, want 0", got)
	}

	plain := sched.NewNodeLedger()
	plain.EnableIdleLadderAdmission(ladder.read)
	if got := plain.ReservationDiscountMB("n1"); got != 0 {
		t.Fatalf("discount without measured admission = %d MB, want 0", got)
	}
}
