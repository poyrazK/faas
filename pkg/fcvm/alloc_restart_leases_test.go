package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"slices"
	"strconv"
	"testing"
)

func TestAllocatorTracksLeasesForTheAdoptionReport(t *testing.T) {
	a := NewAllocator()
	if _, err := a.Acquire("before"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.leasedSinceTracking(); ok {
		t.Fatal("tracking off must report incomplete")
	}
	a.trackLeases()
	if _, err := a.Acquire("woken"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.reserveNetwork("spare"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.adoptNetwork("spare", "prepared"); err != nil {
		t.Fatal(err)
	}
	a.quarantine(map[int]struct{}{7: {}})
	if _, err := a.adoptQuarantined("survivor", 7); err != nil {
		t.Fatal(err)
	}
	// A guest parked and woken again stays listed once.
	if err := a.Release("woken"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Acquire("woken"); err != nil {
		t.Fatal(err)
	}
	got, ok := a.leasedSinceTracking()
	if !ok || !slices.Equal(got, []string{"prepared", "woken"}) {
		t.Fatalf("leased = %v, %v; want the guests this vmmd created, not the survivor", got, ok)
	}
}

func TestAllocatorLeaseTrackingOverflowIsIncomplete(t *testing.T) {
	a := NewAllocator()
	a.trackLeases()
	a.mu.Lock()
	for i := range maxTrackedLeases {
		a.leased["seed-"+strconv.Itoa(i)] = struct{}{}
	}
	a.mu.Unlock()
	if _, err := a.Acquire("one-too-many"); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.leasedSinceTracking(); ok || got != nil {
		t.Fatalf("overflow = %d entries, %v; want incomplete", len(got), ok)
	}
	if _, err := a.Acquire("after"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.leasedSinceTracking(); ok {
		t.Fatal("overflow must stay incomplete")
	}
}
