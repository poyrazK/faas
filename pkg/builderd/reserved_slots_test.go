// adr: 20261118154632
package builderd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
	"github.com/onebox-faas/faas/pkg/state"
)

// A node provisioned with two reserved slots runs two guaranteed builds with
// the flag off, whatever tenant residency and slice memory say, and keeps the
// residency- and memory-gated opportunistic slot as the third.
func TestReservedSlots_FixedAllocatorGuaranteesProvisionedSlots(t *testing.T) {
	b := (&Builderd{resid: fakeResid{mb: api.RAMAdmissionCeilingMB}, sliceFreeMB: func() (int, bool) { return 0, true }}).WithReservedSlots(2)
	if got := b.slotLimit(); got != 3 {
		t.Fatalf("slot limit = %d, want 2 reserved + 1 opportunistic", got)
	}
	var releases []func()
	for i := range 2 {
		slot, release, ok := b.acquireSlot()
		if !ok || slot.Label != "guaranteed" {
			t.Fatalf("slot %d = %+v, %t; want guaranteed", i+1, slot, ok)
		}
		releases = append(releases, release)
	}
	if got, _, ok := b.acquireSlot(); ok {
		t.Fatalf("third slot at full tenant residency = %+v; want the opportunistic gate", got)
	}
	releases[0]()
	if slot, release, ok := b.acquireSlot(); !ok || slot.Label != "guaranteed" {
		t.Fatalf("slot after a release = %+v, %t; want guaranteed again", slot, ok)
	} else {
		release()
	}
	releases[1]()
	if b.activeSlots != 0 || b.activeReserved != 0 {
		t.Fatalf("after releases: slots %d reserved %d", b.activeSlots, b.activeReserved)
	}
}

func TestReservedSlots_OpportunisticAfterReserved(t *testing.T) {
	b := (&Builderd{resid: fakeResid{mb: 0}, sliceFreeMB: func() (int, bool) { return secondSlotNeedMB, true }}).WithReservedSlots(2)
	labels := []string{"guaranteed", "guaranteed", "opportunistic"}
	for i, want := range labels {
		slot, release, ok := b.acquireSlot()
		if !ok || slot.Label != want {
			t.Fatalf("slot %d = %+v, %t; want %s", i+1, slot, ok, want)
		}
		defer release()
	}
	if got, _, ok := b.acquireSlot(); ok {
		t.Fatalf("fourth slot = %+v; want the limit", got)
	}
}

// One reserved slot is exactly today's allocator.
func TestReservedSlots_DefaultIsOne(t *testing.T) {
	for _, n := range []int{0, -3, 1} {
		b := (&Builderd{}).WithReservedSlots(n)
		if b.reservedSlots() != 1 || b.slotLimit() != maxBuilderSlots {
			t.Fatalf("WithReservedSlots(%d): reserved %d limit %d; want 1 and %d", n, b.reservedSlots(), b.slotLimit(), maxBuilderSlots)
		}
	}
	if got := (&Builderd{}).WithReservedSlots(99).reservedSlots(); got != api.BuilderReservedSlotsMax {
		t.Fatalf("WithReservedSlots(99) = %d, want %d", got, api.BuilderReservedSlotsMax)
	}
}

// With the flag on, the node runs its provisioned reserved slots even when
// builder_slots asks for one, and the slot above them is borrowed.
func TestReservedSlots_ElasticRunsProvisionedSlots(t *testing.T) {
	store := state.NewMemStore()
	b, elastic, _ := newElasticBuilderd(t, store, &borrowedFakeVM{supported: true}, builderslots.Slots{Reserved: 1, Max: 3})
	b.WithReservedSlots(2)
	if got := elastic.Applied(); got != (builderslots.Slots{Reserved: 2, Max: 3}) {
		t.Fatalf("applied %+v, want reserved 2 max 3", got)
	}
	ctx := context.Background()
	for i := range 2 {
		slot, release, ok := b.acquireElasticSlot(ctx, state.Build{ID: "reserved"})
		if !ok || slot.Label != "guaranteed" {
			t.Fatalf("slot %d = %+v, %t; want guaranteed", i+1, slot, ok)
		}
		defer release()
	}
	third, release, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store))
	if !ok || third.Label != slotLabelBorrowed {
		t.Fatalf("third slot = %+v, %t; want borrowed", third, ok)
	}
	release()
}

// A build that shares faas-cp-build.slice with another builder no longer
// takes the slice-wide oom_kill delta as its own.
func TestReservedSlots_SharedSliceHidesSliceOOMDelta(t *testing.T) {
	b := (&Builderd{resid: fakeResid{mb: 0}}).WithReservedSlots(2)
	ctx := context.Background()
	_, releaseA, ok := b.acquireBuildSlot(ctx, state.Build{ID: "a"})
	if !ok {
		t.Fatal("slot a")
	}
	probeA := b.sliceSharedProbe("a")
	if probeA == nil || probeA() {
		t.Fatal("a alone in the slice must not be shared")
	}
	_, releaseB, ok := b.acquireBuildSlot(ctx, state.Build{ID: "b"})
	if !ok {
		t.Fatal("slot b")
	}
	probeB := b.sliceSharedProbe("b")
	if !probeA() || probeB == nil || !probeB() {
		t.Fatal("a and b share the slice: both must be marked")
	}
	releaseB()
	if !probeA() {
		t.Fatal("a stays marked after b leaves: the counter may already hold b's kill")
	}
	releaseA()
	if b.sliceSharedProbe("a") != nil || len(b.sliceBuilds) != 0 {
		t.Fatal("released builds must be forgotten")
	}
	_, releaseC, _ := b.acquireBuildSlot(ctx, state.Build{ID: "c"})
	defer releaseC()
	if b.sliceSharedProbe("c")() {
		t.Fatal("c alone after a and b left must not be shared")
	}

	events := filepath.Join(t.TempDir(), "memory.events")
	if err := os.WriteFile(events, []byte("oom 1\noom_kill 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alone := BuildHandle{BuilderSliceOOMCounterValid: true, SliceShared: func() bool { return false }}
	if got := builderSliceOOMDelta(alone, events); got != 1 {
		t.Fatalf("delta for the only builder = %d, want 1", got)
	}
	if got := builderSliceOOMDelta(BuildHandle{BuilderSliceOOMCounterValid: true}, events); got != 1 {
		t.Fatalf("delta with no probe = %d, want 1", got)
	}
	shared := BuildHandle{BuilderSliceOOMCounterValid: true, SliceShared: probeA}
	if got := builderSliceOOMDelta(shared, events); got != 0 {
		t.Fatalf("delta for a shared build = %d, want 0", got)
	}
	if _, oom := builderSliceOOMOutcome(shared, events); oom {
		t.Fatal("a shared build must not be failed by the slice counter")
	}
}
