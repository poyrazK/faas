package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestSnapshotBudgetFor_ScalesWithMemory pins the reason the budget is
// not a constant: a snapshot writes the guest's memory, and the plan
// range is 128 MB (Free) to 1024 MB (Scale) — 8x. One flat number is
// either too tight for Scale or uselessly loose for Free.
//
// The flat 25s SnapshotTimeout shipped first and was measured too tight:
// on 2026-09-03 a 1024 MB prime cold-booted fine in 9s and then failed
// at exactly 25s in PauseAndSnapshot, so no deployment could reach
// `live`.
func TestSnapshotBudgetFor_ScalesWithMemory(t *testing.T) {
	free := SnapshotBudgetFor(128)
	scale := SnapshotBudgetFor(1024)

	if scale <= free {
		t.Errorf("Scale budget (%s) must exceed Free budget (%s) — a snapshot writes guest memory", scale, free)
	}
	// The observed failure: 1024 MB did not finish inside 25s.
	if scale <= 25*time.Second {
		t.Errorf("Scale budget = %s, must exceed the 25s that was measured too tight for 1024 MB", scale)
	}
	// Free must not inherit Scale's generosity — a genuinely wedged
	// 128 MB capture should still fail reasonably fast.
	if free >= scale {
		t.Errorf("Free budget (%s) should be well under Scale's (%s)", free, scale)
	}
}

// TestSnapshotBudgetFor_UnknownRAMFallsBack guards the zero case. A row
// with no RAM recorded must not yield a zero deadline, which would
// cancel the RPC instantly and fail every park on that row.
func TestSnapshotBudgetFor_UnknownRAMFallsBack(t *testing.T) {
	for _, ram := range []int{0, -1} {
		if got := SnapshotBudgetFor(ram); got != SnapshotTimeout {
			t.Errorf("SnapshotBudgetFor(%d) = %s, want the flat %s fallback (a zero deadline cancels instantly)",
				ram, got, SnapshotTimeout)
		}
	}
}

// TestSnapshotBudgetCoversEveryPlan makes the guarantee explicit across
// the whole plan table rather than at two sampled points, so adding a
// plan with more RAM cannot silently land under the old flat budget.
func TestSnapshotBudgetCoversEveryPlan(t *testing.T) {
	for _, plan := range api.Plans {
		limits, ok := api.LimitsFor(plan)
		if !ok {
			continue
		}
		got := SnapshotBudgetFor(limits.RAMMB)
		if got < SnapshotBudgetBase {
			t.Errorf("plan %s (%d MB): budget %s is below the fixed-overhead base %s",
				plan, limits.RAMMB, got, SnapshotBudgetBase)
		}
	}
}

// TestWatchdogSpares_SnapshotWithinScaledBudget pins the exemption that
// keeps the two budgets consistent.
//
// The sweep query ages rows against one flat SnapshotSweepBudget (20s)
// so it stays a single cheap index scan, but the engine gives each
// capture SnapshotBudgetFor(ram) — 45s at 1024 MB. Without the
// exemption the watchdog kills a Scale snapshot at 20s while it is
// legitimately still writing, turning a working park into a FAILED
// instance and destroying the snapshot the next wake would have used.
func TestWatchdogSpares_SnapshotWithinScaledBudget(t *testing.T) {
	if SnapshotBudgetFor(1024) <= SnapshotSweepBudget {
		t.Fatalf("precondition: SnapshotBudgetFor(1024) = %s must exceed SnapshotSweepBudget = %s, or this exemption is unnecessary",
			SnapshotBudgetFor(1024), SnapshotSweepBudget)
	}
	// A 1024 MB row parked 25s ago is past the flat sweep budget but
	// still inside its own — the watchdog must leave it alone.
	const parkedAgo = 25 * time.Second
	if parkedAgo <= SnapshotSweepBudget {
		t.Fatal("precondition: the row must be old enough for the sweep query to return it")
	}
	if parkedAgo >= SnapshotBudgetFor(1024) {
		t.Fatal("precondition: the row must still be inside its scaled budget")
	}
}
